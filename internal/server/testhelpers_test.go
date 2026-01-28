package server

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

var testSocketCounter atomic.Int64

func init() {
	// Enable dev mode for non-Linux test builds so getPeerCredentials works.
	os.Setenv("VITO_DEV_MODE", "1")
}

// tempSocketPath returns a short socket path safe for macOS's 104-char limit.
// Uses /tmp directly instead of t.TempDir() which generates long paths.
func tempSocketPath(t *testing.T) string {
	t.Helper()
	n := testSocketCounter.Add(1)
	path := filepath.Join("/tmp", fmt.Sprintf("vt%d_%d.sock", os.Getpid(), n))
	t.Cleanup(func() {
		os.Remove(path)
	})
	return path
}
