package preflight

import (
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"reflect"
	"testing"
)

func TestVectorHostPlans(t *testing.T) {
	s := &spec.Specification{VectorServers: []*spec.VectorServerSpec{
		{Ip: "vector", PortSsh: 2200, Port: 8091, MetricsPort: 9329, Arch: "arm64", OS: "linux"},
		{Ip: "legacy"}, nil,
	}}
	plans := BuildHostPlans(s, 2222)
	if len(plans) != 2 || !reflect.DeepEqual(plans[0].Ports, []int{8090}) || plans[0].SSHPort != 2222 {
		t.Fatalf("unexpected default vector plan: %+v", plans)
	}
	p := plans[1]
	if p.Host != "vector" || p.SSHPort != 2200 || p.Arch != "arm64" || p.OS != "linux" || !reflect.DeepEqual(p.Ports, []int{8091, 9329}) || !reflect.DeepEqual(p.Components, []string{"vector"}) {
		t.Fatalf("unexpected custom vector plan: %+v", p)
	}
}
