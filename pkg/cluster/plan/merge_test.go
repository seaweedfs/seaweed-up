package plan

import (
	"strings"
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/inventory"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/probe"
)

// buildBaseSpec generates a small but realistic cluster spec we can
// reuse across merge tests. Three masters + one volume host with three
// disks. Stable enough that golden-style byte comparisons hold.
func buildBaseSpec(t *testing.T) ([]byte, *baseState) {
	t.Helper()
	inv := &inventory.Inventory{
		Hosts: []inventory.Host{
			{IP: "10.0.0.11", Roles: []string{"master"}},
			{IP: "10.0.0.12", Roles: []string{"master"}},
			{IP: "10.0.0.13", Roles: []string{"master"}},
			{IP: "10.0.0.21", Roles: []string{"volume"}},
		},
	}
	if err := inv.Validate(); err != nil {
		t.Fatalf("inventory.Validate: %v", err)
	}
	facts := map[string]probe.HostFacts{
		"10.0.0.21:22": {IP: "10.0.0.21", SSHPort: 22, Disks: synthesizeDisks(3, 100)},
	}
	spec, _, err := Generate(inv, facts, Options{ClusterName: "merge-test"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	body, err := Marshal(spec, MarshalOptions{
		InventoryPath: "inventory.yaml",
		Now:           goldenStamp,
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return body, &baseState{inv: inv, facts: facts}
}

// baseState wraps an inventory + facts pair so tests can mutate either
// side and re-Generate without rebuilding the whole closure.
type baseState struct {
	inv   *inventory.Inventory
	facts map[string]probe.HostFacts
}

// TestMerge_noOpRun_byteIdentical is the core contract: a re-run with
// the same inventory+facts must produce a byte-for-byte identical
// cluster.yaml. yaml.Node's preservation guarantees + our "never touch
// existing entries" rule are what get us there.
func TestMerge_noOpRun_byteIdentical(t *testing.T) {
	base, st := buildBaseSpec(t)
	spec, _, err := Generate(st.inv, st.facts, Options{ClusterName: "merge-test"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	merged, report, err := Merge(base, spec, MergeOptions{
		Marshal: MarshalOptions{InventoryPath: "inventory.yaml", Now: goldenStamp},
	})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if string(merged) != string(base) {
		t.Errorf("no-op merge changed bytes\n--- want ---\n%s\n--- got ---\n%s", base, merged)
	}
	if len(report.Appended) != 0 {
		t.Errorf("no-op merge appended entries: %+v", report.Appended)
	}
	if len(report.Orphaned) != 0 {
		t.Errorf("no-op merge reported orphans: %+v", report.Orphaned)
	}
}

// TestMerge_appendOneVolumeHost is the grow-the-cluster contract: add
// a host to inventory, merge, the new entry shows up at the right
// section's tail. Existing bytes (above the tail) are unchanged.
func TestMerge_appendOneVolumeHost(t *testing.T) {
	base, st := buildBaseSpec(t)

	// Add a second volume host.
	st.inv.Hosts = append(st.inv.Hosts, inventory.Host{IP: "10.0.0.22", Roles: []string{"volume"}})
	st.facts["10.0.0.22:22"] = probe.HostFacts{IP: "10.0.0.22", SSHPort: 22, Disks: synthesizeDisks(2, 100)}

	spec, _, err := Generate(st.inv, st.facts, Options{ClusterName: "merge-test"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	merged, report, err := Merge(base, spec, MergeOptions{
		Marshal: MarshalOptions{InventoryPath: "inventory.yaml", Now: goldenStamp},
	})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	got := string(merged)
	want := string(base)

	// The new entry's IP must appear in the merged output and NOT in
	// the base — proves it was actually appended.
	if !strings.Contains(got, "10.0.0.22") {
		t.Errorf("appended host 10.0.0.22 not found in merged output:\n%s", got)
	}
	if strings.Contains(want, "10.0.0.22") {
		t.Fatalf("test setup error: base already contains 10.0.0.22")
	}

	// All bytes of the base (header, comments, existing entries) must
	// still be present in the merged output as a contiguous prefix or
	// substring. Cheap structural check: every line of base appears in
	// merged in the same relative order.
	prev := -1
	for _, line := range strings.Split(want, "\n") {
		if line == "" {
			continue
		}
		idx := strings.Index(got[max(prev, 0):], line)
		if idx < 0 {
			t.Errorf("base line %q lost in merged output", line)
			continue
		}
		prev += idx + len(line)
	}

	// Append went into the right section.
	if appended := report.Appended["volume_servers"]; len(appended) != 1 || appended[0] != "10.0.0.22:8080" {
		t.Errorf("Report.Appended[volume_servers] = %+v, want [10.0.0.22:8080]", appended)
	}
	if len(report.Orphaned) != 0 {
		t.Errorf("unexpected orphans on append: %+v", report.Orphaned)
	}
}

// TestMerge_userEditPreserved: an operator hand-edits a folder's
// `max:` value. Re-running plan must leave that edit alone — we never
// re-emit existing entries. This is the "user-edit survival" guarantee.
func TestMerge_userEditPreserved(t *testing.T) {
	base, st := buildBaseSpec(t)

	// The default volumeSizeLimitMB (5000) gives a 100 GiB SSD a max
	// of 19. Find the first occurrence under /data1 and bump it to a
	// distinctive value the operator would never get from a fresh run.
	const userMax = "max: 9999"
	lines := strings.Split(string(base), "\n")
	editedAtLine := -1
	for i, line := range lines {
		if strings.Contains(line, "folder: /data1") {
			// the next two lines are `disk: <type>` and `max: <n>`.
			for j := i + 1; j < len(lines) && j <= i+4; j++ {
				if strings.Contains(lines[j], "max:") {
					lines[j] = strings.Replace(lines[j], strings.TrimSpace(lines[j]), userMax, 1)
					editedAtLine = j
					break
				}
			}
			break
		}
	}
	if editedAtLine < 0 {
		t.Fatalf("test setup: couldn't find a max: line under /data1\nbase=%s", base)
	}
	edited := strings.Join(lines, "\n")

	spec, _, err := Generate(st.inv, st.facts, Options{ClusterName: "merge-test"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	merged, _, err := Merge([]byte(edited), spec, MergeOptions{
		Marshal: MarshalOptions{InventoryPath: "inventory.yaml", Now: goldenStamp},
	})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !strings.Contains(string(merged), userMax) {
		t.Errorf("hand edit %q lost on merge\n--- got ---\n%s", userMax, merged)
	}
	if got := strings.Count(string(merged), userMax); got != 1 {
		t.Errorf("hand edit appeared %d times in merged output, want 1\n%s", got, merged)
	}
}

// TestMerge_orphanedHostWarned: a host removed from inventory shows up
// in MergeReport.Orphaned but the existing YAML entry stays untouched.
// Append-merge never deletes — the operator decides.
func TestMerge_orphanedHostWarned(t *testing.T) {
	base, st := buildBaseSpec(t)

	// Drop the volume host from inventory; masters stay.
	var kept []inventory.Host
	for _, h := range st.inv.Hosts {
		if h.IP != "10.0.0.21" {
			kept = append(kept, h)
		}
	}
	st.inv.Hosts = kept
	delete(st.facts, "10.0.0.21:22")

	spec, _, err := Generate(st.inv, st.facts, Options{ClusterName: "merge-test"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	merged, report, err := Merge(base, spec, MergeOptions{
		Marshal: MarshalOptions{InventoryPath: "inventory.yaml", Now: goldenStamp},
	})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	if !strings.Contains(string(merged), "10.0.0.21") {
		t.Errorf("orphan entry 10.0.0.21 was deleted on merge:\n%s", merged)
	}
	found := false
	for _, o := range report.Orphaned {
		if strings.Contains(o, "volume_servers") && strings.Contains(o, "10.0.0.21:8080") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Report.Orphaned should list 10.0.0.21:8080 under volume_servers, got %+v", report.Orphaned)
	}
}

// TestMerge_emptyExistingFallsBackToGreenfield: a plan run against a
// brand-new (empty / non-existent) -o file should be indistinguishable
// from a fresh Marshal. Lets the cmd layer call Merge unconditionally
// without first checking whether the file already has content.
func TestMerge_emptyExistingFallsBackToGreenfield(t *testing.T) {
	_, st := buildBaseSpec(t)
	spec, _, err := Generate(st.inv, st.facts, Options{ClusterName: "merge-test"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	greenfield, err := Marshal(spec, MarshalOptions{InventoryPath: "inventory.yaml", Now: goldenStamp})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, in := range [][]byte{nil, {}, []byte("   \n  \n")} {
		merged, _, err := Merge(in, spec, MergeOptions{
			Marshal: MarshalOptions{InventoryPath: "inventory.yaml", Now: goldenStamp},
		})
		if err != nil {
			t.Fatalf("Merge(%q): %v", in, err)
		}
		if string(merged) != string(greenfield) {
			t.Errorf("Merge(empty=%q) diverged from greenfield Marshal", in)
		}
	}
}

// TestMerge_inlineCommentSurvives: a hand-written cluster.yaml carries
// an inline comment on a folder entry. Merge must not drop or move
// that comment. Exercises yaml.v3's head/line/foot comment preservation
// through the parse → mutate → encode round-trip.
func TestMerge_inlineCommentSurvives(t *testing.T) {
	existing := `cluster_name: hand
master_servers:
    - ip: 10.0.0.11   # primary master, do not move
      port.ssh: 22
      port: 9333
      port.grpc: 19333
`
	inv := &inventory.Inventory{
		Hosts: []inventory.Host{
			{IP: "10.0.0.11", Roles: []string{"master"}},
			{IP: "10.0.0.12", Roles: []string{"master"}},
		},
	}
	if err := inv.Validate(); err != nil {
		t.Fatalf("inventory.Validate: %v", err)
	}
	spec, _, err := Generate(inv, map[string]probe.HostFacts{}, Options{ClusterName: "hand"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	merged, _, err := Merge([]byte(existing), spec, MergeOptions{
		Marshal: MarshalOptions{InventoryPath: "inventory.yaml", Now: goldenStamp},
	})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !strings.Contains(string(merged), "primary master, do not move") {
		t.Errorf("inline comment lost on merge:\n%s", merged)
	}
	if !strings.Contains(string(merged), "10.0.0.12") {
		t.Errorf("new master not appended:\n%s", merged)
	}
}

// TestMerge_preservesTwoSpaceIndent confirms that hand-written
// cluster.yaml files using a non-default 2-space indent don't get
// re-flowed to 4-space on merge — yaml.v3's encoder applies one
// global indent setting, so picking the wrong one would touch every
// existing node.
func TestMerge_preservesTwoSpaceIndent(t *testing.T) {
	existing := `cluster_name: hand
master_servers:
  - ip: 10.0.0.11
    port.ssh: 22
    port: 9333
    port.grpc: 19333
`
	inv := &inventory.Inventory{
		Hosts: []inventory.Host{
			{IP: "10.0.0.11", Roles: []string{"master"}},
			{IP: "10.0.0.12", Roles: []string{"master"}},
		},
	}
	if err := inv.Validate(); err != nil {
		t.Fatalf("inventory.Validate: %v", err)
	}
	spec, _, err := Generate(inv, map[string]probe.HostFacts{}, Options{ClusterName: "hand"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	merged, _, err := Merge([]byte(existing), spec, MergeOptions{
		Marshal: MarshalOptions{InventoryPath: "inventory.yaml", Now: goldenStamp},
	})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	// Every existing master entry's `port.ssh` line should still sit at
	// 4-column indent (2 for the dash + 2 for the key under it). If the
	// encoder re-flowed to 4-space, those lines would be at column 6 or 8.
	if !strings.Contains(string(merged), "\n    port.ssh: 22") {
		t.Errorf("2-space indent reflowed to 4-space:\n%s", merged)
	}
	if !strings.Contains(string(merged), "10.0.0.12") {
		t.Errorf("new master not appended:\n%s", merged)
	}
}

// TestMerge_nullSectionStaysNull is the regression test for the
// no-op byte-stability hole: a hand-written `master_servers:` (null
// scalar value, no list) used to be coerced to an empty sequence
// even when the inventory carried no fresh entries for the section.
// yaml.v3 then re-encoded that as `[]`, changing bytes and breaking
// the no-op contract. With the no-fresh-entries fast path, the null
// value should now survive untouched.
func TestMerge_nullSectionStaysNull(t *testing.T) {
	existing := `cluster_name: hand
master_servers:
volume_servers:
    - ip: 10.0.0.21
      port.ssh: 22
      port: 8080
      port.grpc: 18080
      folders:
        - folder: /data1
          disk: hdd
          max: 19
`
	// Inventory has only the existing volume host; no masters at all.
	inv := &inventory.Inventory{
		Hosts: []inventory.Host{{IP: "10.0.0.21", Roles: []string{"volume"}}},
	}
	if err := inv.Validate(); err != nil {
		t.Fatalf("inventory.Validate: %v", err)
	}
	facts := map[string]probe.HostFacts{
		"10.0.0.21:22": {IP: "10.0.0.21", SSHPort: 22, Disks: synthesizeDisks(1, 100)},
	}
	spec, _, err := Generate(inv, facts, Options{ClusterName: "hand"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	merged, _, err := Merge([]byte(existing), spec, MergeOptions{
		Marshal: MarshalOptions{InventoryPath: "inventory.yaml", Now: goldenStamp},
	})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if string(merged) != existing {
		t.Errorf("null master_servers section mutated by no-op merge\n--- want ---\n%s\n--- got ---\n%s",
			existing, merged)
	}
}

// TestMerge_recordsUnparseableExistingEntry: an existing entry that
// keyOfNode can't extract a key from (here: `master_servers` row
// missing `port:`) is kept verbatim but recorded in
// MergeReport.Unparseable so the operator sees that fresh inventory
// entries won't dedupe against it. Without this signal, an inventory
// host with the same IP would silently produce two YAML rows for the
// same host on the next merge.
func TestMerge_recordsUnparseableExistingEntry(t *testing.T) {
	existing := `cluster_name: hand
master_servers:
    - ip: 10.0.0.11
      port.ssh: 22
      port.grpc: 19333
`
	inv := &inventory.Inventory{
		Hosts: []inventory.Host{{IP: "10.0.0.11", Roles: []string{"master"}}},
	}
	if err := inv.Validate(); err != nil {
		t.Fatalf("inventory.Validate: %v", err)
	}
	spec, _, err := Generate(inv, map[string]probe.HostFacts{}, Options{ClusterName: "hand"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	merged, report, err := Merge([]byte(existing), spec, MergeOptions{
		Marshal: MarshalOptions{InventoryPath: "inventory.yaml", Now: goldenStamp},
	})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(report.Unparseable) != 1 {
		t.Fatalf("Report.Unparseable: got %+v, want one entry", report.Unparseable)
	}
	if !strings.Contains(report.Unparseable[0], "master_servers") {
		t.Errorf("Unparseable entry should name the section, got %q", report.Unparseable[0])
	}
	// The fresh master entry was appended (since the existing one is
	// not in the dedup index), so the merged file now has two rows
	// for 10.0.0.11. That's the documented hazard the warning is for.
	if got := strings.Count(string(merged), "10.0.0.11"); got != 2 {
		t.Errorf("expected two 10.0.0.11 rows after merge (the hazard the warning describes), got %d:\n%s", got, merged)
	}
}

// TestMerge_rejectsNonSequenceSection: an existing `master_servers:`
// hand-edited to a scalar or mapping must NOT be silently overwritten.
// A loud error pushes the operator to clean it up before merge instead
// of losing data.
func TestMerge_rejectsNonSequenceSection(t *testing.T) {
	existing := `cluster_name: broken
master_servers: oops_a_string_not_a_list
`
	inv := &inventory.Inventory{
		Hosts: []inventory.Host{{IP: "10.0.0.11", Roles: []string{"master"}}},
	}
	if err := inv.Validate(); err != nil {
		t.Fatalf("inventory.Validate: %v", err)
	}
	spec, _, err := Generate(inv, map[string]probe.HostFacts{}, Options{ClusterName: "broken"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	_, _, err = Merge([]byte(existing), spec, MergeOptions{
		Marshal: MarshalOptions{InventoryPath: "inventory.yaml", Now: goldenStamp},
	})
	if err == nil {
		t.Fatal("expected error on non-sequence section, got nil")
	}
	if !strings.Contains(err.Error(), "master_servers") {
		t.Errorf("error should name the offending section, got: %v", err)
	}
	if !strings.Contains(err.Error(), "--overwrite") {
		t.Errorf("error should hint at --overwrite escape hatch, got: %v", err)
	}
}

// TestDetectIndent covers the small heuristic that picks the
// re-encode indent from the existing input. Direct unit test because
// the detector also runs on edge cases (empty file, comment-only,
// pathological inputs) the higher-level merge tests don't reach.
func TestDetectIndent(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 4},
		{"comments only", "# only comments\n# more\n", 4},
		{"two-space mapping", "root:\n  child: 1\n", 2},
		{"four-space mapping", "root:\n    child: 1\n", 4},
		{"two-space sequence", "items:\n  - a\n  - b\n", 2},
		{"four-space sequence", "items:\n    - a\n    - b\n", 4},
		{"clamp big", "root:\n             child: 1\n", 8},
		{"flat only", "root: 1\nother: 2\n", 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectIndent([]byte(tc.in), 4); got != tc.want {
				t.Errorf("detectIndent(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestMerge_handWrittenSpec_noMarkerSurvives: merging into a hand-
// written cluster.yaml (no plan marker) doesn't sneak the marker in.
// Whether the file gets the marker is the writer's call, not Merge's;
// preserving "this is a hand-written file" is part of byte stability.
func TestMerge_handWrittenSpec_noMarkerSurvives(t *testing.T) {
	existing := `# my hand-rolled cluster
cluster_name: hand
master_servers:
    - ip: 10.0.0.11
      port.ssh: 22
      port: 9333
      port.grpc: 19333
`
	inv := &inventory.Inventory{Hosts: []inventory.Host{{IP: "10.0.0.11", Roles: []string{"master"}}}}
	if err := inv.Validate(); err != nil {
		t.Fatalf("inventory.Validate: %v", err)
	}
	spec, _, err := Generate(inv, map[string]probe.HostFacts{}, Options{ClusterName: "hand"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	merged, _, err := Merge([]byte(existing), spec, MergeOptions{
		Marshal: MarshalOptions{InventoryPath: "inventory.yaml", Now: goldenStamp},
	})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if strings.Contains(string(merged), PlanGeneratedMarker) {
		t.Errorf("Merge stamped the plan marker onto a hand-written file:\n%s", merged)
	}
	if !strings.Contains(string(merged), "my hand-rolled cluster") {
		t.Errorf("hand-written header comment lost on merge:\n%s", merged)
	}
}

