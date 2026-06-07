package state_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/state"
)

func newTestSpec() *spec.Specification {
	return &spec.Specification{
		Name: "testcluster",
		MasterServers: []*spec.MasterServerSpec{
			{Ip: "10.0.0.1"},
		},
		VolumeServers: []*spec.VolumeServerSpec{
			{Ip: "10.0.0.2"},
			{Ip: "10.0.0.3"},
		},
		FilerServers: []*spec.FilerServerSpec{
			{Ip: "10.0.0.1"}, // shared host, must de-dupe
		},
	}
}

func TestDefaultDirUsesEnvOverride(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(state.EnvHome, tmp)

	got, err := state.DefaultDir()
	if err != nil {
		t.Fatalf("DefaultDir: %v", err)
	}
	want := filepath.Join(tmp, "clusters")
	if got != want {
		t.Fatalf("DefaultDir = %q, want %q", got, want)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(state.EnvHome, tmp)

	s, err := state.NewStore("")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if got, want := s.Dir(), filepath.Join(tmp, "clusters"); got != want {
		t.Fatalf("Dir = %q, want %q", got, want)
	}

	sp := newTestSpec()
	meta := state.Meta{Version: "3.75"}
	if err := s.Save("alpha", sp, meta); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loadedSpec, loadedMeta, err := s.Load("alpha")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loadedMeta.Name != "alpha" {
		t.Errorf("Meta.Name = %q, want alpha", loadedMeta.Name)
	}
	if loadedMeta.Version != "3.75" {
		t.Errorf("Meta.Version = %q, want 3.75", loadedMeta.Version)
	}
	if loadedMeta.DeployedAt.IsZero() {
		t.Error("Meta.DeployedAt should be auto-populated")
	}
	if time.Since(loadedMeta.DeployedAt) > time.Minute {
		t.Errorf("Meta.DeployedAt too old: %v", loadedMeta.DeployedAt)
	}
	if len(loadedMeta.Hosts) != 3 {
		t.Errorf("Meta.Hosts len = %d, want 3 (deduped): %v", len(loadedMeta.Hosts), loadedMeta.Hosts)
	}
	if len(loadedSpec.MasterServers) != 1 || loadedSpec.MasterServers[0].Ip != "10.0.0.1" {
		t.Errorf("MasterServers not round-tripped: %+v", loadedSpec.MasterServers)
	}
	if len(loadedSpec.VolumeServers) != 2 {
		t.Errorf("VolumeServers len = %d, want 2", len(loadedSpec.VolumeServers))
	}
}

func TestExistsAndDelete(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(state.EnvHome, tmp)
	s, err := state.NewStore("")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	if s.Exists("ghost") {
		t.Error("Exists reported true for missing cluster")
	}
	if err := s.Save("beta", newTestSpec(), state.Meta{Version: "1"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !s.Exists("beta") {
		t.Error("Exists should report true after Save")
	}
	if err := s.Delete("beta"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if s.Exists("beta") {
		t.Error("Exists should report false after Delete")
	}
}

func TestList(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(state.EnvHome, tmp)
	s, err := state.NewStore("")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	got, err := s.List()
	if err != nil {
		t.Fatalf("List empty store: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List empty store = %d, want 0", len(got))
	}

	for _, name := range []string{"charlie", "alpha", "bravo"} {
		if err := s.Save(name, newTestSpec(), state.Meta{Version: "1"}); err != nil {
			t.Fatalf("Save %s: %v", name, err)
		}
	}
	got, err = s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("List = %d, want 3", len(got))
	}
	wantNames := []string{"alpha", "bravo", "charlie"}
	for i, e := range got {
		if e.Meta.Name != wantNames[i] {
			t.Errorf("List[%d].Name = %q, want %q", i, e.Meta.Name, wantNames[i])
		}
		if e.Spec == nil {
			t.Errorf("List[%d].Spec is nil", i)
		}
	}
}

func TestHostsFromSpecDedupe(t *testing.T) {
	sp := newTestSpec()
	hosts := state.HostsFromSpec(sp)
	if len(hosts) != 3 {
		t.Errorf("HostsFromSpec = %v, want 3 entries", hosts)
	}
	// Sorted
	for i := 1; i < len(hosts); i++ {
		if hosts[i-1] >= hosts[i] {
			t.Errorf("HostsFromSpec not sorted: %v", hosts)
		}
	}
}

func TestSaveRedactsSecretsAndPerms(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(state.EnvHome, tmp)
	s, err := state.NewStore("")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	sp := newTestSpec()
	sp.GlobalOptions.Bastion = &spec.BastionSpec{Host: "203.0.113.10", User: "chris", Password: "BASTION_SECRET"}
	sp.AdminServers = []*spec.AdminServerSpec{{Ip: "10.0.0.1", AdminUser: "admin", AdminPassword: "ADMIN_SECRET"}}
	sp.FilerServers[0].Config = map[string]interface{}{
		"type": "postgres", "hostname": "10.0.0.9", "password": "FILER_DB_SECRET",
	}
	// nested secret, like s3.json identities -> credentials -> secretKey
	sp.S3Servers = []*spec.S3ServerSpec{{Ip: "10.0.0.1", S3Config: map[string]interface{}{
		"identities": []interface{}{
			map[string]interface{}{"name": "u1", "credentials": []interface{}{
				map[string]interface{}{"accessKey": "AKIA", "secretKey": "S3_SECRET"},
			}},
		},
	}}}

	if err := s.Save("c1", sp, state.Meta{Version: "1"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// 1) original spec must NOT be mutated.
	if sp.GlobalOptions.Bastion.Password != "BASTION_SECRET" ||
		sp.AdminServers[0].AdminPassword != "ADMIN_SECRET" ||
		sp.FilerServers[0].Config["password"] != "FILER_DB_SECRET" {
		t.Fatalf("Save mutated the caller's spec secrets")
	}

	// 2) the on-disk topology must contain none of the secret values.
	raw, err := os.ReadFile(filepath.Join(tmp, "clusters", "c1", "topology.yaml"))
	if err != nil {
		t.Fatalf("read topology: %v", err)
	}
	for _, secret := range []string{"BASTION_SECRET", "ADMIN_SECRET", "FILER_DB_SECRET", "S3_SECRET"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("persisted topology leaked secret %q:\n%s", secret, raw)
		}
	}
	// non-secret config is preserved.
	if !strings.Contains(string(raw), "postgres") || !strings.Contains(string(raw), "AKIA") {
		t.Errorf("redaction removed non-secret config:\n%s", raw)
	}

	// 3) files are owner-only (0600).
	for _, f := range []string{"topology.yaml", "state.json"} {
		fi, err := os.Stat(filepath.Join(tmp, "clusters", "c1", f))
		if err != nil {
			t.Fatalf("stat %s: %v", f, err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s perm = %04o, want 0600", f, perm)
		}
	}
}
