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
			name: "duplicate ip+role",
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

func TestValidate_rejectsConflictingSSHForSameTarget(t *testing.T) {
	// A host with multiple roles shares one SSH session (dedup keyed on
	// ip:ssh-port). If two rows disagree on user/identity the dedup would
	// silently pick a winner and the JSON output couldn't record the
	// divergence. Reject at parse time so the bug surfaces before a
	// probe ever runs.
	path := filepath.Join(t.TempDir(), "inv.yaml")
	y := "defaults:\n  ssh:\n    user: ubuntu\nhosts:\n" +
		"  - ip: 10.0.0.1\n    roles: [master]\n" +
		"  - ip: 10.0.0.1\n    roles: [filer]\n    ssh: { user: deploy }\n"
	if err := writeFile(path, y); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	if !strings.Contains(err.Error(), "conflicting ssh config") {
		t.Errorf("error = %q, want 'conflicting ssh config'", err.Error())
	}
}

func TestValidate_allowsSameSSHForSameTarget(t *testing.T) {
	// A host appearing in multiple roles at the same SSH creds is the
	// common case (master + filer colocated) — must validate.
	path := filepath.Join(t.TempDir(), "inv.yaml")
	y := "defaults:\n  ssh:\n    user: ubuntu\nhosts:\n" +
		"  - ip: 10.0.0.1\n    roles: [master]\n" +
		"  - ip: 10.0.0.1\n    roles: [filer]\n"
	if err := writeFile(path, y); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("consistent SSH config should validate: %v", err)
	}
}

func TestValidate_rejectsUnsupportedDeviceGlobs(t *testing.T) {
	// Only a literal path with an optional trailing '*' is supported.
	// Anything fancier would silently match nothing once converted to a
	// prefix at probe time, so reject at parse time.
	cases := []struct {
		name, glob string
	}{
		{"character class", "/dev/sd[ab]"},
		{"interior wildcard", "/dev/sd*n*"},
		{"leading wildcard", "*sd"},
		{"question mark", "/dev/?d1"},
		{"empty", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "inv.yaml")
			y := "defaults:\n  disk:\n    device_globs: [\"" + tc.glob + "\"]\nhosts:\n" +
				"  - ip: 10.0.0.1\n    roles: [volume]\n"
			if err := writeFile(path, y); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatalf("expected error for glob %q", tc.glob)
			}
		})
	}
}

func TestValidate_acceptsTrailingStarAndLiteralGlobs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inv.yaml")
	y := "defaults:\n  disk:\n    device_globs: [\"/dev/sd*\", \"/dev/nvme*\", \"/dev/xvda\"]\nhosts:\n" +
		"  - ip: 10.0.0.1\n    roles: [volume]\n"
	if err := writeFile(path, y); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("supported globs should validate: %v", err)
	}
}

func TestValidate_externalSkipsSSHConflictCheck(t *testing.T) {
	// External hosts don't open SSH sessions, so they don't need to agree
	// with co-located roles on SSH creds — in fact, they might not even
	// share the same IP (the tag-based lookup path doesn't care).
	// Guard that the check doesn't spuriously complain about them.
	path := filepath.Join(t.TempDir(), "inv.yaml")
	y := "defaults:\n  ssh:\n    user: ubuntu\nhosts:\n" +
		"  - ip: 10.0.0.41\n    roles: [external]\n    tag: metadata\n" +
		"  - ip: 10.0.0.1\n    roles: [master]\n"
	if err := writeFile(path, y); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("external host should not participate in ssh conflict check: %v", err)
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

func TestProbeHosts_dedupsBySSHTarget(t *testing.T) {
	// A host appearing in multiple roles (common: master + filer colocated)
	// shares one SSH target — one probe is enough; the planner fans the
	// result out later. Different SSH ports on the same IP stay distinct.
	inv := &Inventory{
		Hosts: []Host{
			{IP: "10.0.0.1", Roles: []string{"master"}},
			{IP: "10.0.0.1", Roles: []string{"filer"}},
			{IP: "10.0.0.1", Roles: []string{"worker"}, SSH: &SSHConfig{Port: 2222}},
			{IP: "10.0.0.2", Roles: []string{"volume"}},
		},
	}
	if err := inv.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	got := inv.ProbeHosts()
	// Expected: 10.0.0.1:22 (first entry — second dedups into it),
	// 10.0.0.1:2222 (third — distinct SSH port), 10.0.0.2:22.
	if len(got) != 3 {
		t.Fatalf("ProbeHosts: got %d entries, want 3: %+v", len(got), got)
	}
	want := []struct {
		ip      string
		sshPort int
	}{
		{"10.0.0.1", 22},
		{"10.0.0.1", 2222},
		{"10.0.0.2", 22},
	}
	for i, h := range got {
		if h.IP != want[i].ip {
			t.Errorf("entry %d: got ip %s, want %s", i, h.IP, want[i].ip)
		}
		if port := inv.EffectiveSSH(h).Port; port != want[i].sshPort {
			t.Errorf("entry %d: got ssh port %d, want %d", i, port, want[i].sshPort)
		}
	}
}

func TestValidate_rejectsDuplicateTag(t *testing.T) {
	// Two hosts carrying the same tag would make tag:<name> rewrites
	// ambiguous. Refuse at load time so the operator sees the
	// collision instead of the planner picking an arbitrary winner.
	inv := &Inventory{
		Hosts: []Host{
			{IP: "10.0.0.41", Roles: []string{"external"}, Tag: "postgres-metadata"},
			{IP: "10.0.0.42", Roles: []string{"external"}, Tag: "postgres-metadata"},
		},
	}
	err := inv.Validate()
	if err == nil {
		t.Fatal("expected duplicate-tag error, got nil")
	}
	if !strings.Contains(err.Error(), "postgres-metadata") {
		t.Errorf("error should name the offending tag, got: %v", err)
	}
	if !strings.Contains(err.Error(), "10.0.0.41") || !strings.Contains(err.Error(), "10.0.0.42") {
		t.Errorf("error should name both hosts, got: %v", err)
	}
}

func TestValidate_emptyTagIsNotShared(t *testing.T) {
	// Empty tag means "no tag at all" — multiple hosts without a tag
	// must not collide with each other.
	inv := &Inventory{
		Hosts: []Host{
			{IP: "10.0.0.11", Roles: []string{"master"}},
			{IP: "10.0.0.12", Roles: []string{"master"}},
		},
	}
	if err := inv.Validate(); err != nil {
		t.Errorf("empty tags should not collide: %v", err)
	}
}

func TestHostByTag(t *testing.T) {
	inv := &Inventory{
		Hosts: []Host{
			{IP: "10.0.0.11", Roles: []string{"master"}},
			{IP: "10.0.0.41", Roles: []string{"external"}, Tag: "postgres-metadata"},
			{IP: "10.0.0.51", Roles: []string{"external"}, Tag: "redis"},
		},
	}
	if err := inv.Validate(); err != nil {
		t.Fatal(err)
	}

	h, ok := inv.HostByTag("postgres-metadata")
	if !ok || h.IP != "10.0.0.41" {
		t.Errorf("HostByTag(postgres-metadata) = (%+v, %v), want 10.0.0.41 / true", h, ok)
	}
	if _, ok := inv.HostByTag("nonexistent"); ok {
		t.Errorf("HostByTag should miss on unknown tag")
	}
	if _, ok := inv.HostByTag(""); ok {
		t.Errorf("HostByTag(\"\") should miss — empty tag is not a key")
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
