package updater

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"
)

const (
	defaultGitHubAPIURL = "https://api.github.com/repos/RichardAnderson/vito-local/releases/latest"
	defaultHTTPTimeout  = 30 * time.Second
)

// Release represents a GitHub release.
type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

// Asset represents a downloadable asset in a GitHub release.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// GitHubClient fetches release information from GitHub.
type GitHubClient struct {
	apiURL     string
	httpClient *http.Client
}

// NewGitHubClient creates a new GitHub client with default settings.
func NewGitHubClient() *GitHubClient {
	return &GitHubClient{
		apiURL: defaultGitHubAPIURL,
		httpClient: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
	}
}

// NewGitHubClientWithURL creates a new GitHub client with a custom API URL.
func NewGitHubClientWithURL(apiURL string) *GitHubClient {
	return &GitHubClient{
		apiURL: apiURL,
		httpClient: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
	}
}

// GetLatestRelease fetches the latest release from GitHub.
func (g *GitHubClient) GetLatestRelease() (*Release, error) {
	req, err := http.NewRequest(http.MethodGet, g.apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "vito-root-service")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decoding release: %w", err)
	}

	return &release, nil
}

// FindAssetForPlatform finds the appropriate asset for the current platform.
// It looks for assets matching patterns like:
// - vito-root-service-linux-amd64.tar.gz
// - vito-root-service-linux-arm64.tar.gz
func (g *GitHubClient) FindAssetForPlatform(release *Release) (*Asset, error) {
	os := runtime.GOOS
	arch := runtime.GOARCH

	// Build expected patterns
	patterns := []string{
		fmt.Sprintf("vito-root-service-%s-%s.tar.gz", os, arch),
		fmt.Sprintf("vito-root-service_%s_%s.tar.gz", os, arch),
		fmt.Sprintf("%s-%s.tar.gz", os, arch),
		fmt.Sprintf("%s_%s.tar.gz", os, arch),
	}

	for _, asset := range release.Assets {
		name := strings.ToLower(asset.Name)
		for _, pattern := range patterns {
			if strings.Contains(name, strings.ToLower(pattern)) || name == strings.ToLower(pattern) {
				return &asset, nil
			}
		}
	}

	// Fallback: look for any asset containing the arch
	archPatterns := []string{
		fmt.Sprintf("%s-%s", os, arch),
		fmt.Sprintf("%s_%s", os, arch),
	}

	for _, asset := range release.Assets {
		name := strings.ToLower(asset.Name)
		for _, pattern := range archPatterns {
			if strings.Contains(name, pattern) && strings.HasSuffix(name, ".tar.gz") {
				return &asset, nil
			}
		}
	}

	return nil, fmt.Errorf("no asset found for %s/%s", os, arch)
}
