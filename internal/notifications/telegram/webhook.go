package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/dtorcivia/schedlock/internal/notifications"
	"github.com/dtorcivia/schedlock/internal/util"
)

// Update represents a Telegram webhook update.
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message,omitempty"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

// Message represents a Telegram message.
type Message struct {
	MessageID      int64   `json:"message_id"`
	From           *User   `json:"from,omitempty"`
	Chat           *Chat   `json:"chat"`
	Text           string  `json:"text,omitempty"`
	ReplyToMessage *Message `json:"reply_to_message,omitempty"`
}

// CallbackQuery represents a callback from an inline keyboard button.
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    *User    `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data,omitempty"`
}

// User represents a Telegram user.
type User struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name,omitempty"`
	Username  string `json:"username,omitempty"`
}

// Chat represents a Telegram chat.
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// WebhookHandler handles incoming Telegram webhook requests.
type WebhookHandler struct {
	provider        *Provider
	callbackHandler notifications.CallbackHandler
	notificationMgr *notifications.Manager
}

// NewWebhookHandler creates a new webhook handler.
func NewWebhookHandler(provider *Provider, callbackHandler notifications.CallbackHandler, notificationMgr *notifications.Manager) *WebhookHandler {
	return &WebhookHandler{
		provider:        provider,
		callbackHandler: callbackHandler,
		notificationMgr: notificationMgr,
	}
}

