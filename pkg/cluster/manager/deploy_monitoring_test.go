package manager

import (
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

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
