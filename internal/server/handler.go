package server

import (
	"context"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"vito-local/internal/executor"
	"vito-local/internal/protocol"
	"vito-local/internal/updater"
)

// blockedEnvVars are environment variable names that clients may not set.
// These are dangerous in a root-execution context (library injection,
// shell startup hijacking, path manipulation).
var blockedEnvVars = map[string]bool{
	"PATH":            true,
	"LD_PRELOAD":      true,
	"LD_LIBRARY_PATH": true,
	"LD_AUDIT":        true,
	"LD_DEBUG":        true,
	"LD_PROFILE":      true,
	"BASH_ENV":        true,
	"ENV":             true,
	"SHELLOPTS":       true,
	"BASHOPTS":        true,
	"IFS":             true,
	"CDPATH":          true,
	"GLOBIGNORE":      true,
}

// blockedEnvPrefixes are environment variable name prefixes that clients may not set.
var blockedEnvPrefixes = []string{
	"LD_",
	"BASH_FUNC_",
}

func isBlockedEnvVar(key string) bool {
	upper := strings.ToUpper(key)
	if blockedEnvVars[upper] {
		return true
	}
	for _, prefix := range blockedEnvPrefixes {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return false
}

func handleConnection(ctx context.Context, conn *net.UnixConn, creds *PeerCredentials, srv *Server, logger *slog.Logger, maxExecTimeout time.Duration) {
	defer conn.Close()

	connLog := logger.With(
		slog.Int("peer_uid", int(creds.UID)),
		slog.Int("peer_pid", int(creds.PID)),
	)

	req, err := protocol.ParseRequest(conn)
	if err != nil {
		connLog.Error("failed to parse request", slog.String("error", err.Error()))
		writeErr := protocol.WriteResponse(conn, protocol.ErrorResponse(err.Error()))
		if writeErr != nil {
			connLog.Error("failed to write error response", slog.String("error", writeErr.Error()))
		}
		return
	}

	// Route based on Action vs Command
	if req.Action != "" {
		connLog = connLog.With(slog.String("action", req.Action))
		connLog.Info("handling action")
		handleAction(ctx, conn, req, srv, connLog)
		return
	}

	connLog = connLog.With(
		slog.String("command", req.Command),
		slog.String("cwd", req.Cwd),
	)
	connLog.Info("executing command")

	// Merge environment: parent env + request env (with blocklist filtering)
	env := os.Environ()
	for k, v := range req.Env {
		if strings.Contains(k, "=") || strings.ContainsRune(k, 0) {
			connLog.Warn("rejected env var with invalid key", slog.String("key", k))
			continue
		}
		if isBlockedEnvVar(k) {
			connLog.Warn("rejected blocked env var", slog.String("key", k))
			continue
		}
		env = append(env, k+"="+v)
	}

	// Context that we cancel on write errors to kill orphaned processes
	execCtx, execCancel := context.WithCancel(ctx)
	defer execCancel()

	// Apply per-command timeout if configured
	if maxExecTimeout > 0 {
		var timeoutCancel context.CancelFunc
		execCtx, timeoutCancel = context.WithTimeout(execCtx, maxExecTimeout)
		defer timeoutCancel()
	}

	var writeMu sync.Mutex

	writeResponse := func(resp protocol.Response) {
		writeMu.Lock()
		defer writeMu.Unlock()
		if err := protocol.WriteResponse(conn, resp); err != nil {
			connLog.Warn("write failed (client disconnected?)", slog.String("error", err.Error()))
			execCancel()
		}
	}

	cmdExec := &executor.Executor{
		Cwd: req.Cwd,
		Env: env,
		OnStdout: func(data string) {
			writeResponse(protocol.StdoutResponse(data))
		},
		OnStderr: func(data string) {
			writeResponse(protocol.StderrResponse(data))
		},
	}

	exitCode, err := cmdExec.Run(execCtx, req.Command)
	if err != nil {
		connLog.Error("command execution failed", slog.String("error", err.Error()))
		writeResponse(protocol.ErrorResponse(err.Error()))
		return
	}

	writeResponse(protocol.ExitResponse(exitCode))
	connLog.Info("command completed", slog.Int("exit_code", exitCode))
}

// handleAction dispatches action requests to the appropriate handler.
func handleAction(ctx context.Context, conn *net.UnixConn, req *protocol.Request, srv *Server, logger *slog.Logger) {
	writeResponse := func(resp protocol.Response) {
		if err := protocol.WriteResponse(conn, resp); err != nil {
			logger.Warn("write failed (client disconnected?)", slog.String("error", err.Error()))
		}
	}

	switch req.Action {
	case "version":
		handleVersion(srv, writeResponse, logger)
	case "check-update":
		handleCheckUpdate(srv, writeResponse, logger)
	case "update":
		handleUpdate(ctx, srv, writeResponse, logger)
	default:
		writeResponse(protocol.ErrorResponse("unknown action: " + req.Action))
	}
}

// handleVersion returns the current version.
func handleVersion(srv *Server, writeResponse func(protocol.Response), logger *slog.Logger) {
	logger.Info("returning version", slog.String("version", srv.Version()))
	writeResponse(protocol.VersionResponse(srv.Version()))
}

// handleCheckUpdate checks if an update is available without performing it.
func handleCheckUpdate(srv *Server, writeResponse func(protocol.Response), logger *slog.Logger) {
	if srv.BinaryPath() == "" {
		writeResponse(protocol.UpdateResponse(
			protocol.UpdateStatusFailed,
			srv.Version(), "",
			"update not supported: binary path not configured",
		))
		return
	}

	u := updater.New(srv.Version(), srv.BinaryPath())
	result, err := u.CheckUpdate()
	if err != nil {
		logger.Error("check update failed", slog.String("error", err.Error()))
	}

	writeResponse(protocol.UpdateResponse(
		protocol.UpdateStatus(result.Status),
		result.CurrentVersion,
		result.LatestVersion,
		result.Message,
	))
}

// restartDelay is the time to wait after sending the restart response before triggering restart.
const restartDelay = 500 * time.Millisecond

// handleUpdate performs the update and schedules a restart.
func handleUpdate(ctx context.Context, srv *Server, writeResponse func(protocol.Response), logger *slog.Logger) {
	if srv.BinaryPath() == "" {
		writeResponse(protocol.UpdateResponse(
			protocol.UpdateStatusFailed,
			srv.Version(), "",
			"update not supported: binary path not configured",
		))
		return
	}

	u := updater.New(srv.Version(), srv.BinaryPath())

	// Progress callback to send status updates
	onProgress := func(status, message string) {
		writeResponse(protocol.UpdateResponse(
			protocol.UpdateStatus(status),
			srv.Version(),
			"", // We don't know latest version in progress callbacks
			message,
		))
	}

	result, err := u.PerformUpdate(ctx, onProgress)
	if err != nil {
		logger.Error("update failed", slog.String("error", err.Error()))
		// Error response already sent via onProgress
		return
	}

	// If we're already current, just return
	if result.Status == "current" {
		logger.Info("already running latest version")
		return
	}

	// Update applied successfully, send restarting status
	logger.Info("update applied, scheduling restart",
		slog.String("from_version", result.CurrentVersion),
		slog.String("to_version", result.LatestVersion),
	)

	writeResponse(protocol.UpdateResponse(
		protocol.UpdateStatusRestarting,
		result.CurrentVersion,
		result.LatestVersion,
		"service will restart momentarily",
	))

	// Give time for the response to be sent
	time.Sleep(restartDelay)

	// Signal restart
	srv.RequestRestart()
}
