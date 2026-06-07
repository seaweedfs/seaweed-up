package spec

import (
	"bytes"
	"strings"
	"testing"
)

// metricsPort must reach the rendered weed options for master, volume and
// filer (it previously only did for s3), so Prometheus can scrape them.
func TestWriteToBuffer_MetricsPort(t *testing.T) {
	masters := []string{"10.0.0.1:9333"}

	t.Run("master emits when set, omits when zero", func(t *testing.T) {
		var set, unset bytes.Buffer
		(&MasterServerSpec{Ip: "10.0.0.1", MetricsPort: 9324}).WriteToBuffer(masters, &set)
		(&MasterServerSpec{Ip: "10.0.0.1"}).WriteToBuffer(masters, &unset)
		if !strings.Contains(set.String(), "metricsPort=9324") {
			t.Errorf("master: missing metricsPort=9324\n%s", set.String())
		}
		if strings.Contains(unset.String(), "metricsPort") {
			t.Errorf("master: should omit metricsPort when unset\n%s", unset.String())
		}
	})

	t.Run("volume emits when set", func(t *testing.T) {
		var b bytes.Buffer
		(&VolumeServerSpec{Ip: "10.0.0.1", MetricsPort: 9325}).WriteToBuffer(masters, &b)
		if !strings.Contains(b.String(), "metricsPort=9325") {
			t.Errorf("volume: missing metricsPort=9325\n%s", b.String())
		}
	})

	t.Run("filer emits when set", func(t *testing.T) {
		var b bytes.Buffer
		(&FilerServerSpec{Ip: "10.0.0.1", MetricsPort: 9327}).WriteToBuffer(masters, &b)
		if !strings.Contains(b.String(), "metricsPort=9327") {
			t.Errorf("filer: missing metricsPort=9327\n%s", b.String())
		}
	})
}

func TestAssignMetricsPorts_PerHostUnique(t *testing.T) {
	s := &Specification{
		Monitoring: &MonitoringSpec{Host: "10.0.0.1"},
		MasterServers: []*MasterServerSpec{
			{Ip: "10.0.0.1"}, {Ip: "10.0.0.2"},
		},
		VolumeServers: []*VolumeServerSpec{
			{Ip: "10.0.0.1", Port: 8080}, {Ip: "10.0.0.1", Port: 8081}, // two on the same host
			{Ip: "10.0.0.2", Port: 8080},
		},
		FilerServers: []*FilerServerSpec{
			{Ip: "10.0.0.1"},
		},
	}
	AssignMetricsPorts(s)

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
			if p < MetricsPortBase {
				t.Errorf("host %s got port %d below base %d", ip, p, MetricsPortBase)
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
	s := &Specification{
		Monitoring: &MonitoringSpec{Host: "10.0.0.1"},
		MasterServers: []*MasterServerSpec{
			{Ip: "10.0.0.1", MetricsPort: 9999},
		},
		VolumeServers: []*VolumeServerSpec{
			{Ip: "10.0.0.1", Port: 8080}, // must not be assigned 9999
		},
	}
	AssignMetricsPorts(s)
	if s.MasterServers[0].MetricsPort != 9999 {
		t.Errorf("explicit master metrics port changed: %d", s.MasterServers[0].MetricsPort)
	}
	if s.VolumeServers[0].MetricsPort == 9999 || s.VolumeServers[0].MetricsPort == 0 {
		t.Errorf("volume should get a fresh non-conflicting port, got %d", s.VolumeServers[0].MetricsPort)
	}
}

// AssignMetricsPorts must be idempotent: RenderPromConfig runs it even after
// the deploy path already has, and a second pass must not shift any port.
func TestAssignMetricsPorts_Idempotent(t *testing.T) {
	s := &Specification{
		MasterServers: []*MasterServerSpec{{Ip: "10.0.0.1"}, {Ip: "10.0.0.1"}},
		VolumeServers: []*VolumeServerSpec{{Ip: "10.0.0.1", Port: 8080}},
	}
	AssignMetricsPorts(s)
	first := []int{s.MasterServers[0].MetricsPort, s.MasterServers[1].MetricsPort, s.VolumeServers[0].MetricsPort}
	AssignMetricsPorts(s)
	second := []int{s.MasterServers[0].MetricsPort, s.MasterServers[1].MetricsPort, s.VolumeServers[0].MetricsPort}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("second AssignMetricsPorts changed ports: %v -> %v", first, second)
			break
		}
	}
}
