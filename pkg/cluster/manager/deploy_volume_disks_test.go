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

// TestDeployVolumeServer_planGeneratedNoSidecarSkipsStaticButKeepsRuntime
// covers the --mount-disks=false + missing-sidecar scenario for a
// plan-generated cluster.yaml. The cmd layer leaves
// PlannedDisksBySSHTarget nil (sidecar absent) but sets PlanGenerated
// based on the marker. The static count guard is gated on the
// allowlist and must NOT fire (no sidecar to compare against), but
// the runtime mountpoint check is gated on PlanGenerated and would
// fire after SSH succeeds. Here SSH fails fast (port :1) so we just
// verify the static-guard string isn't present — i.e. the path goes
// through to SSH instead of bailing on the pre-flight count.
func TestDeployVolumeServer_planGeneratedNoSidecarSkipsStaticGuard(t *testing.T) {
	m := NewManager()
	m.PlanGenerated = true
	// PlannedDisksBySSHTarget left nil to mimic --mount-disks=false
	// reaching DeployVolumeServer without the loaded sidecar.
	vs := &spec.VolumeServerSpec{
		Ip: "127.0.0.1", PortSsh: 1, Port: 8080,
		Folders: []*spec.FolderSpec{{Folder: "/data1"}},
	}
	err := m.DeployVolumeServer(nil, vs, 0)
	if err == nil {
		t.Fatal("expected SSH connect failure, got nil")
	}
	if strings.Contains(err.Error(), "plan-approved disk") {
		t.Errorf("static count guard fired with no sidecar: %v", err)
	}
}

// TestComputeVolumeTargetDemand_aggregatesPerTarget locks in both
// per-target rollups: the mountpoint demand (sum of folders + idx)
// and the volume_server entry count. The mountpoint count drives
// the allowlist comparison; the entry count drives the
// operator-facing error wording (we used to back-derive it from the
// aggregate, which overstated the count for per-host shape — one
// volume_server with N folders looked like N volume_servers).
func TestComputeVolumeTargetDemand_aggregatesPerTarget(t *testing.T) {
	// per-host shape: one entry, multiple folders + idx.
	v0 := &spec.VolumeServerSpec{
		Ip: "10.0.0.20", PortSsh: 22,
		Folders:   []*spec.FolderSpec{{Folder: "/data2"}, {Folder: "/data3"}},
		IdxFolder: "/data1",
	}
	// per-disk shape: two one-folder entries on the same target.
	v1 := &spec.VolumeServerSpec{Ip: "10.0.0.21", PortSsh: 22, Folders: []*spec.FolderSpec{{Folder: "/data1"}}}
	v2 := &spec.VolumeServerSpec{Ip: "10.0.0.21", PortSsh: 22, Folders: []*spec.FolderSpec{{Folder: "/data2"}}}
	// Same IP, different SSH port — separate bucket.
	v3 := &spec.VolumeServerSpec{Ip: "10.0.0.21", PortSsh: 2222, Folders: []*spec.FolderSpec{{Folder: "/data1"}}}
	// Different host with idx; idx contributes to mountpoint demand.
	v4 := &spec.VolumeServerSpec{Ip: "10.0.0.22", PortSsh: 22, Folders: []*spec.FolderSpec{{Folder: "/data2"}}, IdxFolder: "/data1"}

	mounts, servers := computeVolumeTargetDemand([]*spec.VolumeServerSpec{v0, v1, v2, v3, v4, nil})
	wantMounts := map[string]int{
		"10.0.0.20:22":   3, // v0: 2 folders + 1 idx
		"10.0.0.21:22":   2, // v1 + v2
		"10.0.0.21:2222": 1, // v3
		"10.0.0.22:22":   2, // v4: 1 folder + 1 idx
	}
	wantServers := map[string]int{
		"10.0.0.20:22":   1, // per-host shape — single entry
		"10.0.0.21:22":   2, // per-disk shape — two entries
		"10.0.0.21:2222": 1,
		"10.0.0.22:22":   1,
	}
	for k, v := range wantMounts {
		if mounts[k] != v {
			t.Errorf("mountpoints[%s] = %d, want %d", k, mounts[k], v)
		}
	}
	for k, v := range wantServers {
		if servers[k] != v {
			t.Errorf("servers[%s] = %d, want %d", k, servers[k], v)
		}
	}
}

