package spec

import (
	"bytes"
	"strings"
	"testing"
)

func TestVectorMetricsOptions(t *testing.T) {
	v := &VectorServerSpec{Ip: "fd00::20", Masters: "m:9333", MetricsPort: 9329, Config: map[string]interface{}{"metrics-port": 9999}}
	var buffer bytes.Buffer
	v.WriteToBuffer(&buffer)
	if !strings.Contains(buffer.String(), "metrics-port=9329") || !strings.Contains(buffer.String(), "metrics-ip=fd00::20") || strings.Contains(buffer.String(), "9999") {
		t.Fatalf("options: %s", buffer.String())
	}
	if v.MetricsAddress() != "[fd00::20]:9329" {
		t.Fatal(v.MetricsAddress())
	}
	v.MetricsPort = 0
	buffer.Reset()
	v.WriteToBuffer(&buffer)
	if strings.Contains(buffer.String(), "metrics-") {
		t.Fatal("metrics flags enabled on a legacy engine")
	}
}

func TestVectorMetricsPortsAreReserved(t *testing.T) {
	s := &Specification{
		MasterServers: []*MasterServerSpec{{Ip: "host"}},
		VectorServers: []*VectorServerSpec{{Ip: "host", MetricsPort: MetricsPortBase}},
	}
	AssignMetricsPorts(s)
	if s.MasterServers[0].MetricsPort == MetricsPortBase {
		t.Fatal("master metrics collide with vector metrics")
	}
}

func TestVectorMetricsBindAddress(t *testing.T) {
	for _, test := range []struct{ bind, want string }{{"10.0.1.20", "10.0.1.20:9329"}, {"fd00::20", "[fd00::20]:9329"}, {"0.0.0.0", "10.0.0.20:9329"}, {"::", "10.0.0.20:9329"}} {
		v := &VectorServerSpec{Ip: "10.0.0.20", MetricsPort: 9329, MetricsIP: test.bind}
		if got := v.MetricsAddress(); got != test.want {
			t.Errorf("bind %s: %s want %s", test.bind, got, test.want)
		}
	}
}
