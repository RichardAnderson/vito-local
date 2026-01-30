package updater

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// ProgressCallback is called with status updates during the update process.
type ProgressCallback func(status, message string)

// UpdateResult contains the result of an update check or update operation.
type UpdateResult struct {
	Status         string // "current", "available", "downloading", "applied", "restarting", "failed"
	CurrentVersion string
	LatestVersion  string
	Message        string
}

// Updater orchestrates the self-update process.
type Updater struct {
	CurrentVersion string
	BinaryPath     string
	GitHub         *GitHubClient
	downloader     *Downloader
}

// New creates a new Updater with the given configuration.
func New(currentVersion, binaryPath string) *Updater {
	return &Updater{
		CurrentVersion: currentVersion,
		BinaryPath:     binaryPath,
		GitHub:         NewGitHubClient(),
	}
}

// NewWithGitHubClient creates a new Updater with a custom GitHub client.
func NewWithGitHubClient(currentVersion, binaryPath string, github *GitHubClient) *Updater {
	return &Updater{
		CurrentVersion: currentVersion,
		BinaryPath:     binaryPath,
		GitHub:         github,
	}
}

// CheckUpdate checks if a newer version is available.
func (u *Updater) CheckUpdate() (*UpdateResult, error) {
	release, err := u.GitHub.GetLatestRelease()
	if err != nil {
		return &UpdateResult{
			Status:         "failed",
			CurrentVersion: u.CurrentVersion,
			Message:        fmt.Sprintf("failed to fetch latest release: %v", err),
		}, err
	}

	latestVersion := normalizeVersion(release.TagName)
	currentVersion := normalizeVersion(u.CurrentVersion)

	if !isNewerVersion(currentVersion, latestVersion) {
		return &UpdateResult{
			Status:         "current",
			CurrentVersion: u.CurrentVersion,
			LatestVersion:  release.TagName,
			Message:        "already running the latest version",
		}, nil
	}

	return &UpdateResult{
		Status:         "available",
		CurrentVersion: u.CurrentVersion,
		LatestVersion:  release.TagName,
		Message:        fmt.Sprintf("update available: %s -> %s", u.CurrentVersion, release.TagName),
	}, nil
}

// PerformUpdate performs the full update process, calling onProgress with status updates.
// The context can be used to cancel the update (e.g., if the client disconnects).
func (u *Updater) PerformUpdate(ctx context.Context, onProgress ProgressCallback) (*UpdateResult, error) {
	// Check for updates first
	release, err := u.GitHub.GetLatestRelease()
	if err != nil {
		result := &UpdateResult{
			Status:         "failed",
			CurrentVersion: u.CurrentVersion,
			Message:        fmt.Sprintf("failed to fetch latest release: %v", err),
		}
		if onProgress != nil {
			onProgress(result.Status, result.Message)
		}
		return result, err
	}

	latestVersion := normalizeVersion(release.TagName)
	currentVersion := normalizeVersion(u.CurrentVersion)

	if !isNewerVersion(currentVersion, latestVersion) {
		result := &UpdateResult{
			Status:         "current",
			CurrentVersion: u.CurrentVersion,
			LatestVersion:  release.TagName,
			Message:        "already running the latest version",
		}
		if onProgress != nil {
			onProgress(result.Status, result.Message)
		}
		return result, nil
	}

	// Check for cancellation before starting download
	if err := ctx.Err(); err != nil {
		return &UpdateResult{
			Status:         "failed",
			CurrentVersion: u.CurrentVersion,
			LatestVersion:  release.TagName,
			Message:        "update cancelled",
		}, err
	}

	// Find the asset for our platform
	asset, err := u.GitHub.FindAssetForPlatform(release)
	if err != nil {
		result := &UpdateResult{
			Status:         "failed",
			CurrentVersion: u.CurrentVersion,
			LatestVersion:  release.TagName,
			Message:        fmt.Sprintf("no compatible binary found: %v", err),
		}
		if onProgress != nil {
			onProgress(result.Status, result.Message)
		}
		return result, err
	}

	// Notify: downloading
	if onProgress != nil {
		onProgress("downloading", fmt.Sprintf("downloading %s", asset.Name))
	}

	// Download and extract
	u.downloader = NewDownloader()
	defer u.downloader.Cleanup()

	binaryName := filepath.Base(u.BinaryPath)
	extractedPath, err := u.downloader.DownloadAndExtract(ctx, asset.BrowserDownloadURL, binaryName)
	if err != nil {
		result := &UpdateResult{
			Status:         "failed",
			CurrentVersion: u.CurrentVersion,
			LatestVersion:  release.TagName,
			Message:        fmt.Sprintf("download/extract failed: %v", err),
		}
		if onProgress != nil {
			onProgress(result.Status, result.Message)
		}
		return result, err
	}

	// Atomic replace
	if err := AtomicReplace(extractedPath, u.BinaryPath); err != nil {
		result := &UpdateResult{
			Status:         "failed",
			CurrentVersion: u.CurrentVersion,
			LatestVersion:  release.TagName,
			Message:        fmt.Sprintf("failed to replace binary: %v", err),
		}
		if onProgress != nil {
			onProgress(result.Status, result.Message)
		}
		return result, err
	}

	// Notify: applied
	result := &UpdateResult{
		Status:         "applied",
		CurrentVersion: u.CurrentVersion,
		LatestVersion:  release.TagName,
		Message:        fmt.Sprintf("updated from %s to %s", u.CurrentVersion, release.TagName),
	}
	if onProgress != nil {
		onProgress(result.Status, result.Message)
	}

	return result, nil
}

// normalizeVersion removes the "v" prefix and any leading/trailing whitespace.
func normalizeVersion(version string) string {
	v := strings.TrimSpace(version)
	v = strings.TrimPrefix(v, "v")
	return v
}

// isNewerVersion returns true if latest is newer than current.
// Uses simple string comparison which works for semver-like versions.
func isNewerVersion(current, latest string) bool {
	// Handle "dev" or empty versions - always consider updates available
	if current == "" || current == "dev" {
		return latest != "" && latest != "dev"
	}

	// Parse semver-like versions
	currentParts := parseVersion(current)
	latestParts := parseVersion(latest)

	// Compare each part
	for i := 0; i < len(currentParts) && i < len(latestParts); i++ {
		if latestParts[i] > currentParts[i] {
			return true
		}
		if latestParts[i] < currentParts[i] {
			return false
		}
	}

	// If all compared parts are equal, the longer version is newer
	return len(latestParts) > len(currentParts)
}

// parseVersion parses a version string into numeric parts.
func parseVersion(v string) []int {
	parts := strings.Split(v, ".")
	result := make([]int, 0, len(parts))

	for _, part := range parts {
		// Handle pre-release suffixes like "1.0.0-beta"
		if idx := strings.IndexAny(part, "-+"); idx >= 0 {
			part = part[:idx]
		}

		var num int
		_, _ = fmt.Sscanf(part, "%d", &num) // Ignore error; non-numeric parts become 0
		result = append(result, num)
	}

	return result
}
