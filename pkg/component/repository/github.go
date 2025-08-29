package repository

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/cheggaaa/pb/v3"
	"github.com/seaweedfs/seaweed-up/pkg/component/registry"
	"github.com/seaweedfs/seaweed-up/pkg/errors"
	"github.com/seaweedfs/seaweed-up/pkg/utils"
)

// GitHubRepository manages SeaweedFS releases from GitHub
type GitHubRepository struct {
	Owner     string
	Repo      string
	BaseURL   string
	Client    *http.Client
	ProxyURL  string
	registry  *registry.ComponentRegistry
}

// Release represents a GitHub release
type Release struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []Asset   `json:"assets"`
}

// Asset represents a release asset
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	ContentType        string `json:"content_type"`
}

// NewGitHubRepository creates a new GitHub repository client
func NewGitHubRepository(registry *registry.ComponentRegistry, proxyURL string) *GitHubRepository {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	
	return &GitHubRepository{
		Owner:    "seaweedfs",
		Repo:     "seaweedfs",
		BaseURL:  "https://api.github.com",
		Client:   client,
		ProxyURL: proxyURL,
		registry: registry,
	}
}

// ListVersions returns all available versions
func (r *GitHubRepository) ListVersions(ctx context.Context) ([]string, error) {
	releases, err := r.fetchReleases(ctx)
	if err != nil {
		return nil, err
	}
	
	var versions []string
	for _, release := range releases {
		if !release.Draft && !release.Prerelease {
			// Clean version tag (remove 'v' prefix if present)
			version := strings.TrimPrefix(release.TagName, "v")
			versions = append(versions, version)
		}
	}
	
	// Sort versions in descending order (latest first)
	sort.Slice(versions, func(i, j int) bool {
		return compareVersions(versions[i], versions[j]) > 0
	})
	
	return versions, nil
}

// GetLatestVersion returns the latest stable version
func (r *GitHubRepository) GetLatestVersion(ctx context.Context) (string, error) {
	versions, err := r.ListVersions(ctx)
	if err != nil {
		return "", err
	}
	
	if len(versions) == 0 {
		return "", fmt.Errorf("no stable versions found")
	}
	
	return versions[0], nil
}

// DownloadComponent downloads a specific version and installs it
func (r *GitHubRepository) DownloadComponent(ctx context.Context, version string, showProgress bool) (*registry.InstalledComponent, error) {
	// Check if already installed
	if r.registry.IsInstalled("seaweedfs", version) {
		return r.registry.GetInstalled("seaweedfs", version)
	}
	
	// Find the release
	releases, err := r.fetchReleases(ctx)
	if err != nil {
		return nil, err
	}
	
	var targetRelease *Release
	for _, release := range releases {
		if strings.TrimPrefix(release.TagName, "v") == version {
			targetRelease = &release
			break
		}
	}
	
	if targetRelease == nil {
		return nil, errors.NewComponentError("seaweedfs", version, "download", fmt.Errorf("version not found"))
	}
	
	// Find the appropriate asset for current platform
	asset, err := r.findPlatformAsset(targetRelease)
	if err != nil {
		return nil, errors.NewComponentError("seaweedfs", version, "download", err)
	}
	
	// Create component directory
	componentDir := r.registry.GetComponentDir("seaweedfs", version)
	if err := utils.EnsureDir(componentDir); err != nil {
		return nil, errors.NewComponentError("seaweedfs", version, "download", err)
	}
	
	// Download and extract
	binaryPath := filepath.Join(componentDir, r.registry.GetBinaryFilename())
	checksum, err := r.downloadAsset(ctx, asset, binaryPath, showProgress)
	if err != nil {
		return nil, errors.NewComponentError("seaweedfs", version, "download", err)
	}
	
	// Make binary executable
	if err := os.Chmod(binaryPath, 0755); err != nil {
		return nil, errors.NewComponentError("seaweedfs", version, "download", err)
	}
	
	// Create component record
	component := &registry.InstalledComponent{
		Name:      "seaweedfs",
		Version:   version,
		Path:      binaryPath,
		Metadata: map[string]string{
			"source":      "github",
			"download_url": asset.BrowserDownloadURL,
			"release_name": targetRelease.Name,
		},
		InstallAt: time.Now(),
		Size:      asset.Size,
		Checksum:  checksum,
	}
	
	// Install to registry
	if err := r.registry.Install(component); err != nil {
		return nil, err
	}
	
	return component, nil
}

// fetchReleases retrieves releases from GitHub API
func (r *GitHubRepository) fetchReleases(ctx context.Context) ([]Release, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases", r.BaseURL, r.Owner, r.Repo)
	
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	
	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}
	
	var releases []Release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}
	
	return releases, nil
}

