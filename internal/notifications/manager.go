package notifications

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/dtorcivia/schedlock/internal/config"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/util"
)

// ErrProviderNotFound is returned when an unknown provider is addressed.
var ErrProviderNotFound = errors.New("notification provider not found")

// BaseURLProvider supplies the current public base URL, which changes when the
// operator edits it in settings.
type BaseURLProvider func() string

// Manager fans approval notifications out to every enabled provider and records
// what was delivered.
type Manager struct {
	db      *database.DB
	store   *CredentialsStore
	baseURL BaseURLProvider

	mu        sync.RWMutex
	providers []Provider
	// fallback holds the static (file/environment) configuration used when a
	// provider has no stored credentials.
	fallback config.NotificationsConfig
}

// NewManager creates a notification manager.
func NewManager(db *database.DB, store *CredentialsStore, fallback config.NotificationsConfig, baseURL BaseURLProvider) *Manager {
	return &Manager{
		db:       db,
		store:    store,
		baseURL:  baseURL,
		fallback: fallback,
	}
}

// RegisterProvider adds a provider to the fan-out set.
func (m *Manager) RegisterProvider(p Provider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providers = append(m.providers, p)
}

// Providers returns every registered provider.
func (m *Manager) Providers() []Provider {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]Provider(nil), m.providers...)
}

// EnabledProviders returns the providers currently configured to deliver.
func (m *Manager) EnabledProviders() []Provider {
	var enabled []Provider
	for _, p := range m.Providers() {
		if p.Enabled() {
			enabled = append(enabled, p)
		}
	}
	return enabled
}

