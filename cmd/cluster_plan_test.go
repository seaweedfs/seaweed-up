package cmd

import (
	"os"
	"path/filepath"
	"strings"
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
	// Both sidecars must be present for plan-generated detection.
	if err := os.WriteFile(factsFilePath(specPath), []byte("[]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
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

func TestLoadPlannedDeployDisks_handWritten_noSidecars_returnsNilNil(t *testing.T) {
	// Hand-written cluster.yaml: neither facts.json nor deploy-disks
	// sidecar exists. (nil, nil) means deploy keeps its legacy
	// scan-everything path so existing operators aren't broken.
	got, err := loadPlannedDeployDisks(filepath.Join(t.TempDir(), "cluster.yaml"))
	if err != nil {
		t.Fatalf("expected no error for hand-written spec, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil allowlist for hand-written spec, got %+v", got)
	}
}

func TestLoadPlannedDeployDisks_planGenerated_missingDeployDisks_failsClosed(t *testing.T) {
	// facts.json present, deploy-disks.json missing (operator deleted
	// it / sidecar was lost in transit). MUST fail rather than fall
	// back to scan-everything, because that would format disks plan
	// classified out (excludes, ephemeral, foreign mounts).
	dir := t.TempDir()
	specPath := filepath.Join(dir, "cluster.yaml")
	if err := os.WriteFile(factsFilePath(specPath), []byte("[]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadPlannedDeployDisks(specPath)
	if err == nil {
		t.Fatalf("expected fail-closed error, got allowlist %+v", got)
	}
	if !strings.Contains(err.Error(), "deploy-disks") {
		t.Errorf("error should reference the missing sidecar, got %q", err.Error())
	}
}

func TestLoadPlannedDeployDisks_planGenerated_corruptDeployDisks_failsClosed(t *testing.T) {
	// Same fail-closed contract for an unparseable sidecar.
	dir := t.TempDir()
	specPath := filepath.Join(dir, "cluster.yaml")
	if err := os.WriteFile(factsFilePath(specPath), []byte("[]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
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
	if err := os.WriteFile(factsFilePath(specPath), []byte("[]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
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