// ServeHTTP handles incoming webhook requests.
func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Validate Telegram secret token if configured
	if h.provider.config.WebhookSecret != "" {
		secret := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
		if secret != h.provider.config.WebhookSecret {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		util.Error("Failed to read webhook body", "error", err)
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}

	var update Update
	if err := json.Unmarshal(body, &update); err != nil {
		util.Error("Failed to parse webhook update", "error", err)
		http.Error(w, "Failed to parse update", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Handle callback queries (button presses)
	if update.CallbackQuery != nil {
		h.handleCallbackQuery(ctx, update.CallbackQuery)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Handle message replies (suggestions)
	if update.Message != nil && update.Message.ReplyToMessage != nil {
		h.handleReply(ctx, update.Message)
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// handleCallbackQuery processes inline keyboard button presses.
func (h *WebhookHandler) handleCallbackQuery(ctx context.Context, query *CallbackQuery) {
	// Answer the callback query to remove loading state
	h.answerCallbackQuery(ctx, query.ID, "")

	// Parse callback data: "action:request_id"
	parts := strings.SplitN(query.Data, ":", 2)
	if len(parts) != 2 {
		util.Warn("Invalid callback data", "data", query.Data)
		return
	}

	action := parts[0]
	requestID := parts[1]

	if query.Message == nil || query.Message.Chat == nil {
		util.Warn("Missing chat info in callback query", "request_id", requestID)
		return
	}

	// Enforce allowed chat ID
	if !h.isAllowedChat(query.Message.Chat.ID) {
		util.Warn("Unauthorized chat for callback", "chat_id", query.Message.Chat.ID)
		return
	}

	// Handle test button
	if action == "test" {
		h.answerCallbackQuery(ctx, query.ID, "Test button clicked!")
		return
	}

	// Handle noop (already processed)
	if action == "noop" {
		return
	}

	// Only allow approve/deny actions
	if action != "approve" && action != "deny" {
		util.Warn("Unknown callback action", "action", action)
		return
	}

	// Get user info for audit
	respondedBy := "telegram"
	if query.From != nil {
		if query.From.Username != "" {
			respondedBy = fmt.Sprintf("telegram:@%s", query.From.Username)
		} else {
			respondedBy = fmt.Sprintf("telegram:%s %s", query.From.FirstName, query.From.LastName)
		}
	}

	// Create callback
	callback := &notifications.Callback{
		Provider:    "telegram",
		RequestID:   requestID,
		Action:      action,
		RespondedBy: respondedBy,
	}

	if query.Message != nil {
		callback.MessageID = fmt.Sprintf("%d", query.Message.MessageID)
		callback.ChatID = fmt.Sprintf("%d", query.Message.Chat.ID)
	}

	// Process the callback
	if err := h.callbackHandler.HandleCallback(ctx, callback); err != nil {
		util.Error("Failed to handle callback", "error", err, "request_id", requestID)
		h.answerCallbackQuery(ctx, query.ID, "Error: "+err.Error())
		return
	}

	// Update the message to remove the keyboard
	if query.Message != nil {
		status := "approved"
		if action == "deny" {
			status = "denied"
		}
		h.provider.RemoveKeyboard(ctx, query.Message.MessageID, status)
	}

	util.Info("Processed Telegram callback",
		"action", action,
		"request_id", requestID,
		"responded_by", respondedBy,
	)
}

// handleReply processes message replies as suggestions.
func (h *WebhookHandler) handleReply(ctx context.Context, msg *Message) {
	// Find the original notification by message ID
	originalMsgID := fmt.Sprintf("%d", msg.ReplyToMessage.MessageID)

	if msg.Chat == nil || !h.isAllowedChat(msg.Chat.ID) {
		util.Warn("Unauthorized chat for suggestion", "chat_id", msg.Chat.ID)
		return
	}

	notifLog, err := h.notificationMgr.FindByMessageID(ctx, "telegram", originalMsgID)
	if err != nil || notifLog == nil {
		util.Warn("Could not find notification for reply", "message_id", originalMsgID)
		return
	}

	// Get user info
	respondedBy := "telegram"
	if msg.From != nil {
		if msg.From.Username != "" {
			respondedBy = fmt.Sprintf("telegram:@%s", msg.From.Username)
		} else {
			respondedBy = fmt.Sprintf("telegram:%s %s", msg.From.FirstName, msg.From.LastName)
		}
	}

	// Create suggestion callback
	callback := &notifications.Callback{
		Provider:    "telegram",
		RequestID:   notifLog.RequestID,
		Action:      "suggest",
		Suggestion:  msg.Text,
		MessageID:   originalMsgID,
		ChatID:      fmt.Sprintf("%d", msg.Chat.ID),
		RespondedBy: respondedBy,
	}

	// Process the suggestion
	if err := h.callbackHandler.HandleCallback(ctx, callback); err != nil {
		util.Error("Failed to handle suggestion", "error", err, "request_id", notifLog.RequestID)
		return
	}

	// Update the original message
	h.provider.RemoveKeyboard(ctx, msg.ReplyToMessage.MessageID, "change_requested")

	// Send confirmation
	h.sendReply(ctx, msg.Chat.ID, msg.MessageID, "Suggestion recorded. The request has been updated.")

	util.Info("Processed Telegram suggestion",
		"request_id", notifLog.RequestID,
		"responded_by", respondedBy,
	)
}

func (h *WebhookHandler) isAllowedChat(chatID int64) bool {
	if h.provider == nil || h.provider.config == nil || h.provider.config.ChatID == "" {
		return false
	}
	return fmt.Sprintf("%d", chatID) == h.provider.config.ChatID
}

// answerCallbackQuery acknowledges a callback query.
func (h *WebhookHandler) answerCallbackQuery(ctx context.Context, queryID, text string) {
	req := map[string]interface{}{
		"callback_query_id": queryID,
	}
	if text != "" {
		req["text"] = text
		req["show_alert"] = true
	}

	data, _ := json.Marshal(req)
	h.provider.apiCall(ctx, "answerCallbackQuery", data)
}

// sendReply sends a reply to a message.
func (h *WebhookHandler) sendReply(ctx context.Context, chatID, replyToMsgID int64, text string) {
	req := map[string]interface{}{
		"chat_id":             strconv.FormatInt(chatID, 10),
		"text":                text,
		"reply_to_message_id": replyToMsgID,
	}

	data, _ := json.Marshal(req)
	h.provider.apiCall(ctx, "sendMessage", data)
}
