package server

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"os/user"
	"strconv"
	"strings"
	"testing"
	"time"

	"vito-local/internal/config"
	"vito-local/internal/protocol"
)

func testConfig(t *testing.T, sockPath string) *config.Config {
	t.Helper()

	u, err := user.Current()
	if err != nil {
		t.Fatalf("failed to get current user: %v", err)
	}

	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		t.Fatalf("failed to parse UID: %v", err)
	}

	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		t.Fatalf("failed to parse GID: %v", err)
	}

	return &config.Config{
		SocketPath:     sockPath,
		AllowedUser:    u.Username,
		AllowedUID:     uint32(uid),
		SocketGroup:    u.Username,
		SocketGroupGID: uint32(gid),
		SocketMode:     0660,
		LogLevel:       "info",
		MaxConnections: 100,
	}
}

func TestServer_StartAndShutdown(t *testing.T) {
	sockPath := tempSocketPath(t)

	cfg := testConfig(t, sockPath)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	srv := New(cfg, logger)

	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	// Verify socket file exists
	if _, err := os.Stat(sockPath); err != nil {
		t.Fatalf("socket file should exist: %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("failed to shutdown server: %v", err)
	}

	// Verify socket file is removed
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Error("socket file should be removed after shutdown")
	}
}

func TestServer_ExecuteCommand(t *testing.T) {
	sockPath := tempSocketPath(t)

	cfg := testConfig(t, sockPath)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	srv := New(cfg, logger)

	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	// Connect and send command
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: sockPath, Net: "unix"})
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	req := protocol.Request{Command: "echo integration_test"}
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	conn.Write(data)

	scanner := bufio.NewScanner(conn)
	var responses []protocol.Response
	for scanner.Scan() {
		var resp protocol.Response
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}
		responses = append(responses, resp)
		if resp.Type == protocol.TypeExit || resp.Type == protocol.TypeError {
			break
		}
	}

	if len(responses) < 2 {
		t.Fatalf("expected at least 2 responses, got %d", len(responses))
	}

	var foundOutput bool
	for _, r := range responses {
		if r.Type == protocol.TypeStdout && strings.Contains(r.Data, "integration_test") {
			foundOutput = true
		}
	}
	if !foundOutput {
		t.Error("expected stdout containing 'integration_test'")
	}

	last := responses[len(responses)-1]
	if last.Type != protocol.TypeExit {
		t.Errorf("expected last response to be exit, got %q", last.Type)
	}
	if last.Code == nil || *last.Code != 0 {
		t.Errorf("expected exit code 0, got %v", last.Code)
	}
}

func TestServer_StaleSocketCleanup(t *testing.T) {
	sockPath := tempSocketPath(t)

	// Create a stale socket file
	os.WriteFile(sockPath, []byte("stale"), 0600)

	cfg := testConfig(t, sockPath)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	srv := New(cfg, logger)

	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("server should start despite stale socket: %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)
}

func TestServer_ConnectionDraining(t *testing.T) {
	sockPath := tempSocketPath(t)

	cfg := testConfig(t, sockPath)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	srv := New(cfg, logger)

	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	// Start a slow command
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: sockPath, Net: "unix"})
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	req := protocol.Request{Command: "sleep 0.2 && echo done"}
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	conn.Write(data)

	// Give the command a moment to start
	time.Sleep(50 * time.Millisecond)

	// Shutdown should wait for the command to complete
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	srv.Shutdown(shutdownCtx)

	// Read remaining output
	scanner := bufio.NewScanner(conn)
	var responses []protocol.Response
	for scanner.Scan() {
		var resp protocol.Response
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			break
		}
		responses = append(responses, resp)
		if resp.Type == protocol.TypeExit || resp.Type == protocol.TypeError {
			break
		}
	}

	// Should have completed the command
	var foundExit bool
	for _, r := range responses {
		if r.Type == protocol.TypeExit {
			foundExit = true
		}
	}
	if !foundExit {
		t.Error("expected command to complete during graceful shutdown")
	}
}
