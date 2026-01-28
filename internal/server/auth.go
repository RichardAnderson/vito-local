package server

import (
	"fmt"
	"net"
)

// PeerCredentials holds the identity of the connecting process.
type PeerCredentials struct {
	UID uint32
	GID uint32
	PID int32
}

// AuthorizeConnection checks that the connecting peer's UID matches the allowed UID.
func AuthorizeConnection(conn *net.UnixConn, allowedUID uint32) (*PeerCredentials, error) {
	creds, err := getPeerCredentials(conn)
	if err != nil {
		return nil, fmt.Errorf("getting peer credentials: %w", err)
	}

	if creds.UID != allowedUID {
		return creds, fmt.Errorf("unauthorized: peer UID %d does not match allowed UID %d", creds.UID, allowedUID)
	}

	return creds, nil
}