// TestDeployVolumeServer_perHostShape_errorNamesActualServerCount
// pins down the count-accuracy fix: the error message must report
// the actual number of volume_server entries (1, in this per-host
// shape with 3 folders), not the aggregate mountpoint count (3).
// The old back-derived count overstated the server count when one
// volume_server had multiple folders.
func TestDeployVolumeServer_perHostShape_errorNamesActualServerCount(t *testing.T) {
	m := NewManager()
	m.PrepareVolumeDisks = true
	m.PlannedDisksBySSHTarget = map[string]map[string]struct{}{
		"10.0.0.20:22": {"/dev/sdb": {}}, // one stale disk
	}
	v0 := &spec.VolumeServerSpec{
		Ip: "10.0.0.20", PortSsh: 22, Port: 8080,
		Folders: []*spec.FolderSpec{
			{Folder: "/data1"}, {Folder: "/data2"}, {Folder: "/data3"},
		},
	}
	m.requiredDisksByTarget, m.volumeServerCountByTarget = computeVolumeTargetDemand([]*spec.VolumeServerSpec{v0})

	err := m.DeployVolumeServer(nil, v0, 0)
	if err == nil {
		t.Fatal("expected refusal: 3 mountpoints needed but only 1 approved")
	}
	if !strings.Contains(err.Error(), "expects 3 mountpoint") {
		t.Errorf("error should report aggregate mountpoint count (3), got: %v", err)
	}
	// The fix: report the actual server count (1), not 3 (which is
	// what back-deriving from the mountpoint aggregate would yield).
	if !strings.Contains(err.Error(), "the 1 volume_server(s)") {
		t.Errorf("error should name the actual server count (1), got: %v", err)
	}
}

