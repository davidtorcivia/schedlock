package notifications

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"

	"github.com/dtorcivia/schedlock/internal/config"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/util"
)

// Manager handles multi-provider notification delivery.
type Manager struct {
	db        *database.DB
	config    *config.Config
	providers []Provider
	mu        sync.RWMutex
}

// NewManager creates a new notification manager.
func NewManager(db *database.DB, cfg *config.Config) *Manager {
	return &Manager{
		db:        db,
		config:    cfg,
		providers: make([]Provider, 0),
	}
}

// RegisterProvider adds a notification provider.
func (m *Manager) RegisterProvider(p Provider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providers = append(m.providers, p)
	util.Info("Registered notification provider", "provider", p.Name(), "enabled", p.Enabled())
}

// GetProviders returns all registered providers.
func (m *Manager) GetProviders() []Provider {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.providers
}

// GetEnabledProviders returns only enabled providers.
func (m *Manager) GetEnabledProviders() []Provider {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var enabled []Provider
	for _, p := range m.providers {
		if p.Enabled() {
			enabled = append(enabled, p)
		}
	}
	return enabled
}

// SendApprovalRequest sends approval notifications to all enabled providers.
func (m *Manager) SendApprovalRequest(ctx context.Context, notification *ApprovalNotification) error {
	providers := m.GetEnabledProviders()
	if len(providers) == 0 {
		util.Warn("No notification providers enabled")
		return nil
	}

	m.populateApprovalURLs(notification)

	var lastErr error
	successCount := 0

	for _, provider := range providers {
		messageID, err := provider.SendApproval(ctx, notification)
		if err != nil {
			util.Error("Failed to send notification",
				"provider", provider.Name(),
				"request_id", notification.RequestID,
				"error", err,
			)
			lastErr = err
			m.logNotification(ctx, notification.RequestID, provider.Name(), "", database.NotificationFailed, err.Error())
			continue
		}

		m.logNotification(ctx, notification.RequestID, provider.Name(), messageID, database.NotificationSent, "")
		successCount++

		util.Info("Sent approval notification",
			"provider", provider.Name(),
			"request_id", notification.RequestID,
			"message_id", messageID,
		)
	}

	if successCount == 0 && lastErr != nil {
		return fmt.Errorf("all notification providers failed: %w", lastErr)
	}

	return nil
}

// SendResult sends result notifications to all enabled providers.
func (m *Manager) SendResult(ctx context.Context, notification *ResultNotification) error {
	providers := m.GetEnabledProviders()

	for _, provider := range providers {
		if err := provider.SendResult(ctx, notification); err != nil {
			util.Error("Failed to send result notification",
				"provider", provider.Name(),
				"request_id", notification.RequestID,
				"error", err,
			)
		}
	}

	return nil
}

// TestProvider sends a test notification to a specific provider.
func (m *Manager) TestProvider(ctx context.Context, providerName string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, p := range m.providers {
		if p.Name() == providerName {
			if !p.Enabled() {
				return fmt.Errorf("provider %s is not enabled", providerName)
			}
			return p.SendTest(ctx)
		}
	}

	return fmt.Errorf("provider %s not found", providerName)
}

