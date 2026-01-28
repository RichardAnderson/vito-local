package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const (
	bufferSize       = 4096
	cancelGracePeriod = 5 * time.Second
)

// OutputCallback is called for each chunk of output from the command.
type OutputCallback func(data string)

// Executor runs shell commands and streams their output.
type Executor struct {
	Cwd      string
	Env      []string
	OnStdout OutputCallback
	OnStderr OutputCallback
}

// Run executes a command via /bin/bash -c and returns its exit code.
// Returns a non-nil error only for infrastructure failures (not command exit codes).
func (e *Executor) Run(ctx context.Context, command string) (int, error) {
	cmd := exec.CommandContext(ctx, "/bin/bash", "-c", command)

	if e.Cwd != "" {
		cmd.Dir = e.Cwd
	}
	if len(e.Env) > 0 {
		cmd.Env = e.Env
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// On context cancellation, send SIGTERM to the entire process group
	// instead of SIGKILL to the process only. This allows child processes
	// to clean up gracefully.
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = cancelGracePeriod

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return -1, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return -1, err
	}

	if err := cmd.Start(); err != nil {
		return -1, err
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		e.readPipe(stdoutPipe, e.OnStdout)
	}()

	go func() {
		defer wg.Done()
		e.readPipe(stderrPipe, e.OnStderr)
	}()

	// Wait for pipes to drain before calling cmd.Wait() to prevent data loss.
	wg.Wait()

	err = cmd.Wait()
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return -1, fmt.Errorf("command failed: %w", err)
}

func (e *Executor) readPipe(pipe io.ReadCloser, callback OutputCallback) {
	if callback == nil {
		return
	}
	buf := make([]byte, bufferSize)
	for {
		n, err := pipe.Read(buf)
		if n > 0 {
			callback(string(buf[:n]))
		}
		if err != nil {
			break
		}
	}
}

