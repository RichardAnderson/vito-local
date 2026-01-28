//go:build !linux

package server

import (
	"fmt"
	"log/slog"
	"net"
	"os"
)

// getPeerCredentials returns stub credentials on non-Linux platforms.
// This is only permitted when VITO_DEV_MODE=1 is set, to prevent accidental
// deployment without real authentication. In dev mode, the current process
// credentials are returned, allowing any local connection to authenticate.
func getPeerCredentials(conn *net.UnixConn) (*PeerCredentials, error) {
	if os.Getenv("VITO_DEV_MODE") != "1" {
		return nil, fmt.Errorf("SO_PEERCRED authentication is not available on this platform; set VITO_DEV_MODE=1 to bypass for development")
	}
	slog.Warn("SO_PEERCRED not available on this platform, returning current process credentials (dev mode only)")
	return &PeerCredentials{
		UID: uint32(os.Getuid()),
		GID: uint32(os.Getgid()),
		PID: int32(os.Getpid()),
	}, nil
}
