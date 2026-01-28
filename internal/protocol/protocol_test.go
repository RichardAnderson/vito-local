package protocol

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestParseRequest_Valid(t *testing.T) {
	input := `{"command":"echo hello","env":{"FOO":"bar"},"cwd":"/tmp"}` + "\n"
	req, err := ParseRequest(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Command != "echo hello" {
		t.Errorf("expected command 'echo hello', got %q", req.Command)
	}
	if req.Env["FOO"] != "bar" {
		t.Errorf("expected env FOO=bar, got %q", req.Env["FOO"])
	}
	if req.Cwd != "/tmp" {
		t.Errorf("expected cwd '/tmp', got %q", req.Cwd)
	}
}

func TestParseRequest_MinimalValid(t *testing.T) {
	input := `{"command":"ls"}` + "\n"
	req, err := ParseRequest(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Command != "ls" {
		t.Errorf("expected command 'ls', got %q", req.Command)
	}
	if req.Env != nil {
		t.Errorf("expected nil env, got %v", req.Env)
	}
}

func TestParseRequest_InvalidJSON(t *testing.T) {
	input := "not json\n"
	_, err := ParseRequest(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "parsing request JSON") {
		t.Errorf("expected JSON parse error, got: %v", err)
	}
}

func TestParseRequest_EmptyCommand(t *testing.T) {
	input := `{"command":""}` + "\n"
	_, err := ParseRequest(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for empty command")
	}
	if !strings.Contains(err.Error(), "empty command") {
		t.Errorf("expected empty command error, got: %v", err)
	}
}

func TestParseRequest_MissingCommand(t *testing.T) {
	input := `{"env":{"FOO":"bar"}}` + "\n"
	_, err := ParseRequest(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for missing command")
	}
}

func TestParseRequest_NoNewline(t *testing.T) {
	input := `{"command":"echo hello"}`
	// Should return error since no newline delimiter (connection closed)
	_, err := ParseRequest(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for missing newline")
	}
}

func TestParseRequest_TooLarge(t *testing.T) {
	// Create a request larger than MaxRequestSize
	large := strings.Repeat("x", MaxRequestSize+1) + "\n"
	_, err := ParseRequest(strings.NewReader(large))
	if err == nil {
		t.Fatal("expected error for oversized request")
	}
	if !strings.Contains(err.Error(), "request too large") {
		t.Errorf("expected 'request too large' error, got: %v", err)
	}
}

func TestWriteResponse_Stdout(t *testing.T) {
	var buf bytes.Buffer
	resp := StdoutResponse("hello world")

	err := WriteResponse(&buf, resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded Response
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &decoded); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if decoded.Type != TypeStdout {
		t.Errorf("expected type stdout, got %q", decoded.Type)
	}
	if decoded.Data != "hello world" {
		t.Errorf("expected data 'hello world', got %q", decoded.Data)
	}
}

func TestWriteResponse_Stderr(t *testing.T) {
	var buf bytes.Buffer
	resp := StderrResponse("error output")

	err := WriteResponse(&buf, resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded Response
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &decoded); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if decoded.Type != TypeStderr {
		t.Errorf("expected type stderr, got %q", decoded.Type)
	}
	if decoded.Data != "error output" {
		t.Errorf("expected data 'error output', got %q", decoded.Data)
	}
}

func TestWriteResponse_Exit(t *testing.T) {
	var buf bytes.Buffer
	resp := ExitResponse(42)

	err := WriteResponse(&buf, resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded Response
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &decoded); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if decoded.Type != TypeExit {
		t.Errorf("expected type exit, got %q", decoded.Type)
	}
	if decoded.Code == nil || *decoded.Code != 42 {
		t.Errorf("expected exit code 42, got %v", decoded.Code)
	}
}

func TestWriteResponse_Error(t *testing.T) {
	var buf bytes.Buffer
	resp := ErrorResponse("something went wrong")

	err := WriteResponse(&buf, resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded Response
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &decoded); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if decoded.Type != TypeError {
		t.Errorf("expected type error, got %q", decoded.Type)
	}
	if decoded.Message != "something went wrong" {
		t.Errorf("expected message 'something went wrong', got %q", decoded.Message)
	}
}

func TestWriteResponse_NewlineDelimited(t *testing.T) {
	var buf bytes.Buffer
	resp := StdoutResponse("test")

	err := WriteResponse(&buf, resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.HasSuffix(output, "\n") {
		t.Error("response should end with newline")
	}
	if strings.Count(output, "\n") != 1 {
		t.Error("response should contain exactly one newline")
	}
}

func TestRoundTrip(t *testing.T) {
	// Write a request, parse it back
	req := Request{
		Command: "echo test",
		Env:     map[string]string{"KEY": "value"},
		Cwd:     "/home",
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	data = append(data, '\n')

	parsed, err := ParseRequest(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if parsed.Command != req.Command {
		t.Errorf("command mismatch: %q vs %q", parsed.Command, req.Command)
	}
	if parsed.Env["KEY"] != req.Env["KEY"] {
		t.Errorf("env mismatch")
	}
	if parsed.Cwd != req.Cwd {
		t.Errorf("cwd mismatch")
	}
}
