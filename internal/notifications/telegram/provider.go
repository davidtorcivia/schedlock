// Package telegram delivers notifications through a Telegram bot.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/dtorcivia/schedlock/internal/notifications"
	"github.com/dtorcivia/schedlock/internal/util"
)

const apiBase = "https://api.telegram.org/bot"

// settings is an immutable configuration snapshot.
type settings struct {
	enabled       bool
	botToken      string
	chatID        string
	webhookSecret string
}

// Provider implements Telegram notifications with inline approve/deny buttons.
type Provider struct {
	current atomic.Pointer[settings]
	client  *http.Client
}

// NewProvider creates a Telegram provider. It is inert until Configure runs.
func NewProvider() *Provider {
	p := &Provider{client: notifications.NewHTTPClient(0)}
	p.current.Store(&settings{})
	return p
}

// Name returns the provider name.
func (p *Provider) Name() string { return notifications.ProviderTelegram }

// Enabled reports whether Telegram is configured and switched on.
func (p *Provider) Enabled() bool {
	s := p.current.Load()
	return s.enabled && s.botToken != "" && s.chatID != ""
}

// Configure applies a settings snapshot.
func (p *Provider) Configure(creds *notifications.ProviderCredentials) {
	next := &settings{}
	if creds != nil {
		next.enabled = creds.Enabled
		if c, ok := creds.Credentials.(*notifications.TelegramCredentials); ok && c != nil {
			next.botToken = c.BotToken
			next.chatID = c.ChatID
			next.webhookSecret = c.WebhookSecret
		}
	}
	p.current.Store(next)
}

// WebhookSecret returns the shared secret Telegram must present on webhook
// calls.
func (p *Provider) WebhookSecret() string { return p.current.Load().webhookSecret }

// ChatID returns the chat authorized to make decisions.
func (p *Provider) ChatID() string { return p.current.Load().chatID }

// InlineKeyboardButton is one inline keyboard button.
type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

// InlineKeyboardMarkup is a grid of inline keyboard buttons.
type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

type sendMessageRequest struct {
	ChatID      string                `json:"chat_id"`
	Text        string                `json:"text"`
	ParseMode   string                `json:"parse_mode,omitempty"`
	ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

type editMessageRequest struct {
	ChatID      string                `json:"chat_id"`
	MessageID   int64                 `json:"message_id"`
	ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result,omitempty"`
	Description string          `json:"description,omitempty"`
	ErrorCode   int             `json:"error_code,omitempty"`
}

// SendApproval sends an approval request with inline decision buttons.
func (p *Provider) SendApproval(ctx context.Context, n *notifications.ApprovalNotification) (string, error) {
	s := p.current.Load()

	var text strings.Builder
	fmt.Fprintf(&text, "*%s*\n\n", escapeMarkdown(n.Summary))
	fmt.Fprintf(&text, "*Operation:* %s\n", escapeMarkdown(n.Operation))

	if d := n.Details; d != nil {
		formatter := util.GetDefaultFormatter()
		if d.Title != "" {
			fmt.Fprintf(&text, "*Event:* %s\n", escapeMarkdown(d.Title))
		}
		if !d.StartTime.IsZero() {
			fmt.Fprintf(&text, "*Starts:* %s\n", escapeMarkdown(formatter.FormatDateTime(d.StartTime)))
		}
		if !d.EndTime.IsZero() {
			fmt.Fprintf(&text, "*Ends:* %s\n", escapeMarkdown(formatter.FormatDateTime(d.EndTime)))
		}
		if d.Location != "" {
			fmt.Fprintf(&text, "*Where:* %s\n", escapeMarkdown(d.Location))
		}
		if len(d.Attendees) > 0 {
			fmt.Fprintf(&text, "*Attendees:* %s\n", escapeMarkdown(strings.Join(d.Attendees, ", ")))
		}
		if d.Description != "" {
			fmt.Fprintf(&text, "\n_%s_\n", escapeMarkdown(util.TruncateString(d.Description, 200)))
		}
	}

	fmt.Fprintf(&text, "\n*Expires in:* %s\n", escapeMarkdown(n.ExpiresIn))
	fmt.Fprintf(&text, "\n_Request %s_", escapeMarkdown(n.RequestID))
	text.WriteString("\n\nReply to this message to suggest changes\\.")

	keyboard := &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{{
			{Text: "Approve", CallbackData: "approve:" + n.RequestID},
			{Text: "Deny", CallbackData: "deny:" + n.RequestID},
		}},
	}
	if reviewURL := firstNonEmpty(n.ApprovePageURL, n.WebURL); reviewURL != "" {
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard,
			[]InlineKeyboardButton{{Text: "View details", URL: reviewURL}})
	}

	return p.sendMessage(ctx, &sendMessageRequest{
		ChatID:      s.chatID,
		Text:        text.String(),
		ParseMode:   "MarkdownV2",
		ReplyMarkup: keyboard,
	})
}

