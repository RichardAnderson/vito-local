//go:build linux

package server

import (
	"fmt"
	"net"
	"syscall"
)

func getPeerCredentials(conn *net.UnixConn) (*PeerCredentials, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("getting syscall conn: %w", err)
	}

	var cred *syscall.Ucred
	var credErr error

	err = raw.Control(func(fd uintptr) {
		cred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if err != nil {
		return nil, fmt.Errorf("control: %w", err)
	}
	if credErr != nil {
		return nil, fmt.Errorf("getsockopt SO_PEERCRED: %w", credErr)
	}

	return &PeerCredentials{
		UID: cred.Uid,
		GID: cred.Gid,
		PID: cred.Pid,
	}, nil
}
