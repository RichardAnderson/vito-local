package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"sync"

	"vito-local/internal/config"
	"vito-local/internal/protocol"
)

// Server listens on a Unix socket and handles command execution requests.
type Server struct {
	cfg           *config.Config
	logger        *slog.Logger
	listener      *net.UnixListener
	wg            sync.WaitGroup
	systemdSocket bool
	connSem       chan struct{}
}

// New creates a new Server with the given configuration and logger.
func New(cfg *config.Config, logger *slog.Logger) *Server {
	maxConn := cfg.MaxConnections
	if maxConn <= 0 {
		maxConn = 100
	}
	return &Server{
		cfg:     cfg,
		logger:  logger,
		connSem: make(chan struct{}, maxConn),
	}
}

// Start begins listening for connections and handling them.
func (s *Server) Start(ctx context.Context) error {
	listener, err := s.createListener()
	if err != nil {
		return fmt.Errorf("creating listener: %w", err)
	}
	s.listener = listener

	// Set socket permissions (skip for systemd-managed sockets)
	if !s.systemdSocket {
		if err := s.setSocketPermissions(); err != nil {
			s.logger.Warn("failed to set socket permissions (may be expected on non-Linux)",
				slog.String("error", err.Error()))
		}
	}

	s.logger.Info("server started",
		slog.String("socket", s.cfg.SocketPath),
		slog.String("allowed_user", s.cfg.AllowedUser),
		slog.Int("allowed_uid", int(s.cfg.AllowedUID)),
		slog.Bool("systemd_activated", s.systemdSocket),
		slog.Int("max_connections", cap(s.connSem)),
	)

	go s.acceptLoop(ctx)

	return nil
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down server")

	if s.listener != nil {
		_ = s.listener.Close()
	}

	// Wait for in-flight connections with context timeout
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		s.logger.Info("all connections drained")
	case <-ctx.Done():
		s.logger.Warn("shutdown timed out, some connections may be interrupted")
	}

	// Only remove socket file in standalone mode; systemd owns it during socket activation.
	if !s.systemdSocket {
		if err := os.Remove(s.cfg.SocketPath); err != nil && !os.IsNotExist(err) {
			s.logger.Warn("failed to remove socket file", slog.String("error", err.Error()))
		}
	}

	return nil
}

func (s *Server) createListener() (*net.UnixListener, error) {
	// Check for systemd socket activation (LISTEN_FDS)
	if listenFDs := os.Getenv("LISTEN_FDS"); listenFDs != "" {
		n, err := strconv.Atoi(listenFDs)
		if err == nil && n > 0 {
			// fd 3 is the first passed fd (after stdin/stdout/stderr)
			f := os.NewFile(3, "systemd-socket")
			if f == nil {
				return nil, fmt.Errorf("failed to create file from fd 3")
			}
			defer func() { _ = f.Close() }()

			l, err := net.FileListener(f)
			if err != nil {
				return nil, fmt.Errorf("creating listener from systemd fd: %w", err)
			}

			ul, ok := l.(*net.UnixListener)
			if !ok {
				_ = l.Close()
				return nil, fmt.Errorf("systemd fd is not a Unix socket")
			}

			s.systemdSocket = true
			s.logger.Info("using systemd socket activation")
			return ul, nil
		}
	}

	// Standalone mode: remove stale socket and create new listener
	if err := os.Remove(s.cfg.SocketPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("removing stale socket: %w", err)
	}

	addr := &net.UnixAddr{Name: s.cfg.SocketPath, Net: "unix"}
	listener, err := net.ListenUnix("unix", addr)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", s.cfg.SocketPath, err)
	}

	return listener, nil
}

func (s *Server) setSocketPermissions() error {
	if err := os.Chmod(s.cfg.SocketPath, os.FileMode(s.cfg.SocketMode)); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	// Set group ownership so the allowed user's group can connect.
	// Chown may fail on non-Linux or without root privileges, which is
	// expected during development.
	if err := os.Chown(s.cfg.SocketPath, -1, int(s.cfg.SocketGroupGID)); err != nil {
		s.logger.Warn("failed to chown socket (expected on non-Linux or without root)",
			slog.String("error", err.Error()),
			slog.String("group", s.cfg.SocketGroup),
			slog.Int("gid", int(s.cfg.SocketGroupGID)),
		)
	}

	return nil
}

func (s *Server) acceptLoop(ctx context.Context) {
	for {
		conn, err := s.listener.AcceptUnix()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			s.logger.Error("accept error", slog.String("error", err.Error()))
			continue
		}

		creds, err := AuthorizeConnection(conn, s.cfg.AllowedUID)
		if err != nil {
			s.logger.Warn("connection rejected",
				slog.String("error", err.Error()),
			)
			if creds != nil {
				resp := errorResponseBytes("unauthorized: connection rejected")
				_, _ = conn.Write(resp)
			}
			_ = conn.Close()
			continue
		}

		// Enforce concurrent connection limit
		select {
		case s.connSem <- struct{}{}:
			s.wg.Add(1)
			go func() {
				defer func() { <-s.connSem }()
				defer s.wg.Done()
				handleConnection(ctx, conn, creds, s.logger, s.cfg.MaxExecTimeout)
			}()
		default:
			s.logger.Warn("max connections reached, rejecting",
				slog.Int("peer_uid", int(creds.UID)),
				slog.Int("peer_pid", int(creds.PID)),
			)
			resp := errorResponseBytes("server at maximum capacity")
			_, _ = conn.Write(resp)
			_ = conn.Close()
		}
	}
}

// errorResponseBytes creates a safe JSON error response for writing before
// handler setup. Uses json.Marshal to prevent injection.
func errorResponseBytes(msg string) []byte {
	resp := protocol.ErrorResponse(msg)
	data, err := json.Marshal(resp)
	if err != nil {
		// Fallback: this should never happen with a simple string message
		return []byte(`{"type":"error","message":"internal error"}` + "\n")
	}
	return append(data, '\n')
}
