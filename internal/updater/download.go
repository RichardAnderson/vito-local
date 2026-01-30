package updater

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	// minBinarySize is the minimum expected size for the binary (100KB)
	minBinarySize = 100 * 1024
)

// Downloader handles downloading and extracting update binaries.
type Downloader struct {
	httpClient *http.Client
	tempDir    string
}

// NewDownloader creates a new Downloader.
func NewDownloader() *Downloader {
	return &Downloader{
		httpClient: &http.Client{
			Timeout: 0, // No timeout for downloads (could be large)
		},
	}
}

// DownloadAndExtract downloads a tarball from the given URL and extracts the binary.
// It returns the path to the extracted binary. The context can be used to cancel the download.
func (d *Downloader) DownloadAndExtract(ctx context.Context, url, binaryName string) (string, error) {
	// Create temp directory
	tempDir, err := os.MkdirTemp("", "vito-update-*")
	if err != nil {
		return "", fmt.Errorf("creating temp directory: %w", err)
	}
	d.tempDir = tempDir

	// Download the tarball
	tarballPath := filepath.Join(tempDir, "update.tar.gz")
	if err := d.downloadFile(ctx, url, tarballPath); err != nil {
		return "", fmt.Errorf("downloading tarball: %w", err)
	}

	// Extract the binary
	binaryPath, err := d.extractBinary(tarballPath, binaryName, tempDir)
	if err != nil {
		return "", fmt.Errorf("extracting binary: %w", err)
	}

	return binaryPath, nil
}

// downloadFile downloads a file from the URL to the destination path.
// The context can be used to cancel the download.
func (d *Downloader) downloadFile(ctx context.Context, url, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP GET: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("creating file: %w", err)
	}
	defer out.Close()

	// Use a context-aware copy by wrapping the response body
	_, err = io.Copy(out, readerWithContext(ctx, resp.Body))
	if err != nil {
		return fmt.Errorf("writing file: %w", err)
	}

	return nil
}

// readerWithContext wraps a reader to respect context cancellation.
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func readerWithContext(ctx context.Context, r io.Reader) io.Reader {
	return &contextReader{ctx: ctx, r: r}
}

func (r *contextReader) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.r.Read(p)
	}
}

// maxExtractSize is the maximum size for extracted files (500MB) to prevent decompression bombs.
const maxExtractSize = 500 * 1024 * 1024

// extractBinary extracts the specified binary from a tar.gz archive.
func (d *Downloader) extractBinary(tarballPath, binaryName, destDir string) (string, error) {
	f, err := os.Open(tarballPath)
	if err != nil {
		return "", fmt.Errorf("opening tarball: %w", err)
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("creating gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("reading tar: %w", err)
		}

		// Look for the binary - it could be at root or in a subdirectory
		name := filepath.Base(header.Name)
		if name != binaryName || header.Typeflag != tar.TypeReg {
			continue
		}

		// Validate the extraction path to prevent path traversal
		extractedPath, err := sanitizePath(destDir, binaryName)
		if err != nil {
			return "", fmt.Errorf("invalid extraction path: %w", err)
		}

		outFile, err := os.OpenFile(extractedPath, os.O_CREATE|os.O_WRONLY, os.FileMode(header.Mode))
		if err != nil {
			return "", fmt.Errorf("creating output file: %w", err)
		}

		// Limit copy to prevent decompression bombs
		written, err := io.Copy(outFile, io.LimitReader(tr, maxExtractSize))
		outFile.Close()
		if err != nil {
			return "", fmt.Errorf("extracting file: %w", err)
		}

		// Verify minimum size
		if written < minBinarySize {
			os.Remove(extractedPath)
			return "", fmt.Errorf("binary too small (%d bytes), expected at least %d bytes", written, minBinarySize)
		}

		// Make executable
		if err := os.Chmod(extractedPath, 0755); err != nil {
			return "", fmt.Errorf("chmod: %w", err)
		}

		return extractedPath, nil
	}

	return "", fmt.Errorf("binary %q not found in tarball", binaryName)
}

// Cleanup removes the temporary directory and all its contents.
func (d *Downloader) Cleanup() {
	if d.tempDir != "" {
		os.RemoveAll(d.tempDir)
		d.tempDir = ""
	}
}

// ValidateBinary performs basic validation on the extracted binary.
func ValidateBinary(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat binary: %w", err)
	}

	if info.Size() < minBinarySize {
		return fmt.Errorf("binary too small (%d bytes)", info.Size())
	}

	// Check it's executable
	if info.Mode()&0111 == 0 {
		return fmt.Errorf("binary is not executable")
	}

	return nil
}

// AtomicReplace atomically replaces the target file with the source file.
// It first copies to a temporary location next to the target, then renames.
func AtomicReplace(srcPath, targetPath string) error {
	// Validate source
	if err := ValidateBinary(srcPath); err != nil {
		return fmt.Errorf("validating source binary: %w", err)
	}

	// Create temp file next to target for atomic rename
	dir := filepath.Dir(targetPath)
	base := filepath.Base(targetPath)
	tempPath := filepath.Join(dir, "."+base+".new")

	// Remove any existing temp file
	os.Remove(tempPath)

	// Copy source to temp location
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("opening source: %w", err)
	}
	defer src.Close()

	// Get source permissions
	srcInfo, err := src.Stat()
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}

	dst, err := os.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}

	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(tempPath)
		return fmt.Errorf("copying binary: %w", err)
	}
	dst.Close()

	// Validate the copy
	if err := ValidateBinary(tempPath); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("validating copied binary: %w", err)
	}

	// Preserve ownership if possible (requires root)
	targetInfo, err := os.Stat(targetPath)
	if err == nil {
		// Try to preserve ownership - ignore errors (might not have permission)
		if stat, ok := getFileStat(targetInfo); ok {
			_ = os.Chown(tempPath, int(stat.uid), int(stat.gid))
		}
	}

	// Atomic rename
	if err := os.Rename(tempPath, targetPath); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("atomic rename: %w", err)
	}

	return nil
}

// fileStat holds uid/gid for a file.
type fileStat struct {
	uid, gid uint32
}

// getFileStat extracts uid/gid from FileInfo on Unix systems.
func getFileStat(info os.FileInfo) (fileStat, bool) {
	// This is handled in a platform-specific way
	return getFileStatImpl(info)
}

// sanitizePath prevents path traversal attacks.
func sanitizePath(base, name string) (string, error) {
	// Clean the name to remove any .. or other tricks
	cleaned := filepath.Clean(name)
	if strings.HasPrefix(cleaned, "..") || strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("invalid path: %s", name)
	}
	full := filepath.Join(base, cleaned)
	// Ensure the result is still under base
	if !strings.HasPrefix(full, filepath.Clean(base)+string(os.PathSeparator)) && full != filepath.Clean(base) {
		return "", fmt.Errorf("path escapes base directory: %s", name)
	}
	return full, nil
}