// GetProviderByName returns a provider by name.
func (m *Manager) GetProviderByName(name string) Provider {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, p := range m.providers {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

// populateApprovalURLs fills in callback and review URLs if missing.
func (m *Manager) populateApprovalURLs(notification *ApprovalNotification) {
	if notification == nil {
		return
	}

	baseURL := strings.TrimRight(m.config.Server.BaseURL, "/")
	if baseURL == "" {
		return
	}

	if notification.WebURL == "" {
		notification.WebURL = fmt.Sprintf("%s/requests/%s", baseURL, notification.RequestID)
	}

	if notification.DecisionToken == "" {
		return
	}

	// API callback URLs (for background HTTP actions like ntfy)
	if notification.ApproveURL == "" {
		notification.ApproveURL = fmt.Sprintf("%s/api/callback/approve/%s", baseURL, notification.DecisionToken)
	}
	if notification.DenyURL == "" {
		notification.DenyURL = fmt.Sprintf("%s/api/callback/deny/%s", baseURL, notification.DecisionToken)
	}
	if notification.SuggestURL == "" {
		notification.SuggestURL = fmt.Sprintf("%s/api/callback/suggest/%s", baseURL, notification.DecisionToken)
	}
	// Public approval page URL (for browser links like Pushover)
	if notification.ApprovePageURL == "" {
		notification.ApprovePageURL = fmt.Sprintf("%s/approve/%s", baseURL, notification.DecisionToken)
	}
}

// logNotification logs a notification to the database.
func (m *Manager) logNotification(ctx context.Context, requestID, provider, messageID, status, errorMsg string) {
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO notification_log (request_id, provider, status, message_id, error)
		VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''))
	`, requestID, provider, status, messageID, errorMsg)

	if err != nil {
		util.Error("Failed to log notification", "error", err)
	}
}

// GetNotificationLog retrieves notification logs for a request.
func (m *Manager) GetNotificationLog(ctx context.Context, requestID string) ([]NotificationLog, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, request_id, provider, status, message_id, sent_at, callback_at, error, response
		FROM notification_log
		WHERE request_id = ?
		ORDER BY sent_at DESC
	`, requestID)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []NotificationLog
	for rows.Next() {
		var log NotificationLog
		var messageID, errorMsg sql.NullString
		var sentAt, callbackAt sql.NullString
		var response sql.NullString

		if err := rows.Scan(
			&log.ID, &log.RequestID, &log.Provider, &log.Status,
			&messageID, &sentAt, &callbackAt, &errorMsg, &response,
		); err != nil {
			return nil, err
		}

		if messageID.Valid {
			log.MessageID = messageID.String
		}
		if errorMsg.Valid {
			log.ErrorMessage = errorMsg.String
		}
		if sentAt.Valid {
			log.SentAt, _ = util.ParseSQLiteTimestamp(sentAt.String)
		}
		if callbackAt.Valid {
			t, _ := util.ParseSQLiteTimestamp(callbackAt.String)
			log.CallbackAt = &t
		}
		if response.Valid {
			log.Response = []byte(response.String)
		}

		logs = append(logs, log)
	}

	return logs, rows.Err()
}

// FindByMessageID finds a notification log by provider and message ID.
func (m *Manager) FindByMessageID(ctx context.Context, provider, messageID string) (*NotificationLog, error) {
	var log NotificationLog
	var msgID, errorMsg sql.NullString
	var sentAt, callbackAt sql.NullString
	var response sql.NullString

	err := m.db.QueryRowContext(ctx, `
		SELECT id, request_id, provider, status, message_id, sent_at, callback_at, error, response
		FROM notification_log
		WHERE provider = ? AND message_id = ?
	`, provider, messageID).Scan(
		&log.ID, &log.RequestID, &log.Provider, &log.Status,
		&msgID, &sentAt, &callbackAt, &errorMsg, &response,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if msgID.Valid {
		log.MessageID = msgID.String
	}
	if errorMsg.Valid {
		log.ErrorMessage = errorMsg.String
	}
	if sentAt.Valid {
		log.SentAt, _ = util.ParseSQLiteTimestamp(sentAt.String)
	}
	if callbackAt.Valid {
		t, _ := util.ParseSQLiteTimestamp(callbackAt.String)
		log.CallbackAt = &t
	}
	if response.Valid {
		log.Response = []byte(response.String)
	}

	return &log, nil
}

// MarkCallback marks a notification as having received a callback.
func (m *Manager) MarkCallback(ctx context.Context, provider, requestID, messageID string) {
	if provider == "" {
		return
	}

	query := `
		UPDATE notification_log
		SET status = ?, callback_at = datetime('now')
		WHERE id = (
			SELECT id FROM notification_log
			WHERE provider = ? AND request_id = ?
			AND (message_id = ? OR ? = '')
			ORDER BY sent_at DESC
			LIMIT 1
		)
	`
	_, err := m.db.ExecContext(ctx, query, database.NotificationCallbackReceived, provider, requestID, messageID, messageID)
	if err != nil {
		util.Error("Failed to mark notification callback", "error", err)
	}
}
