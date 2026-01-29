// Package main is the entry point for the SchedLock Calendar Proxy server.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dtorcivia/schedlock/internal/config"
	schedcrypto "github.com/dtorcivia/schedlock/internal/crypto"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/server"
	"github.com/dtorcivia/schedlock/internal/settings"
	"github.com/dtorcivia/schedlock/internal/util"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "hash-password":
			if len(os.Args) < 3 {
				fmt.Fprintln(os.Stderr, "Usage: schedlock hash-password \"YourPassword\"")
				os.Exit(1)
			}
			hash, err := schedcrypto.HashPassword(os.Args[2])
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error hashing password: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(hash)
			return
		}
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Initialize logger
	logger := util.NewLogger(cfg.Logging.Level, cfg.Logging.Format)
	util.SetDefaultLogger(logger)

	logger.Info("Starting SchedLock Calendar Proxy",
		"version", "1.0.0",
		"port", cfg.Server.Port,
	)

	// Open database
	db, err := database.Open(cfg.Database.Path)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer db.Close()

	logger.Info("Database initialized",
		"path", cfg.Database.Path,
	)

	// Load runtime settings (database overrides)
	settingsStore := settings.NewStore(db)
	runtimeSettings, err := settingsStore.Load(context.Background())
	if err != nil {
		logger.Warn("Failed to load runtime settings", "error", err)
	} else if runtimeSettings != nil {
		if err := runtimeSettings.ApplyTo(cfg); err != nil {
			logger.Warn("Failed to apply runtime settings", "error", err)
		} else {
			logger = util.NewLogger(cfg.Logging.Level, cfg.Logging.Format)
			util.SetDefaultLogger(logger)
			logger.Info("Runtime settings applied")
		}
	}

	// Create and configure server
	srv, err := server.New(cfg, db)
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	// Start server in background
	httpServer := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:      srv.Handler(),
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	// Channel for server errors
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("HTTP server listening",
			"addr", httpServer.Addr,
			"base_url", cfg.Server.BaseURL,
		)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	// Start background workers
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := srv.StartBackgroundWorkers(ctx); err != nil {
		return fmt.Errorf("failed to start background workers: %w", err)
	}

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		logger.Info("Received shutdown signal", "signal", sig.String())
	case err := <-serverErr:
		return fmt.Errorf("server error: %w", err)
	}

	// Graceful shutdown
	logger.Info("Shutting down gracefully...")
	cancel() // Stop background workers

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("Server shutdown error", "error", err)
	}

	logger.Info("Server stopped")
	return nil
}
