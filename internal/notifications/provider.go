// Package notifications provides multi-provider notification delivery.
package notifications

import (
	"context"
)

// Provider defines the interface for notification providers.
type Provider interface {
	// Name returns the provider name (e.g., "ntfy", "pushover", "telegram").
	Name() string

	// Enabled returns whether the provider is configured and enabled.
	Enabled() bool

	// SendApproval sends an approval request notification.
	// Returns a message ID that can be used for reply tracking.
	SendApproval(ctx context.Context, notification *ApprovalNotification) (messageID string, err error)

	// SendResult sends a notification about request completion.
	SendResult(ctx context.Context, notification *ResultNotification) error

	// SendTest sends a test notification.
	SendTest(ctx context.Context) error
}

// CallbackHandler handles approval callbacks from providers that support them.
type CallbackHandler interface {
	// HandleCallback processes an approval callback.
	HandleCallback(ctx context.Context, callback *Callback) error
}
