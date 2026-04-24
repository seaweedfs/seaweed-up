package inventory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_typical(t *testing.T) {
	inv, err := Load(filepath.Join("testdata", "typical.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(inv.Hosts) != 6 {
		t.Fatalf("Hosts: got %d, want 6", len(inv.Hosts))
	}
	if inv.Defaults.SSH.User != "ubuntu" {
		t.Errorf("defaults.ssh.user: got %q, want ubuntu", inv.Defaults.SSH.User)
	}
	// Per-host SSH override should layer onto defaults.
	var volumeHost *Host
	for i := range inv.Hosts {
		if inv.Hosts[i].IP == "10.0.0.21" {
			volumeHost = &inv.Hosts[i]
		}
	}
	if volumeHost == nil {
		t.Fatal("10.0.0.21 missing from inventory")
	}
	ssh := inv.EffectiveSSH(volumeHost)
	if ssh.User != "deploy" {
		t.Errorf("per-host ssh override: got %q, want deploy", ssh.User)
	}
	if ssh.Port != 22 {
		t.Errorf("ssh port fallback: got %d, want 22", ssh.Port)
	}
}

func TestValidate_errors(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "no hosts",
			yaml: "defaults: {}\nhosts: []\n",
			want: "no hosts",
		},
		{
			name: "host without ip",
			yaml: "hosts:\n  - roles: [master]\n",
			want: "has no ip",
		},
		{
			name: "host without roles",
			yaml: "hosts:\n  - ip: 10.0.0.1\n",
			want: "has no roles",
		},
		{
			name: "unknown role",
			yaml: "hosts:\n  - ip: 10.0.0.1\n    roles: [controlplane]\n",
			want: "unknown role",
		},
		{
			name: "duplicate ip+role+port",
			yaml: "hosts:\n  - ip: 10.0.0.1\n    roles: [volume]\n  - ip: 10.0.0.1\n    roles: [volume]\n",
			want: "twice",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "inv.yaml")
			if err := writeFile(path, tc.yaml); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestValidate_allowsDupIPAcrossPorts(t *testing.T) {
	// The design permits multi-instance volume servers on the same host
	// (distinguished by port). Make sure validation doesn't reject that.
	path := filepath.Join(t.TempDir(), "inv.yaml")
	y := "hosts:\n" +
		"  - ip: 10.0.0.1\n    roles: [volume]\n    port: 8080\n" +
		"  - ip: 10.0.0.1\n    roles: [volume]\n    port: 8081\n"
	if err := writeFile(path, y); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("multi-instance on same host should validate: %v", err)
	}
}

func TestProbeHosts_skipsExternal(t *testing.T) {
	inv := &Inventory{
		Hosts: []Host{
			{IP: "10.0.0.1", Roles: []string{"master"}},
			{IP: "10.0.0.41", Roles: []string{"external"}, Tag: "postgres"},
		},
	}
	if err := inv.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	got := inv.ProbeHosts()
	if len(got) != 1 || got[0].IP != "10.0.0.1" {
		t.Errorf("ProbeHosts: got %+v, want just 10.0.0.1", got)
	}
}

func TestHasRole(t *testing.T) {
	h := &Host{Roles: []string{"master", "filer"}}
	if !h.HasRole("master") || !h.HasRole("filer") {
		t.Error("HasRole: expected true for declared roles")
	}
	if h.HasRole("volume") {
		t.Error("HasRole: expected false for undeclared role")
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
