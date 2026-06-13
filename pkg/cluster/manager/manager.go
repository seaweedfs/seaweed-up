package manager

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/progress"
	"github.com/seaweedfs/seaweed-up/pkg/config"
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
// string, inserting an escaped quote, and reopening: ' -> '\”.
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
	// PlanGenerated is true when the cluster.yaml was emitted by
	// `cluster plan -o` (detected via the header marker). Tracked
	// separately from PlannedDisksBySSHTarget because a plan-generated
	// spec can legitimately reach deploy without its sidecar — e.g.
	// `--mount-disks=false` deploys keep the sidecar optional so
	// operators can ship master-only or service-only updates without
	// it. The runtime mountpoint check still needs to fire in that
	// case (a missing /dataN would still be silently mkdir'd on
	// rootfs), so DeployVolumeServer gates the runtime check on this
	// flag while the static count guard stays gated on
	// PlannedDisksBySSHTarget (which requires the sidecar contents).
	PlanGenerated bool
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
	// volumeServerCountByTarget records how many volume_server entries
	// point at each SSH target. Populated alongside requiredDisksByTarget
	// so DeployVolumeServer's error wording can name the actual server
	// count (1 in per-host shape with N folders; N in per-disk shape).
	// Without this we'd have to back-derive from the aggregated mount
	// count, which overstates the count for per-host shape.
	volumeServerCountByTarget map[string]int
	HostPrep                  bool
	ForceRestart              bool
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
	// devAsset holds the resolved rolling "dev" build of the Go `weed`
	// server (set by resolveDevAsset when Version == "dev"); nil otherwise.
	devAsset *config.DevAsset
	// rustDevAsset holds the resolved rolling "dev" build of the standalone
	// Rust `weed-volume` binary, set only when Version == "dev" and the spec
	// has a Rust volume server (the dev release datestamps it separately
	// from the Go asset); nil otherwise.
	rustDevAsset *config.DevAsset

	// prepareHostAddressFn overrides PrepareHostAddress for tests. When nil,
	// PrepareAllHosts calls PrepareHostAddress directly.
	prepareHostAddressFn func(ip string, sshPort int) error

	// Reporter renders the per-component console. Defaults to a plain
	// (line-by-line) reporter via NewManager so non-TTY runs and tests keep
	// their historical output; the cmd layer swaps in a live reporter on a
	// TTY. All component logging routes through it (see info/taskFor).
	Reporter progress.Reporter
	// tasks maps a component instance id ("volume3") to its progress line.
	// DeployCluster/UpgradeCluster populate it before fanning work out, then
	// only read it concurrently, so a plain RWMutex suffices.
	tasksMu sync.RWMutex
	tasks   map[string]*progress.Task
}

func NewManager() *Manager {
	return &Manager{
		skipConfig: false,
		skipEnable: false,
		skipStart:  false,
		Version:    "",
		sudoPass:   "",
		Reporter:   progress.NewPlain(os.Stdout),
	}
}

// reporter returns the manager's console reporter, defaulting to plain so
// directly-constructed &Manager{} values (e.g. in tests) never nil-panic.
func (m *Manager) reporter() progress.Reporter {
	if m.Reporter == nil {
		m.Reporter = progress.NewPlain(os.Stdout)
	}
	return m.Reporter
}

// info logs a non-component message above the live block (live mode) or as a
// plain "[INFO] …" line (plain mode). It replaces the old package-level
// info() so that, during a live render, stray log lines can't corrupt the
// in-place block.
func (m *Manager) info(message string) {
	m.reporter().Log(message)
}

// registerTasks installs the per-instance task map for the duration of a
// deploy/upgrade so callbacks deep in the call tree can find their line.
func (m *Manager) registerTasks(tasks map[string]*progress.Task) {
	m.tasksMu.Lock()
	m.tasks = tasks
	m.tasksMu.Unlock()
}

// taskFor returns the progress task for a component instance id. When none was
// pre-registered (direct Deploy*Server calls, tests) it returns a detached
// task so callers never need a nil check.
func (m *Manager) taskFor(id string) *progress.Task {
	m.tasksMu.RLock()
	t := m.tasks[id]
	m.tasksMu.RUnlock()
	if t != nil {
		return t
	}
	return m.reporter().AddTask(id, id)
}

// bindTask redirects an operator's streamed command output to the component's
// progress line, so install/disk-prep output drives that line's detail (live
// mode) instead of scrolling to stdout. No-op for operators that don't support
// redirection (test fakes).
func bindTask(op operator.CommandOperator, task *progress.Task) {
	if s, ok := op.(operator.OutputSink); ok {
		w := task.Writer()
		s.SetOutput(w, w)
	}
}

func (m *Manager) sudo(op operator.CommandOperator, cmd string) error {
	// In live mode the streamed command output already drives the component
	// line, so suppress the verbose per-command trace that would otherwise
	// scroll above the block. Plain mode keeps the historical "[INFO]
	// [execute] …" line.
	if !m.reporter().Live() {
		m.info("[execute] " + cmd)
	}
	// Already root: run the command directly (the box may not even have
	// sudo installed).
	if m.User == "root" {
		return op.Execute(cmd)
	}
	// Non-root without a sudo password: assume passwordless (NOPASSWD)
	// sudo and elevate non-interactively. Running bare here was a bug —
	// privileged commands (mkdir under /, writes to /etc/seaweed, ...)
	// would fail with permission denied for a normal login user.
	if m.sudoPass == "" {
		return op.Execute("sudo -n " + cmd)
	}
	defer fmt.Println()
	return op.Execute(fmt.Sprintf("echo %s | sudo -S %s", shellSingleQuote(m.sudoPass), cmd))
}
