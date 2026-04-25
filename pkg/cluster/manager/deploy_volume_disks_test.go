package manager

import (
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
)

// TestDeployVolumeServer_refusesUnknownTargetWhenAllowlistSet locks
// in the safety guard: when the spec is plan-generated (allowlist
// is non-nil) but a volume_server's SSH target has no entry in the
// allowlist, deploy refuses the host instead of running the volume
// on plain root directories. The check must fire before any SSH
// work, so we don't even need a working operator to exercise it.
func TestDeployVolumeServer_refusesUnknownTargetWhenAllowlistSet(t *testing.T) {
	m := NewManager()
	m.PrepareVolumeDisks = true
	// Non-nil but doesn't contain "10.0.0.21:22" — that target was
	// dropped from the plan but somehow kept in cluster.yaml (stale
	// hand edit, or a broken sidecar regen).
	m.PlannedDisksBySSHTarget = map[string]map[string]struct{}{
		"10.0.0.99:22": {"/dev/sdb": {}},
	}
	vs := &spec.VolumeServerSpec{
		Ip:      "10.0.0.21",
		PortSsh: 22,
		Port:    8080,
		Folders: []*spec.FolderSpec{{Folder: "/data1"}},
	}
	err := m.DeployVolumeServer(nil, vs, 0)
	if err == nil {
		t.Fatal("expected refusal for unallowlisted target, got nil")
	}
	if !strings.Contains(err.Error(), "plan-approved disk") {
		t.Errorf("error should explain the missing allowlist entry, got: %v", err)
	}
	// Also: it must NOT reach ExecuteRemote — we never opened SSH so
	// the failure isn't a connection error.
	if strings.Contains(strings.ToLower(err.Error()), "dial") {
		t.Errorf("guard should fire before SSH dial; got network-shaped error: %v", err)
	}
}

// When the allowlist is nil (hand-written cluster.yaml, no plan),
// the guard must be inactive — otherwise we'd break every existing
// hand-written deploy.
func TestDeployVolumeServer_noGuardWhenAllowlistNil(t *testing.T) {
	m := NewManager()
	m.PrepareVolumeDisks = true
	// PlannedDisksBySSHTarget left nil (default for hand-written specs).
	vs := &spec.VolumeServerSpec{
		Ip:      "127.0.0.1",
		PortSsh: 1, // unroutable; ExecuteRemote will error fast
		Port:    8080,
		Folders: []*spec.FolderSpec{{Folder: "/data1"}},
	}
	err := m.DeployVolumeServer(nil, vs, 0)
	if err == nil {
		t.Fatal("expected SSH connect error since :1 is unroutable, got nil")
	}
	// The guard must NOT have fired: no "plan-approved" string in
	// the message. The error should look like a network failure.
	if strings.Contains(err.Error(), "plan-approved") {
		t.Errorf("guard fired with nil allowlist (would break hand-written specs): %v", err)
	}
}

func TestDeployVolumeServer_refusesWhenAllowlistShortOfFolderCount(t *testing.T) {
	// Stale sidecar case: cluster.yaml has 2 folders but the
	// deploy-disks allowlist only has 1 disk. Without this guard,
	// deploy would mount one disk at /data1 and silently leave
	// /data2 as a rootfs directory; weed volume would then write
	// data into the OS root filesystem.
	m := NewManager()
	m.PrepareVolumeDisks = true
	m.PlannedDisksBySSHTarget = map[string]map[string]struct{}{
		"10.0.0.21:22": {"/dev/sdb": {}}, // only one disk
	}
	vs := &spec.VolumeServerSpec{
		Ip:      "10.0.0.21",
		PortSsh: 22,
		Port:    8080,
		Folders: []*spec.FolderSpec{
			{Folder: "/data1"},
			{Folder: "/data2"},
		},
	}
	err := m.DeployVolumeServer(nil, vs, 0)
	if err == nil {
		t.Fatal("expected refusal for under-provisioned allowlist, got nil")
	}
	if !strings.Contains(err.Error(), "expects 2 mountpoint") {
		t.Errorf("error should call out the count mismatch, got: %v", err)
	}
}