// SendResult reports the outcome of a decided request.
func (p *Provider) SendResult(ctx context.Context, n *notifications.ResultNotification) error {
	s := p.current.Load()

	var text strings.Builder
	fmt.Fprintf(&text, "*%s*\n\n%s", escapeMarkdown(resultTitle(n)), escapeMarkdown(n.Message))
	if n.Error != "" {
		fmt.Fprintf(&text, "\n\n_%s_", escapeMarkdown(util.TruncateString(n.Error, 300)))
	}

	_, err := p.sendMessage(ctx, &sendMessageRequest{
		ChatID:    s.chatID,
		Text:      text.String(),
		ParseMode: "MarkdownV2",
	})
	return err
}

// SendTest sends a test notification.
func (p *Provider) SendTest(ctx context.Context) error {
	s := p.current.Load()
	_, err := p.sendMessage(ctx, &sendMessageRequest{
		ChatID:    s.chatID,
		Text:      escapeMarkdown("SchedLock test: if you can see this, Telegram is configured correctly."),
		ParseMode: "MarkdownV2",
	})
	return err
}

// ReplaceKeyboard swaps the inline keyboard for a static outcome label, so a
// decided request cannot be decided again from the chat history.
func (p *Provider) ReplaceKeyboard(ctx context.Context, messageID int64, outcome string) error {
	s := p.current.Load()

	data, err := json.Marshal(editMessageRequest{
		ChatID:    s.chatID,
		MessageID: messageID,
		ReplyMarkup: &InlineKeyboardMarkup{
			InlineKeyboard: [][]InlineKeyboardButton{{
				{Text: outcome, CallbackData: "noop"},
			}},
		},
	})
	if err != nil {
		return err
	}

	_, err = p.apiCall(ctx, "editMessageReplyMarkup", data)
	return err
}

// AnswerCallbackQuery acknowledges a button press.
func (p *Provider) AnswerCallbackQuery(ctx context.Context, queryID, text string) error {
	payload := map[string]any{"callback_query_id": queryID}
	if text != "" {
		payload["text"] = text
		payload["show_alert"] = true
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	_, err = p.apiCall(ctx, "answerCallbackQuery", data)
	return err
}

// SendReply posts a plain-text reply to a message.
func (p *Provider) SendReply(ctx context.Context, chatID, replyToMessageID int64, text string) error {
	data, err := json.Marshal(map[string]any{
		"chat_id":             chatID,
		"text":                text,
		"reply_to_message_id": replyToMessageID,
	})
	if err != nil {
		return err
	}

	_, err = p.apiCall(ctx, "sendMessage", data)
	return err
}

// sendMessage posts a message and returns its ID.
func (p *Provider) sendMessage(ctx context.Context, req *sendMessageRequest) (string, error) {
	if req.ChatID == "" {
		return "", fmt.Errorf("telegram chat ID is not configured")
	}

	data, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	result, err := p.apiCall(ctx, "sendMessage", data)
	if err != nil {
		return "", err
	}

	var msg struct {
		MessageID int64 `json:"message_id"`
	}
	if err := json.Unmarshal(result, &msg); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	return fmt.Sprintf("%d", msg.MessageID), nil
}

// apiCall invokes a Telegram Bot API method.
func (p *Provider) apiCall(ctx context.Context, method string, body []byte) (json.RawMessage, error) {
	s := p.current.Load()
	if s.botToken == "" {
		return nil, fmt.Errorf("telegram bot token is not configured")
	}

	// The token is built into the URL on every call rather than cached at
	// construction, so rotating it in settings takes effect immediately.
	endpoint := apiBase + url.PathEscape(s.botToken) + "/" + method

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call telegram: %w", err)
	}
	defer resp.Body.Close()

	var parsed apiResponse
	if err := json.Unmarshal(notifications.ReadLimited(resp.Body), &parsed); err != nil {
		return nil, fmt.Errorf("telegram returned status %d with an unreadable body", resp.StatusCode)
	}
	if !parsed.OK {
		return nil, fmt.Errorf("telegram API error %d: %s", parsed.ErrorCode, parsed.Description)
	}

	return parsed.Result, nil
}

// markdownV2Escaper escapes every character Telegram reserves in MarkdownV2.
//
// Telegram rejects the whole message when an unescaped reserved character
// appears, so a request ID such as "req_a1b2" or any event title containing a
// hyphen or period would previously fail to send at all. The backslash is
// escaped first by virtue of being in the same replacer pass.
var markdownV2Escaper = strings.NewReplacer(
	`\`, `\\`,
	"_", `\_`,
	"*", `\*`,
	"[", `\[`,
	"]", `\]`,
	"(", `\(`,
	")", `\)`,
	"~", `\~`,
	"`", "\\`",
	">", `\>`,
	"#", `\#`,
	"+", `\+`,
	"-", `\-`,
	"=", `\=`,
	"|", `\|`,
	"{", `\{`,
	"}", `\}`,
	".", `\.`,
	"!", `\!`,
)

// escapeMarkdown makes arbitrary text safe to embed in a MarkdownV2 message.
func escapeMarkdown(text string) string {
	return markdownV2Escaper.Replace(text)
}

func resultTitle(n *notifications.ResultNotification) string {
	switch n.Status {
	case "completed":
		return "Calendar request completed"
	case "failed":
		return "Calendar request failed"
	case "denied":
		return "Calendar request denied"
	case "expired":
		return "Calendar request expired"
	default:
		return fmt.Sprintf("Calendar request %s", n.Status)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
