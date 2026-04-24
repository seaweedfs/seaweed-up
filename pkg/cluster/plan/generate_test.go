package plan

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/inventory"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/probe"
)

// update rewrites golden files from the current output. Run with
// `go test ./pkg/cluster/plan/... -update` after intentional changes.
var update = flag.Bool("update", false, "rewrite golden files")

// Stable timestamp so golden files don't churn.
var goldenStamp = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

// synthesizeDisks produces a plausible set of DiskFact values for a
// volume host. sizeGiB is the per-disk size; n is the count. Disks are
// produced in /dev/sd{b,c,d,...} order (skipping /dev/sda) so tests
// exercise the exclude-boot-disk path. All emit FSType="" and
// MountPoint="" so they qualify for provisioning.
func synthesizeDisks(n int, sizeGiB uint64) []probe.DiskFact {
	out := make([]probe.DiskFact, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, probe.DiskFact{
			Path:       "/dev/sd" + string(rune('b'+i)),
			Size:       sizeGiB * 1024 * 1024 * 1024,
			Rotational: boolPtr(false), // all SSDs for determinism
			Model:      "Virtual SSD",
		})
	}
	return out
}

func boolPtr(b bool) *bool { return &b }

// runGolden loads inventory+facts, runs Generate + Marshal, and
// compares against the golden file. When -update is passed the golden
// file is rewritten.
func runGolden(t *testing.T, invPath, goldenPath string, facts map[string]probe.HostFacts, opts Options) {
	t.Helper()

	inv, err := inventory.Load(invPath)
	if err != nil {
		t.Fatalf("inventory.Load(%s): %v", invPath, err)
	}

	spec, _, err := Generate(inv, facts, opts)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	got, err := Marshal(spec, MarshalOptions{
		InventoryPath: filepath.Base(invPath),
		Now:           goldenStamp,
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if *update {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", goldenPath, err)
		}
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden %s (run with -update to create): %v", goldenPath, err)
	}
	if string(got) != string(want) {
		t.Errorf("golden mismatch for %s\n-- got --\n%s\n-- want --\n%s", goldenPath, got, want)
	}

	// Round-trip the output back through the spec loader to confirm
	// deploy would still accept it.
	if _, err := UnmarshalForRoundTrip(got); err != nil {
		t.Errorf("round-trip: %v", err)
	}
}

func TestGenerate_oneHostDev(t *testing.T) {
	facts := map[string]probe.HostFacts{
		"10.0.0.1:22": {
			IP:       "10.0.0.1",
			SSHPort:  22,
			CPUCores: 4,
			Disks:    synthesizeDisks(1, 100), // 100 GiB
		},
	}
	runGolden(t,
		filepath.Join("testdata", "one_host_dev.inventory.yaml"),
		filepath.Join("testdata", "one_host_dev.cluster.yaml"),
		facts,
		Options{ClusterName: "dev"})
}

func TestGenerate_threeByThreeByThreeTypical(t *testing.T) {
	facts := map[string]probe.HostFacts{
		"10.0.0.21:22": {IP: "10.0.0.21", SSHPort: 22, CPUCores: 16, Disks: synthesizeDisks(4, 1024)},
		"10.0.0.22:22": {IP: "10.0.0.22", SSHPort: 22, CPUCores: 16, Disks: synthesizeDisks(4, 1024)},
		"10.0.0.23:22": {IP: "10.0.0.23", SSHPort: 22, CPUCores: 16, Disks: synthesizeDisks(4, 1024)},
	}
	runGolden(t,
		filepath.Join("testdata", "three_three_three.inventory.yaml"),
		filepath.Join("testdata", "three_three_three.cluster.yaml"),
		facts,
		Options{
			ClusterName:       "prod",
			VolumeSizeLimitMB: 30000,
		})
}

func TestGenerate_mixedFiveWithFilerBackend(t *testing.T) {
	facts := map[string]probe.HostFacts{
		"10.0.0.21:22": {IP: "10.0.0.21", SSHPort: 22, Disks: synthesizeDisks(2, 512)},
		"10.0.0.22:22": {IP: "10.0.0.22", SSHPort: 22, Disks: synthesizeDisks(2, 512)},
	}
	backend, err := ParseFilerBackendDSN("postgres://seaweed:s3cret@10.0.0.41:5432/seaweedfs?sslmode=disable")
	if err != nil {
		t.Fatalf("ParseFilerBackendDSN: %v", err)
	}
	runGolden(t,
		filepath.Join("testdata", "mixed_five.inventory.yaml"),
		filepath.Join("testdata", "mixed_five.cluster.yaml"),
		facts,
		Options{
			ClusterName:  "mixed",
			FilerBackend: backend,
		})
}

func TestGenerate_volumeHostWithNoFreeDisksIsDropped(t *testing.T) {
	// A volume role on a host with no eligible disks is dropped entirely.
	// Emitting an entry with folders: [] would start `weed volume`
	// without -dir, which runs against the working directory instead of
	// failing loudly. Surfacing it in Report.VolumeHostsNoDisks lets the
	// CLI warn the operator, who can fix the inventory and re-run.
	inv := &inventory.Inventory{
		Hosts: []inventory.Host{
			{IP: "10.0.0.1", Roles: []string{"master"}},
			{IP: "10.0.0.2", Roles: []string{"volume"}},
		},
	}
	if err := inv.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	spec, report, err := Generate(inv, nil, Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(spec.VolumeServers) != 0 {
		t.Errorf("expected 0 volume entries (role dropped), got %d", len(spec.VolumeServers))
	}
	if len(report.VolumeHostsNoDisks) != 1 || report.VolumeHostsNoDisks[0] != "10.0.0.2" {
		t.Errorf("Report.VolumeHostsNoDisks = %+v, want [10.0.0.2]", report.VolumeHostsNoDisks)
	}
	// The master on a separate host is still emitted.
	if len(spec.MasterServers) != 1 {
		t.Errorf("expected 1 master entry, got %d", len(spec.MasterServers))
	}
}

func TestGenerate_hostWithProbeErrorIsSkippedEntirely(t *testing.T) {
	// Any host with a non-empty ProbeError is dropped from every
	// *_servers section. Emitting partial entries for unreachable hosts
	// produces a cluster.yaml that deploy can't actually apply.
	inv := &inventory.Inventory{
		Hosts: []inventory.Host{
			{IP: "10.0.0.1", Roles: []string{"master", "filer"}},
			{IP: "10.0.0.2", Roles: []string{"master", "filer"}},
			{IP: "10.0.0.3", Roles: []string{"master"}},
		},
	}
	if err := inv.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	facts := map[string]probe.HostFacts{
		"10.0.0.1:22": {IP: "10.0.0.1", SSHPort: 22},
		"10.0.0.2:22": {IP: "10.0.0.2", SSHPort: 22, ProbeError: "dial tcp: i/o timeout"},
		"10.0.0.3:22": {IP: "10.0.0.3", SSHPort: 22},
	}
	spec, report, err := Generate(inv, facts, Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(spec.MasterServers) != 2 {
		t.Errorf("expected 2 masters (one dropped), got %d: %+v", len(spec.MasterServers), spec.MasterServers)
	}
	if len(spec.FilerServers) != 1 {
		t.Errorf("expected 1 filer (the other host's filer role dropped too), got %d", len(spec.FilerServers))
	}
	if len(report.ProbeFailed) != 1 || report.ProbeFailed[0].IP != "10.0.0.2" {
		t.Errorf("Report.ProbeFailed = %+v, want one entry for 10.0.0.2", report.ProbeFailed)
	}
}

func TestGenerate_adminGetsChangeMePlaceholders(t *testing.T) {
	// admin_servers require admin_user / admin_password. Leaving them
	// empty starts the admin UI unauthenticated because
	// AdminServerSpec.WriteToBuffer only emits auth flags when they're
	// set. Plan writes CHANGE_ME placeholders matching the convention
	// in examples/typical.yaml.
	inv := &inventory.Inventory{
		Hosts: []inventory.Host{
			{IP: "10.0.0.1", Roles: []string{"master"}},
			{IP: "10.0.0.61", Roles: []string{"admin"}},
		},
	}
	if err := inv.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	spec, _, err := Generate(inv, nil, Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(spec.AdminServers) != 1 {
		t.Fatalf("expected 1 admin, got %d", len(spec.AdminServers))
	}
	if spec.AdminServers[0].AdminUser != "admin" {
		t.Errorf("admin_user: got %q, want admin", spec.AdminServers[0].AdminUser)
	}
	if spec.AdminServers[0].AdminPassword != AdminPasswordPlaceholder {
		t.Errorf("admin_password: got %q, want %q", spec.AdminServers[0].AdminPassword, AdminPasswordPlaceholder)
	}

	// Marshal output should warn about the CHANGE_ME placeholder in the
	// header so operators don't miss it.
	body, err := Marshal(spec, MarshalOptions{Now: goldenStamp})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if want := "admin_password"; !containsFold(string(body), want) ||
		!containsFold(string(body), "CHANGE_ME") {
		t.Errorf("expected header to warn about CHANGE_ME admin_password, got:\n%s", body)
	}
}

func TestGenerate_labelsMapToDataCenterAndRack(t *testing.T) {
	inv := &inventory.Inventory{
		Hosts: []inventory.Host{
			{IP: "10.0.0.1", Roles: []string{"master"}},
			{
				IP:     "10.0.0.2",
				Roles:  []string{"volume", "filer"},
				Labels: map[string]string{"zone": "us-east-1a", "rack": "r1"},
			},
		},
	}
	if err := inv.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	// Give the volume host at least one eligible disk so the volume
	// role isn't dropped (no-disks path is covered separately).
	facts := map[string]probe.HostFacts{
		"10.0.0.2:22": {IP: "10.0.0.2", SSHPort: 22, Disks: synthesizeDisks(1, 100)},
	}
	spec, _, err := Generate(inv, facts, Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got := spec.VolumeServers[0].DataCenter; got != "us-east-1a" {
		t.Errorf("volume DataCenter: got %q, want us-east-1a", got)
	}
	if got := spec.VolumeServers[0].Rack; got != "r1" {
		t.Errorf("volume Rack: got %q, want r1", got)
	}
	if got := spec.FilerServers[0].DataCenter; got != "us-east-1a" {
		t.Errorf("filer DataCenter: got %q, want us-east-1a", got)
	}
}

func TestGenerate_filerBackendFannedOutToEveryFiler(t *testing.T) {
	inv := &inventory.Inventory{
		Hosts: []inventory.Host{
			{IP: "10.0.0.1", Roles: []string{"master", "filer"}},
			{IP: "10.0.0.2", Roles: []string{"master", "filer"}},
			{IP: "10.0.0.3", Roles: []string{"master"}},
		},
	}
	if err := inv.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	backend := map[string]interface{}{
		"type":     "postgres",
		"hostname": "10.0.0.41",
		"port":     5432,
	}
	spec, _, err := Generate(inv, nil, Options{FilerBackend: backend})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(spec.FilerServers) != 2 {
		t.Fatalf("expected 2 filers, got %d", len(spec.FilerServers))
	}
	for i, f := range spec.FilerServers {
		if f.Config["type"] != "postgres" || f.Config["hostname"] != "10.0.0.41" {
			t.Errorf("filer[%d] config: got %+v, want backend applied", i, f.Config)
		}
	}
	// Confirm we copy the map so mutation doesn't leak across filers.
	spec.FilerServers[0].Config["type"] = "tampered"
	if spec.FilerServers[1].Config["type"] != "postgres" {
		t.Error("filer configs share backing storage; mutation leaked")
	}
}

func TestGenerate_s3AndWorkerWiring(t *testing.T) {
	inv := &inventory.Inventory{
		Hosts: []inventory.Host{
			{IP: "10.0.0.1", Roles: []string{"master"}},
			{IP: "10.0.0.11", Roles: []string{"filer"}},
			{IP: "10.0.0.31", Roles: []string{"s3"}},
			{IP: "10.0.0.41", Roles: []string{"admin"}},
			{IP: "10.0.0.51", Roles: []string{"worker"}},
		},
	}
	if err := inv.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	spec, _, err := Generate(inv, nil, Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got := spec.S3Servers[0].Filer; got != "10.0.0.11:8888" {
		t.Errorf("S3 filer wiring: got %q, want 10.0.0.11:8888", got)
	}
	if got := spec.WorkerServers[0].Admin; got != "10.0.0.41:23646" {
		t.Errorf("worker admin wiring: got %q, want 10.0.0.41:23646", got)
	}
}

func TestParseFilerBackendDSN(t *testing.T) {
	cases := []struct {
		name string
		dsn  string
		want map[string]interface{}
	}{
		{
			name: "postgres full",
			dsn:  "postgres://seaweed:secret@10.0.0.41:5432/seaweedfs?sslmode=disable",
			want: map[string]interface{}{
				"type":     "postgres",
				"hostname": "10.0.0.41",
				"port":     5432,
				"username": "seaweed",
				"password": "secret",
				"database": "seaweedfs",
				"sslmode":  "disable",
			},
		},
		{
			name: "postgres default port, no password",
			dsn:  "postgres://seaweed@db.internal/seaweedfs",
			want: map[string]interface{}{
				"type":     "postgres",
				"hostname": "db.internal",
				"port":     5432,
				"username": "seaweed",
				"database": "seaweedfs",
			},
		},
		{
			name: "postgresql alias",
			dsn:  "postgresql://u:p@h/d",
			want: map[string]interface{}{
				"type":     "postgres",
				"hostname": "h",
				"port":     5432,
				"username": "u",
				"password": "p",
				"database": "d",
			},
		},
		{
			name: "mysql",
			dsn:  "mysql://root:toor@10.0.0.41:3306/seaweedfs",
			want: map[string]interface{}{
				"type":     "mysql",
				"hostname": "10.0.0.41",
				"port":     3306,
				"username": "root",
				"password": "toor",
				"database": "seaweedfs",
			},
		},
		{
			name: "redis with password and db index",
			dsn:  "redis://:hunter2@10.0.0.41:6379/3",
			want: map[string]interface{}{
				"type":     "redis2",
				"address":  "10.0.0.41:6379",
				"password": "hunter2",
				"database": 3,
			},
		},
		{
			name: "redis defaults",
			dsn:  "redis://10.0.0.41",
			want: map[string]interface{}{
				"type":    "redis2",
				"address": "10.0.0.41:6379",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseFilerBackendDSN(tc.dsn)
			if err != nil {
				t.Fatalf("ParseFilerBackendDSN: %v", err)
			}
			if !mapsEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseFilerBackendDSN_errors(t *testing.T) {
	cases := []struct{ name, dsn, wantContains string }{
		{"empty", "", "empty"},
		{"unknown scheme", "sqlite:///db", "unsupported"},
		{"no host", "postgres:///db", "missing host"},
		{"redis non-integer db", "redis://10.0.0.41:6379/notanumber", "database path must be an integer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseFilerBackendDSN(tc.dsn)
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantContains)
			}
			if !containsFold(err.Error(), tc.wantContains) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantContains)
			}
		})
	}
}

func TestComputeMax(t *testing.T) {
	const GiB = uint64(1024) * 1024 * 1024
	cases := []struct {
		name       string
		size       uint64
		reservePct int
		limitMB    int
		want       int
	}{
		{"100GiB@5%/30GB", 100 * GiB, 5, 30000, 3},    // (100*1024)*0.95 / 30000 = 3
		{"1TiB@5%/30GB (10GiB cap)", 1024 * GiB, 5, 30000, 34}, // reserve capped at 10GiB
		{"10GiB@5%/5GB", 10 * GiB, 5, 5000, 1},
		{"tiny disk", 1 * GiB, 5, 5000, 0},
		{"zero-limit guard", 100 * GiB, 5, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := computeMax(tc.size, tc.reservePct, tc.limitMB); got != tc.want {
				t.Errorf("computeMax(%d, %d, %d) = %d, want %d", tc.size, tc.reservePct, tc.limitMB, got, tc.want)
			}
		})
	}
}

// mapsEqual checks structural equality for the small maps we build.
func mapsEqual(a, b map[string]interface{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok || va != vb {
			return false
		}
	}
	return true
}

func containsFold(s, sub string) bool {
	return len(sub) == 0 || len(s) >= len(sub) && indexFold(s, sub) >= 0
}

func indexFold(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		ok := true
		for j := 0; j < len(sub); j++ {
			if toLower(s[i+j]) != toLower(sub[j]) {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

func toLower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + 32
	}
	return b
}
