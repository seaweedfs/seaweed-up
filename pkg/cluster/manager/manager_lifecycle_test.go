package manager

import (
	"strings"
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

func sampleSpec() *spec.Specification {
	return &spec.Specification{
		Name: "test",
		GlobalOptions: spec.GlobalOptions{
			ConfigDir: "/etc/seaweed",
			DataDir:   "/opt/seaweed",
		},
		MasterServers: []*spec.MasterServerSpec{
			{Ip: "10.0.0.1", PortSsh: 22},
		},
		VolumeServers: []*spec.VolumeServerSpec{
			{Ip: "10.0.0.1", PortSsh: 22},
			{Ip: "10.0.0.2", PortSsh: 22},
		},
		FilerServers: []*spec.FilerServerSpec{
			{Ip: "10.0.0.3", PortSsh: 22},
		},
		EnvoyServers: []*spec.EnvoyServerSpec{
			{Ip: "10.0.0.4", PortSsh: 22},
		},
		AdminServers: []*spec.AdminServerSpec{
			{Ip: "10.0.0.5", PortSsh: 22},
		},
		WorkerServers: []*spec.WorkerServerSpec{
			{Ip: "10.0.0.6", PortSsh: 22},
			{Ip: "10.0.0.6", PortSsh: 22},
		},
	}
}

func TestUniqueHosts(t *testing.T) {
	s := sampleSpec()
	hosts := uniqueHosts(s, "")
	if len(hosts) != 6 {
		t.Fatalf("expected 6 unique hosts, got %d", len(hosts))
	}

	if got := uniqueHosts(s, "master"); len(got) != 1 || got[0].ip != "10.0.0.1" {
		t.Errorf("master filter wrong: %+v", got)
	}
	if got := uniqueHosts(s, "volume"); len(got) != 2 {
		t.Errorf("volume filter wrong: %+v", got)
	}
	if got := uniqueHosts(s, "filer"); len(got) != 1 || got[0].ip != "10.0.0.3" {
		t.Errorf("filer filter wrong: %+v", got)
	}
	if got := uniqueHosts(s, "envoy"); len(got) != 1 || got[0].ip != "10.0.0.4" {
		t.Errorf("envoy filter wrong: %+v", got)
	}
	if got := uniqueHosts(s, "admin"); len(got) != 1 || got[0].ip != "10.0.0.5" {
		t.Errorf("admin filter wrong: %+v", got)
	}
	if got := uniqueHosts(s, "worker"); len(got) != 1 || got[0].ip != "10.0.0.6" {
		t.Errorf("worker filter wrong: %+v", got)
	}
}

func TestUniqueHostsEnvoyOnlyHost(t *testing.T) {
	// A host that exclusively runs envoy must still be enumerated for
	// cluster-wide lifecycle operations.
	s := &spec.Specification{
		MasterServers: []*spec.MasterServerSpec{{Ip: "10.0.0.1", PortSsh: 22}},
		EnvoyServers:  []*spec.EnvoyServerSpec{{Ip: "10.0.0.9", PortSsh: 22}},
	}
	hosts := uniqueHosts(s, "")
	found := false
	for _, h := range hosts {
		if h.ip == "10.0.0.9" {
			found = true
		}
	}
	if !found {
		t.Fatalf("envoy-only host 10.0.0.9 missing from uniqueHosts: %+v", hosts)
	}
}

func TestServicesForHost(t *testing.T) {
	s := sampleSpec()

	svcs := servicesForHost(s, "10.0.0.1", "")
	if len(svcs) != 2 {
		t.Fatalf("expected 2 services for 10.0.0.1, got %v", svcs)
	}
	joined := strings.Join(svcs, ",")
	if !strings.Contains(joined, "seaweed_master0.service") {
		t.Errorf("missing master0: %s", joined)
	}
	if !strings.Contains(joined, "seaweed_volume0.service") {
		t.Errorf("missing volume0: %s", joined)
	}

	if got := servicesForHost(s, "10.0.0.1", "master"); len(got) != 1 || got[0] != "seaweed_master0.service" {
		t.Errorf("master filter: %v", got)
	}

	if got := servicesForHost(s, "10.0.0.2", ""); len(got) != 1 || got[0] != "seaweed_volume1.service" {
		t.Errorf("10.0.0.2 volume1: %v", got)
	}

	if got := servicesForHost(s, "10.0.0.4", ""); len(got) != 1 || got[0] != "seaweed_envoy0.service" {
		t.Errorf("10.0.0.4 envoy0: %v", got)
	}
	if got := servicesForHost(s, "10.0.0.4", "envoy"); len(got) != 1 || got[0] != "seaweed_envoy0.service" {
		t.Errorf("envoy filter: %v", got)
	}

	if got := servicesForHost(s, "10.0.0.5", "admin"); len(got) != 1 || got[0] != "seaweed_admin0.service" {
		t.Errorf("admin filter: %v", got)
	}

	// Two worker instances co-located on the same host should both surface.
	workerSvcs := servicesForHost(s, "10.0.0.6", "worker")
	if len(workerSvcs) != 2 {
		t.Fatalf("expected 2 worker services, got %v", workerSvcs)
	}
	if workerSvcs[0] != "seaweed_worker0.service" || workerSvcs[1] != "seaweed_worker1.service" {
		t.Errorf("worker services wrong: %v", workerSvcs)
	}
}

func TestBuildLifecycleCommand(t *testing.T) {
	cmd := buildLifecycleCommand(LifecycleStart, []string{"seaweed_master0.service", "seaweed_volume0.service"})
	if !strings.HasPrefix(cmd, "systemctl start ") {
		t.Errorf("expected systemctl start prefix: %s", cmd)
	}
	if !strings.Contains(cmd, "'seaweed_master0.service'") {
		t.Errorf("service not quoted: %s", cmd)
	}
	if !strings.HasSuffix(cmd, "|| true") {
		t.Errorf("expected error tolerance: %s", cmd)
	}

	if got := buildLifecycleCommand(LifecycleStop, nil); got != "true" {
		t.Errorf("empty services should be no-op true, got %q", got)
	}
}

func TestBuildDestroyCommand(t *testing.T) {
	svcs := []string{"seaweed_master0.service"}
	cmd := buildDestroyCommand(svcs, "/opt/seaweed", "/etc/seaweed", false)

	for _, expect := range []string{
		"systemctl stop 'seaweed_master0.service'",
		"systemctl disable 'seaweed_master0.service'",
		"rm -f '/etc/systemd/system/seaweed_master0.service'",
		"systemctl daemon-reload",
	} {
		if !strings.Contains(cmd, expect) {
			t.Errorf("missing %q in %s", expect, cmd)
		}
	}
	if strings.Contains(cmd, "seaweed_*.service") {
		t.Errorf("should not use wildcard unit matching: %s", cmd)
	}
	if strings.Contains(cmd, "rm -rf /opt/seaweed") || strings.Contains(cmd, "rm -rf '/opt/seaweed'") {
		t.Errorf("should not remove data without flag: %s", cmd)
	}

	cmd2 := buildDestroyCommand(svcs, "/opt/seaweed", "/etc/seaweed", true)
	if !strings.Contains(cmd2, "rm -rf '/opt/seaweed'") {
		t.Errorf("should remove quoted data dir: %s", cmd2)
	}
	if !strings.Contains(cmd2, "rm -rf '/etc/seaweed'") {
		t.Errorf("should remove quoted config dir: %s", cmd2)
	}

	cmd3 := buildDestroyCommand(svcs, "/opt/seaweed", "/opt/seaweed", true)
	if strings.Count(cmd3, "rm -rf '/opt/seaweed'") != 1 {
		t.Errorf("should dedupe identical dirs: %s", cmd3)
	}

	// Unsafe dirs must not result in an rm -rf call.
	for _, bad := range []string{"", " ", "/"} {
		c := buildDestroyCommand(svcs, bad, bad, true)
		if strings.Contains(c, "rm -rf") {
			t.Errorf("unsafe dir %q should be filtered: %s", bad, c)
		}
	}

	// Single-quote escaping for data dirs containing apostrophes.
	c := buildDestroyCommand(svcs, "/opt/sea'weed", "", true)
	if !strings.Contains(c, `rm -rf '/opt/sea'\''weed'`) {
		t.Errorf("single quote not escaped: %s", c)
	}
}

func TestUniqueHostsSameIPDifferentPorts(t *testing.T) {
	s := &spec.Specification{
		MasterServers: []*spec.MasterServerSpec{
			{Ip: "10.0.0.1", PortSsh: 22},
			{Ip: "10.0.0.1", PortSsh: 2222},
		},
	}
	hosts := uniqueHosts(s, "")
	if len(hosts) != 2 {
		t.Fatalf("expected 2 unique (ip,port) entries, got %d: %+v", len(hosts), hosts)
	}
	ports := map[int]bool{}
	for _, h := range hosts {
		ports[h.sshPort] = true
	}
	if !ports[22] || !ports[2222] {
		t.Errorf("expected both ports 22 and 2222: %+v", hosts)
	}
}
