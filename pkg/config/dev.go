package config

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"

	"golang.org/x/net/context/ctxhttp"
)

// DevTag is the rolling pre-release tag SeaweedFS publishes continuous
// builds under.
const DevTag = "dev"

// DevAsset is a resolved rolling-"dev" build artifact for one os/arch.
type DevAsset struct {
	// DownloadURL is the browser download URL of the tarball.
	DownloadURL string
	// Md5URL is DownloadURL + ".md5".
	Md5URL string
	// BuildID is the build datestamp embedded in the asset name
	// (e.g. "20260607-1918"). It changes on every dev build, so it is the
	// identity used to decide whether a re-upgrade is needed.
	BuildID string
}

// devAssetRe matches a dev asset name and captures its build datestamp.
// largeDisk builds are named weed-large-disk-<stamp>-linux-<arch>.tar.gz;
// regular builds weed-<stamp>-linux-<arch>.tar.gz. The \d right after the
// "weed-" prefix in the regular pattern keeps it from also matching the
// "weed-large-disk-" ones.
func devAssetRe(largeDisk bool, goArch string) *regexp.Regexp {
	if largeDisk {
		return regexp.MustCompile(`^weed-large-disk-(\d{8}-\d{4})-linux-` + regexp.QuoteMeta(goArch) + `\.tar\.gz$`)
	}
	return regexp.MustCompile(`^weed-(\d{8}-\d{4})-linux-` + regexp.QuoteMeta(goArch) + `\.tar\.gz$`)
}

// pickDevAsset returns the name and build datestamp of the newest dev
// asset for the given variant/arch. Datestamps are YYYYMMDD-HHMM, so a
// plain string comparison orders them chronologically.
func pickDevAsset(assets []Asset, largeDisk bool, goArch string) (name, buildID string, ok bool) {
	re := devAssetRe(largeDisk, goArch)
	for _, a := range assets {
		m := re.FindStringSubmatch(a.Name)
		if m == nil {
			continue
		}
		if buildID == "" || m[1] > buildID {
			buildID = m[1]
			name = a.Name
			ok = true
		}
	}
	return name, buildID, ok
}

// GitHubReleaseByTag fetches a single release by tag name.
func GitHubReleaseByTag(ctx context.Context, owner, repo, tag string) (Release, error) {
	ctx, cancel := context.WithTimeout(ctx, githubAPITimeout)
	defer cancel()

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s", owner, repo, tag)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	setGithubAuthHeaders(req)

	res, err := ctxhttp.Do(ctx, http.DefaultClient, req)
	if err != nil {
		return Release{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("github: GET %s returned %d", url, res.StatusCode)
	}

	var rel Release
	if err := json.NewDecoder(res.Body).Decode(&rel); err != nil {
		return Release{}, err
	}
	return rel, nil
}

// ResolveDevAsset resolves the newest rolling "dev" build for the given
// repo / variant / architecture into a concrete download.
func ResolveDevAsset(ctx context.Context, owner, repo string, largeDisk bool, goArch string) (DevAsset, error) {
	rel, err := GitHubReleaseByTag(ctx, owner, repo, DevTag)
	if err != nil {
		return DevAsset{}, fmt.Errorf("resolve dev release: %w", err)
	}
	name, buildID, ok := pickDevAsset(rel.Assets, largeDisk, goArch)
	if !ok {
		variant := "weed"
		if largeDisk {
			variant = "weed-large-disk"
		}
		return DevAsset{}, fmt.Errorf("no %s-*-linux-%s.tar.gz asset in the %q release", variant, goArch, DevTag)
	}
	dl := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s", owner, repo, DevTag, name)
	return DevAsset{DownloadURL: dl, Md5URL: dl + ".md5", BuildID: buildID}, nil
}
