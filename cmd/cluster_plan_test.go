package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/plan"
)

func TestFactsFilePath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"cluster.yaml", "cluster.facts.json"},
		{"cluster.yml", "cluster.facts.json"},
		{"/etc/seaweed-up/prod.yaml", "/etc/seaweed-up/prod.facts.json"},
		{"./out/topo.yml", "./out/topo.facts.json"},
		// No recognized extension — append, don't strip.
		{"cluster", "cluster.facts.json"},
		{"plan.txt", "plan.txt.facts.json"},
	}
	for _, tc := range cases {
		if got := factsFilePath(tc.in); got != tc.want {
			t.Errorf("factsFilePath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDeployDisksFilePath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"cluster.yaml", "cluster.deploy-disks.json"},
		{"cluster.yml", "cluster.deploy-disks.json"},
		{"/etc/seaweed-up/prod.yaml", "/etc/seaweed-up/prod.deploy-disks.json"},
		{"plan", "plan.deploy-disks.json"},
	}
	for _, tc := range cases {
		if got := deployDisksFilePath(tc.in); got != tc.want {
			t.Errorf("deployDisksFilePath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// writeMarkerSpec writes a stub cluster.yaml carrying the
// planGeneratedMarker so isPlanGeneratedSpec recognizes it.
func writeMarkerSpec(t *testing.T, path string) {
	t.Helper()
	body := plan.PlanGeneratedMarker + "\n# stub for tests\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadPlannedDeployDisks_readsSidecar(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "cluster.yaml")
	writeMarkerSpec(t, specPath)
	body := `{
  "10.0.0.21:22":   ["/dev/nvme0n1", "/dev/nvme2n1"],
  "10.0.0.21:2222": ["/dev/sdb"],
  "10.0.0.22:22":   ["/dev/sdc"]
}
`
	if err := os.WriteFile(deployDisksFilePath(specPath), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadPlannedDeployDisks(specPath)
	if err != nil {
		t.Fatalf("loadPlannedDeployDisks: %v", err)
	}
	if got == nil {
		t.Fatal("loadPlannedDeployDisks returned nil with no error")
	}
	if _, ok := got["10.0.0.21:22"]["/dev/nvme0n1"]; !ok {
		t.Errorf("missing /dev/nvme0n1 on 10.0.0.21:22; got %+v", got["10.0.0.21:22"])
	}
	if _, ok := got["10.0.0.21:2222"]["/dev/sdb"]; !ok {
		t.Errorf("missing /dev/sdb on 10.0.0.21:2222 (different SSH port, same IP); got %+v", got["10.0.0.21:2222"])
	}
	if _, ok := got["10.0.0.22:22"]["/dev/sdc"]; !ok {
		t.Errorf("missing /dev/sdc on 10.0.0.22:22; got %+v", got["10.0.0.22:22"])
	}
}

func TestLoadPlannedDeployDisks_handWritten_noMarkerNoSidecar_returnsNilNil(t *testing.T) {
	// Hand-written cluster.yaml: no marker, no sidecar.
	// (nil, nil) so deploy keeps its legacy scan-everything path.
	dir := t.TempDir()
	specPath := filepath.Join(dir, "cluster.yaml")
	if err := os.WriteFile(specPath, []byte("# hand-written spec\nmaster_servers: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadPlannedDeployDisks(specPath)
	if err != nil {
		t.Fatalf("expected no error for hand-written spec, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil allowlist for hand-written spec, got %+v", got)
	}
}

// TestRunClusterPlan_dryRunRequiresOutput pins down the early-exit
// validation: --dry-run renders a diff against -o, so without -o
// there's no diff target to render. The check fires before any SSH
// probe so the test doesn't need network or fake hosts.
func TestRunClusterPlan_dryRunRequiresOutput(t *testing.T) {
	dir := t.TempDir()
	invPath := filepath.Join(dir, "inv.yaml")
	if err := os.WriteFile(invPath, []byte("hosts:\n  - ip: 10.0.0.1\n    roles: [master]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := &ClusterPlanOptions{
		InventoryFile: invPath,
		DryRun:        true, // OutputFile left empty
	}
	err := runClusterPlan(nil, opts)
	if err == nil {
		t.Fatal("expected refusal when --dry-run is set without -o")
	}
	if !strings.Contains(err.Error(), "--dry-run requires -o") {
		t.Errorf("error should explain the missing -o, got: %v", err)
	}
}

// TestIsPlanGeneratedSpec_detectsMarker is the wiring test for the
// PlanGenerated flag the cmd layer pushes into the Manager. The flag
// drives the runtime mountpoint check independently of the sidecar,
// so a plan-generated cluster.yaml deployed with --mount-disks=false
// still gets fail-closed treatment even when the sidecar is absent.
func TestIsPlanGeneratedSpec_detectsMarker(t *testing.T) {
	dir := t.TempDir()

	marked := filepath.Join(dir, "marked.yaml")
	writeMarkerSpec(t, marked)
	if got, err := isPlanGeneratedSpec(marked); err != nil || !got {
		t.Errorf("isPlanGeneratedSpec(marked) = (%v, %v), want (true, nil)", got, err)
	}

	plain := filepath.Join(dir, "plain.yaml")
	if err := os.WriteFile(plain, []byte("# hand written\nmaster_servers: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := isPlanGeneratedSpec(plain); err != nil || got {
		t.Errorf("isPlanGeneratedSpec(plain) = (%v, %v), want (false, nil)", got, err)
	}
}

func TestLoadPlannedDeployDisks_planMarker_missingSidecar_failsClosed(t *testing.T) {
	// Spec carries the plan marker (it's plan-generated) but the
	// sidecar is missing — operator scp'd just the YAML, or the
	// sidecar was lost in transit. MUST fail closed: silent fallback
	// to scan-everything would format disks the planner deliberately
	// excluded (excludes, ephemeral, foreign mounts).
	dir := t.TempDir()
	specPath := filepath.Join(dir, "cluster.yaml")
	writeMarkerSpec(t, specPath)
	got, err := loadPlannedDeployDisks(specPath)
	if err == nil {
		t.Fatalf("expected fail-closed error, got allowlist %+v", got)
	}
	if !strings.Contains(err.Error(), "deploy-disks") {
		t.Errorf("error should reference the missing sidecar, got %q", err.Error())
	}
}

func TestLoadPlannedDeployDisks_planMarker_corruptSidecar_failsClosed(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "cluster.yaml")
	writeMarkerSpec(t, specPath)
	if err := os.WriteFile(deployDisksFilePath(specPath), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPlannedDeployDisks(specPath); err == nil {
		t.Fatal("expected parse error from malformed deploy-disks.json")
	}
}

func TestLoadPlannedDeployDisks_emptyMap_isAuthoritative(t *testing.T) {
	// Empty allowlist `{}` is legitimate for a cluster with no volume
	// hosts. Deploy must apply the (empty) filter rather than fall
	// back, so a trailing volume_server entry that wasn't classified
	// by plan still gets refused at deploy.
	dir := t.TempDir()
	specPath := filepath.Join(dir, "cluster.yaml")
	writeMarkerSpec(t, specPath)
	if err := os.WriteFile(deployDisksFilePath(specPath), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadPlannedDeployDisks(specPath)
	if err != nil {
		t.Fatalf("loadPlannedDeployDisks: %v", err)
	}
	if got == nil {
		t.Fatal("empty {} should return non-nil empty map (authoritative), got nil")
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %+v", got)
	}
}
