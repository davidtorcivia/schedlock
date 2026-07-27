// Package server wires the application together and serves HTTP.
package server

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dtorcivia/schedlock/internal/api"
	"github.com/dtorcivia/schedlock/internal/apikeys"
	"github.com/dtorcivia/schedlock/internal/config"
	"github.com/dtorcivia/schedlock/internal/crypto"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/engine"
	"github.com/dtorcivia/schedlock/internal/google"
	"github.com/dtorcivia/schedlock/internal/notifications"
	"github.com/dtorcivia/schedlock/internal/notifications/ntfy"
	"github.com/dtorcivia/schedlock/internal/notifications/pushover"
	"github.com/dtorcivia/schedlock/internal/notifications/telegram"
	webhooknotify "github.com/dtorcivia/schedlock/internal/notifications/webhook"
	"github.com/dtorcivia/schedlock/internal/requests"
	"github.com/dtorcivia/schedlock/internal/server/middleware"
	"github.com/dtorcivia/schedlock/internal/settings"
	"github.com/dtorcivia/schedlock/internal/tokens"
	"github.com/dtorcivia/schedlock/internal/util"
	"github.com/dtorcivia/schedlock/internal/web"
	"github.com/dtorcivia/schedlock/internal/webhook"
	"github.com/dtorcivia/schedlock/internal/workers"
)

// Server owns every long-lived component and the HTTP router.
type Server struct {
	config          *config.Config
	db              *database.DB
	router          *http.ServeMux
	rateLimiter     *middleware.RateLimiter
	clientIP        *middleware.ClientIPResolver
	usageRecorder   *apikeys.UsageRecorder
	oauthMgr        *google.OAuthManager
	engine          *engine.Engine
	notificationMgr *notifications.Manager
	webhookClient   *webhook.Client
	apiHandler      *api.Handler
	webHandler      *web.Handler
	timeoutWorker   *workers.TimeoutWorker
	cleanupWorker   *workers.CleanupWorker
	telegram        *telegram.Provider
	telegramHandler *telegram.WebhookHandler

	workers sync.WaitGroup
}

// New builds a server from configuration and an open database.
func New(cfg *config.Config, db *database.DB) (*Server, error) {
	apiKeyHasher, err := crypto.NewAPIKeyHasher(cfg.Auth.SecretKey)
	if err != nil {
		return nil, err
	}

	encryptor, err := crypto.NewEncryptor(cfg.Auth.EncryptionKey)
	if err != nil {
		return nil, err
	}

	displayFormat, err := util.NewDisplayFormatter(
		cfg.Display.Timezone, cfg.Display.DateFormat, cfg.Display.TimeFormat, cfg.Display.DatetimeFormat)
	if err != nil {
		return nil, err
	}
	util.SetDefaultFormatter(displayFormat)

	clientIP, invalidProxies := middleware.NewClientIPResolver(cfg.Server.TrustedProxies)
	for _, entry := range invalidProxies {
		util.Warn("Ignoring unparsable trusted proxy entry", "value", entry)
	}

	apiKeyRepo := apikeys.NewRepository(db, apiKeyHasher)
	requestRepo := requests.NewRepository(db)
	tokenRepo := tokens.NewRepository(db)
	settingsStore := settings.NewStore(db)
	auditLogger := engine.NewAuditLogger(db)

	credentialsStore, err := notifications.NewCredentialsStore(db, cfg.Auth.EncryptionKey)
	if err != nil {
		return nil, err
	}

	oauthMgr := google.NewOAuthManager(cfg, db, encryptor)
	oauthMgr.SetCredentialStore(credentialsStore)
	calendarClient := google.NewCalendarClient(oauthMgr)

	eng := engine.NewEngine(cfg, requestRepo, calendarClient, auditLogger, tokenRepo)

	// The base URL is read through a closure because the operator can change it
	// in settings, and notification links must use the current value.
	notificationMgr := notifications.NewManager(db, credentialsStore, cfg.Notifications,
		func() string { return cfg.Server.BaseURL })

	telegramProvider := telegram.NewProvider()
	notificationMgr.RegisterProvider(ntfy.NewProvider())
	notificationMgr.RegisterProvider(pushover.NewProvider())
	notificationMgr.RegisterProvider(telegramProvider)
	notificationMgr.RegisterProvider(webhooknotify.NewProvider())

	// Providers start from stored credentials, falling back to file and
	// environment configuration.
	if err := notificationMgr.Reload(context.Background()); err != nil {
		util.Warn("Failed to load notification credentials", "error", err)
	}

	eng.SetNotifier(notificationMgr)

	webhookClient := webhook.NewClient(&cfg.Moltbot, db)
	eng.SetWebhookClient(webhookClient)

	sessionMgr := web.NewSessionManager(db, &cfg.Auth)
	usageRecorder := apikeys.NewUsageRecorder(apiKeyRepo, 30*time.Second)

	apiHandler := api.NewHandler(cfg, eng, requestRepo, apiKeyRepo, tokenRepo,
		calendarClient, notificationMgr, auditLogger)

	webHandler, err := web.NewHandler(web.Dependencies{
		Config:           cfg,
		SessionManager:   sessionMgr,
		RequestRepo:      requestRepo,
		APIKeyRepo:       apiKeyRepo,
		TokenRepo:        tokenRepo,
		SettingsStore:    settingsStore,
		CredentialsStore: credentialsStore,
		Engine:           eng,
		OAuthManager:     oauthMgr,
		CalendarClient:   calendarClient,
		NotificationMgr:  notificationMgr,
		AuditLogger:      auditLogger,
		ClientIP:         clientIP.ClientIP,
	})
	if err != nil {
		return nil, err
	}

	s := &Server{
		config:          cfg,
		db:              db,
		router:          http.NewServeMux(),
		rateLimiter:     middleware.NewRateLimiter(cfg.RateLimits),
		clientIP:        clientIP,
		usageRecorder:   usageRecorder,
		oauthMgr:        oauthMgr,
		engine:          eng,
		notificationMgr: notificationMgr,
		webhookClient:   webhookClient,
		apiHandler:      apiHandler,
		webHandler:      webHandler,
		timeoutWorker:   workers.NewTimeoutWorker(requestRepo, eng, auditLogger, &cfg.Approval, 30*time.Second),
		cleanupWorker:   workers.NewCleanupWorker(db, &cfg.Retention),
		telegram:        telegramProvider,
		telegramHandler: telegram.NewWebhookHandler(telegramProvider, apiHandler, notificationMgr),
	}

	s.setupRoutes()
	return s, nil
}

