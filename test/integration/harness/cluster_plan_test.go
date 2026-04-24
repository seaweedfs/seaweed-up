//go:build integration

package harness

import (
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

	// Shape assertions: 3 masters + 3 volumes + 1 filer matching the
	// inventory's role assignments. IPs must round-trip.
	if got.Name != "harness-cluster" {
		t.Errorf("cluster_name: got %q, want harness-cluster", got.Name)
	}
	if len(got.MasterServers) != 3 {
		t.Fatalf("master_servers: got %d, want 3", len(got.MasterServers))
	}
	if len(got.VolumeServers) != 3 {
		t.Fatalf("volume_servers: got %d, want 3", len(got.VolumeServers))
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
	if got.VolumeServers[0].Port != 8080 {
		t.Errorf("volume[0].port: got %d, want 8080", got.VolumeServers[0].Port)
	}

	// Probe-error sanity: every host should have been reachable; plan
	// would still succeed on unreachable hosts but leave ProbeError in
	// the facts. We can't read facts from -o output, so instead confirm
	// stderr carries "ok" lines for every host, not "FAIL".
	if strings.Contains(out, "FAIL:") {
		t.Errorf("cluster plan reported a FAIL probe; stderr was:\n%s", out)
	}

	// --force must overwrite an existing file. Without --force, the
	// second run should refuse.
	noForceOut, noForceErr := runPlan(h, invPath, outPath)
	if noForceErr == nil {
		t.Errorf("expected refusal on existing -o without --force; output:\n%s", noForceOut)
	}

	forceOut, forceErr := runPlan(h, invPath, outPath, "--force")
	if forceErr != nil {
		t.Errorf("cluster plan --force failed: %v\noutput:\n%s", forceErr, forceOut)
	}
}

// writeInventory renders an inventory.yaml against the harness hosts with
// the first host colocated as master+filer and the remaining hosts as
// additional masters/volumes. Keeps the shape simple — detailed role
// matrices are exercised by the unit tests.
func writeInventory(path string, hosts []Host, keyPath string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "defaults:\n  ssh:\n    user: root\n    port: 22\n    identity: %s\n\n", keyPath)
	b.WriteString("hosts:\n")
	for i, host := range hosts {
		roles := []string{"master", "volume"}
		if i == 0 {
			roles = append(roles, "filer")
		}
		fmt.Fprintf(&b, "  - ip: %s\n    roles: [%s]\n", host.IP, strings.Join(roles, ", "))
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
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
