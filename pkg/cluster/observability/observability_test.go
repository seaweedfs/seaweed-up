package observability

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

func TestDashboardJSONIsValidGrafanaDashboard(t *testing.T) {
	raw := DashboardJSON()
	if len(raw) == 0 {
		t.Fatal("DashboardJSON() returned empty bytes")
	}

	var dash map[string]interface{}
	if err := json.Unmarshal(raw, &dash); err != nil {
		t.Fatalf("dashboard JSON does not parse: %v", err)
	}
	if _, ok := dash["title"]; !ok {
		t.Error("dashboard missing required field: title")
	}
	if _, ok := dash["schemaVersion"]; !ok {
		t.Error("dashboard missing required field: schemaVersion")
	}
	panels, ok := dash["panels"].([]interface{})
	if !ok {
		t.Fatalf("dashboard missing required field: panels (type=%T)", dash["panels"])
	}
	if len(panels) == 0 {
		t.Error("dashboard has zero panels")
	}
}

func TestRenderPromConfigGolden(t *testing.T) {
	s := &spec.Specification{
		Name: "golden",
		MasterServers: []*spec.MasterServerSpec{
			{Ip: "10.0.0.1", MetricsPort: 9324},
			{Ip: "10.0.0.2"},
		},
		VolumeServers: []*spec.VolumeServerSpec{
			{Ip: "10.0.0.3"},
		},
		FilerServers: []*spec.FilerServerSpec{
			{Ip: "10.0.0.4", MetricsPort: 9330},
		},
	}

	got := RenderPromConfig(s)

	want := `scrape_configs:
  - job_name: "golden-master"
    metrics_path: /metrics
    static_configs:
      - targets:
          - "10.0.0.1:9324"
          - "10.0.0.2:9324"
        labels:
          cluster: "golden"
  - job_name: "golden-volume"
    metrics_path: /metrics
    static_configs:
      - targets:
          - "10.0.0.3:9325"
        labels:
          cluster: "golden"
  - job_name: "golden-filer"
    metrics_path: /metrics
    static_configs:
      - targets:
          - "10.0.0.4:9330"
        labels:
          cluster: "golden"
`
	if !strings.HasPrefix(got, want) {
		t.Errorf("RenderPromConfig golden mismatch:\nwant prefix:\n%s\ngot:\n%s", want, got)
	}

	// The node_exporter job is rendered last with a map-ordered target list,
	// so verify its presence and unique hosts rather than exact ordering.
	if !strings.Contains(got, `job_name: "golden-node-exporter"`) {
		t.Error("missing node-exporter job")
	}
	for _, ip := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4"} {
		if !strings.Contains(got, ip+":9100") {
			t.Errorf("node_exporter target %s:9100 not found", ip)
		}
	}
}

func TestRenderPromConfigNilReturnsEmpty(t *testing.T) {
	if RenderPromConfig(nil) != "" {
		t.Error("expected empty string for nil spec")
	}
}

func TestNodeExporterUnitContainsPort(t *testing.T) {
	unit := nodeExporterUnit()
	if !strings.Contains(unit, ":9100") {
		t.Error("systemd unit does not expose :9100")
	}
	if !strings.Contains(unit, "ExecStart=/usr/local/bin/node_exporter") {
		t.Error("systemd unit missing ExecStart")
	}
}
