package server

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"

	"vito-local/internal/config"
	"vito-local/internal/protocol"
)

func setupTestSocket(t *testing.T) (server *net.UnixConn, client *net.UnixConn, cleanup func()) {
	t.Helper()

	sockPath := tempSocketPath(t)

	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sockPath, Net: "unix"})
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	clientDone := make(chan *net.UnixConn, 1)
	go func() {
		c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: sockPath, Net: "unix"})
		if err != nil {
			t.Errorf("failed to connect: %v", err)
			return
		}
		clientDone <- c
	}()

	serverConn, err := listener.AcceptUnix()
	if err != nil {
		t.Fatalf("failed to accept: %v", err)
	}

	clientConn := <-clientDone
	listener.Close()

	return serverConn, clientConn, func() {
		serverConn.Close()
		clientConn.Close()
	}
}

// testServer creates a minimal server instance for handler tests.
func testServer(t *testing.T, logger *slog.Logger) *Server {
	t.Helper()
	cfg := &config.Config{
		MaxConnections: 10,
	}
	return New(cfg, logger, WithVersion("test-version"))
}

func TestHandleConnection_Echo(t *testing.T) {
	serverConn, clientConn, cleanup := setupTestSocket(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	creds := &PeerCredentials{UID: uint32(os.Getuid()), PID: int32(os.Getpid())}
	srv := testServer(t, logger)

	// Send request from client
	req := protocol.Request{Command: "echo hello"}
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	clientConn.Write(data)

	// Handle connection on server side
	done := make(chan struct{})
	go func() {
		handleConnection(context.Background(), serverConn, creds, srv, logger, 0)
		close(done)
	}()

	// Read responses from client side
	scanner := bufio.NewScanner(clientConn)
	var responses []protocol.Response
	for scanner.Scan() {
		var resp protocol.Response
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		responses = append(responses, resp)
		if resp.Type == protocol.TypeExit || resp.Type == protocol.TypeError {
			break
		}
	}

	<-done

	if len(responses) < 2 {
		t.Fatalf("expected at least 2 responses (stdout + exit), got %d", len(responses))
	}

	// Check we have stdout and exit responses
	var hasStdout, hasExit bool
	var stdoutData string
	for _, r := range responses {
		switch r.Type {
		case protocol.TypeStdout:
			hasStdout = true
			stdoutData += r.Data
		case protocol.TypeExit:
			hasExit = true
			if r.Code == nil || *r.Code != 0 {
				t.Errorf("expected exit code 0, got %v", r.Code)
			}
		}
	}

	if !hasStdout {
		t.Error("expected stdout response")
	}
	if !strings.Contains(stdoutData, "hello") {
		t.Errorf("expected stdout to contain 'hello', got %q", stdoutData)
	}
	if !hasExit {
		t.Error("expected exit response")
	}
}

func TestHandleConnection_InvalidJSON(t *testing.T) {
	serverConn, clientConn, cleanup := setupTestSocket(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	creds := &PeerCredentials{UID: uint32(os.Getuid()), PID: int32(os.Getpid())}
	srv := testServer(t, logger)

	// Send invalid JSON
	clientConn.Write([]byte("not json\n"))

	done := make(chan struct{})
	go func() {
		handleConnection(context.Background(), serverConn, creds, srv, logger, 0)
		close(done)
	}()

	scanner := bufio.NewScanner(clientConn)
	if scanner.Scan() {
		var resp protocol.Response
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		if resp.Type != protocol.TypeError {
			t.Errorf("expected error response, got %q", resp.Type)
		}
	}

	<-done
}

func TestHandleConnection_EmptyRequest(t *testing.T) {
	serverConn, clientConn, cleanup := setupTestSocket(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	creds := &PeerCredentials{UID: uint32(os.Getuid()), PID: int32(os.Getpid())}
	srv := testServer(t, logger)

	// Empty request (no command or action)
	req := protocol.Request{Command: ""}
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	clientConn.Write(data)

	done := make(chan struct{})
	go func() {
		handleConnection(context.Background(), serverConn, creds, srv, logger, 0)
		close(done)
	}()

	scanner := bufio.NewScanner(clientConn)
	if scanner.Scan() {
		var resp protocol.Response
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		if resp.Type != protocol.TypeError {
			t.Errorf("expected error response, got %q", resp.Type)
		}
		if !strings.Contains(resp.Message, "command or action") {
			t.Errorf("expected 'command or action' error, got %q", resp.Message)
		}
	}

	<-done
}

func TestHandleConnection_Stderr(t *testing.T) {
	serverConn, clientConn, cleanup := setupTestSocket(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	creds := &PeerCredentials{UID: uint32(os.Getuid()), PID: int32(os.Getpid())}
	srv := testServer(t, logger)

	req := protocol.Request{Command: "echo err >&2"}
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	clientConn.Write(data)

	done := make(chan struct{})
	go func() {
		handleConnection(context.Background(), serverConn, creds, srv, logger, 0)
		close(done)
	}()

	scanner := bufio.NewScanner(clientConn)
	var responses []protocol.Response
	for scanner.Scan() {
		var resp protocol.Response
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		responses = append(responses, resp)
		if resp.Type == protocol.TypeExit || resp.Type == protocol.TypeError {
			break
		}
	}

	<-done

	var hasStderr bool
	for _, r := range responses {
		if r.Type == protocol.TypeStderr {
			hasStderr = true
			if !strings.Contains(r.Data, "err") {
				t.Errorf("expected stderr to contain 'err', got %q", r.Data)
			}
		}
	}
	if !hasStderr {
		t.Error("expected stderr response")
	}
}

func TestHandleConnection_VersionAction(t *testing.T) {
	serverConn, clientConn, cleanup := setupTestSocket(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	creds := &PeerCredentials{UID: uint32(os.Getuid()), PID: int32(os.Getpid())}
	srv := testServer(t, logger)

	// Send version action request
	req := protocol.Request{Action: "version"}
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	clientConn.Write(data)

	done := make(chan struct{})
	go func() {
		handleConnection(context.Background(), serverConn, creds, srv, logger, 0)
		close(done)
	}()

	scanner := bufio.NewScanner(clientConn)
	if scanner.Scan() {
		var resp protocol.Response
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		if resp.Type != protocol.TypeVersion {
			t.Errorf("expected version response, got %q", resp.Type)
		}
		if resp.CurrentVersion != "test-version" {
			t.Errorf("expected version 'test-version', got %q", resp.CurrentVersion)
		}
	}

	<-done
}

func TestHandleConnection_CheckUpdateAction_NoBinaryPath(t *testing.T) {
	serverConn, clientConn, cleanup := setupTestSocket(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	creds := &PeerCredentials{UID: uint32(os.Getuid()), PID: int32(os.Getpid())}
	// Server without binary path configured
	cfg := &config.Config{MaxConnections: 10}
	srv := New(cfg, logger, WithVersion("test-version"))

	// Send check-update action request
	req := protocol.Request{Action: "check-update"}
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	clientConn.Write(data)

	done := make(chan struct{})
	go func() {
		handleConnection(context.Background(), serverConn, creds, srv, logger, 0)
		close(done)
	}()

	scanner := bufio.NewScanner(clientConn)
	if scanner.Scan() {
		var resp protocol.Response
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		if resp.Type != protocol.TypeUpdate {
			t.Errorf("expected update response, got %q", resp.Type)
		}
		if resp.UpdateStatus != protocol.UpdateStatusFailed {
			t.Errorf("expected failed status, got %q", resp.UpdateStatus)
		}
		if !strings.Contains(resp.Message, "binary path not configured") {
			t.Errorf("expected 'binary path not configured' message, got %q", resp.Message)
		}
	}

	<-done
}

func TestHandleConnection_UnknownAction(t *testing.T) {
	serverConn, clientConn, cleanup := setupTestSocket(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	creds := &PeerCredentials{UID: uint32(os.Getuid()), PID: int32(os.Getpid())}
	srv := testServer(t, logger)

	// Send unknown action - this should be caught by protocol validation
	// but let's test the handler dispatch too
	data := []byte(`{"action":"unknown-action"}` + "\n")
	clientConn.Write(data)

	done := make(chan struct{})
	go func() {
		handleConnection(context.Background(), serverConn, creds, srv, logger, 0)
		close(done)
	}()

	scanner := bufio.NewScanner(clientConn)
	if scanner.Scan() {
		var resp protocol.Response
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		if resp.Type != protocol.TypeError {
			t.Errorf("expected error response, got %q", resp.Type)
		}
		if !strings.Contains(resp.Message, "unknown action") {
			t.Errorf("expected 'unknown action' error, got %q", resp.Message)
		}
	}

	<-done
}
