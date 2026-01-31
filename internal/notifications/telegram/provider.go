// Package telegram provides Telegram Bot notification delivery.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dtorcivia/schedlock/internal/config"
	"github.com/dtorcivia/schedlock/internal/notifications"
)

const telegramAPIBase = "https://api.telegram.org/bot"

// Provider implements Telegram notifications with inline keyboards.
type Provider struct {
	config  *config.TelegramConfig
	client  *http.Client
	baseURL string
}

// NewProvider creates a new Telegram provider.
func NewProvider(cfg *config.TelegramConfig) *Provider {
	return &Provider{
		config:  cfg,
		client:  &http.Client{Timeout: 30 * time.Second},
		baseURL: telegramAPIBase + cfg.BotToken,
	}
}

// Name returns the provider name.
func (p *Provider) Name() string {
	return "telegram"
}

// Enabled returns whether Telegram is configured and enabled.
func (p *Provider) Enabled() bool {
	return p.config.Enabled && p.config.BotToken != "" && p.config.ChatID != ""
}

// InlineKeyboardButton represents a button in an inline keyboard.
type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

// InlineKeyboardMarkup represents an inline keyboard.
type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

// sendMessageRequest represents the Telegram sendMessage API request.
type sendMessageRequest struct {
	ChatID      string                `json:"chat_id"`
	Text        string                `json:"text"`
	ParseMode   string                `json:"parse_mode,omitempty"`
	ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

// editMessageRequest represents the Telegram editMessageReplyMarkup API request.
type editMessageRequest struct {
	ChatID      string                `json:"chat_id"`
	MessageID   int64                 `json:"message_id"`
	ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

// telegramResponse represents a Telegram API response.
type telegramResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result,omitempty"`
	Description string          `json:"description,omitempty"`
	ErrorCode   int             `json:"error_code,omitempty"`
}

// messageResult represents the result of sending a message.
type messageResult struct {
	MessageID int64 `json:"message_id"`
}

// SendApproval sends an approval request notification with inline keyboard.
func (p *Provider) SendApproval(ctx context.Context, notification *notifications.ApprovalNotification) (string, error) {
	var text strings.Builder
	text.WriteString(fmt.Sprintf("*%s*\n\n", escapeMarkdown(notification.Summary)))
	text.WriteString(fmt.Sprintf("*Operation:* %s\n", escapeMarkdown(notification.Operation)))

	if notification.Details != nil {
		if notification.Details.Title != "" {
			text.WriteString(fmt.Sprintf("*Event:* %s\n", escapeMarkdown(notification.Details.Title)))
		}
		if !notification.Details.StartTime.IsZero() {
			text.WriteString(fmt.Sprintf("*When:* %s\n", notification.Details.StartTime.Format("Mon Jan 2, 3:04 PM")))
		}
		if notification.Details.Location != "" {
			text.WriteString(fmt.Sprintf("*Where:* %s\n", escapeMarkdown(notification.Details.Location)))
		}
		if len(notification.Details.Attendees) > 0 {
			text.WriteString(fmt.Sprintf("*Attendees:* %s\n", escapeMarkdown(strings.Join(notification.Details.Attendees, ", "))))
		}
		if notification.Details.Description != "" {
			desc := notification.Details.Description
			if len(desc) > 200 {
				desc = desc[:197] + "..."
			}
			text.WriteString(fmt.Sprintf("\n_%s_\n", escapeMarkdown(desc)))
		}
	}

	text.WriteString(fmt.Sprintf("\n*Expires:* %s\n", notification.ExpiresIn))
	text.WriteString(fmt.Sprintf("\n_Request ID: %s_", notification.RequestID))
	text.WriteString("\n\nReply to this message to suggest changes")

	// Create inline keyboard with approve/deny buttons
	keyboard := &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: "Approve", CallbackData: fmt.Sprintf("approve:%s", notification.RequestID)},
				{Text: "Deny", CallbackData: fmt.Sprintf("deny:%s", notification.RequestID)},
			},
		},
	}
	// Add link to public approval page (works without login)
	if notification.ApprovePageURL != "" {
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []InlineKeyboardButton{
			{Text: "View Details", URL: notification.ApprovePageURL},
		})
	} else if notification.WebURL != "" {
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []InlineKeyboardButton{
			{Text: "View Details", URL: notification.WebURL},
		})
	}

	req := sendMessageRequest{
		ChatID:      p.config.ChatID,
		Text:        text.String(),
		ParseMode:   "MarkdownV2",
		ReplyMarkup: keyboard,
	}

	return p.sendMessage(ctx, &req)
}

