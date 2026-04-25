package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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

func TestLoadPlannedDisksFromFacts_classifies(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "cluster.yaml")
	factsPath := factsFilePath(specPath)

	// Mix of disk classes per host:
	//   /dev/nvme0n1 — fresh, eligible
	//   /dev/nvme1n1 — ephemeral, must be skipped
	//   /dev/nvme2n1 — claimed at /data1, eligible (re-deploy idempotent)
	//   /dev/nvme3n1 — foreign mount /var/lib/docker, must be skipped
	//   /dev/nvme4n1 — has fs but no claim, must be skipped
	facts := []map[string]interface{}{
		{
			"ip":       "10.0.0.21",
			"ssh_port": 22,
			"disks": []map[string]interface{}{
				{"path": "/dev/nvme0n1"},
				{"path": "/dev/nvme1n1", "ephemeral": true},
				{"path": "/dev/nvme2n1", "fstype": "ext4", "mountpoint": "/data1"},
				{"path": "/dev/nvme3n1", "fstype": "ext4", "mountpoint": "/var/lib/docker"},
				{"path": "/dev/nvme4n1", "fstype": "ext4"},
			},
		},
	}
	body, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(factsPath, body, 0o600); err != nil {
		t.Fatal(err)
	}

	got := loadPlannedDisksFromFacts(specPath)
	if got == nil {
		t.Fatal("loadPlannedDisksFromFacts returned nil")
	}
	approved := got["10.0.0.21"]
	if _, ok := approved["/dev/nvme0n1"]; !ok {
		t.Error("fresh disk /dev/nvme0n1 missing from allowlist")
	}
	if _, ok := approved["/dev/nvme2n1"]; !ok {
		t.Error("claimed-/data1 disk /dev/nvme2n1 missing from allowlist")
	}
	for _, skip := range []string{"/dev/nvme1n1", "/dev/nvme3n1", "/dev/nvme4n1"} {
		if _, ok := approved[skip]; ok {
			t.Errorf("ineligible disk %s wrongly in allowlist", skip)
		}
	}
}

func TestLoadPlannedDisksFromFacts_missingSidecar(t *testing.T) {
	if got := loadPlannedDisksFromFacts(filepath.Join(t.TempDir(), "no-such.yaml")); got != nil {
		t.Errorf("expected nil for missing sidecar, got %+v", got)
	}
}

func TestLoadPlannedDisksFromFacts_skipsHostWithProbeError(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "cluster.yaml")
	facts := []map[string]interface{}{
		{
			"ip":          "10.0.0.99",
			"probe_error": "dial tcp: i/o timeout",
			"disks":       []map[string]interface{}{{"path": "/dev/sda"}},
		},
	}
	body, _ := json.Marshal(facts)
	_ = os.WriteFile(factsFilePath(specPath), body, 0o600)
	if got := loadPlannedDisksFromFacts(specPath); got != nil {
		t.Errorf("probe-failed host should not contribute to allowlist, got %+v", got)
	}
}
