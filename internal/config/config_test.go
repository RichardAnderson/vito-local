package config

import (
	"os/user"
	"strings"
	"testing"
)

func TestNew_CurrentUser(t *testing.T) {
	u, err := user.Current()
	if err != nil {
		t.Fatalf("failed to get current user: %v", err)
	}

	cfg, err := New("/tmp/test.sock", u.Username, "info", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.SocketPath != "/tmp/test.sock" {
		t.Errorf("expected socket path '/tmp/test.sock', got %q", cfg.SocketPath)
	}
	if cfg.AllowedUser != u.Username {
		t.Errorf("expected user %q, got %q", u.Username, cfg.AllowedUser)
	}
	if cfg.AllowedUID == 0 && u.Uid != "0" {
		t.Errorf("unexpected UID 0 for non-root user")
	}
	if cfg.SocketGroup != u.Username {
		t.Errorf("expected group %q, got %q", u.Username, cfg.SocketGroup)
	}
	if cfg.SocketMode != 0660 {
		t.Errorf("expected mode 0660, got %o", cfg.SocketMode)
	}
	if cfg.SocketGroupGID == 0 && u.Gid != "0" {
		t.Error("expected non-zero SocketGroupGID for non-root user")
	}
	if cfg.MaxConnections != 100 {
		t.Errorf("expected default MaxConnections 100, got %d", cfg.MaxConnections)
	}
}

func TestNew_DefaultSocketPath(t *testing.T) {
	u, err := user.Current()
	if err != nil {
		t.Fatalf("failed to get current user: %v", err)
	}

	cfg, err := New("", u.Username, "info", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.SocketPath != "/run/vito-root.sock" {
		t.Errorf("expected default socket path, got %q", cfg.SocketPath)
	}
}

func TestNew_UnknownUser(t *testing.T) {
	_, err := New("/tmp/test.sock", "nonexistent_user_12345", "info", false)
	if err == nil {
		t.Fatal("expected error for unknown user")
	}
	if !strings.Contains(err.Error(), "looking up user") {
		t.Errorf("expected user lookup error, got: %v", err)
	}
}

func TestNew_EmptyUser(t *testing.T) {
	_, err := New("/tmp/test.sock", "", "info", false)
	if err == nil {
		t.Fatal("expected error for empty user")
	}
	if !strings.Contains(err.Error(), "allowed user must be specified") {
		t.Errorf("expected empty user error, got: %v", err)
	}
}

func TestNew_LogLevelValidation(t *testing.T) {
	u, err := user.Current()
	if err != nil {
		t.Fatalf("failed to get current user: %v", err)
	}

	tests := []struct {
		level string
		valid bool
	}{
		{"debug", true},
		{"info", true},
		{"warn", true},
		{"error", true},
		{"DEBUG", true}, // case insensitive
		{"INFO", true},
		{"", true}, // defaults to info
		{"invalid", false},
		{"trace", false},
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			cfg, err := New("/tmp/test.sock", u.Username, tt.level, false)
			if tt.valid {
				if err != nil {
					t.Fatalf("unexpected error for level %q: %v", tt.level, err)
				}
				if tt.level == "" && cfg.LogLevel != "info" {
					t.Errorf("expected default level 'info', got %q", cfg.LogLevel)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error for level %q", tt.level)
				}
				if !strings.Contains(err.Error(), "invalid log level") {
					t.Errorf("expected log level error, got: %v", err)
				}
			}
		})
	}
}

func TestNew_LogJSON(t *testing.T) {
	u, err := user.Current()
	if err != nil {
		t.Fatalf("failed to get current user: %v", err)
	}

	cfg, err := New("/tmp/test.sock", u.Username, "info", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.LogJSON {
		t.Error("expected LogJSON to be true")
	}
}
