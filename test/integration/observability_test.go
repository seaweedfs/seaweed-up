//go:build integration
// +build integration

package integration

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestNodeExporterInstall deploys a small cluster, installs node_exporter on
// every host, and verifies that each host exposes the node_exporter metrics
// endpoint on :9100.
func TestNodeExporterInstall(t *testing.T) {
	env := NewTestEnvironment(t)
	env.SkipIfNotAvailable(t)

	if err := env.BuildSeaweedUp(); err != nil {
		t.Fatalf("build seaweed-up: %v", err)
	}

	if err := env.Setup(); err != nil {
		t.Fatalf("setup: %v", err)
	}
	defer func() {
		if err := env.Teardown(); err != nil {
			t.Errorf("teardown: %v", err)
		}
	}()

	deployOut, err := env.RunSeaweedUp("cluster", "deploy",
		"-f", "test/integration/testdata/cluster-single.yaml",
		"--user", "root",
		"--identity", "test/integration/testdata/ssh_key",
		"--yes",
	)
	if err != nil {
		t.Fatalf("cluster deploy failed: %v\noutput: %s", err, deployOut)
	}

	installOut, err := env.RunSeaweedUp("cluster", "node-exporter", "install",
		"test-cluster",
		"-f", "test/integration/testdata/cluster-single.yaml",
		"--user", "root",
		"--identity", "test/integration/testdata/ssh_key",
	)
	if err != nil {
		t.Fatalf("node-exporter install failed: %v\noutput: %s", err, installOut)
	}

	// Poll each host's metrics endpoint until 9100 responds or we time out.
	client := &http.Client{Timeout: 5 * time.Second}
	for _, h := range env.hosts[:1] { // single-node cluster
		url := "http://" + h.IP + ":9100/metrics"
		deadline := time.Now().Add(60 * time.Second)
		var lastErr error
		var body string
		for time.Now().Before(deadline) {
			resp, err := client.Get(url)
			if err != nil {
				lastErr = err
				time.Sleep(2 * time.Second)
				continue
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				body = string(b)
				lastErr = nil
				break
			}
			lastErr = nil
			time.Sleep(2 * time.Second)
		}
		if lastErr != nil {
			t.Fatalf("GET %s failed: %v", url, lastErr)
		}
		if !strings.Contains(body, "node_exporter_build_info") {
			t.Errorf("metrics from %s missing node_exporter_build_info (len=%d)", url, len(body))
		}
	}

	// Also smoke-test the prometheus-config command.
	promOut, err := env.RunSeaweedUp("cluster", "prometheus-config",
		"test-cluster",
		"-f", "test/integration/testdata/cluster-single.yaml",
	)
	if err != nil {
		t.Fatalf("prometheus-config failed: %v\noutput: %s", err, promOut)
	}
	if !strings.Contains(promOut, "scrape_configs") {
		t.Errorf("prometheus-config output missing scrape_configs: %s", promOut)
	}
	if !strings.Contains(promOut, ":9100") {
		t.Errorf("prometheus-config output missing node_exporter targets: %s", promOut)
	}
}
