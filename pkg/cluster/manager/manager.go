package manager

import (
	"fmt"
	"strings"
	"sync"

	"github.com/seaweedfs/seaweed-up/pkg/operator"
)

// prepareDisksGate pairs a sync.Once with the result of the single
// prepareUnmountedDisks call so all volume_servers on the same host see
// the same outcome instead of each independently retrying.
type prepareDisksGate struct {
	once sync.Once
	err  error
}

// GitHub repos that host SeaweedFS release tarballs. Both repos are
// public; the Manager picks between them via the Enterprise flag.
const (
	ossReleaseOwner        = "seaweedfs"
	ossReleaseRepo         = "seaweedfs"
	enterpriseReleaseOwner = "seaweedfs"
	enterpriseReleaseRepo  = "artifactory"
)

// Enterprise releases publish assets under a different naming scheme
// than the OSS repo:
//
//	OSS:         linux_amd64_full_large_disk.tar.gz
//	Enterprise:  weed-enterprise-linux_amd64_large_disk.tar.gz
//
// The enterprise build is a single "full" flavor, so there is no
// "_full" segment. The binary inside the tarball is still `weed`, so
// the existing install.sh unpack path works unchanged.
const enterpriseAssetPrefix = "weed-enterprise-"

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
	EnvoyVersion       string // envoy release to pin; when empty the latest is looked up from github.com/envoyproxy/envoy
	SshPort            int
	PrepareVolumeDisks bool
	// PlannedDisksBySSHTarget is the optional plan-side allowlist of
	// block devices deploy is allowed to mkfs+mount, keyed by
	// `<ip>:<ssh-port>` (the same key inventory.ProbeHosts dedups by).
	// Keying on the SSH target rather than just IP correctly handles
	// inventories where two SSH endpoints share an IP but differ in
	// port (uncommon but legal — see the inventory schema). The value
	// is the set of /dev paths plan classified as eligible (fresh or
	// claimed-at-/dataN, not ephemeral, not excluded, not foreign).
	// When non-nil, prepareUnmountedDisks restricts itself to disks in
	// the corresponding target's set; when nil it falls back to its
	// historical "every unmounted prefix-matching disk" behavior
	// (preserves backwards compatibility for hand-written cluster.yaml
	// files that don't ship a plan-emitted allowlist).
	PlannedDisksBySSHTarget map[string]map[string]struct{}
	// prepareDisksOnce gates prepareUnmountedDisks per SSH target.
	// With the per-disk volume_server shape, deploy fans out multiple
	// volume_server entries on the same host concurrently — each would
	// otherwise call prepareUnmountedDisks and race on mkfs/mount
	// assignment. The sync.Once per ip:ssh-port makes the disk prep
	// effectively a per-target pre-step.
	prepareDisksOnce sync.Map // <ip>:<ssh-port> -> *prepareDisksGate
	// requiredDisksByTarget aggregates each SSH target's mountpoint
	// demand (sum of Folders+IdxFolder across every volume_server
	// pointing at it). Populated once by DeployCluster before the
	// concurrent volume-server fan-out so DeployVolumeServer's
	// allowlist check sees the host total rather than per-spec
	// folder count — necessary for --volume-server-shape=per-disk
	// where one host has many one-folder specs.
	requiredDisksByTarget map[string]int
	HostPrep         bool
	ForceRestart     bool
	// Enterprise, when true, pulls SeaweedFS release binaries from the
	// public enterprise release repo (github.com/seaweedfs/artifactory)
	// instead of the standard OSS repo (github.com/seaweedfs/seaweedfs).
	// Both repos are public; no authentication is required, though
	// setting $GITHUB_TOKEN is still recommended to avoid the 60 req/hr
	// anonymous rate limit on the release metadata lookup.
	Enterprise bool
	// Concurrency limits the number of concurrent per-host deploy goroutines.
	// If <=0, deploys run with unlimited concurrency (default behavior).
	Concurrency int

	skipConfig bool
	skipEnable bool
	skipStart  bool
	sudoPass   string
	confDir    string
	dataDir    string

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