// Handler returns the fully wrapped HTTP handler.
func (s *Server) Handler() http.Handler {
	useTLS := strings.HasPrefix(s.config.Server.BaseURL, "https://")

	// Middleware is applied outermost-last: recovery wraps everything, so a
	// panic anywhere below still produces a response.
	var handler http.Handler = s.router
	handler = middleware.SecurityHeaders(useTLS)(handler)
	handler = middleware.CORS(handler)
	handler = middleware.Logging(s.clientIP.ClientIP)(handler)
	handler = middleware.Recovery(handler)
	return handler
}

// StartBackgroundWorkers launches every background goroutine.
func (s *Server) StartBackgroundWorkers(ctx context.Context) {
	s.engine.Start(ctx)

	s.goWorker(func() { s.timeoutWorker.Start(ctx) })
	s.goWorker(func() { s.cleanupWorker.Start(ctx) })
	s.goWorker(func() { s.webhookClient.StartRetryWorker(ctx) })
	s.goWorker(func() { s.usageRecorder.Run(ctx) })
	s.goWorker(func() { s.rateLimiter.StartCleanup(ctx, 10*time.Minute) })

	s.registerTelegramWebhook(ctx)

	util.Info("Background workers started")
}

func (s *Server) goWorker(fn func()) {
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		fn()
	}()
}

// registerTelegramWebhook points Telegram at this server when configured.
func (s *Server) registerTelegramWebhook(ctx context.Context) {
	if !s.config.Notifications.Telegram.AutoRegisterWebhook || !s.telegram.Enabled() {
		return
	}
	if s.telegram.WebhookSecret() == "" {
		util.Warn("Not registering the Telegram webhook: no webhook secret is configured. " +
			"Set one in Settings so incoming updates can be authenticated.")
		return
	}
	if s.config.Server.BaseURL == "" {
		util.Warn("Not registering the Telegram webhook: no public base URL is configured")
		return
	}

	s.telegram.RegisterWebhookAsync(ctx, s.config.Server.BaseURL+s.config.Notifications.Telegram.WebhookPath)
}

// Shutdown stops background work, draining in-flight executions first.
//
// Order matters: the execution queue is drained before the worker context is
// cancelled, so a request that has been claimed for execution reaches a
// terminal state instead of being left marked "executing" forever.
func (s *Server) Shutdown(ctx context.Context) {
	s.engine.Stop(ctx)

	done := make(chan struct{})
	go func() {
		s.workers.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		util.Warn("Background workers did not stop before the shutdown deadline")
	}
}
