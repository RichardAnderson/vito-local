package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"vito-local/internal/config"
	"vito-local/internal/server"
)

var version = "dev"

func main() {
	socketPath := flag.String("socket", "/run/vito-root.sock", "Path to the Unix socket")
	allowedUser := flag.String("user", "vito", "Allowed connecting user")
	logLevel := flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	logJSON := flag.Bool("log-json", false, "Output logs as JSON")
	maxExecTimeout := flag.Duration("max-exec-timeout", 0, "Maximum command execution time (0 = no limit)")
	maxConnections := flag.Int("max-connections", 100, "Maximum concurrent connections")
	showVersion := flag.Bool("version", false, "Show version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("vito-root-service", version)
		os.Exit(0)
	}

	// Initialize logger
	logger := initLogger(*logLevel, *logJSON)

	// Load configuration
	cfg, err := config.New(*socketPath, *allowedUser, *logLevel, *logJSON)
	if err != nil {
		logger.Error("failed to load configuration", slog.String("error", err.Error()))
		os.Exit(1)
	}
	cfg.MaxExecTimeout = *maxExecTimeout
	cfg.MaxConnections = *maxConnections

	// Get the path to our own binary for self-update
	binaryPath, err := os.Executable()
	if err != nil {
		logger.Warn("failed to get executable path, self-update will be disabled",
			slog.String("error", err.Error()))
		binaryPath = ""
	}

	// Create and start server with version and binary path for self-update
	srv := server.New(cfg, logger,
		server.WithVersion(version),
		server.WithBinaryPath(binaryPath),
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	if err := srv.Start(ctx); err != nil {
		logger.Error("failed to start server", slog.String("error", err.Error()))
		os.Exit(1)
	}

	logger.Info("server running", slog.String("version", version))

	// Wait for shutdown signal or restart request
	var restartRequested bool
	select {
	case <-ctx.Done():
		logger.Info("received shutdown signal")
	case <-srv.RestartChan():
		logger.Info("restart requested for update")
		restartRequested = true
		stop() // Cancel the signal context
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown error", slog.String("error", err.Error()))
		os.Exit(1)
	}

	if restartRequested {
		logger.Info("server stopped for restart, exiting with code 0 for systemd restart")
		// Exit with code 0 so systemd will restart us with the new binary
		os.Exit(0)
	}

	logger.Info("server stopped")
}

func initLogger(level string, jsonOutput bool) *slog.Logger {
	var slogLevel slog.Level
	switch level {
	case "debug":
		slogLevel = slog.LevelDebug
	case "warn":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: slogLevel}

	var handler slog.Handler
	if jsonOutput {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}