// TestDeployVolumeServer_refusesPerDiskShapeWithStaleSidecar is the
// regression test for the per-spec-vs-per-target gap. Two one-folder
// specs on the same SSH target each individually pass the per-spec
// folder count, but the host's aggregate (2) exceeds the stale
// sidecar's one approved disk. Without aggregation the second
// /data<N> would get mkdir'd on rootfs.
func TestDeployVolumeServer_refusesPerDiskShapeWithStaleSidecar(t *testing.T) {
	m := NewManager()
	m.PrepareVolumeDisks = true
	m.PlannedDisksBySSHTarget = map[string]map[string]struct{}{
		"10.0.0.21:22": {"/dev/sdb": {}}, // one disk
	}
	// Mimic what DeployCluster would do before the fan-out.
	v1 := &spec.VolumeServerSpec{
		Ip: "10.0.0.21", PortSsh: 22, Port: 8080,
		Folders: []*spec.FolderSpec{{Folder: "/data1"}},
	}
	v2 := &spec.VolumeServerSpec{
		Ip: "10.0.0.21", PortSsh: 22, Port: 8081,
		Folders: []*spec.FolderSpec{{Folder: "/data2"}},
	}
	m.requiredDisksByTarget, m.volumeServerCountByTarget = computeVolumeTargetDemand([]*spec.VolumeServerSpec{v1, v2})

	err := m.DeployVolumeServer(nil, v1, 0)
	if err == nil {
		t.Fatal("expected refusal: aggregate of 2 mountpoints on target with 1 approved disk")
	}
	if !strings.Contains(err.Error(), "expects 2 mountpoint") {
		t.Errorf("error should report the aggregate count (2), got: %v", err)
	}
	if !strings.Contains(err.Error(), "volume_server(s) on this target") {
		t.Errorf("error should label the scope as per-target, got: %v", err)
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

// scriptedOp is a CommandOperator stub for tests that need to assert
// against, or vary the response of, op.Output / op.Execute calls.
type scriptedOp struct {
	output func(cmd string) ([]byte, error)
	exec   func(cmd string) error
}

func (s scriptedOp) Execute(c string) error {
	if s.exec == nil {
		return nil
	}
	return s.exec(c)
}
func (s scriptedOp) Output(c string) ([]byte, error) {
	if s.output == nil {
		return nil, nil
	}
	return s.output(c)
}
func (scriptedOp) Upload(io.Reader, string, string) error  { return nil }
func (scriptedOp) UploadFile(string, string, string) error { return nil }

// TestVerifyVolumeFoldersAreMountpoints_allMounted: the happy path —
// prepareUnmountedDisks did its job; every -dir/-dir.idx path is a
// real mountpoint. The check should pass cleanly.
func TestVerifyVolumeFoldersAreMountpoints_allMounted(t *testing.T) {
	m := NewManager()
	vs := &spec.VolumeServerSpec{
		Folders: []*spec.FolderSpec{{Folder: "/data1"}, {Folder: "/data2"}},
	}
	op := scriptedOp{output: func(cmd string) ([]byte, error) {
		if !strings.Contains(cmd, "mountpoint -q") {
			t.Errorf("expected mountpoint command, got: %s", cmd)
		}
		return nil, nil // empty = all mounted
	}}
	if err := m.verifyVolumeFoldersAreMountpoints(op, vs); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

// TestVerifyVolumeFoldersAreMountpoints_oneDrifted: this is the
// regression case for the reviewer's finding. The static count guard
// passed (sidecar had two approved disks), but at deploy time one of
// them acquired a partition or was mounted elsewhere, so
// prepareUnmountedDisks only mounted one disk. The runtime check
// fires and refuses to proceed before ensureVolumeFolders mkdirs on
// rootfs.
func TestVerifyVolumeFoldersAreMountpoints_oneDrifted(t *testing.T) {
	m := NewManager()
	vs := &spec.VolumeServerSpec{
		Folders: []*spec.FolderSpec{{Folder: "/data1"}, {Folder: "/data2"}},
	}
	op := scriptedOp{output: func(string) ([]byte, error) {
		// /data2 isn't a mountpoint — script echoes its path.
		return []byte("/data2\n"), nil
	}}
	err := m.verifyVolumeFoldersAreMountpoints(op, vs)
	if err == nil {
		t.Fatal("expected refusal, got nil")
	}
	if !strings.Contains(err.Error(), "/data2") {
		t.Errorf("error should name the drifted path, got: %v", err)
	}
	if !strings.Contains(err.Error(), "root filesystem") {
		t.Errorf("error should explain the rootfs failure mode, got: %v", err)
	}
}

// TestVerifyVolumeFoldersAreMountpoints_idxFolderChecked: the idx
// folder counts as a separate mountpoint and must be verified too.
// A drifted idx disk would otherwise cause weed volume -dir.idx to
// write index files into rootfs.
func TestVerifyVolumeFoldersAreMountpoints_idxFolderChecked(t *testing.T) {
	m := NewManager()
	vs := &spec.VolumeServerSpec{
		Folders:   []*spec.FolderSpec{{Folder: "/data2"}},
		IdxFolder: "/data1",
	}
	var seen string
	op := scriptedOp{output: func(cmd string) ([]byte, error) {
		seen = cmd
		return []byte("/data1\n"), nil // idx folder drifted
	}}
	err := m.verifyVolumeFoldersAreMountpoints(op, vs)
	if err == nil {
		t.Fatal("expected refusal when idx folder is not a mountpoint")
	}
	if !strings.Contains(seen, "/data1") || !strings.Contains(seen, "/data2") {
		t.Errorf("check should include both data and idx folders, got: %s", seen)
	}
	if !strings.Contains(err.Error(), "/data1") {
		t.Errorf("error should name /data1 (idx folder), got: %v", err)
	}
}

// TestVerifyVolumeFoldersAreMountpoints_emptyPaths: a spec with no
// folders is a no-op (other validation prevents this in practice).
func TestVerifyVolumeFoldersAreMountpoints_emptyPaths(t *testing.T) {
	m := NewManager()
	vs := &spec.VolumeServerSpec{}
	op := scriptedOp{output: func(string) ([]byte, error) {
		t.Error("op.Output must not be called when there's nothing to verify")
		return nil, nil
	}}
	if err := m.verifyVolumeFoldersAreMountpoints(op, vs); err != nil {
		t.Errorf("expected nil for empty paths, got %v", err)
	}
}
