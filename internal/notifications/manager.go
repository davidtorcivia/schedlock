package notifications

import (
	"context"
	"fmt"
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
			m.logNotification(ctx, notification.RequestID, provider.Name(), "", err.Error())
			continue
		}

		m.logNotification(ctx, notification.RequestID, provider.Name(), messageID, "")
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

// logNotification logs a notification to the database.
func (m *Manager) logNotification(ctx context.Context, requestID, provider, messageID, errorMsg string) {
	var errPtr *string
	if errorMsg != "" {
		errPtr = &errorMsg
	}

	_, err := m.db.ExecContext(ctx, `
		INSERT INTO notification_log (request_id, provider, message_id, error_message)
		VALUES (?, ?, NULLIF(?, ''), ?)
	`, requestID, provider, messageID, errPtr)

	if err != nil {
		util.Error("Failed to log notification", "error", err)
	}
}

// GetNotificationLog retrieves notification logs for a request.
func (m *Manager) GetNotificationLog(ctx context.Context, requestID string) ([]NotificationLog, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, request_id, provider, message_id, sent_at, delivered_at, error_message
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
		var messageID, errorMsg *string
		var sentAt, deliveredAt *string

		if err := rows.Scan(&log.ID, &log.RequestID, &log.Provider, &messageID, &sentAt, &deliveredAt, &errorMsg); err != nil {
			return nil, err
		}

		if messageID != nil {
			log.MessageID = *messageID
		}
		if errorMsg != nil {
			log.ErrorMessage = *errorMsg
		}
		if sentAt != nil {
			log.SentAt, _ = util.ParseSQLiteTimestamp(*sentAt)
		}
		if deliveredAt != nil {
			t, _ := util.ParseSQLiteTimestamp(*deliveredAt)
			log.DeliveredAt = &t
		}

		logs = append(logs, log)
	}

	return logs, rows.Err()
}

// FindByMessageID finds a notification log by provider and message ID.
func (m *Manager) FindByMessageID(ctx context.Context, provider, messageID string) (*NotificationLog, error) {
	var log NotificationLog
	var msgID, errorMsg *string
	var sentAt, deliveredAt *string

	err := m.db.QueryRowContext(ctx, `
		SELECT id, request_id, provider, message_id, sent_at, delivered_at, error_message
		FROM notification_log
		WHERE provider = ? AND message_id = ?
	`, provider, messageID).Scan(&log.ID, &log.RequestID, &log.Provider, &msgID, &sentAt, &deliveredAt, &errorMsg)

	if err != nil {
		return nil, err
	}

	if msgID != nil {
		log.MessageID = *msgID
	}
	if errorMsg != nil {
		log.ErrorMessage = *errorMsg
	}
	if sentAt != nil {
		log.SentAt, _ = util.ParseSQLiteTimestamp(*sentAt)
	}
	if deliveredAt != nil {
		t, _ := util.ParseSQLiteTimestamp(*deliveredAt)
		log.DeliveredAt = &t
	}

	return &log, nil
}
