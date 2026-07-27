package telegram

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/dtorcivia/schedlock/internal/notifications"
	"github.com/dtorcivia/schedlock/internal/util"
)

// maxUpdateBytes bounds an incoming webhook body. The endpoint is reachable
// from the internet, so it must not accept an unbounded upload.
const maxUpdateBytes = 1 << 20

// Update is an incoming Telegram update.
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message,omitempty"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

// Message is a Telegram message.
type Message struct {
	MessageID      int64    `json:"message_id"`
	From           *User    `json:"from,omitempty"`
	Chat           *Chat    `json:"chat"`
	Text           string   `json:"text,omitempty"`
	ReplyToMessage *Message `json:"reply_to_message,omitempty"`
}

// CallbackQuery is an inline keyboard button press.
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    *User    `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data,omitempty"`
}

// User is a Telegram user.
type User struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name,omitempty"`
	Username  string `json:"username,omitempty"`
}

// Chat is a Telegram chat.
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// WebhookHandler receives Telegram updates and turns them into decisions.
type WebhookHandler struct {
	provider        *Provider
	callbackHandler notifications.CallbackHandler
	manager         *notifications.Manager
}

// NewWebhookHandler creates a Telegram webhook handler.
func NewWebhookHandler(provider *Provider, callbackHandler notifications.CallbackHandler, manager *notifications.Manager) *WebhookHandler {
	return &WebhookHandler{
		provider:        provider,
		callbackHandler: callbackHandler,
		manager:         manager,
	}
}

// ServeHTTP handles an incoming Telegram update.
//
// Two independent checks gate every update. The shared secret proves the
// request came from Telegram, and the chat ID check proves it came from the
// chat the operator configured. Without the secret this endpoint would accept
// approvals from anyone who could guess a chat ID, so an unconfigured secret
// closes the endpoint rather than opening it.
func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	secret := h.provider.WebhookSecret()
	if secret == "" {
		util.Warn("Rejecting Telegram webhook: no webhook secret is configured")
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	presented := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	if subtle.ConstantTimeCompare([]byte(presented), []byte(secret)) != 1 {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var update Update
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUpdateBytes)).Decode(&update); err != nil {
		util.Warn("Failed to parse Telegram update", "error", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	switch {
	case update.CallbackQuery != nil:
		h.handleCallbackQuery(ctx, update.CallbackQuery)
	case update.Message != nil && update.Message.ReplyToMessage != nil:
		h.handleReply(ctx, update.Message)
	}

	// Telegram retries on a non-2xx response, so an update that cannot be acted
	// on is still acknowledged.
	w.WriteHeader(http.StatusOK)
}

// handleCallbackQuery processes an approve/deny button press.
func (h *WebhookHandler) handleCallbackQuery(ctx context.Context, query *CallbackQuery) {
	if err := h.provider.AnswerCallbackQuery(ctx, query.ID, ""); err != nil {
		util.Debug("Failed to acknowledge callback query", "error", err)
	}

	if query.Message == nil || query.Message.Chat == nil {
		util.Warn("Ignoring Telegram callback without chat context")
		return
	}
	if !h.isAllowedChat(query.Message.Chat.ID) {
		util.Warn("Ignoring Telegram callback from an unauthorized chat", "chat_id", query.Message.Chat.ID)
		return
	}

	action, requestID, found := strings.Cut(query.Data, ":")
	if !found {
		util.Warn("Ignoring malformed Telegram callback data")
		return
	}

	switch action {
	case "noop":
		return
	case "approve", "deny":
	default:
		util.Warn("Ignoring unknown Telegram callback action", "action", action)
		return
	}

	callback := &notifications.Callback{
		Provider:    notifications.ProviderTelegram,
		RequestID:   requestID,
		Action:      action,
		MessageID:   strconv.FormatInt(query.Message.MessageID, 10),
		ChatID:      strconv.FormatInt(query.Message.Chat.ID, 10),
		RespondedBy: describeUser(query.From),
	}

	if err := h.callbackHandler.HandleCallback(ctx, callback); err != nil {
		util.Error("Failed to handle Telegram callback", "error", err, "request_id", requestID)
		if err := h.provider.AnswerCallbackQuery(ctx, query.ID, friendlyError(err)); err != nil {
			util.Debug("Failed to report callback error", "error", err)
		}
		return
	}

	outcome := "Approved"
	if action == "deny" {
		outcome = "Denied"
	}
	if err := h.provider.ReplaceKeyboard(ctx, query.Message.MessageID, outcome); err != nil {
		util.Debug("Failed to update Telegram keyboard", "error", err)
	}

	util.Info("Processed Telegram decision",
		"action", action, "request_id", requestID, "responded_by", callback.RespondedBy)
}

// handleReply turns a reply to a notification into a change suggestion.
func (h *WebhookHandler) handleReply(ctx context.Context, msg *Message) {
	if msg.Chat == nil || !h.isAllowedChat(msg.Chat.ID) {
		util.Warn("Ignoring Telegram reply from an unauthorized chat")
		return
	}
	if strings.TrimSpace(msg.Text) == "" {
		return
	}

	originalMsgID := strconv.FormatInt(msg.ReplyToMessage.MessageID, 10)

	record, err := h.manager.FindByMessageID(ctx, notifications.ProviderTelegram, originalMsgID)
	if err != nil || record == nil {
		util.Warn("No request matches the replied-to Telegram message", "message_id", originalMsgID)
		return
	}

	callback := &notifications.Callback{
		Provider:    notifications.ProviderTelegram,
		RequestID:   record.RequestID,
		Action:      "suggest",
		Suggestion:  msg.Text,
		MessageID:   originalMsgID,
		ChatID:      strconv.FormatInt(msg.Chat.ID, 10),
		RespondedBy: describeUser(msg.From),
	}

	if err := h.callbackHandler.HandleCallback(ctx, callback); err != nil {
		util.Error("Failed to record Telegram suggestion", "error", err, "request_id", record.RequestID)
		if err := h.provider.SendReply(ctx, msg.Chat.ID, msg.MessageID, friendlyError(err)); err != nil {
			util.Debug("Failed to report suggestion error", "error", err)
		}
		return
	}

	if err := h.provider.ReplaceKeyboard(ctx, msg.ReplyToMessage.MessageID, "Changes requested"); err != nil {
		util.Debug("Failed to update Telegram keyboard", "error", err)
	}
	if err := h.provider.SendReply(ctx, msg.Chat.ID, msg.MessageID,
		"Suggestion recorded. The request has been returned to the requester."); err != nil {
		util.Debug("Failed to confirm suggestion", "error", err)
	}

	util.Info("Processed Telegram suggestion",
		"request_id", record.RequestID, "responded_by", callback.RespondedBy)
}

// isAllowedChat reports whether decisions from a chat are accepted.
func (h *WebhookHandler) isAllowedChat(chatID int64) bool {
	configured := h.provider.ChatID()
	if configured == "" {
		return false
	}
	return strconv.FormatInt(chatID, 10) == configured
}

func describeUser(user *User) string {
	if user == nil {
		return "telegram"
	}
	if user.Username != "" {
		return "telegram:@" + user.Username
	}
	name := strings.TrimSpace(user.FirstName + " " + user.LastName)
	if name == "" {
		return fmt.Sprintf("telegram:%d", user.ID)
	}
	return "telegram:" + name
}

// friendlyError renders an error for display in a chat.
func friendlyError(err error) string {
	return util.TruncateString(err.Error(), 180)
}