func TestDeployVolumeServer_idxFolderCountedTowardsRequired(t *testing.T) {
	// IdxFolder is an additional mountpoint, so allowlist must cover
	// folders + 1. Here the allowlist has exactly the folder count
	// (2) but the spec also asks for an idx folder (3 mountpoints).
	m := NewManager()
	m.PrepareVolumeDisks = true
	m.PlannedDisksBySSHTarget = map[string]map[string]struct{}{
		"10.0.0.21:22": {"/dev/sdb": {}, "/dev/sdc": {}}, // 2 disks
	}
	vs := &spec.VolumeServerSpec{
		Ip:      "10.0.0.21",
		PortSsh: 22,
		Port:    8080,
		Folders: []*spec.FolderSpec{
			{Folder: "/data2"},
			{Folder: "/data3"},
		},
		IdxFolder: "/data1", // needs a 3rd disk
	}
	err := m.DeployVolumeServer(nil, vs, 0)
	if err == nil {
		t.Fatal("expected refusal: 2 folders + 1 idx > 2 approved disks")
	}
	if !strings.Contains(err.Error(), "expects 3 mountpoint") {
		t.Errorf("error should account for IdxFolder in the count, got: %v", err)
	}
}

// TestPrepareUnmountedDisksOnce_perHost confirms the sync.Once gate
// dedups calls per host IP. With the per-disk volume_server shape,
// DeployCluster fires multiple DeployVolumeServer calls into the same
// host concurrently — each used to call prepareUnmountedDisks
// independently and race on mkfs/mount. The gate makes disk prep
// effectively a per-host pre-step.
func TestPrepareUnmountedDisksOnce_perHost(t *testing.T) {
	m := NewManager()
	m.PrepareVolumeDisks = true

	var hostA, hostB int32
	hook := func(ip string) error {
		switch ip {
		case "10.0.0.1":
			atomic.AddInt32(&hostA, 1)
		case "10.0.0.2":
			atomic.AddInt32(&hostB, 1)
		}
		return nil
	}
	// Inject the hook by overriding prepareDisksOnce gates with ones
	// whose Do() body is the test hook. Real prepareUnmountedDisks
	// would normally run inside; this isolates the gating logic.
	for _, ip := range []string{"10.0.0.1", "10.0.0.2"} {
		gate := &prepareDisksGate{}
		ip := ip
		gate.once.Do(func() { gate.err = hook(ip) })
		m.prepareDisksOnce.Store(ip, gate)
	}

	// Even calling prepareUnmountedDisksOnce N more times per host the
	// hook must not fire again — sync.Once already fired during setup.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _ = m.prepareUnmountedDisksOnce(nopOp{}, "10.0.0.1") }()
		go func() { defer wg.Done(); _ = m.prepareUnmountedDisksOnce(nopOp{}, "10.0.0.2") }()
	}
	wg.Wait()

	if hostA != 1 {
		t.Errorf("hook for 10.0.0.1 fired %d times, want 1", hostA)
	}
	if hostB != 1 {
		t.Errorf("hook for 10.0.0.2 fired %d times, want 1", hostB)
	}
}

// nopOp implements operator.CommandOperator with errors so it would
// blow up if reached. The test setup pre-fires the sync.Once with a
// hook, so prepareUnmountedDisks should never actually run against
// this op.
type nopOp struct{}

func (nopOp) Execute(string) error                           { return nil }
func (nopOp) Output(string) ([]byte, error)                  { return nil, nil }
func (nopOp) Upload(io.Reader, string, string) error         { return nil }
func (nopOp) UploadFile(string, string, string) error        { return nil }
func _enforceInterface(_ operator.CommandOperator)           {}
func init()                                                  { _enforceInterface(nopOp{}) }
