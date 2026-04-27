package manager

import (
	"strings"
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

func TestValidateSingleAdminServer_zeroOrOneOK(t *testing.T) {
	// Zero admins: cluster runs without the admin UI. Allowed.
	if err := validateSingleAdminServer(&spec.Specification{}); err != nil {
		t.Errorf("zero admins should pass: %v", err)
	}
	// One admin: canonical shape. Allowed.
	one := &spec.Specification{}
	one.AdminServers = append(one.AdminServers, &spec.AdminServerSpec{Ip: "10.0.0.61"})
	if err := validateSingleAdminServer(one); err != nil {
		t.Errorf("single admin should pass: %v", err)
	}
}

func TestValidateSingleAdminServer_rejectsNilEntry(t *testing.T) {
	// A YAML null list item (`admin_servers: [null]` or a stray
	// bullet on its own line) decodes to a nil *AdminServerSpec.
	// Without the defensive nil check, resolveWorkerDefaultAdmins
	// would later panic on AdminServers[0].Port; the validator
	// turns that into an actionable error.
	s := &spec.Specification{}
	s.AdminServers = append(s.AdminServers, nil)
	err := validateSingleAdminServer(s)
	if err == nil {
		t.Fatal("expected error for nil admin entry, got nil")
	}
	if !strings.Contains(err.Error(), "null") {
		t.Errorf("error should mention the null entry, got: %v", err)
	}

	// Same protection when the nil sits next to a real entry —
	// the count alone (>1) might mask the nil otherwise.
	s2 := &spec.Specification{}
	s2.AdminServers = append(s2.AdminServers,
		&spec.AdminServerSpec{Ip: "10.0.0.61"},
		nil,
	)
	if err := validateSingleAdminServer(s2); err == nil {
		t.Fatal("expected error for mixed nil-and-valid admin entries, got nil")
	}
}

func TestValidateSingleAdminServer_rejectsTwo(t *testing.T) {
	// Two admins: SeaweedFS's admin UI is single-instance, so this
	// would race on task scheduling. Refuse before any SSH session.
	s := &spec.Specification{}
	s.AdminServers = append(s.AdminServers,
		&spec.AdminServerSpec{Ip: "10.0.0.61"},
		&spec.AdminServerSpec{Ip: "10.0.0.62"},
	)
	err := validateSingleAdminServer(s)
	if err == nil {
		t.Fatal("expected error for two admins, got nil")
	}
	if !strings.Contains(err.Error(), "10.0.0.61") || !strings.Contains(err.Error(), "10.0.0.62") {
		t.Errorf("error should name both admin IPs, got: %v", err)
	}
	if !strings.Contains(err.Error(), "single-instance") {
		t.Errorf("error should explain the rationale, got: %v", err)
	}
}

func TestValidateSftpFilerPrerequisite_NoSftp(t *testing.T) {
	s := &spec.Specification{}
	if err := validateSftpFilerPrerequisite(s); err != nil {
		t.Fatalf("expected no error for empty sftp, got %v", err)
	}
}

func TestValidateSftpFilerPrerequisite_FilerDefined(t *testing.T) {
	s := &spec.Specification{
		FilerServers: []*spec.FilerServerSpec{{Ip: "10.0.0.1", Port: 8888}},
		SftpServers:  []*spec.SftpServerSpec{{Ip: "10.0.0.5"}},
	}
	if err := validateSftpFilerPrerequisite(s); err != nil {
		t.Fatalf("expected no error when filer server exists, got %v", err)
	}
}

func TestValidateSftpFilerPrerequisite_ExplicitFiler(t *testing.T) {
	s := &spec.Specification{
		SftpServers: []*spec.SftpServerSpec{
			{Ip: "10.0.0.5", Filer: "external:8888"},
			{Ip: "10.0.0.6", Filer: "external:8888"},
		},
	}
	if err := validateSftpFilerPrerequisite(s); err != nil {
		t.Fatalf("expected no error when every sftp has explicit filer, got %v", err)
	}
}

func TestValidateSftpFilerPrerequisite_MissingFiler(t *testing.T) {
	s := &spec.Specification{
		SftpServers: []*spec.SftpServerSpec{
			{Ip: "10.0.0.5", Filer: "external:8888"},
			{Ip: "10.0.0.6"}, // missing
		},
	}
	err := validateSftpFilerPrerequisite(s)
	if err == nil {
		t.Fatalf("expected error when an sftp server lacks filer")
	}
	if !strings.Contains(err.Error(), "10.0.0.6") {
		t.Fatalf("error should mention offending host, got %v", err)
	}
}

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
