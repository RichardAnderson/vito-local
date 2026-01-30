package updater

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"v1.2.3", "1.2.3"},
		{"1.2.3", "1.2.3"},
		{"  v1.2.3  ", "1.2.3"},
		{"v0.1.0", "0.1.0"},
		{"", ""},
	}

	for _, tc := range tests {
		result := normalizeVersion(tc.input)
		if result != tc.expected {
			t.Errorf("normalizeVersion(%q) = %q, expected %q", tc.input, result, tc.expected)
		}
	}
}

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		current  string
		latest   string
		expected bool
	}{
		{"1.0.0", "1.0.1", true},
		{"1.0.0", "1.1.0", true},
		{"1.0.0", "2.0.0", true},
		{"1.0.1", "1.0.0", false},
		{"1.1.0", "1.0.0", false},
		{"2.0.0", "1.0.0", false},
		{"1.0.0", "1.0.0", false},
		{"dev", "1.0.0", true},
		{"", "1.0.0", true},
		{"1.0.0", "dev", false},
		{"0.1.0", "0.1.1", true},
		{"0.1.10", "0.1.9", false},
		{"0.1.9", "0.1.10", true},
	}

	for _, tc := range tests {
		result := isNewerVersion(tc.current, tc.latest)
		if result != tc.expected {
			t.Errorf("isNewerVersion(%q, %q) = %v, expected %v", tc.current, tc.latest, result, tc.expected)
		}
	}
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected []int
	}{
		{"1.2.3", []int{1, 2, 3}},
		{"0.1.0", []int{0, 1, 0}},
		{"1.0.0-beta", []int{1, 0, 0}},
		{"2.0", []int{2, 0}},
		{"1", []int{1}},
	}

	for _, tc := range tests {
		result := parseVersion(tc.input)
		if len(result) != len(tc.expected) {
			t.Errorf("parseVersion(%q) length = %d, expected %d", tc.input, len(result), len(tc.expected))
			continue
		}
		for i, v := range result {
			if v != tc.expected[i] {
				t.Errorf("parseVersion(%q)[%d] = %d, expected %d", tc.input, i, v, tc.expected[i])
			}
		}
	}
}

func TestGitHubClient_GetLatestRelease(t *testing.T) {
	// Create a mock server
	release := Release{
		TagName: "v0.2.0",
		Assets: []Asset{
			{Name: "vito-root-service-linux-amd64.tar.gz", BrowserDownloadURL: "https://example.com/download"},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(release)
	}))
	defer server.Close()

	client := NewGitHubClientWithURL(server.URL)
	result, err := client.GetLatestRelease()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TagName != "v0.2.0" {
		t.Errorf("expected tag v0.2.0, got %q", result.TagName)
	}
	if len(result.Assets) != 1 {
		t.Errorf("expected 1 asset, got %d", len(result.Assets))
	}
}

func TestGitHubClient_FindAssetForPlatform(t *testing.T) {
	release := &Release{
		TagName: "v0.2.0",
		Assets: []Asset{
			{Name: "vito-root-service-linux-amd64.tar.gz", BrowserDownloadURL: "https://example.com/linux-amd64"},
			{Name: "vito-root-service-linux-arm64.tar.gz", BrowserDownloadURL: "https://example.com/linux-arm64"},
			{Name: "vito-root-service-darwin-amd64.tar.gz", BrowserDownloadURL: "https://example.com/darwin-amd64"},
			{Name: "vito-root-service-darwin-arm64.tar.gz", BrowserDownloadURL: "https://example.com/darwin-arm64"},
		},
	}

	client := NewGitHubClient()
	asset, err := client.FindAssetForPlatform(release)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The result depends on the current platform, so just verify we got something
	if asset == nil {
		t.Error("expected to find an asset")
	}
	if asset.BrowserDownloadURL == "" {
		t.Error("expected asset to have a download URL")
	}
}

func TestGitHubClient_FindAssetForPlatform_NotFound(t *testing.T) {
	release := &Release{
		TagName: "v0.2.0",
		Assets: []Asset{
			{Name: "vito-root-service-windows-amd64.zip", BrowserDownloadURL: "https://example.com/windows"},
		},
	}

	client := NewGitHubClient()
	_, err := client.FindAssetForPlatform(release)
	if err == nil {
		t.Error("expected error when no matching asset found")
	}
}

func TestUpdater_CheckUpdate_CurrentVersion(t *testing.T) {
	release := Release{
		TagName: "v0.1.0",
		Assets:  []Asset{},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(release)
	}))
	defer server.Close()

	u := NewWithGitHubClient("v0.1.0", "/usr/local/bin/vito-root-service", NewGitHubClientWithURL(server.URL))
	result, err := u.CheckUpdate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "current" {
		t.Errorf("expected status 'current', got %q", result.Status)
	}
}

func TestUpdater_CheckUpdate_Available(t *testing.T) {
	release := Release{
		TagName: "v0.2.0",
		Assets:  []Asset{},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(release)
	}))
	defer server.Close()

	u := NewWithGitHubClient("v0.1.0", "/usr/local/bin/vito-root-service", NewGitHubClientWithURL(server.URL))
	result, err := u.CheckUpdate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "available" {
		t.Errorf("expected status 'available', got %q", result.Status)
	}
	if result.LatestVersion != "v0.2.0" {
		t.Errorf("expected latest version 'v0.2.0', got %q", result.LatestVersion)
	}
}

func TestValidateBinary(t *testing.T) {
	// Create a temp file that's too small
	tmpDir := t.TempDir()
	smallFile := filepath.Join(tmpDir, "small")
	if err := os.WriteFile(smallFile, []byte("too small"), 0755); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	err := ValidateBinary(smallFile)
	if err == nil {
		t.Error("expected error for small binary")
	}

	// Create a file that's large enough
	largeFile := filepath.Join(tmpDir, "large")
	data := make([]byte, minBinarySize+1)
	if err := os.WriteFile(largeFile, data, 0755); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	err = ValidateBinary(largeFile)
	if err != nil {
		t.Errorf("unexpected error for valid binary: %v", err)
	}
}

func TestSanitizePath(t *testing.T) {
	tests := []struct {
		base    string
		name    string
		wantErr bool
	}{
		{"/tmp/foo", "binary", false},
		{"/tmp/foo", "../etc/passwd", true},
		{"/tmp/foo", "/etc/passwd", true},
		{"/tmp/foo", "subdir/binary", false},
	}

	for _, tc := range tests {
		_, err := sanitizePath(tc.base, tc.name)
		if tc.wantErr && err == nil {
			t.Errorf("sanitizePath(%q, %q) expected error", tc.base, tc.name)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("sanitizePath(%q, %q) unexpected error: %v", tc.base, tc.name, err)
		}
	}
}
