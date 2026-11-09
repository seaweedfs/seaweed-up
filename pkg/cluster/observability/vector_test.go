package observability

import (
	"strings"
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

func TestVectorScrapeTargets(t *testing.T) {
	s := &spec.Specification{VectorServers: []*spec.VectorServerSpec{{Ip: "10.0.0.20", MetricsPort: 9329}, {Ip: "10.0.0.21"}, nil}}
	config := RenderPromConfig(s)
	for _, value := range []string{"seaweedfs-vector", "10.0.0.20:9329", "10.0.0.20:9100", "10.0.0.21:9100"} {
		if !strings.Contains(config, value) {
			t.Fatalf("missing %q: %s", value, config)
		}
	}
	if strings.Contains(config, "10.0.0.21:0") {
		t.Fatal("disabled engine metrics were added")
	}
}

func TestVectorGatewayMetrics(t *testing.T) {
	s := &spec.Specification{S3Servers: []*spec.S3ServerSpec{{Ip: "fd00::30", MetricsPort: 9326}, nil}}
	output := RenderPromConfig(s)
	if !strings.Contains(output, "seaweedfs-s3") || !strings.Contains(output, "[fd00::30]:9326") {
		t.Fatal(output)
	}
}
