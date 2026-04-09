package manager

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/seaweedfs/seaweed-up/pkg/config"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
)

// GitHub repos that host SeaweedFS release tarballs. Enterprise releases
// live in a private repository and require authenticated access via
// $GITHUB_TOKEN or $GH_TOKEN.
const (
	ossReleaseOwner        = "seaweedfs"
	ossReleaseRepo         = "seaweedfs"
	enterpriseReleaseOwner = "seaweedfs"
	enterpriseReleaseRepo  = "artifactory"
)

// ReleaseOwnerRepo returns the GitHub owner/repo pair that the manager
// should pull SeaweedFS binaries from, picking the enterprise repo when
// Enterprise mode is enabled.
func (m *Manager) ReleaseOwnerRepo() (owner, repo string) {
	if m.Enterprise {
		return enterpriseReleaseOwner, enterpriseReleaseRepo
	}
	return ossReleaseOwner, ossReleaseRepo
}

// shellSingleQuote wraps s in single quotes so it is safe to embed in a POSIX
// shell command. Any single quote inside s is escaped by closing the quoted
// string, inserting an escaped quote, and reopening: ' -> '\''.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

type Manager struct {
	User               string // username to login to the SSH server
	IdentityFile       string // path to the private key file
	UsePassword        bool   // use password instead of identity file for ssh connection
	ProxyUrl           string // proxy URL for binary download
	ComponentToDeploy  string
	Version            string
	SshPort            int
	PrepareVolumeDisks bool
	HostPrep           bool
	ForceRestart       bool
	// Enterprise, when true, pulls SeaweedFS release binaries from the
	// private enterprise release repo (github.com/seaweedfs/artifactory)
	// instead of the public OSS repo. Requires $GITHUB_TOKEN or $GH_TOKEN
	// with read access to that repo; the controller downloads the binary
	// locally and uploads it to each target host over SSH so that remote
	// hosts never need a GitHub token.
	Enterprise bool
	// TargetArch is the target binary architecture for enterprise downloads
	// (e.g. "amd64", "arm64"). Defaults to "amd64" when Enterprise is true.
	// Unused for OSS deploys, which detect the arch on each remote host.
	TargetArch string
	// Concurrency limits the number of concurrent per-host deploy goroutines.
	// If <=0, deploys run with unlimited concurrency (default behavior).
	Concurrency int

	skipConfig bool
	skipEnable bool
	skipStart  bool
	sudoPass   string
	confDir    string
	dataDir    string

	// enterpriseBinaryOnce guards a one-shot fetch of the enterprise
	// binary + md5 so that concurrent per-host deploys share a single
	// in-memory copy instead of each hitting the GitHub API.
	enterpriseBinaryOnce sync.Once
	enterpriseBinary     []byte
	enterpriseBinaryMD5  []byte
	enterpriseAssetName  string
	enterpriseFetchErr   error

	// prepareHostAddressFn overrides PrepareHostAddress for tests. When nil,
	// PrepareAllHosts calls PrepareHostAddress directly.
	prepareHostAddressFn func(ip string, sshPort int) error
}

func NewManager() *Manager {
	return &Manager{
		skipConfig: false,
		skipEnable: false,
		skipStart:  false,
		Version:    "",
		sudoPass:   "",
	}
}

func info(message string) {
	fmt.Println("[INFO] " + message)
}

func (m *Manager) sudo(op operator.CommandOperator, cmd string) error {
	info("[execute] " + cmd)
	if m.sudoPass == "" {
		return op.Execute(cmd)
	}
	defer fmt.Println()
	return op.Execute(fmt.Sprintf("echo %s | sudo -S %s", shellSingleQuote(m.sudoPass), cmd))
}

// ensureEnterpriseBinary fetches the enterprise tarball + md5 once and
// caches them on the Manager so that subsequent per-host deploys reuse the
// same in-memory copies. Safe for concurrent callers.
//
// The asset suffix is derived from m.TargetArch (defaulting to amd64 if
// unset) and the standard "_full_large_disk" build flavor used by the
// install script.
func (m *Manager) ensureEnterpriseBinary(ctx context.Context) (tarball, md5 []byte, assetName string, err error) {
	m.enterpriseBinaryOnce.Do(func() {
		arch := m.TargetArch
		if arch == "" {
			arch = "amd64"
		}
		suffix := config.BuildAssetSuffix("linux", arch, true, true)
		version := m.Version
		if version == "" {
			version = "0" // GitHubLatestRelease: "0" means latest
		}
		bin, sum, name, resolved, ferr := config.FetchReleaseBinary(ctx, enterpriseReleaseOwner, enterpriseReleaseRepo, version, suffix)
		if ferr != nil {
			m.enterpriseFetchErr = fmt.Errorf("fetch enterprise release %s/%s %s: %w", enterpriseReleaseOwner, enterpriseReleaseRepo, suffix, ferr)
			return
		}
		m.enterpriseBinary = bin
		m.enterpriseBinaryMD5 = sum
		m.enterpriseAssetName = name
		if m.Version == "" {
			m.Version = resolved
		}
	})
	return m.enterpriseBinary, m.enterpriseBinaryMD5, m.enterpriseAssetName, m.enterpriseFetchErr
}
