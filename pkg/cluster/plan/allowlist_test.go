package plan

import (
	"reflect"
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/inventory"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/probe"
)

func TestEligibleDisks_honorsInventoryExcludes(t *testing.T) {
	// Regression: a previous deploy-side allowlist re-derived from raw
	// facts.json and did NOT see the inventory's
	// defaults.disk.exclude rules, so a disk plan deliberately skipped
	// could still be formatted by deploy. EligibleDisks now mirrors
	// the planner's own classification — including excludes — so plan
	// and deploy agree.
	inv := &inventory.Inventory{
		Defaults: inventory.Defaults{
			Disk: inventory.DiskDefaults{Exclude: []string{"/dev/sda"}},
		},
		Hosts: []inventory.Host{
			{IP: "10.0.0.1", Roles: []string{"master"}},
			{IP: "10.0.0.21", Roles: []string{"volume"}},
		},
	}
	if err := inv.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	facts := map[string]probe.HostFacts{
		"10.0.0.21:22": {
			IP:      "10.0.0.21",
			SSHPort: 22,
			Disks: []probe.DiskFact{
				{Path: "/dev/sda", Rotational: boolPtr(true)},     // boot disk; excluded by inventory
				{Path: "/dev/sdb", Rotational: boolPtr(true)},     // fresh, eligible
				{Path: "/dev/sdc", Ephemeral: true},               // ephemeral, skipped
				{Path: "/dev/sdd", FSType: "ext4"},                // fs without claim, skipped
				{Path: "/dev/sde", MountPoint: "/var/lib/docker"}, // foreign mount
			},
		},
	}
	got := EligibleDisks(inv, facts)
	want := DeployDiskAllowlist{"10.0.0.21:22": {"/dev/sdb"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EligibleDisks =\n  got:  %+v\n  want: %+v", got, want)
	}
}

func TestEligibleDisks_keysBySSHTarget(t *testing.T) {
	// Two SSH endpoints on the same IP (different ports / creds) get
	// distinct allowlist slots — they're distinct probe targets so
	// they can have different disk topologies.
	inv := &inventory.Inventory{
		Defaults: inventory.Defaults{SSH: inventory.SSHConfig{User: "ubuntu"}},
		Hosts: []inventory.Host{
			{IP: "10.0.0.1", Roles: []string{"volume"}, SSH: &inventory.SSHConfig{Port: 22}},
			{IP: "10.0.0.1", Roles: []string{"master"}, SSH: &inventory.SSHConfig{Port: 2222}},
		},
	}
	if err := inv.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	facts := map[string]probe.HostFacts{
		"10.0.0.1:22":   {IP: "10.0.0.1", SSHPort: 22, Disks: []probe.DiskFact{{Path: "/dev/sdb"}}},
		"10.0.0.1:2222": {IP: "10.0.0.1", SSHPort: 2222, Disks: []probe.DiskFact{{Path: "/dev/sdc"}}},
	}
	got := EligibleDisks(inv, facts)
	if _, ok := got["10.0.0.1:22"]; !ok {
		t.Error("missing 10.0.0.1:22 (volume role)")
	}
	if _, ok := got["10.0.0.1:2222"]; ok {
		// 10.0.0.1:2222 is master-only, no volume role → no allowlist
		// entry expected
		t.Error("master-only target should not appear in allowlist")
	}
}

func TestEligibleDisks_skipsProbeFailedHosts(t *testing.T) {
	inv := &inventory.Inventory{
		Hosts: []inventory.Host{{IP: "10.0.0.99", Roles: []string{"volume"}}},
	}
	if err := inv.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	facts := map[string]probe.HostFacts{
		"10.0.0.99:22": {IP: "10.0.0.99", SSHPort: 22, ProbeError: "i/o timeout"},
	}
	if got := EligibleDisks(inv, facts); len(got) != 0 {
		t.Errorf("probe-failed host should not contribute to allowlist; got %+v", got)
	}
}
