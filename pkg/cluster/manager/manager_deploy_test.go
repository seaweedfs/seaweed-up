package manager

import (
	"strings"
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

// TestResolveWorkerDefaultAdmins covers the three precedence paths documented
// on resolveWorkerDefaultAdmins:
//  1. Explicit per-worker Admin wins — no default required.
//  2. AdminServers defined — first admin server becomes the default.
//  3. Neither explicit Admin nor AdminServers — clear error, never a master
//     IP (port 23646 belongs to `weed admin`, not `weed master`).
func TestResolveWorkerDefaultAdmins(t *testing.T) {
	t.Run("explicit worker admin requires no default", func(t *testing.T) {
		sp := &spec.Specification{
			MasterServers: []*spec.MasterServerSpec{{Ip: "10.0.0.1", Port: 9333}},
			WorkerServers: []*spec.WorkerServerSpec{
				{Ip: "10.0.0.10", PortSsh: 22, Admin: "admin.example:23646"},
			},
		}
		defaults, err := resolveWorkerDefaultAdmins(sp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(defaults) != 0 {
			t.Fatalf("expected empty defaults when every worker has explicit Admin, got %v", defaults)
		}
	})

	t.Run("first admin server used when admin_servers defined", func(t *testing.T) {
		sp := &spec.Specification{
			MasterServers: []*spec.MasterServerSpec{
				{Ip: "10.0.0.1", Port: 9333},
				{Ip: "10.0.0.2", Port: 9333},
			},
			AdminServers: []*spec.AdminServerSpec{
				{Ip: "10.0.0.100", Port: 23646},
				{Ip: "10.0.0.101", Port: 23646},
			},
			WorkerServers: []*spec.WorkerServerSpec{
				{Ip: "10.0.0.10", PortSsh: 22},
			},
		}
		defaults, err := resolveWorkerDefaultAdmins(sp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(defaults) != 1 || defaults[0] != "10.0.0.100:23646" {
			t.Fatalf("expected [10.0.0.100:23646], got %v", defaults)
		}
		// Guardrail for the original bug: must NOT default to a master IP.
		for _, d := range defaults {
			if strings.HasPrefix(d, "10.0.0.1:") || strings.HasPrefix(d, "10.0.0.2:") {
				t.Fatalf("default admin must not point at a master, got %s", d)
			}
		}
	})

	t.Run("admin server with zero port falls back to 23646", func(t *testing.T) {
		sp := &spec.Specification{
			AdminServers: []*spec.AdminServerSpec{
				{Ip: "10.0.0.100"}, // Port intentionally unset
			},
			WorkerServers: []*spec.WorkerServerSpec{
				{Ip: "10.0.0.10", PortSsh: 22},
			},
		}
		defaults, err := resolveWorkerDefaultAdmins(sp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(defaults) != 1 || defaults[0] != "10.0.0.100:23646" {
			t.Fatalf("expected [10.0.0.100:23646], got %v", defaults)
		}
	})

	t.Run("no admin_servers and no explicit admin returns clear error", func(t *testing.T) {
		sp := &spec.Specification{
			// Masters are defined to prove that the fix does NOT silently
			// fall back to a master IP. Upstream `weed worker -admin` points
			// to the admin component on port 23646, not a master.
			MasterServers: []*spec.MasterServerSpec{{Ip: "10.0.0.1", Port: 9333}},
			WorkerServers: []*spec.WorkerServerSpec{
				{Ip: "10.0.0.10", PortSsh: 22},
			},
		}
		defaults, err := resolveWorkerDefaultAdmins(sp)
		if err == nil {
			t.Fatalf("expected error when no admin endpoint can be resolved, got defaults=%v", defaults)
		}
		msg := err.Error()
		if !strings.Contains(msg, "worker requires an admin endpoint") &&
			!strings.Contains(msg, "admin endpoint") {
			t.Fatalf("error message should mention missing admin endpoint, got %q", msg)
		}
		if !strings.Contains(msg, "admin_server") {
			t.Fatalf("error message should direct the user to define an admin_server, got %q", msg)
		}
	})
}