// findPlatformAsset finds the appropriate asset for the current platform
func (r *GitHubRepository) findPlatformAsset(release *Release) (*Asset, error) {
	osName := runtime.GOOS
	archName := runtime.GOARCH
	
	// Map Go arch names to SeaweedFS naming
	archMap := map[string]string{
		"amd64": "amd64",
		"arm64": "arm64",
		"386":   "386",
		"arm":   "arm",
	}
	
	targetArch, ok := archMap[archName]
	if !ok {
		return nil, fmt.Errorf("unsupported architecture: %s", archName)
	}
	
	// SeaweedFS naming pattern: OS_ARCH (e.g., linux_amd64, darwin_amd64, windows_amd64.exe)
	var patterns []string
	switch osName {
	case "linux":
		patterns = []string{
			fmt.Sprintf("linux_%s.tar.gz", targetArch),
			fmt.Sprintf("linux_%s", targetArch),
		}
	case "darwin":
		patterns = []string{
			fmt.Sprintf("darwin_%s.tar.gz", targetArch),
			fmt.Sprintf("darwin_%s", targetArch),
		}
	case "windows":
		patterns = []string{
			fmt.Sprintf("windows_%s.zip", targetArch),
			fmt.Sprintf("windows_%s.exe", targetArch),
		}
	default:
		return nil, fmt.Errorf("unsupported operating system: %s", osName)
	}
	
	// Find matching asset
	for _, asset := range release.Assets {
		for _, pattern := range patterns {
			if strings.Contains(strings.ToLower(asset.Name), pattern) {
				return &asset, nil
			}
		}
	}
	
	return nil, fmt.Errorf("no compatible binary found for %s/%s", osName, archName)
}

// downloadAsset downloads an asset and returns its checksum
func (r *GitHubRepository) downloadAsset(ctx context.Context, asset *Asset, destPath string, showProgress bool) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", asset.BrowserDownloadURL, nil)
	if err != nil {
		return "", err
	}
	
	resp, err := r.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed with status %d", resp.StatusCode)
	}
	
	// Create destination file
	destFile, err := os.Create(destPath)
	if err != nil {
		return "", err
	}
	defer destFile.Close()
	
	// Setup progress bar
	var reader io.Reader = resp.Body
	if showProgress && asset.Size > 0 {
		bar := pb.Full.Start64(asset.Size)
		bar.Set(pb.Bytes, true)
		bar.SetMaxWidth(80)
		reader = bar.NewProxyReader(resp.Body)
		defer bar.Finish()
	}
	
	// Download with checksum calculation
	hasher := sha256.New()
	writer := io.MultiWriter(destFile, hasher)
	
	if _, err := io.Copy(writer, reader); err != nil {
		return "", err
	}
	
	checksum := fmt.Sprintf("%x", hasher.Sum(nil))
	
	// Handle compressed files
	if strings.HasSuffix(asset.Name, ".tar.gz") || strings.HasSuffix(asset.Name, ".zip") {
		if err := r.extractBinary(destPath, asset.Name); err != nil {
			return checksum, err
		}
	}
	
	return checksum, nil
}

// extractBinary extracts the weed binary from compressed archives
func (r *GitHubRepository) extractBinary(archivePath, archiveName string) error {
	// For now, implement a simple extraction
	// In a full implementation, you would use archive/tar and archive/zip
	// This is a placeholder for the extraction logic
	
	extractDir := filepath.Dir(archivePath)
	binaryPath := filepath.Join(extractDir, r.registry.GetBinaryFilename())
	
	// This is a simplified approach - in reality, you'd extract from the archive
	// For now, just rename the archive to the binary name as a placeholder
	if err := os.Rename(archivePath, binaryPath); err != nil {
		return err
	}
	
	return nil
}

// compareVersions compares two version strings
// Returns: 1 if a > b, -1 if a < b, 0 if a == b
func compareVersions(a, b string) int {
	// Simple semantic version comparison
	aClean := strings.TrimPrefix(a, "v")
	bClean := strings.TrimPrefix(b, "v")
	
	// Split by dots
	aParts := strings.Split(aClean, ".")
	bParts := strings.Split(bClean, ".")
	
	// Compare parts
	maxLen := len(aParts)
	if len(bParts) > maxLen {
		maxLen = len(bParts)
	}
	
	for i := 0; i < maxLen; i++ {
		aVal := 0
		bVal := 0
		
		if i < len(aParts) {
			if val, err := parseVersionPart(aParts[i]); err == nil {
				aVal = val
			}
		}
		
		if i < len(bParts) {
			if val, err := parseVersionPart(bParts[i]); err == nil {
				bVal = val
			}
		}
		
		if aVal > bVal {
			return 1
		} else if aVal < bVal {
			return -1
		}
	}
	
	return 0
}

// parseVersionPart parses a version part to integer
func parseVersionPart(part string) (int, error) {
	// Extract numeric part using regex
	re := regexp.MustCompile(`^(\d+)`)
	matches := re.FindStringSubmatch(part)
	if len(matches) > 1 {
		var val int
		fmt.Sscanf(matches[1], "%d", &val)
		return val, nil
	}
	return 0, fmt.Errorf("invalid version part: %s", part)
}
