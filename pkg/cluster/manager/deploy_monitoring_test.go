package manager

import (
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

func TestAssignMetricsPorts_PerHostUnique(t *testing.T) {
	s := &spec.Specification{
		Monitoring: &spec.MonitoringSpec{Host: "10.0.0.1"},
		MasterServers: []*spec.MasterServerSpec{
			{Ip: "10.0.0.1"}, {Ip: "10.0.0.2"},
		},
		VolumeServers: []*spec.VolumeServerSpec{
			{Ip: "10.0.0.1", Port: 8080}, {Ip: "10.0.0.1", Port: 8081}, // two on the same host
			{Ip: "10.0.0.2", Port: 8080},
		},
		FilerServers: []*spec.FilerServerSpec{
			{Ip: "10.0.0.1"},
		},
	}
	assignMetricsPorts(s)

	// collect ports per host; each host's ports must be unique and >= base
	perHost := map[string][]int{}
	for _, m := range s.MasterServers {
		perHost[m.Ip] = append(perHost[m.Ip], m.MetricsPort)
	}
	for _, v := range s.VolumeServers {
		perHost[v.Ip] = append(perHost[v.Ip], v.MetricsPort)
	}
	for _, f := range s.FilerServers {
		perHost[f.Ip] = append(perHost[f.Ip], f.MetricsPort)
	}
	for ip, ports := range perHost {
		seen := map[int]bool{}
		for _, p := range ports {
			if p < metricsPortBase {
				t.Errorf("host %s got port %d below base %d", ip, p, metricsPortBase)
			}
			if seen[p] {
				t.Errorf("host %s has duplicate metrics port %d (%v)", ip, p, ports)
			}
			seen[p] = true
		}
	}
	// host 10.0.0.1 has 4 components -> 4 distinct ports
	if len(perHost["10.0.0.1"]) != 4 || len(map[int]bool{
		perHost["10.0.0.1"][0]: true, perHost["10.0.0.1"][1]: true,
		perHost["10.0.0.1"][2]: true, perHost["10.0.0.1"][3]: true,
	}) != 4 {
		t.Errorf("expected 4 distinct ports on 10.0.0.1, got %v", perHost["10.0.0.1"])
	}
}

func TestAssignMetricsPorts_PreservesExplicit(t *testing.T) {
	s := &spec.Specification{
		Monitoring: &spec.MonitoringSpec{Host: "10.0.0.1"},
		MasterServers: []*spec.MasterServerSpec{
			{Ip: "10.0.0.1", MetricsPort: 9999},
		},
		VolumeServers: []*spec.VolumeServerSpec{
			{Ip: "10.0.0.1", Port: 8080}, // must not be assigned 9999
		},
	}
	assignMetricsPorts(s)
	if s.MasterServers[0].MetricsPort != 9999 {
		t.Errorf("explicit master metrics port changed: %d", s.MasterServers[0].MetricsPort)
	}
	if s.VolumeServers[0].MetricsPort == 9999 || s.VolumeServers[0].MetricsPort == 0 {
		t.Errorf("volume should get a fresh non-conflicting port, got %d", s.VolumeServers[0].MetricsPort)
	}
}

func TestMonitoringNodeHosts_Dedup(t *testing.T) {
	s := &spec.Specification{
		MasterServers: []*spec.MasterServerSpec{{Ip: "10.0.0.1", PortSsh: 22}},
		VolumeServers: []*spec.VolumeServerSpec{{Ip: "10.0.0.1", PortSsh: 22}, {Ip: "10.0.0.2", PortSsh: 22}},
		FilerServers:  []*spec.FilerServerSpec{{Ip: "10.0.0.1", PortSsh: 22}},
	}
	hosts := monitoringNodeHosts(s)
	if len(hosts) != 2 {
		t.Fatalf("expected 2 unique hosts, got %d: %+v", len(hosts), hosts)
	}
}
