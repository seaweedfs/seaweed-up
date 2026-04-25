package cmd

import (
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

func TestLoadPlannedDeployDisks_readsSidecar(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "cluster.yaml")
	// Allowlist sidecar carries the planner's authoritative
	// classification result, keyed by SSH target. Two distinct SSH
	// endpoints can share an IP — verify both slots survive the
	// load.
	body := `{
  "10.0.0.21:22":   ["/dev/nvme0n1", "/dev/nvme2n1"],
  "10.0.0.21:2222": ["/dev/sdb"],
  "10.0.0.22:22":   ["/dev/sdc"]
}
`
	if err := os.WriteFile(deployDisksFilePath(specPath), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := loadPlannedDeployDisks(specPath)
	if got == nil {
		t.Fatal("loadPlannedDeployDisks returned nil")
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

func TestLoadPlannedDeployDisks_missingSidecar(t *testing.T) {
	if got := loadPlannedDeployDisks(filepath.Join(t.TempDir(), "no-such.yaml")); got != nil {
		t.Errorf("expected nil for missing sidecar, got %+v", got)
	}
}

func TestLoadPlannedDeployDisks_emptyMap(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "cluster.yaml")
	if err := os.WriteFile(deployDisksFilePath(specPath), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadPlannedDeployDisks(specPath); got != nil {
		t.Errorf("empty allowlist should return nil so deploy falls back to legacy behavior, got %+v", got)
	}
}
