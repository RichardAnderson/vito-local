package executor

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRun_Stdout(t *testing.T) {
	var mu sync.Mutex
	var output []string

	e := &Executor{
		OnStdout: func(data string) {
			mu.Lock()
			defer mu.Unlock()
			output = append(output, data)
		},
		OnStderr: func(data string) {},
	}

	code, err := e.Run(context.Background(), "echo hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}

	mu.Lock()
	combined := strings.Join(output, "")
	mu.Unlock()

	if !strings.Contains(combined, "hello") {
		t.Errorf("expected stdout to contain 'hello', got %q", combined)
	}
}

func TestRun_Stderr(t *testing.T) {
	var mu sync.Mutex
	var output []string

	e := &Executor{
		OnStdout: func(data string) {},
		OnStderr: func(data string) {
			mu.Lock()
			defer mu.Unlock()
			output = append(output, data)
		},
	}

	code, err := e.Run(context.Background(), "echo error >&2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}

	mu.Lock()
	combined := strings.Join(output, "")
	mu.Unlock()

	if !strings.Contains(combined, "error") {
		t.Errorf("expected stderr to contain 'error', got %q", combined)
	}
}

func TestRun_ExitCode(t *testing.T) {
	e := &Executor{
		OnStdout: func(data string) {},
		OnStderr: func(data string) {},
	}

	code, err := e.Run(context.Background(), "exit 42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != 42 {
		t.Errorf("expected exit code 42, got %d", code)
	}
}

func TestRun_EnvVars(t *testing.T) {
	var mu sync.Mutex
	var output []string

	e := &Executor{
		Env: append(os.Environ(), "TEST_VAR=hello_from_env"),
		OnStdout: func(data string) {
			mu.Lock()
			defer mu.Unlock()
			output = append(output, data)
		},
		OnStderr: func(data string) {},
	}

	code, err := e.Run(context.Background(), "echo $TEST_VAR")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}

	mu.Lock()
	combined := strings.Join(output, "")
	mu.Unlock()

	if !strings.Contains(combined, "hello_from_env") {
		t.Errorf("expected output to contain 'hello_from_env', got %q", combined)
	}
}

func TestRun_Cwd(t *testing.T) {
	var mu sync.Mutex
	var output []string

	e := &Executor{
		Cwd: "/",
		OnStdout: func(data string) {
			mu.Lock()
			defer mu.Unlock()
			output = append(output, data)
		},
		OnStderr: func(data string) {},
	}

	code, err := e.Run(context.Background(), "pwd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}

	mu.Lock()
	combined := strings.TrimSpace(strings.Join(output, ""))
	mu.Unlock()

	if combined != "/" {
		t.Errorf("expected cwd '/', got %q", combined)
	}
}

func TestRun_ContextCancellation(t *testing.T) {
	e := &Executor{
		OnStdout: func(data string) {},
		OnStderr: func(data string) {},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	code, err := e.Run(ctx, "sleep 30")
	// Context cancellation should result in either a non-zero exit code
	// (process killed by SIGTERM) or an infrastructure error.
	if err == nil && code == 0 {
		t.Error("expected non-zero exit code or error for cancelled command")
	}
}

func TestRun_NilCallbacks(t *testing.T) {
	e := &Executor{}

	code, err := e.Run(context.Background(), "echo hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}
}
