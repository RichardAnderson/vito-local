package config

import (
	"fmt"
	"os/user"
	"strconv"
	"strings"
	"time"
)

// Config holds the service configuration.
type Config struct {
	SocketPath     string
	AllowedUser    string
	AllowedUID     uint32
	SocketGroup    string
	SocketGroupGID uint32
	SocketMode     uint32
	LogLevel       string
	LogJSON        bool
	MaxExecTimeout time.Duration
	MaxConnections int
}

var validLogLevels = map[string]bool{
	"debug": true,
	"info":  true,
	"warn":  true,
	"error": true,
}

// New creates a new Config, resolving the user to a UID.
func New(socketPath, username, logLevel string, logJSON bool) (*Config, error) {
	if socketPath == "" {
		socketPath = "/run/vito-root.sock"
	}

	if username == "" {
		return nil, fmt.Errorf("allowed user must be specified")
	}

	u, err := user.Lookup(username)
	if err != nil {
		return nil, fmt.Errorf("looking up user %q: %w", username, err)
	}

	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("parsing UID %q: %w", u.Uid, err)
	}

	logLevel = strings.ToLower(logLevel)
	if logLevel == "" {
		logLevel = "info"
	}
	if !validLogLevels[logLevel] {
		return nil, fmt.Errorf("invalid log level %q (valid: debug, info, warn, error)", logLevel)
	}

	// Resolve group GID for socket ownership.
	// Try looking up a group matching the username; fall back to user's primary group.
	var socketGID uint32
	grp, grpErr := user.LookupGroup(username)
	if grpErr == nil {
		gid, err := strconv.ParseUint(grp.Gid, 10, 32)
		if err == nil {
			socketGID = uint32(gid)
		}
	}
	if socketGID == 0 && grpErr != nil {
		gid, err := strconv.ParseUint(u.Gid, 10, 32)
		if err == nil {
			socketGID = uint32(gid)
		}
	}

	return &Config{
		SocketPath:     socketPath,
		AllowedUser:    username,
		AllowedUID:     uint32(uid),
		SocketGroup:    username,
		SocketGroupGID: socketGID,
		SocketMode:     0660,
		LogLevel:       logLevel,
		LogJSON:        logJSON,
		MaxConnections: 100,
	}, nil
}
