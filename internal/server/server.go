// Package server provides the HTTP server and routing for SchedLock.
package server

import (
	"context"
	"net/http"
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
	"github.com/dtorcivia/schedlock/internal/requests"
	"github.com/dtorcivia/schedlock/internal/server/middleware"
	"github.com/dtorcivia/schedlock/internal/settings"
	"github.com/dtorcivia/schedlock/internal/tokens"
	"github.com/dtorcivia/schedlock/internal/util"
	"github.com/dtorcivia/schedlock/internal/web"
	"github.com/dtorcivia/schedlock/internal/webhook"
	"github.com/dtorcivia/schedlock/internal/workers"
)

// Server is the main HTTP server for SchedLock.
type Server struct {
	config          *config.Config
	db              *database.DB
	router          *http.ServeMux
	apiKeyRepo      *apikeys.Repository
	requestRepo     *requests.Repository
	tokenRepo       *tokens.Repository
	apiKeyHasher    *crypto.APIKeyHasher
	encryptor       *crypto.Encryptor
	rateLimiter     *middleware.RateLimiter
	displayFormat   *util.DisplayFormatter
	oauthMgr        *google.OAuthManager
	calendarClient  *google.CalendarClient
	engine          *engine.Engine
	notificationMgr *notifications.Manager
	webhookClient   *webhook.Client
	auditLogger     *engine.AuditLogger
	sessionMgr      *web.SessionManager
	apiHandler      *api.Handler
	webHandler      *web.Handler
	timeoutWorker   *workers.TimeoutWorker
	cleanupWorker   *workers.CleanupWorker
	telegramHandler *telegram.WebhookHandler
}

// New creates a new Server instance.
func New(cfg *config.Config, db *database.DB) (*Server, error) {
	// Initialize crypto components
	apiKeyHasher, err := crypto.NewAPIKeyHasher(cfg.Auth.SecretKey)
	if err != nil {
		return nil, err
	}

	encryptor, err := crypto.NewEncryptor(cfg.Auth.EncryptionKey)
	if err != nil {
		return nil, err
	}

	// Initialize display formatter
	displayFormat, err := util.NewDisplayFormatter(
		cfg.Display.Timezone,
		cfg.Display.DateFormat,
		cfg.Display.TimeFormat,
		cfg.Display.DatetimeFormat,
	)
	if err != nil {
		return nil, err
	}
	util.SetDefaultFormatter(displayFormat)

	// Initialize repositories
	apiKeyRepo := apikeys.NewRepository(db, apiKeyHasher)
	requestRepo := requests.NewRepository(db)
	tokenRepo := tokens.NewRepository(db)

	// Initialize rate limiter
	rateLimiter := middleware.NewRateLimiter(cfg.RateLimits)

	// Initialize OAuth manager
	oauthMgr := google.NewOAuthManager(cfg, db, encryptor)

	// Initialize Calendar client
	calendarClient := google.NewCalendarClient(oauthMgr)

	// Initialize audit logger
	auditLogger := engine.NewAuditLogger(db)

	// Initialize engine
	eng := engine.NewEngine(cfg, requestRepo, calendarClient, auditLogger, tokenRepo)

	// Initialize notification credentials store
	credentialsStore, err := notifications.NewCredentialsStore(db, cfg.Auth.EncryptionKey)
	if err != nil {
		return nil, err
	}

	// Wire credential store to OAuth manager for dynamic credential updates
	oauthMgr.SetCredentialStore(credentialsStore)

	// Initialize notification manager
	notificationMgr := notifications.NewManager(db, cfg)

	// Register notification providers
	if cfg.Notifications.Ntfy.Enabled {
		notificationMgr.RegisterProvider(ntfy.NewProvider(&cfg.Notifications.Ntfy))
	}
	if cfg.Notifications.Pushover.Enabled {
		notificationMgr.RegisterProvider(pushover.NewProvider(&cfg.Notifications.Pushover))
	}

	var telegramProvider *telegram.Provider
	if cfg.Notifications.Telegram.Enabled {
		telegramProvider = telegram.NewProvider(&cfg.Notifications.Telegram)
		notificationMgr.RegisterProvider(telegramProvider)
	}

	// Set notification manager on engine
	eng.SetNotifier(notificationMgr)

	// Initialize webhook client
	webhookClient := webhook.NewClient(&cfg.Moltbot, db)
	eng.SetWebhookClient(webhookClient)

	// Initialize session manager
	sessionMgr := web.NewSessionManager(db, &cfg.Auth)

	// Initialize settings store
	settingsStore := settings.NewStore(db)

	// Initialize API handler
	apiHandler := api.NewHandler(
		cfg,
		eng,
		requestRepo,
		apiKeyRepo,
		tokenRepo,
		calendarClient,
		notificationMgr,
		auditLogger,
	)

	// Initialize web handler
	webHandler, err := web.NewHandler(
		cfg,
		sessionMgr,
		requestRepo,
		apiKeyRepo,
		tokenRepo,
		settingsStore,
		credentialsStore,
		eng,
		oauthMgr,
		notificationMgr,
		auditLogger,
	)
	if err != nil {
		return nil, err
	}

	// Initialize workers
	timeoutWorker := workers.NewTimeoutWorker(requestRepo, db, eng, &cfg.Approval, 30*time.Second)
	cleanupWorker := workers.NewCleanupWorker(db, &cfg.Retention)

	s := &Server{
		config:          cfg,
		db:              db,
		router:          http.NewServeMux(),
		apiKeyRepo:      apiKeyRepo,
		requestRepo:     requestRepo,
		tokenRepo:       tokenRepo,
		apiKeyHasher:    apiKeyHasher,
		encryptor:       encryptor,
		rateLimiter:     rateLimiter,
		displayFormat:   displayFormat,
		oauthMgr:        oauthMgr,
		calendarClient:  calendarClient,
		engine:          eng,
		notificationMgr: notificationMgr,
		webhookClient:   webhookClient,
		auditLogger:     auditLogger,
		sessionMgr:      sessionMgr,
		apiHandler:      apiHandler,
		webHandler:      webHandler,
		timeoutWorker:   timeoutWorker,
		cleanupWorker:   cleanupWorker,
	}

	// Initialize Telegram webhook handler if enabled
	if telegramProvider != nil {
		s.telegramHandler = telegram.NewWebhookHandler(telegramProvider, apiHandler, notificationMgr)
	}

	// Setup routes
	s.setupRoutes()

	return s, nil
}

