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
	// merged in the same relative order. The search window starts at
	// `prev` (initially 0, then advanced past each match) so two
	// adjacent lines can't accidentally overlap into a false positive
	// match against the previous line's tail byte.
	prev := 0
	for _, line := range strings.Split(want, "\n") {
		if line == "" {
			continue
		}
		idx := strings.Index(got[prev:], line)
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

// TestMerge_createsAbsentSection covers the "section absent →
// newly created" branch in mergeSection. Existing cluster.yaml has
// only `cluster_name` + `master_servers`; inventory adds a volume
// host. Merge must (1) introduce the new `volume_servers:` key and
// its sequence, (2) put the new entry inside, (3) record the append
// in MergeReport, and (4) preserve every existing line.
func TestMerge_createsAbsentSection(t *testing.T) {
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
			{IP: "10.0.0.22", Roles: []string{"volume"}},
		},
	}
	if err := inv.Validate(); err != nil {
		t.Fatalf("inventory.Validate: %v", err)
	}
	facts := map[string]probe.HostFacts{
		"10.0.0.22:22": {IP: "10.0.0.22", SSHPort: 22, Disks: synthesizeDisks(1, 100)},
	}
	spec, _, err := Generate(inv, facts, Options{ClusterName: "hand"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	merged, report, err := Merge([]byte(existing), spec, MergeOptions{
		Marshal: MarshalOptions{InventoryPath: "inventory.yaml", Now: goldenStamp},
	})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	got := string(merged)

	if !strings.Contains(got, "\nvolume_servers:") {
		t.Errorf("new volume_servers: key not found in merged output:\n%s", got)
	}
	if !strings.Contains(got, "10.0.0.22") {
		t.Errorf("appended volume host 10.0.0.22 not found in merged output:\n%s", got)
	}
	if appended := report.Appended["volume_servers"]; len(appended) != 1 || appended[0] != "10.0.0.22:8080" {
		t.Errorf("Report.Appended[volume_servers] = %+v, want [10.0.0.22:8080]", appended)
	}
	if len(report.Orphaned) != 0 {
		t.Errorf("unexpected orphans on absent-section append: %+v", report.Orphaned)
	}
	// Every original line must still appear in order.
	prev := 0
	for _, line := range strings.Split(existing, "\n") {
		if line == "" {
			continue
		}
		idx := strings.Index(got[prev:], line)
		if idx < 0 {
			t.Errorf("base line %q lost in merged output", line)
			continue
		}
		prev += idx + len(line)
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

// TestMerge_refreshHost_replacesSingleEntryOnly: --refresh-host=<ip>
// re-emits one host's entry from the fresh spec while leaving every
// other entry's bytes intact. Operator workflow: drift detection
// flagged 10.0.0.21, operator runs `plan --refresh-host=10.0.0.21`
// to fix that one entry without --overwrite (which would discard
// every hand edit).
func TestMerge_refreshHost_replacesSingleEntryOnly(t *testing.T) {
	base, st := buildBaseSpec(t)

	// Operator hand-tightened max: 9999 on /data1 of 10.0.0.21. After
	// refresh we expect THAT edit to be clobbered (refresh re-emits
	// the entry from fresh facts), but sibling entries (the masters)
	// should stay byte-identical.
	const userMax = "max: 9999"
	edited := strings.Replace(string(base), "max: 19", userMax, 1)
	if edited == string(base) {
		t.Fatal("test setup: max: 19 not present in baseline")
	}

	spec, _, err := Generate(st.inv, st.facts, Options{ClusterName: "merge-test"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	merged, report, err := Merge([]byte(edited), spec, MergeOptions{
		Marshal:      MarshalOptions{InventoryPath: "inventory.yaml", Now: goldenStamp},
		RefreshHosts: map[string]struct{}{"10.0.0.21": {}},
	})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	got := string(merged)

	// 1. Hand edit on the refreshed entry is gone (refresh re-emits).
	if strings.Contains(got, userMax) {
		t.Errorf("--refresh-host should clobber hand edits on the refreshed entry, but %q survived:\n%s", userMax, got)
	}
	// 2. Refresh report names the section + key.
	if len(report.Refreshed) != 1 || report.Refreshed[0] != "volume_servers: 10.0.0.21:8080" {
		t.Errorf("Report.Refreshed = %+v, want [volume_servers: 10.0.0.21:8080]", report.Refreshed)
	}
	// 3. Sibling entries (masters) stay byte-identical. Cheap check:
	// every master_servers line from the baseline must appear in the
	// merged output.
	masters := []string{
		"    - ip: 10.0.0.11",
		"    - ip: 10.0.0.12",
		"    - ip: 10.0.0.13",
	}
	for _, line := range masters {
		if !strings.Contains(got, line) {
			t.Errorf("sibling entry line %q lost during refresh:\n%s", line, got)
		}
	}
	// 4. No spurious appends or orphans.
	if total := len(report.Appended); total != 0 {
		t.Errorf("Report.Appended should be empty on a no-change refresh, got %+v", report.Appended)
	}
	if len(report.RefreshNotFound) != 0 {
		t.Errorf("Report.RefreshNotFound should be empty for a matched IP, got %+v", report.RefreshNotFound)
	}
}

// TestMerge_refreshHost_unmatchedSurfaces: an IP passed to
// --refresh-host that doesn't match any existing entry shows up in
// MergeReport.RefreshNotFound. Typical cause: typo, or the host
// hasn't been deployed yet.
func TestMerge_refreshHost_unmatchedSurfaces(t *testing.T) {
	base, st := buildBaseSpec(t)
	spec, _, err := Generate(st.inv, st.facts, Options{ClusterName: "merge-test"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	merged, report, err := Merge(base, spec, MergeOptions{
		Marshal:      MarshalOptions{InventoryPath: "inventory.yaml", Now: goldenStamp},
		RefreshHosts: map[string]struct{}{"10.0.0.99": {}, "10.0.0.21": {}},
	})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	// 10.0.0.21 matched the volume host; 10.0.0.99 didn't match anything.
	if got, want := report.RefreshNotFound, []string{"10.0.0.99"}; !equalStrings(got, want) {
		t.Errorf("RefreshNotFound = %+v, want %+v", got, want)
	}
	if len(report.Refreshed) != 1 {
		t.Errorf("Refreshed should still capture the matched IP, got %+v", report.Refreshed)
	}
	// Other hosts' bytes still survive.
	for _, ip := range []string{"10.0.0.11", "10.0.0.12", "10.0.0.13"} {
		if !strings.Contains(string(merged), ip) {
			t.Errorf("non-refreshed host %s lost from merged output", ip)
		}
	}
}

// TestMerge_refreshHost_preservesEntryComment: head/line/foot
// comments attached to the SEQUENCE-ITEM node survive a refresh.
// Operators frequently annotate entries with a leading comment
// (`# primary HDD bank`); losing those on a refresh would surprise
// them. Field-level inline comments on individual key/value pairs
// inside the mapping (e.g. `ip: 10.0.0.11   # primary`) are NOT
// preserved by Phase 4 — see TestMerge_refreshHost_dropsFieldLevelComment.
func TestMerge_refreshHost_preservesEntryComment(t *testing.T) {
	existing := `cluster_name: merge-test
master_servers:
    # primary master, do not move
    - ip: 10.0.0.11
      port.ssh: 22
      port: 9333
      port.grpc: 19333
`
	inv := &inventory.Inventory{
		Hosts: []inventory.Host{{IP: "10.0.0.11", Roles: []string{"master"}}},
	}
	if err := inv.Validate(); err != nil {
		t.Fatalf("inventory.Validate: %v", err)
	}
	spec, _, err := Generate(inv, map[string]probe.HostFacts{}, Options{ClusterName: "merge-test"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	merged, _, err := Merge([]byte(existing), spec, MergeOptions{
		Marshal:      MarshalOptions{InventoryPath: "inventory.yaml", Now: goldenStamp},
		RefreshHosts: map[string]struct{}{"10.0.0.11": {}},
	})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !strings.Contains(string(merged), "primary master, do not move") {
		t.Errorf("entry head comment lost on refresh:\n%s", merged)
	}
}

// TestMerge_refreshHost_dropsFieldLevelComment pins down the
// documented Phase 4 limitation: comments attached to individual
// key/value pairs INSIDE the mapping (yaml.v3 stores these as
// LineComment on the value scalar) do not survive a refresh.
// Operators who care can re-add the comment after refresh, or
// promote it to an entry head comment (which IS preserved).
//
// Implementing field-level comment carry would require pairing each
// fresh field with its corresponding old field by key — explicitly
// scoped out of Phase 4 to keep the merge surface small.
func TestMerge_refreshHost_dropsFieldLevelComment(t *testing.T) {
	existing := `cluster_name: merge-test
master_servers:
    - ip: 10.0.0.11   # primary master, do not move
      port.ssh: 22
      port: 9333
      port.grpc: 19333
`
	inv := &inventory.Inventory{
		Hosts: []inventory.Host{{IP: "10.0.0.11", Roles: []string{"master"}}},
	}
	if err := inv.Validate(); err != nil {
		t.Fatalf("inventory.Validate: %v", err)
	}
	spec, _, err := Generate(inv, map[string]probe.HostFacts{}, Options{ClusterName: "merge-test"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	merged, _, err := Merge([]byte(existing), spec, MergeOptions{
		Marshal:      MarshalOptions{InventoryPath: "inventory.yaml", Now: goldenStamp},
		RefreshHosts: map[string]struct{}{"10.0.0.11": {}},
	})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if strings.Contains(string(merged), "primary master, do not move") {
		t.Errorf("documented limitation broke: field-level inline comment survived a refresh — if this is now intentional, update the doc on carryEntryComments and remove this test:\n%s", merged)
	}
}

// TestMerge_refreshHost_noOpWhenSetEmpty: an empty / nil
// RefreshHosts map leaves Merge in its existing append-only mode —
// no refreshes, no RefreshNotFound entries. Belt-and-braces test in
// case future code refactors break the gate.
func TestMerge_refreshHost_noOpWhenSetEmpty(t *testing.T) {
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
		t.Errorf("nil RefreshHosts should leave bytes byte-identical")
	}
	if len(report.Refreshed) != 0 || len(report.RefreshNotFound) != 0 {
		t.Errorf("nil RefreshHosts should produce no refresh report; got Refreshed=%+v NotFound=%+v",
			report.Refreshed, report.RefreshNotFound)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
