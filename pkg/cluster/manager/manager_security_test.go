package manager

import (
	"strings"
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

// TestEnsureSecurityTomlEarlyReturns covers the three gating paths in
// EnsureSecurityToml that short-circuit before touching SSH:
//
//  1. TLS is enabled — cluster cert init's UploadBundle already wrote
//     security.toml on every host, so a JWT-only overwrite would clobber
//     the [grpc.*] sections.
//  2. No filer or admin hosts — nothing to install on.
//  3. Cluster name unset — we cannot persist or look up the JWT key
//     without it; fail loud rather than silently rotating it on every
//     deploy.
//
// The deploy-flow path (TLS off + filer/admin hosts present) requires
// real SSH and is exercised by the integration suite.
func TestEnsureSecurityTomlEarlyReturns(t *testing.T) {
	t.Run("TLS enabled is a no-op", func(t *testing.T) {
		m := NewManager()
		s := &spec.Specification{
			Name:          "tls-cluster",
			GlobalOptions: spec.GlobalOptions{TLSEnabled: true},
			FilerServers:  []*spec.FilerServerSpec{{Ip: "10.0.0.1"}},
			AdminServers:  []*spec.AdminServerSpec{{Ip: "10.0.0.2"}},
		}
		if err := m.EnsureSecurityToml(s); err != nil {
			t.Fatalf("expected no-op, got error: %v", err)
		}
	})

	t.Run("no filer or admin is a no-op", func(t *testing.T) {
		m := NewManager()
		s := &spec.Specification{
			Name:          "master-only",
			MasterServers: []*spec.MasterServerSpec{{Ip: "10.0.0.1"}},
			VolumeServers: []*spec.VolumeServerSpec{{Ip: "10.0.0.2"}},
		}
		if err := m.EnsureSecurityToml(s); err != nil {
			t.Fatalf("expected no-op, got error: %v", err)
		}
	})

	t.Run("missing cluster name errors loudly", func(t *testing.T) {
		m := NewManager()
		s := &spec.Specification{
			Name:         "",
			FilerServers: []*spec.FilerServerSpec{{Ip: "10.0.0.1"}},
		}
		err := m.EnsureSecurityToml(s)
		if err == nil {
			t.Fatal("expected error when cluster name is empty, got nil")
		}
		// The error must mention the JWT key so the operator knows
		// what's missing rather than a generic "name required".
		if !strings.Contains(err.Error(), "jwt") {
			t.Errorf("error should reference the JWT key it cannot persist, got: %v", err)
		}
	})
}