// Handler returns the HTTP handler with all middleware applied.
func (s *Server) Handler() http.Handler {
	// Build middleware chain (applied in reverse order)
	var handler http.Handler = s.router

	// Recovery middleware (outermost - catches panics)
	handler = middleware.Recovery(handler)

	// Logging middleware
	handler = middleware.Logging(handler)

	// CORS middleware (if needed for external API access)
	handler = middleware.CORS(handler)

	// Security headers
	handler = middleware.SecurityHeaders(handler)

	return handler
}

// StartBackgroundWorkers starts all background workers.
func (s *Server) StartBackgroundWorkers(ctx context.Context) error {
	// Start engine execution queue
	s.engine.Start(ctx)

	// Start timeout worker
	go s.timeoutWorker.Start(ctx)

	// Start cleanup worker
	go s.cleanupWorker.Start(ctx)

	// Start webhook retry worker
	go s.webhookClient.StartRetryWorker(ctx)

	// Register Telegram webhook if enabled
	if s.config.Notifications.Telegram.Enabled && s.config.Notifications.Telegram.BotToken != "" && s.config.Notifications.Telegram.AutoRegisterWebhook {
		webhookURL := s.config.Server.BaseURL + s.config.Notifications.Telegram.WebhookPath
		provider := s.notificationMgr.GetProviderByName("telegram")
		if tgProvider, ok := provider.(*telegram.Provider); ok {
			tgProvider.RegisterWebhookAsync(webhookURL)
		}
	}

	util.Info("Background workers started")
	return nil
}

// Stop gracefully stops the server.
func (s *Server) Stop() {
	s.engine.Stop()
}

// DB returns the database connection.
func (s *Server) DB() *database.DB {
	return s.db
}

// Config returns the server configuration.
func (s *Server) Config() *config.Config {
	return s.config
}

// APIKeyRepo returns the API key repository.
func (s *Server) APIKeyRepo() *apikeys.Repository {
	return s.apiKeyRepo
}

// Encryptor returns the encryption handler.
func (s *Server) Encryptor() *crypto.Encryptor {
	return s.encryptor
}

// DisplayFormat returns the display formatter.
func (s *Server) DisplayFormat() *util.DisplayFormatter {
	return s.displayFormat
}
