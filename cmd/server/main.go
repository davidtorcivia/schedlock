// Command schedlock runs the SchedLock calendar approval proxy.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/dtorcivia/schedlock/internal/config"
	schedcrypto "github.com/dtorcivia/schedlock/internal/crypto"
	"github.com/dtorcivia/schedlock/internal/database"
	"github.com/dtorcivia/schedlock/internal/server"
	"github.com/dtorcivia/schedlock/internal/settings"
	"github.com/dtorcivia/schedlock/internal/util"
	"github.com/dtorcivia/schedlock/internal/web"
)

// shutdownTimeout bounds graceful shutdown: in-flight requests and queued
// calendar operations get this long to finish.
const shutdownTimeout = 30 * time.Second

func main() {
	if len(os.Args) > 1 {
		if err := runCommand(os.Args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// runCommand handles the command-line subcommands.
func runCommand(args []string) error {
	switch args[0] {
	case "hash-password":
		if len(args) < 2 {
			return errors.New(`usage: schedlock hash-password "your password"`)
		}
		hash, err := schedcrypto.HashPassword(args[1])
		if err != nil {
			return fmt.Errorf("failed to hash the password: %w", err)
		}
		fmt.Println(hash)
		return nil

	case "version":
		fmt.Println(server.Version)
		return nil

	case "help", "-h", "--help":
		printUsage()
		return nil

	default:
		printUsage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func printUsage() {
	fmt.Print(`SchedLock - human approval for AI calendar changes

Usage:
  schedlock                            Run the server
  schedlock hash-password "password"   Print an Argon2id hash for the admin password
  schedlock version                    Print the version
  schedlock help                       Show this message

Configuration is read from the config file (default /data/config.yaml) and the
environment. See .env.example for the available settings.
`)
}

func run() error {
	cfg, needsSetup, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	util.SetDefaultLogger(util.NewLogger(cfg.Logging.Level, cfg.Logging.Format))

	if needsSetup {
		util.Info("Starting in setup mode", "port", cfg.Server.Port)
		return runSetup(cfg)
	}

	for _, warning := range cfg.Warnings() {
		util.Warn(warning)
	}

	db, err := database.Open(cfg.Database.Path)
	if err != nil {
		return fmt.Errorf("failed to open the database: %w", err)
	}
	defer db.Close()

	util.Info("Database ready", "path", cfg.Database.Path)

	// Runtime settings are applied over the file and environment configuration.
	if err := applyRuntimeSettings(cfg, db); err != nil {
		util.Warn("Could not apply stored settings", "error", err)
	}

	srv, err := server.New(cfg, db)
	if err != nil {
		return fmt.Errorf("failed to create the server: %w", err)
	}

	httpServer := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:      srv.Handler(),
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
		// A slow client must not be able to hold a connection open indefinitely
		// while dribbling out request headers.
		ReadHeaderTimeout: 10 * time.Second,
	}

	workerCtx, stopWorkers := context.WithCancel(context.Background())
	defer stopWorkers()

	srv.StartBackgroundWorkers(workerCtx)

	serverErr := make(chan error, 1)
	go func() {
		util.Info("HTTP server listening", "addr", httpServer.Addr, "base_url", cfg.Server.BaseURL)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		util.Info("Shutting down", "signal", sig.String())
	case err := <-serverErr:
		return fmt.Errorf("server error: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	// Stop accepting requests first, then let queued calendar operations
	// finish. Cancelling the workers before draining would abandon a request
	// that had already been claimed for execution.
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		util.Warn("HTTP server did not shut down cleanly", "error", err)
	}
	srv.Shutdown(shutdownCtx)
	stopWorkers()

	util.Info("Server stopped")
	return nil
}

// applyRuntimeSettings overlays the settings stored in the database.
func applyRuntimeSettings(cfg *config.Config, db *database.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stored, err := settings.NewStore(db).Load(ctx)
	if err != nil {
		return err
	}
	if stored == nil {
		return nil
	}

	if err := stored.ApplyTo(cfg); err != nil {
		return err
	}

	util.SetDefaultLogger(util.NewLogger(cfg.Logging.Level, cfg.Logging.Format))
	util.Info("Applied stored settings")
	return nil
}

// runSetup serves the first-run wizard until setup completes.
func runSetup(cfg *config.Config) error {
	configPath := config.GetConfigFilePath()

	// Completing setup writes the configuration and signals a restart: the
	// application reads its configuration once at startup, so it must be
	// re-executed to pick up the new file. Under Docker or systemd the
	// supervisor restarts the process automatically.
	restart := make(chan struct{})
	var restartOnce sync.Once

	setupHandler, err := web.NewSetupHandler(cfg, configPath, func() {
		restartOnce.Do(func() {
			// Give the browser a moment to receive the completion page.
			time.Sleep(time.Second)
			close(restart)
		})
	})
	if err != nil {
		return fmt.Errorf("failed to create the setup handler: %w", err)
	}

	mux := http.NewServeMux()
	setupHandler.RegisterRoutes(mux)

	httpServer := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:           mux,
		ReadTimeout:       cfg.Server.ReadTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		ReadHeaderTimeout: 10 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		util.Info("Setup server listening",
			"addr", httpServer.Addr,
			"message", "Open this address in a browser to finish setup")
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		util.Info("Shutting down setup", "signal", sig.String())
	case err := <-serverErr:
		return fmt.Errorf("setup server error: %w", err)
	case <-restart:
		util.Info("Setup complete; restarting to load the new configuration")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		util.Warn("Setup server did not shut down cleanly", "error", err)
	}

	return nil
}
