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

// TestLoadPreviousFacts covers the soft-fail loader: a missing,
// unreadable, or malformed cluster.facts.json must return nil rather
// than failing the plan run. Drift detection is a soft advisory
// signal; a corrupt sidecar shouldn't block legitimate work.
func TestLoadPreviousFacts(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "cluster.yaml")
	factsPath := factsFilePath(specPath)

	// 1. Missing sidecar → nil.
	if got := loadPreviousFacts(specPath); got != nil {
		t.Errorf("missing sidecar should return nil, got %+v", got)
	}

	// 2. Malformed JSON → nil (soft fail, no error surface).
	if err := os.WriteFile(factsPath, []byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadPreviousFacts(specPath); got != nil {
		t.Errorf("malformed sidecar should return nil, got %+v", got)
	}

	// 3. Valid JSON → decoded into HostFacts.
	body := `[{"ip":"10.0.0.21","ssh_port":22,"disks":[{"path":"/dev/sdb","size_bytes":0}]}]`
	if err := os.WriteFile(factsPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := loadPreviousFacts(specPath)
	if len(got) != 1 {
		t.Fatalf("valid sidecar: got %d facts, want 1", len(got))
	}
	if got[0].IP != "10.0.0.21" || len(got[0].Disks) != 1 || got[0].Disks[0].Path != "/dev/sdb" {
		t.Errorf("decoded facts mismatch: %+v", got[0])
	}

	// 4. Empty outputFile → nil (defensive; the --json branch never
	// reaches this helper, but the contract is "nil when nothing to load").
	if got := loadPreviousFacts(""); got != nil {
		t.Errorf("empty path should return nil, got %+v", got)
	}
}

// TestRunClusterPlan_refreshHostRequiresOutput mirrors the dry-run
// validator: --refresh-host targets entries in -o, so without -o
// the flag is meaningless. The check fires before any SSH probe so
// the test doesn't need network or fake hosts.
func TestRunClusterPlan_refreshHostRequiresOutput(t *testing.T) {
	dir := t.TempDir()
	invPath := filepath.Join(dir, "inv.yaml")
	if err := os.WriteFile(invPath, []byte("hosts:\n  - ip: 10.0.0.1\n    roles: [master]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := &ClusterPlanOptions{
		InventoryFile: invPath,
		RefreshHosts:  []string{"10.0.0.1"},
	}
	err := runClusterPlan(nil, opts)
	if err == nil {
		t.Fatal("expected refusal when --refresh-host is set without -o")
	}
	if !strings.Contains(err.Error(), "--refresh-host requires -o") {
		t.Errorf("error should explain the missing -o, got: %v", err)
	}
}

// TestRunClusterPlan_refreshHostBlankOnlyTreatedAsEmpty pins the
// trim-before-validate fix: passing only whitespace values to
// --refresh-host used to slip past the require-`-o` check (the
// validator counted raw flag values, refreshHostSet then trimmed
// them all to nil, and plan.Merge silently no-op'd). The trimmed
// set should now share the empty-input semantics — no
// `--refresh-host requires -o` error fires because there's nothing
// effectively requested.
//
// Routed through a malformed inventory path so the call exits
// early without reaching the SSH probe (which would crash on a
// nil cobra cmd in this unit-test scaffolding).
func TestRunClusterPlan_refreshHostBlankOnlyTreatedAsEmpty(t *testing.T) {
	opts := &ClusterPlanOptions{
		InventoryFile: filepath.Join(t.TempDir(), "does-not-exist.yaml"),
		RefreshHosts:  []string{"  ", ""},
		// OutputFile left empty — without trimming, the validator
		// would fire a `--refresh-host requires -o` error here.
	}
	err := runClusterPlan(nil, opts)
	if err == nil {
		t.Fatal("expected inventory-load error, got nil")
	}
	if strings.Contains(err.Error(), "--refresh-host requires -o") {
		t.Errorf("blank-only --refresh-host should not trigger the require-o error, got: %v", err)
	}
}

// TestRefreshHostSet covers the small slice→set converter: blank
// entries get dropped, an empty input returns nil so plan.Merge
// takes the no-refresh fast path.
func TestRefreshHostSet(t *testing.T) {
	if refreshHostSet(nil) != nil {
		t.Errorf("nil input should return nil")
	}
	if refreshHostSet([]string{}) != nil {
		t.Errorf("empty input should return nil")
	}
	if refreshHostSet([]string{"  ", ""}) != nil {
		t.Errorf("all-blank input should return nil after trimming")
	}
	got := refreshHostSet([]string{"10.0.0.21", "  10.0.0.22  ", ""})
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(got), got)
	}
	if _, ok := got["10.0.0.21"]; !ok {
		t.Errorf("10.0.0.21 missing from set: %+v", got)
	}
	if _, ok := got["10.0.0.22"]; !ok {
		t.Errorf("trimmed 10.0.0.22 missing from set: %+v", got)
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