// SendResult sends a result notification.
func (p *Provider) SendResult(ctx context.Context, notification *notifications.ResultNotification) error {
	var emoji string
	switch notification.Status {
	case "completed":
		emoji = "OK"
	case "failed":
		emoji = "FAILED"
	case "denied":
		emoji = "DENIED"
	case "expired":
		emoji = "EXPIRED"
	default:
		emoji = "STATUS"
	}

	text := fmt.Sprintf("%s *%s: %s*\n\n%s",
		emoji,
		escapeMarkdown(notification.Operation),
		escapeMarkdown(notification.Status),
		escapeMarkdown(notification.Message),
	)

	if notification.Error != "" {
		text += fmt.Sprintf("\n\n_Error: %s_", escapeMarkdown(notification.Error))
	}

	req := sendMessageRequest{
		ChatID:    p.config.ChatID,
		Text:      text,
		ParseMode: "MarkdownV2",
	}

	_, err := p.sendMessage(ctx, &req)
	return err
}

// SendTest sends a test notification.
func (p *Provider) SendTest(ctx context.Context) error {
	keyboard := &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: "Test Button", CallbackData: "test:button"},
			},
		},
	}

	req := sendMessageRequest{
		ChatID:      p.config.ChatID,
		Text:        "*SchedLock Test*\n\nThis is a test notification from SchedLock\\. If you can see this, Telegram is configured correctly\\.\n\nClick the button below to test inline keyboard functionality\\.",
		ParseMode:   "MarkdownV2",
		ReplyMarkup: keyboard,
	}

	_, err := p.sendMessage(ctx, &req)
	return err
}

// RemoveKeyboard removes the inline keyboard from a message.
func (p *Provider) RemoveKeyboard(ctx context.Context, messageID int64, status string) error {
	emoji := "OK"
	if status == "denied" {
		emoji = "DENIED"
	} else if status == "change_requested" {
		emoji = "UPDATED"
	}

	req := editMessageRequest{
		ChatID:    p.config.ChatID,
		MessageID: messageID,
		ReplyMarkup: &InlineKeyboardMarkup{
			InlineKeyboard: [][]InlineKeyboardButton{
				{
					{Text: fmt.Sprintf("%s %s", emoji, strings.Title(status)), CallbackData: "noop"},
				},
			},
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		return err
	}

	_, err = p.apiCall(ctx, "editMessageReplyMarkup", data)
	return err
}

// sendMessage sends a message and returns the message ID.
func (p *Provider) sendMessage(ctx context.Context, req *sendMessageRequest) (string, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	result, err := p.apiCall(ctx, "sendMessage", data)
	if err != nil {
		return "", err
	}

	var msg messageResult
	if err := json.Unmarshal(result, &msg); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	return fmt.Sprintf("%d", msg.MessageID), nil
}

// apiCall makes a call to the Telegram Bot API.
func (p *Provider) apiCall(ctx context.Context, method string, body []byte) (json.RawMessage, error) {
	url := fmt.Sprintf("%s/%s", p.baseURL, method)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	var tgResp telegramResponse
	if err := json.Unmarshal(respBody, &tgResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !tgResp.OK {
		return nil, fmt.Errorf("telegram API error %d: %s", tgResp.ErrorCode, tgResp.Description)
	}

	return tgResp.Result, nil
}

// GetBotInfo returns information about the bot.
func (p *Provider) GetBotInfo(ctx context.Context) (map[string]interface{}, error) {
	result, err := p.apiCall(ctx, "getMe", nil)
	if err != nil {
		return nil, err
	}

	var info map[string]interface{}
	if err := json.Unmarshal(result, &info); err != nil {
		return nil, err
	}

	return info, nil
}

// escapeMarkdown escapes special characters for MarkdownV2.
func escapeMarkdown(text string) string {
	replacer := strings.NewReplacer(
		"_", "\\_",
		"*", "\\*",
		"[", "\\[",
		"]", "\\]",
		"(", "\\(",
		")", "\\)",
		"~", "\\~",
		"`", "\\`",
		">", "\\>",
		"#", "\\#",
		"+", "\\+",
		"-", "\\-",
		"=", "\\=",
		"|", "\\|",
		"{", "\\{",
		"}", "\\}",
		".", "\\.",
		"!", "\\!",
	)
	return replacer.Replace(text)
}
