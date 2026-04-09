package config

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cheggaaa/pb/v3"
	"golang.org/x/net/context/ctxhttp"
)

// Release collects data about a single release on GitHub.
type Release struct {
	Name        string    `json:"name"`
	TagName     string    `json:"tag_name"`
	Draft       bool      `json:"draft"`
	PreRelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []Asset   `json:"assets"`

	Version string `json:"-"` // set manually in the code
}

// Asset is a file uploaded and attached to a release.
type Asset struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

const githubAPITimeout = 30 * time.Second

// githubError is returned by the GitHub API, e.g. for rate-limiting.
type githubError struct {
	Message string
}

// githubToken returns a GitHub API token from the environment if one is set.
// Checked in order: GITHUB_TOKEN (populated automatically in GitHub Actions),
// then GH_TOKEN (used by the gh CLI). Returns "" when neither is set, in
// which case API calls are made anonymously.
func githubToken() string {
	for _, env := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return v
		}
	}
	return ""
}

// setGithubAuthHeaders adds an Authorization header to req when a GitHub
// token is available in the environment. GitHub's anonymous rate limit is
// 60 req/hour per IP, which is easily exhausted on shared CI runners;
// authenticated requests get a much higher quota.
func setGithubAuthHeaders(req *http.Request) {
	if token := githubToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
}

// GitHubLatestRelease uses the GitHub API to get information about the specific
// release of a repository.
func GitHubLatestRelease(ctx context.Context, ver string, owner, repo string) (Release, error) {
	ctx, cancel := context.WithTimeout(ctx, githubAPITimeout)
	defer cancel()

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases", owner, repo)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}

	// pin API version 3
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	setGithubAuthHeaders(req)

	res, err := ctxhttp.Do(ctx, http.DefaultClient, req)
	if err != nil {
		return Release{}, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		content := res.Header.Get("Content-Type")
		if strings.Contains(content, "application/json") {
			// try to decode error message
			var msg githubError
			jerr := json.NewDecoder(res.Body).Decode(&msg)
			if jerr == nil {
				return Release{}, fmt.Errorf("unexpected status %v (%v) returned, message:\n  %v", res.StatusCode, res.Status, msg.Message)
			}
		}

		return Release{}, fmt.Errorf("unexpected status %v (%v) returned", res.StatusCode, res.Status)
	}

	buf, err := io.ReadAll(res.Body)
	if err != nil {
		return Release{}, err
	}

	var release Release
	var releaseList []Release
	err = json.Unmarshal(buf, &releaseList)
	if err != nil {
		return Release{}, err
	}
	if ver == "0" {
		release = releaseList[0]
		log.Printf("latest version is %v / %v", release.TagName, release.PublishedAt.Local())
	} else {
		for _, r := range releaseList {
			if r.TagName == ver {
				release = r
				break
			}
		}
	}

	if release.TagName == "" {
		return Release{}, fmt.Errorf("can not find the specific version")
	}

	release.Version = release.TagName
	return release, nil
}

// BuildAssetSuffix returns the SeaweedFS release asset file suffix matching
// the given target os/arch and build flavor flags. Example:
// "linux_amd64_full_large_disk.tar.gz".
func BuildAssetSuffix(targetOS, arch string, isLargeDisk, isFull bool) string {
	largeDiskSuffix := ""
	if isLargeDisk {
		largeDiskSuffix = "_large_disk"
	}
	fullSuffix := ""
	if isFull {
		fullSuffix = "_full"
	}
	return fmt.Sprintf("%s_%s%s%s.tar.gz", targetOS, arch, fullSuffix, largeDiskSuffix)
}

// FetchReleaseBinary resolves a GitHub release (owner/repo, version or "0"
// for latest) and downloads the asset matching assetSuffix along with its
// .md5 checksum file. It returns the raw tarball bytes, the raw md5 file
// bytes, the resolved asset filename, and the resolved release version.
//
// Asset data is pulled via the GitHub API asset URL which requires an
// Authorization header for private repositories — this is supplied
// automatically from $GITHUB_TOKEN / $GH_TOKEN by getGithubData.
func FetchReleaseBinary(ctx context.Context, owner, repo, ver, assetSuffix string) (tarball []byte, md5Data []byte, assetName string, version string, err error) {
	rel, err := GitHubLatestRelease(ctx, ver, owner, repo)
	if err != nil {
		return nil, nil, "", "", err
	}
	_, md5Data, err = getGithubDataFile(ctx, rel.Assets, assetSuffix+".md5")
	if err != nil {
		return nil, nil, "", "", fmt.Errorf("fetch md5 for %s: %w", assetSuffix, err)
	}
	assetName, tarball, err = getGithubDataFile(ctx, rel.Assets, assetSuffix)
	if err != nil {
		return nil, nil, "", "", fmt.Errorf("fetch binary %s: %w", assetSuffix, err)
	}
	return tarball, md5Data, assetName, rel.Version, nil
}

func getGithubData(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	// request binary data
	req.Header.Set("Accept", "application/octet-stream")
	// Asset download URLs are served from api.github.com; authenticate when
	// possible to avoid anonymous rate-limiting on shared CI runners.
	if strings.Contains(url, "api.github.com") {
		setGithubAuthHeaders(req)
	}

	res, err := ctxhttp.Do(ctx, http.DefaultClient, req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %v (%v) returned", res.StatusCode, res.Status)
	}

	readerCloser := withProgressBar(res.Body, int(res.ContentLength))
	defer readerCloser.Close()

	buf, err := io.ReadAll(readerCloser)
	if err != nil {
		return nil, err
	}

	return buf, nil
}

func getGithubDataFile(ctx context.Context, assets []Asset, suffix string) (filename string, data []byte, err error) {
	var url string
	for _, a := range assets {
		if strings.HasSuffix(a.Name, suffix) {
			url = a.URL
			filename = a.Name
			break
		}
	}

	if url == "" {
		return "", nil, fmt.Errorf("unable to find file with suffix %v", suffix)
	}

	log.Printf("download %v\n", filename)
	data, err = getGithubData(ctx, url)
	if err != nil {
		return "", nil, err
	}

	return filename, data, nil
}

func withProgressBar(r io.ReadCloser, length int) io.ReadCloser {
	bar := pb.Simple.New(length).Start()
	return bar.NewProxyReader(r)
}
