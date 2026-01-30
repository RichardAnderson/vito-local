package protocol

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// MaxRequestSize is the maximum allowed size for a single request line (10 MB).
const MaxRequestSize = 10 << 20

// Request represents a command execution request from a client.
type Request struct {
	Command string            `json:"command,omitempty"`
	Action  string            `json:"action,omitempty"` // "update", "check-update", "version"
	Env     map[string]string `json:"env,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
}

// ResponseType identifies the kind of response message.
type ResponseType string

const (
	TypeStdout  ResponseType = "stdout"
	TypeStderr  ResponseType = "stderr"
	TypeExit    ResponseType = "exit"
	TypeError   ResponseType = "error"
	TypeUpdate  ResponseType = "update"
	TypeVersion ResponseType = "version"
)

// UpdateStatus identifies the status of an update operation.
type UpdateStatus string

const (
	UpdateStatusCurrent     UpdateStatus = "current"
	UpdateStatusAvailable   UpdateStatus = "available"
	UpdateStatusDownloading UpdateStatus = "downloading"
	UpdateStatusApplied     UpdateStatus = "applied"
	UpdateStatusRestarting  UpdateStatus = "restarting"
	UpdateStatusFailed      UpdateStatus = "failed"
)

// Response represents a single line of output sent back to the client.
type Response struct {
	Type           ResponseType `json:"type"`
	Data           string       `json:"data,omitempty"`
	Code           *int         `json:"code,omitempty"`
	Message        string       `json:"message,omitempty"`
	UpdateStatus   UpdateStatus `json:"update_status,omitempty"`
	CurrentVersion string       `json:"current_version,omitempty"`
	LatestVersion  string       `json:"latest_version,omitempty"`
}

// StdoutResponse creates a response for a line of stdout output.
func StdoutResponse(data string) Response {
	return Response{Type: TypeStdout, Data: data}
}

// StderrResponse creates a response for a line of stderr output.
func StderrResponse(data string) Response {
	return Response{Type: TypeStderr, Data: data}
}

// ExitResponse creates a response indicating the command has exited.
func ExitResponse(code int) Response {
	return Response{Type: TypeExit, Code: &code}
}

// ErrorResponse creates a response indicating a protocol or execution error.
func ErrorResponse(message string) Response {
	return Response{Type: TypeError, Message: message}
}

// UpdateResponse creates a response for update status updates.
func UpdateResponse(status UpdateStatus, currentVersion, latestVersion, message string) Response {
	return Response{
		Type:           TypeUpdate,
		UpdateStatus:   status,
		CurrentVersion: currentVersion,
		LatestVersion:  latestVersion,
		Message:        message,
	}
}

// VersionResponse creates a response with the current version.
func VersionResponse(currentVersion string) Response {
	return Response{
		Type:           TypeVersion,
		CurrentVersion: currentVersion,
	}
}

// ParseRequest reads a single newline-delimited JSON request from the reader.
// The request is limited to MaxRequestSize bytes to prevent memory exhaustion.
func ParseRequest(reader io.Reader) (*Request, error) {
	lr := &io.LimitedReader{R: reader, N: MaxRequestSize}
	br := bufio.NewReader(lr)

	line, err := br.ReadBytes('\n')
	if err != nil {
		if lr.N <= 0 {
			return nil, fmt.Errorf("request too large (max %d bytes)", MaxRequestSize)
		}
		return nil, fmt.Errorf("reading request: %w", err)
	}

	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		return nil, fmt.Errorf("parsing request JSON: %w", err)
	}

	// Validate: must have either Command or Action, but not both empty
	if req.Command == "" && req.Action == "" {
		return nil, fmt.Errorf("request must have either command or action")
	}

	// Validate Action if provided
	if req.Action != "" {
		switch req.Action {
		case "update", "check-update", "version":
			// valid actions
		default:
			return nil, fmt.Errorf("unknown action: %s", req.Action)
		}
	}

	return &req, nil
}

// WriteResponse marshals a response as newline-delimited JSON to the writer.
func WriteResponse(writer io.Writer, resp Response) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshaling response: %w", err)
	}
	data = append(data, '\n')
	_, err = writer.Write(data)
	if err != nil {
		return fmt.Errorf("writing response: %w", err)
	}
	return nil
}
