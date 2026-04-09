package state_test

import (
	"path/filepath"
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
	s, _ := state.NewStore("")

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
	s, _ := state.NewStore("")

	if got := s.List(); len(got) != 0 {
		t.Errorf("List empty store = %d, want 0", len(got))
	}

	for _, name := range []string{"charlie", "alpha", "bravo"} {
		if err := s.Save(name, newTestSpec(), state.Meta{Version: "1"}); err != nil {
			t.Fatalf("Save %s: %v", name, err)
		}
	}
	got := s.List()
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
