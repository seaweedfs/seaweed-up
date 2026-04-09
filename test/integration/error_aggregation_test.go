//go:build integration

package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seaweedfs/seaweed-up/test/integration/harness"
)

// TestDeployErrorAggregation verifies the fix for the manager_deploy.go race:
//
//  1. per-host errors are appended under a mutex (no data race), and
//  2. errgroup.Wait()'s single-error return is NOT the only thing surfaced -
//     the final error must mention every failing host, not just the first one.
//
// We stand up three ubuntu+systemd hosts, stop sshd on the two that are
// supposed to run volume + filer servers, and run `seaweed-up cluster deploy`
// against that spec. The master (on host1) deploys sequentially and succeeds;
// the volume + filer deploys fan out concurrently via errgroup and both fail
// with ssh connection errors. The combined error returned by DeployCluster
// must mention BOTH failing host IPs.
func TestDeployErrorAggregation(t *testing.T) {
	h := harness.New(t, 3)
	h.BuildBinary(t)

	hosts := h.Hosts()
	if len(hosts) < 3 {
		t.Fatalf("expected at least 3 hosts, got %d", len(hosts))
	}
	host1, host2, host3 := hosts[0], hosts[1], hosts[2]

	// Write a spec with master on host1, volume on host2, filer on host3.
	spec := fmt.Sprintf(`global:
  dir.conf: "/etc/seaweed"
  dir.data: "/opt/seaweed"
  volumeSizeLimitMB: 100
  replication: "000"

master_servers:
  - ip: %s
    port: 9333

volume_servers:
  - ip: %s
    port: 8080
    folders:
      - folder: /opt/seaweed/volume0
        disk: ""

filer_servers:
  - ip: %s
    port: 8888
`, host1.IP, host2.IP, host3.IP)

	specPath := filepath.Join(h.TempDir(), "cluster-error-agg.yaml")
	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	// Break sshd on the two hosts that run the concurrent component deploys.
	// "stop sshd on host2 before deploy" from the task description; host3 is
	// stopped as well so that BOTH concurrent errgroup goroutines fail and
	// the aggregation behavior is actually exercised.
	h.StopSSH(t, host2.Name)
	h.StopSSH(t, host3.Name)

	out, err := h.Deploy(t, specPath)
	if err == nil {
		t.Fatalf("expected deploy to fail, got success. output:\n%s", out)
	}
	combined := out + "\n" + err.Error()

	if !strings.Contains(combined, host2.IP) {
		t.Errorf("expected error output to mention host2 ip %s; got:\n%s", host2.IP, combined)
	}
	if !strings.Contains(combined, host3.IP) {
		t.Errorf("expected error output to mention host3 ip %s; got:\n%s", host3.IP, combined)
	}
}
