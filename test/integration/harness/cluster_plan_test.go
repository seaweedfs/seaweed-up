//go:build integration

package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

// TestClusterPlanGreenfield exercises `cluster plan -o cluster.yaml` end to
// end against three real SSH-reachable containers booted by the harness.
//
// The harness containers have no spare block devices (overlay rootfs only),
// so volume hosts will come back with `folders: []` — that's fine for this
// test, which is about the SSH probe path and the shape of the generated
// cluster.yaml, not the disk-provisioning arithmetic (covered by unit tests
// in pkg/cluster/plan).
func TestClusterPlanGreenfield(t *testing.T) {
	h := New(t, 3)
	hosts := h.Hosts()

	// Build a minimal inventory pointing at the container IPs, keyed
	// against the harness's generated SSH identity.
	invPath := filepath.Join(h.TempDir(), "inventory.yaml")
	if err := writeInventory(invPath, hosts, h.SSHKey()); err != nil {
		t.Fatalf("write inventory: %v", err)
	}
	outPath := filepath.Join(h.TempDir(), "cluster.yaml")

	// Build the seaweed-up binary and run `cluster plan -o`.
	h.BuildBinary(t)
	out, err := runPlan(h, invPath, outPath)
	if err != nil {
		t.Fatalf("cluster plan failed: %v\noutput:\n%s", err, out)
	}

	// Confirm the file was written and parses back as a Specification.
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read %s: %v", outPath, err)
	}
	var got spec.Specification
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal generated cluster.yaml: %v\nbody:\n%s", err, data)
	}

	// The probe facts are saved as a sidecar JSON so operators have a
	// record of what plan saw. cluster.yaml -> cluster.facts.json.
	factsPath := strings.TrimSuffix(outPath, ".yaml") + ".facts.json"
	factsData, err := os.ReadFile(factsPath)
	if err != nil {
		t.Fatalf("read %s: %v", factsPath, err)
	}
	if !json.Valid(factsData) {
		t.Errorf("facts file is not valid JSON:\n%s", factsData)
	}

	// Shape assertions. The harness containers expose only an overlay
	// rootfs — no free block devices — so the volume role gets dropped
	// (Report.VolumeHostsNoDisks, surfaced on stderr). Masters and the
	// filer colocated with host[0] do still land in cluster.yaml.
	if got.Name != "harness-cluster" {
		t.Errorf("cluster_name: got %q, want harness-cluster", got.Name)
	}
	if len(got.MasterServers) != 3 {
		t.Fatalf("master_servers: got %d, want 3", len(got.MasterServers))
	}
	if len(got.VolumeServers) != 0 {
		t.Errorf("volume_servers: got %d, want 0 (no free disks in harness containers)", len(got.VolumeServers))
	}
	if len(got.FilerServers) != 1 {
		t.Fatalf("filer_servers: got %d, want 1", len(got.FilerServers))
	}
	if got.MasterServers[0].Ip != hosts[0].IP {
		t.Errorf("master[0].ip: got %q, want %q", got.MasterServers[0].Ip, hosts[0].IP)
	}
	if got.MasterServers[0].Port != 9333 {
		t.Errorf("master[0].port: got %d, want 9333", got.MasterServers[0].Port)
	}

	// Probe-error sanity: every host should have been reachable; plan
	// would still succeed on unreachable hosts but leave ProbeError in
	// the facts. We can't read facts from -o output, so instead confirm
	// stderr carries no "FAIL:" lines.
	if strings.Contains(out, "FAIL:") {
		t.Errorf("cluster plan reported a FAIL probe; stderr was:\n%s", out)
	}
	// The volume-role drop should be reported on stderr so operators
	// aren't left wondering why volume_servers is empty.
	if !strings.Contains(out, "dropped volume role") {
		t.Errorf("expected stderr to report the volume-role drop; got:\n%s", out)
	}

	// Phase 3: a second run without --overwrite append-merges into the
	// existing file. With the same inventory and facts, the merged
	// cluster.yaml must equal the first run's output byte-for-byte
	// (no-op stability guarantee).
	originalBytes := append([]byte(nil), data...)
	mergeOut, mergeErr := runPlan(h, invPath, outPath)
	if mergeErr != nil {
		t.Fatalf("cluster plan (re-run / append-merge) failed: %v\noutput:\n%s", mergeErr, mergeOut)
	}
	mergedBytes, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read %s after merge: %v", outPath, err)
	}
	if string(mergedBytes) != string(originalBytes) {
		t.Errorf("no-op merge changed cluster.yaml bytes\n--- before ---\n%s\n--- after ---\n%s",
			originalBytes, mergedBytes)
	}

	overwriteOut, overwriteErr := runPlan(h, invPath, outPath, "--overwrite")
	if overwriteErr != nil {
		t.Errorf("cluster plan --overwrite failed: %v\noutput:\n%s", overwriteErr, overwriteOut)
	}
}

// writeInventory renders an inventory.yaml against the harness hosts with
// the first host colocated as master+filer and the remaining hosts as
// additional masters/volumes. Keeps the shape simple — detailed role
// matrices are exercised by the unit tests.
func writeInventory(path string, hosts []Host, keyPath string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "defaults:\n  ssh:\n    user: root\n    port: 22\n    identity: %s\n", keyPath)
	// Exclude every device-path prefix a probe might surface. GitHub
	// Actions runners expose the host's /dev/sda through privileged
	// containers, which is partitioned but can still leak into the
	// eligible-disk list on some runner images. Force a deterministic
	// "no eligible disks" outcome so the test assertions don't depend
	// on the runner's disk layout.
	b.WriteString("  disk:\n    exclude: [\"/dev/sd*\", \"/dev/nvme*\", \"/dev/vd*\", \"/dev/xvd*\"]\n\n")
	b.WriteString("hosts:\n")
	for i, host := range hosts {
		roles := []string{"master", "volume"}
		if i == 0 {
			roles = append(roles, "filer")
		}
		fmt.Fprintf(&b, "  - ip: %s\n    roles: [%s]\n", host.IP, strings.Join(roles, ", "))
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

// runPlan invokes the built seaweed-up binary with `cluster plan` args.
// Returns combined stdout+stderr so test failures surface the progress
// lines the command writes to stderr.
func runPlan(h *Harness, invPath, outPath string, extraArgs ...string) (string, error) {
	args := []string{
		"cluster", "plan",
		"-i", invPath,
		"-o", outPath,
		"--cluster-name", "harness-cluster",
		"--concurrency", "3",
	}
	args = append(args, extraArgs...)
	cmd := exec.Command(h.binaryPath, args...)
	cmd.Dir = h.projectRoot
	out, err := cmd.CombinedOutput()
	return string(out), err
}
