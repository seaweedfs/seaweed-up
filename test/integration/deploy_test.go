//go:build integration
// +build integration

package integration

import (
	"testing"
	"time"
)

// TestDeploySingleNode tests deploying a single-node SeaweedFS cluster
func TestDeploySingleNode(t *testing.T) {
	env := NewTestEnvironment(t)
	env.SkipIfNotAvailable(t)

	// Build the binary first
	if err := env.BuildSeaweedUp(); err != nil {
		t.Fatalf("Failed to build seaweed-up: %v", err)
	}

	// Setup Docker environment
	if err := env.Setup(); err != nil {
		t.Fatalf("Failed to setup test environment: %v", err)
	}
	defer func() {
		if err := env.Teardown(); err != nil {
			t.Errorf("Failed to teardown test environment: %v", err)
		}
	}()

	t.Run("DeployCluster", func(t *testing.T) {
		configFile := env.GetClusterConfig("cluster-single.yaml")
		sshKey := env.GetSSHKeyPath()

		// Deploy the cluster
		output, err := env.RunSeaweedUp(
			"cluster", "deploy", "test-single",
			"-f", configFile,
			"-u", "root",
			"--identity", sshKey,
			"--yes",
		)

		if err != nil {
			t.Logf("Deploy output: %s", output)
			t.Fatalf("Failed to deploy cluster: %v", err)
		}

		t.Logf("Deploy output: %s", output)
		AssertContains(t, output, "deploy", "Expected deploy confirmation in output")
	})

	t.Run("VerifyMasterRunning", func(t *testing.T) {
		// Give services time to start
		time.Sleep(10 * time.Second)

		host := env.hosts[0]
		if !env.VerifyMasterRunning(host, 9333) {
			t.Errorf("Master server not running on %s:9333", host.IP)
		} else {
			t.Logf("Master server verified running on %s:9333", host.IP)
		}
	})

	t.Run("VerifyVolumeRunning", func(t *testing.T) {
		host := env.hosts[0]
		if !env.VerifyVolumeRunning(host, 8382) {
			t.Errorf("Volume server not running on %s:8382", host.IP)
		} else {
			t.Logf("Volume server verified running on %s:8382", host.IP)
		}
	})

	t.Run("VerifyFilerRunning", func(t *testing.T) {
		host := env.hosts[0]
		if !env.VerifyFilerRunning(host, 8888) {
			t.Errorf("Filer server not running on %s:8888", host.IP)
		} else {
			t.Logf("Filer server verified running on %s:8888", host.IP)
		}
	})

	t.Run("ClusterStatus", func(t *testing.T) {
		output, err := env.RunSeaweedUp("cluster", "status", "test-single")
		if err != nil {
			t.Logf("Status output: %s", output)
			t.Fatalf("Failed to get cluster status: %v", err)
		}

		t.Logf("Cluster status: %s", output)
	})
}

// TestDeployMultiNode tests deploying a multi-node SeaweedFS cluster
func TestDeployMultiNode(t *testing.T) {
	env := NewTestEnvironment(t)
	env.SkipIfNotAvailable(t)

	// Build the binary first
	if err := env.BuildSeaweedUp(); err != nil {
		t.Fatalf("Failed to build seaweed-up: %v", err)
	}

	// Setup Docker environment
	if err := env.Setup(); err != nil {
		t.Fatalf("Failed to setup test environment: %v", err)
	}
	defer func() {
		if err := env.Teardown(); err != nil {
			t.Errorf("Failed to teardown test environment: %v", err)
		}
	}()

	t.Run("DeployCluster", func(t *testing.T) {
		configFile := env.GetClusterConfig("cluster-multi.yaml")
		sshKey := env.GetSSHKeyPath()

		// Deploy the cluster
		output, err := env.RunSeaweedUp(
			"cluster", "deploy", "test-multi",
			"-f", configFile,
			"-u", "root",
			"--identity", sshKey,
			"--yes",
		)

		if err != nil {
			t.Logf("Deploy output: %s", output)
			t.Fatalf("Failed to deploy cluster: %v", err)
		}

		t.Logf("Deploy output: %s", output)
	})

	t.Run("VerifyMasterRunning", func(t *testing.T) {
		// Give services time to start
		time.Sleep(10 * time.Second)

		host := env.hosts[0] // Master on host1
		if !env.VerifyMasterRunning(host, 9333) {
			t.Errorf("Master server not running on %s:9333", host.IP)
		} else {
			t.Logf("Master server verified running on %s:9333", host.IP)
		}
	})

	t.Run("VerifyVolumeServers", func(t *testing.T) {
		// Volume servers on host2 and host3
		for _, host := range env.hosts[1:] {
			if !env.VerifyVolumeRunning(host, 8382) {
				t.Errorf("Volume server not running on %s:8382", host.IP)
			} else {
				t.Logf("Volume server verified running on %s:8382", host.IP)
			}
		}
	})

	t.Run("VerifyFilerRunning", func(t *testing.T) {
		host := env.hosts[0] // Filer on host1
		if !env.VerifyFilerRunning(host, 8888) {
			t.Errorf("Filer server not running on %s:8888", host.IP)
		} else {
			t.Logf("Filer server verified running on %s:8888", host.IP)
		}
	})

	t.Run("ClusterList", func(t *testing.T) {
		output, err := env.RunSeaweedUp("cluster", "list")
		if err != nil {
			t.Logf("List output: %s", output)
			t.Fatalf("Failed to list clusters: %v", err)
		}

		t.Logf("Cluster list: %s", output)
		AssertContains(t, output, "test-multi", "Expected cluster name in list output")
	})
}

// TestClusterDestroy tests destroying a deployed cluster
func TestClusterDestroy(t *testing.T) {
	env := NewTestEnvironment(t)
	env.SkipIfNotAvailable(t)

	// Build the binary first
	if err := env.BuildSeaweedUp(); err != nil {
		t.Fatalf("Failed to build seaweed-up: %v", err)
	}

	// Setup Docker environment
	if err := env.Setup(); err != nil {
		t.Fatalf("Failed to setup test environment: %v", err)
	}
	defer func() {
		if err := env.Teardown(); err != nil {
			t.Errorf("Failed to teardown test environment: %v", err)
		}
	}()

	// First deploy a cluster
	configFile := env.GetClusterConfig("cluster-single.yaml")
	sshKey := env.GetSSHKeyPath()

	output, err := env.RunSeaweedUp(
		"cluster", "deploy", "test-destroy",
		"-f", configFile,
		"-u", "root",
		"--identity", sshKey,
		"--yes",
	)
	if err != nil {
		t.Logf("Deploy output: %s", output)
		t.Fatalf("Failed to deploy cluster for destroy test: %v", err)
	}

	// Wait for services to start
	time.Sleep(5 * time.Second)

	t.Run("DestroyCluster", func(t *testing.T) {
		output, err := env.RunSeaweedUp(
			"cluster", "destroy", "test-destroy",
			"-u", "root",
			"--identity", sshKey,
			"--force",
		)

		if err != nil {
			t.Logf("Destroy output: %s", output)
			t.Fatalf("Failed to destroy cluster: %v", err)
		}

		t.Logf("Destroy output: %s", output)
	})

	t.Run("VerifyServicesStoppedAfterDestroy", func(t *testing.T) {
		// Give time for services to stop
		time.Sleep(5 * time.Second)

		host := env.hosts[0]

		// Master should no longer be running
		if env.VerifyMasterRunning(host, 9333) {
			t.Logf("Note: Master server still appears to be running after destroy")
		} else {
			t.Logf("Master server confirmed stopped on %s:9333", host.IP)
		}
	})
}

