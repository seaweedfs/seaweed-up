package manager

import (
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

// TestPrepare_S3ConfigInheritsGlobal verifies an s3_servers entry without its
// own s3_config inherits global.s3_config, while an entry with its own keeps it.
// Filer is likewise defaulted from the first filer.
func TestPrepare_S3ConfigInheritsGlobal(t *testing.T) {
	global := map[string]interface{}{"identities": "shared"}
	own := map[string]interface{}{"identities": "own"}
	s := &spec.Specification{
		GlobalOptions: spec.GlobalOptions{S3Config: global},
		FilerServers:  []*spec.FilerServerSpec{{Ip: "10.0.0.1", Port: 8888}},
		S3Servers: []*spec.S3ServerSpec{
			{Ip: "10.0.0.1"},                // inherits global s3_config + default filer
			{Ip: "10.0.0.2", S3Config: own}, // keeps its own
		},
	}

	m := &Manager{User: "root"} // root skips the sudo-password prompt in prepare
	m.prepare(s)

	if got := s.S3Servers[0].S3Config["identities"]; got != "shared" {
		t.Errorf("s3[0] should inherit global s3_config, got %v", got)
	}
	if got := s.S3Servers[1].S3Config["identities"]; got != "own" {
		t.Errorf("s3[1] should keep its own s3_config, got %v", got)
	}
	if got := s.S3Servers[0].Filer; got != "10.0.0.1:8888" {
		t.Errorf("s3[0] filer should default to first filer, got %q", got)
	}
}
