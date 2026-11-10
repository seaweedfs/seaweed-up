package manager

import (
	"strings"
	"testing"

	"github.com/mitchellh/go-homedir"
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

func TestLoadInstanceSecurity(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	homedir.Reset()
	t.Cleanup(homedir.Reset)

	t.Run("deploys without admin, worker or s3 skip the keys", func(t *testing.T) {
		m := NewManager()
		s := &spec.Specification{
			FilerServers:  []*spec.FilerServerSpec{{Ip: "10.0.0.1"}},
			WorkerServers: []*spec.WorkerServerSpec{{Ip: "10.0.0.3"}},
		}
		m.ComponentToDeploy = "filer"
		if m.installsKeyedInstances(s) {
			t.Error("a filer-only deploy must not need the admin signing key")
		}
		m.ComponentToDeploy = ""
		if !m.installsKeyedInstances(s) {
			t.Error("a full deploy with workers needs the admin signing key")
		}
		if got := m.instanceSecurityToml("worker"); got != nil {
			t.Fatalf("expected no security.toml without loaded keys, got %d files", len(got))
		}
		m.ComponentToDeploy = "s3"
		if m.installsKeyedInstances(&spec.Specification{S3Servers: []*spec.S3ServerSpec{{Ip: "10.0.0.4"}}}) == false {
			t.Error("an s3 deploy needs the signing key: the Lance namespace verifies cluster tokens with it")
		}
	})

	t.Run("s3 instances get the keys without TLS only", func(t *testing.T) {
		m := NewManager()
		m.filerSigning = filerSigningKeys{write: "w-key", read: "r-key"}
		files := m.s3SecurityToml()
		if len(files) != 1 || files[0].Name != "security.toml" || !strings.Contains(files[0].Content.String(), `key = "w-key"`) {
			t.Fatalf("s3 security.toml: %+v", files)
		}
		m.tlsCluster = true
		if files := m.s3SecurityToml(); files != nil {
			t.Errorf("a TLS cluster's s3 instance must keep the cluster's security.toml, got %d files", len(files))
		}
	})

	t.Run("worker-only deploy requires a cluster name", func(t *testing.T) {
		m := NewManager()
		m.ComponentToDeploy = "worker"
		s := &spec.Specification{WorkerServers: []*spec.WorkerServerSpec{{Ip: "10.0.0.3"}}}
		err := m.loadInstanceSecurity(s)
		if err == nil || !strings.Contains(err.Error(), "jwt") {
			t.Fatalf("expected missing JWT persistence error, got: %v", err)
		}
	})

	t.Run("admin and workers share the cluster key", func(t *testing.T) {
		s := &spec.Specification{
			Name:          "auth-cluster",
			AdminServers:  []*spec.AdminServerSpec{{Ip: "10.0.0.2"}},
			WorkerServers: []*spec.WorkerServerSpec{{Ip: "10.0.0.3"}},
		}
		m := NewManager()
		if err := m.loadInstanceSecurity(s); err != nil {
			t.Fatalf("loadInstanceSecurity: %v", err)
		}
		admin := m.instanceSecurityToml("admin")
		worker := m.instanceSecurityToml("worker")
		if len(admin) != 1 || len(worker) != 1 {
			t.Fatalf("expected one security.toml each, got admin=%d worker=%d", len(admin), len(worker))
		}
		if admin[0].Name != "security.toml" || admin[0].Mode != "0600" {
			t.Errorf("security.toml must be owner-only: %+v", admin[0])
		}
		adminToml, workerToml := admin[0].Content.String(), worker[0].Content.String()
		for _, want := range []string{"[jwt.filer_signing]", m.filerSigning.write, m.filerSigning.read} {
			if !strings.Contains(adminToml, want) || !strings.Contains(workerToml, want) {
				t.Errorf("admin and worker security.toml must both carry %q", want)
			}
		}
		if strings.Contains(workerToml, "[grpc") {
			t.Errorf("instance security.toml must not enable gRPC TLS:\n%s", workerToml)
		}

		again := NewManager()
		again.ComponentToDeploy = "worker"
		if err := again.loadInstanceSecurity(s); err != nil {
			t.Fatalf("worker-only reload: %v", err)
		}
		if again.filerSigning != m.filerSigning {
			t.Errorf("worker-only redeploy must reuse the persisted keys")
		}
	})
}
