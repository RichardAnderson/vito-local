package server

import (
	"net"
	"os"
	"strings"
	"testing"
)

func TestGetPeerCredentials(t *testing.T) {
	sockPath := tempSocketPath(t)

	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sockPath, Net: "unix"})
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	done := make(chan *PeerCredentials, 1)
	errCh := make(chan error, 1)

	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close()

		creds, err := getPeerCredentials(conn)
		if err != nil {
			errCh <- err
			return
		}
		done <- creds
	}()

	client, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: sockPath, Net: "unix"})
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer client.Close()

	select {
	case creds := <-done:
		expectedUID := uint32(os.Getuid())
		if creds.UID != expectedUID {
			t.Errorf("expected UID %d, got %d", expectedUID, creds.UID)
		}
	case err := <-errCh:
		t.Fatalf("error getting credentials: %v", err)
	}
}

func TestAuthorizeConnection_Authorized(t *testing.T) {
	sockPath := tempSocketPath(t)

	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sockPath, Net: "unix"})
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	done := make(chan error, 1)

	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()

		_, err = AuthorizeConnection(conn, uint32(os.Getuid()))
		done <- err
	}()

	client, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: sockPath, Net: "unix"})
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer client.Close()

	if err := <-done; err != nil {
		t.Fatalf("authorization should succeed: %v", err)
	}
}

func TestAuthorizeConnection_Unauthorized(t *testing.T) {
	sockPath := tempSocketPath(t)

	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sockPath, Net: "unix"})
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	done := make(chan error, 1)

	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()

		_, err = AuthorizeConnection(conn, 99999)
		done <- err
	}()

	client, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: sockPath, Net: "unix"})
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer client.Close()

	err = <-done
	if err == nil {
		t.Fatal("authorization should fail for wrong UID")
	}
	if !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("expected unauthorized error, got: %v", err)
	}
}