// ProviderByName returns a registered provider, or nil.
func (m *Manager) ProviderByName(name string) Provider {
	for _, p := range m.Providers() {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

// Reload applies stored credentials to every provider.
//
// Notification settings are edited through the web UI at runtime. Without this
// step a provider would keep using whatever configuration existed at startup,
// so enabling a channel or rotating a token would appear to succeed but change
// nothing until the process restarted.
func (m *Manager) Reload(ctx context.Context) error {
	stored := map[string]*ProviderCredentials{}
	if m.store != nil {
		loaded, err := m.store.LoadAll(ctx)
		if err != nil {
			return fmt.Errorf("failed to load notification credentials: %w", err)
		}
		stored = loaded
	}

	for _, provider := range m.Providers() {
		name := provider.Name()
		creds, ok := stored[name]
		if !ok || creds.Credentials == nil {
			// Nothing stored: fall back to file/environment configuration so a
			// deployment configured purely by environment still works.
			creds = m.fallbackFor(name)
		}
		provider.Configure(creds)

		util.Debug("Configured notification provider", "provider", name, "enabled", provider.Enabled())
	}

	return nil
}

// fallbackFor converts static configuration into a credentials snapshot.
func (m *Manager) fallbackFor(name string) *ProviderCredentials {
	switch name {
	case ProviderNtfy:
		c := m.fallback.Ntfy
		return &ProviderCredentials{
			Provider: name,
			Enabled:  c.Enabled,
			Credentials: &NtfyCredentials{
				ServerURL:      c.Server,
				Topic:          c.Topic,
				Token:          c.Token,
				Priority:       c.Priority,
				MinimalContent: c.MinimalContent,
			},
		}
	case ProviderPushover:
		c := m.fallback.Pushover
		return &ProviderCredentials{
			Provider: name,
			Enabled:  c.Enabled,
			Credentials: &PushoverCredentials{
				AppToken: c.AppToken,
				UserKey:  c.UserKey,
				Priority: c.Priority,
				Sound:    c.Sound,
			},
		}
	case ProviderTelegram:
		c := m.fallback.Telegram
		return &ProviderCredentials{
			Provider: name,
			Enabled:  c.Enabled,
			Credentials: &TelegramCredentials{
				BotToken:      c.BotToken,
				ChatID:        c.ChatID,
				WebhookSecret: c.WebhookSecret,
			},
		}
	case ProviderWebhook:
		c := m.fallback.Webhook
		return &ProviderCredentials{
			Provider: name,
			Enabled:  c.Enabled,
			Credentials: &WebhookCredentials{
				URL:            c.URL,
				Secret:         c.Secret,
				TimeoutSeconds: c.TimeoutSeconds,
			},
		}
	default:
		return &ProviderCredentials{Provider: name}
	}
}

// SendApprovalRequest delivers an approval request to every enabled provider.
//
// Delivery is best-effort per provider: one channel being down must not stop
// the others from reaching the approver. An error is returned only when no
// provider succeeded.
func (m *Manager) SendApprovalRequest(ctx context.Context, notification *ApprovalNotification) error {
	providers := m.EnabledProviders()
	if len(providers) == 0 {
		util.Warn("No notification providers enabled; approval is only reachable in the web UI",
			"request_id", notification.RequestID)
		return nil
	}

	m.populateURLs(notification)

	var (
		lastErr      error
		successCount int
	)

	for _, provider := range providers {
		messageID, err := provider.SendApproval(ctx, notification)
		if err != nil {
			util.Error("Failed to send notification",
				"provider", provider.Name(), "request_id", notification.RequestID, "error", err)
			lastErr = err
			m.logDelivery(ctx, notification.RequestID, provider.Name(), "", database.NotificationFailed, err.Error())
			continue
		}

		m.logDelivery(ctx, notification.RequestID, provider.Name(), messageID, database.NotificationSent, "")
		successCount++

		util.Info("Sent approval notification",
			"provider", provider.Name(), "request_id", notification.RequestID, "message_id", messageID)
	}

	if successCount == 0 && lastErr != nil {
		return fmt.Errorf("all notification providers failed: %w", lastErr)
	}
	return nil
}

// SendResult reports the outcome of a request to every enabled provider, so an
// approver learns whether the operation they authorized actually succeeded.
func (m *Manager) SendResult(ctx context.Context, notification *ResultNotification) {
	for _, provider := range m.EnabledProviders() {
		if err := provider.SendResult(ctx, notification); err != nil {
			util.Warn("Failed to send result notification",
				"provider", provider.Name(), "request_id", notification.RequestID, "error", err)
		}
	}
}

// TestProvider sends a test notification through one provider.
func (m *Manager) TestProvider(ctx context.Context, providerName string) error {
	provider := m.ProviderByName(providerName)
	if provider == nil {
		return fmt.Errorf("%w: %s", ErrProviderNotFound, providerName)
	}
	if !provider.Enabled() {
		return fmt.Errorf("provider %s is not enabled", providerName)
	}
	return provider.SendTest(ctx)
}

// populateURLs fills in the action links a notification offers.
func (m *Manager) populateURLs(notification *ApprovalNotification) {
	if notification == nil || m.baseURL == nil {
		return
	}

	baseURL := strings.TrimRight(m.baseURL(), "/")
	if baseURL == "" {
		return
	}

	notification.WebURL = fmt.Sprintf("%s/requests/%s", baseURL, notification.RequestID)

	if notification.DecisionToken == "" {
		return
	}

	// Direct-action endpoints, for clients that POST from a notification action.
	notification.ApproveURL = fmt.Sprintf("%s/api/callback/approve/%s", baseURL, notification.DecisionToken)
	notification.DenyURL = fmt.Sprintf("%s/api/callback/deny/%s", baseURL, notification.DecisionToken)
	notification.SuggestURL = fmt.Sprintf("%s/api/callback/suggest/%s", baseURL, notification.DecisionToken)
	// The page a human opens, which shows the request before asking to confirm.
	notification.ApprovePageURL = fmt.Sprintf("%s/approve/%s", baseURL, notification.DecisionToken)
}

// logDelivery records a delivery attempt.
func (m *Manager) logDelivery(ctx context.Context, requestID, provider, messageID, status, errorMsg string) {
	if _, err := m.db.ExecContext(context.WithoutCancel(ctx), `
		INSERT INTO notification_log (request_id, provider, status, message_id, error)
		VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''))
	`, requestID, provider, status, messageID, errorMsg); err != nil {
		util.Error("Failed to log notification delivery", "error", err, "provider", provider)
	}
}

// FindByMessageID locates the delivery record for a provider message, which is
// how a chat reply is matched back to the request it concerns.
func (m *Manager) FindByMessageID(ctx context.Context, provider, messageID string) (*DeliveryRecord, error) {
	var (
		record  DeliveryRecord
		msgID   sql.NullString
		sentAt  sql.NullString
		callbck sql.NullString
		errMsg  sql.NullString
	)

	err := m.db.QueryRowContext(ctx, `
		SELECT id, request_id, provider, status, message_id, sent_at, callback_at, error
		FROM notification_log
		WHERE provider = ? AND message_id = ?
		ORDER BY sent_at DESC
		LIMIT 1
	`, provider, messageID).Scan(
		&record.ID, &record.RequestID, &record.Provider, &record.Status,
		&msgID, &sentAt, &callbck, &errMsg,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	record.MessageID = msgID.String
	record.ErrorMessage = errMsg.String
	if ts := database.NullTimeText(sentAt); ts.Valid {
		record.SentAt = ts.Time
	}
	if ts := database.NullTimeText(callbck); ts.Valid {
		t := ts.Time
		record.CallbackAt = &t
	}

	return &record, nil
}

// MarkCallback records that a decision arrived through a provider.
func (m *Manager) MarkCallback(ctx context.Context, provider, requestID, messageID string) {
	if provider == "" || requestID == "" {
		return
	}

	if _, err := m.db.ExecContext(context.WithoutCancel(ctx), `
		UPDATE notification_log
		SET status = ?, callback_at = datetime('now')
		WHERE id = (
			SELECT id FROM notification_log
			WHERE provider = ? AND request_id = ?
			AND (? = '' OR message_id = ?)
			ORDER BY sent_at DESC
			LIMIT 1
		)
	`, database.NotificationCallbackReceived, provider, requestID, messageID, messageID); err != nil {
		util.Error("Failed to mark notification callback", "error", err)
	}
}
