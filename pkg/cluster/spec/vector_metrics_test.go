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
