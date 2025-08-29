package suites

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/testing"
)

// ConnectivityTestSuite tests basic connectivity to cluster components
type ConnectivityTestSuite struct {
	name        string
	description string
	tests       []testing.Test
}

// NewConnectivityTestSuite creates a new connectivity test suite
func NewConnectivityTestSuite() *ConnectivityTestSuite {
	suite := &ConnectivityTestSuite{
		name:        "connectivity",
		description: "Tests basic connectivity to all cluster components",
	}

	// Register tests
	suite.tests = []testing.Test{
		&MasterConnectivityTest{},
		&VolumeConnectivityTest{},
		&FilerConnectivityTest{},
		&S3ConnectivityTest{},
		&ClusterHealthTest{},
	}

	return suite
}

// Name returns the test suite name
func (cts *ConnectivityTestSuite) Name() string {
	return cts.name
}

// Description returns the test suite description
func (cts *ConnectivityTestSuite) Description() string {
	return cts.description
}

// Tests returns all tests in the suite
func (cts *ConnectivityTestSuite) Tests() []testing.Test {
	return cts.tests
}

// Setup prepares the test suite
func (cts *ConnectivityTestSuite) Setup(ctx context.Context, cluster *spec.Specification) error {
	// No setup required for connectivity tests
	return nil
}

// Teardown cleans up after the test suite
func (cts *ConnectivityTestSuite) Teardown(ctx context.Context, cluster *spec.Specification) error {
	// No teardown required for connectivity tests
	return nil
}

// MasterConnectivityTest tests master server connectivity
type MasterConnectivityTest struct{}

func (mct *MasterConnectivityTest) Name() string {
	return "master-connectivity"
}

func (mct *MasterConnectivityTest) Description() string {
	return "Tests HTTP connectivity to all master servers"
}

func (mct *MasterConnectivityTest) Timeout() time.Duration {
	return 30 * time.Second
}

func (mct *MasterConnectivityTest) RequiresCluster() bool {
	return true
}

func (mct *MasterConnectivityTest) Execute(ctx context.Context, cluster *spec.Specification) testing.TestResult {
	result := testing.TestResult{
		Name:      mct.Name(),
		Success:   true,
		Timestamp: time.Now(),
	}

	if len(cluster.MasterServers) == 0 {
		result.Success = false
		result.Error = "no master servers configured"
		return result
	}

	var failedMasters []string
	successfulConnections := 0

	for i, master := range cluster.MasterServers {
		url := fmt.Sprintf("http://%s:%d/cluster/status", master.Host, master.Port)
		
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			failedMasters = append(failedMasters, fmt.Sprintf("master-%d: failed to create request", i))
			continue
		}

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			failedMasters = append(failedMasters, fmt.Sprintf("master-%d (%s:%d): %v", i, master.Host, master.Port, err))
			continue
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			failedMasters = append(failedMasters, fmt.Sprintf("master-%d (%s:%d): HTTP %d", i, master.Host, master.Port, resp.StatusCode))
			continue
		}

		successfulConnections++
	}

	if len(failedMasters) > 0 {
		result.Success = false
		result.Error = fmt.Sprintf("failed to connect to %d master(s): %v", len(failedMasters), failedMasters)
	} else {
		result.Message = fmt.Sprintf("successfully connected to all %d master servers", successfulConnections)
	}

	result.Details = map[string]interface{}{
		"total_masters":       len(cluster.MasterServers),
		"successful_connections": successfulConnections,
		"failed_masters":      failedMasters,
	}

	return result
}

// VolumeConnectivityTest tests volume server connectivity
type VolumeConnectivityTest struct{}

func (vct *VolumeConnectivityTest) Name() string {
	return "volume-connectivity"
}

func (vct *VolumeConnectivityTest) Description() string {
	return "Tests HTTP connectivity to all volume servers"
}

func (vct *VolumeConnectivityTest) Timeout() time.Duration {
	return 30 * time.Second
}

func (vct *VolumeConnectivityTest) RequiresCluster() bool {
	return true
}

func (vct *VolumeConnectivityTest) Execute(ctx context.Context, cluster *spec.Specification) testing.TestResult {
	result := testing.TestResult{
		Name:      vct.Name(),
		Success:   true,
		Timestamp: time.Now(),
	}

	if len(cluster.VolumeServers) == 0 {
		result.Success = false
		result.Error = "no volume servers configured"
		return result
	}

	var failedVolumes []string
	successfulConnections := 0

	for i, volume := range cluster.VolumeServers {
		url := fmt.Sprintf("http://%s:%d/status", volume.Host, volume.Port)
		
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			failedVolumes = append(failedVolumes, fmt.Sprintf("volume-%d: failed to create request", i))
			continue
		}

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			failedVolumes = append(failedVolumes, fmt.Sprintf("volume-%d (%s:%d): %v", i, volume.Host, volume.Port, err))
			continue
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			failedVolumes = append(failedVolumes, fmt.Sprintf("volume-%d (%s:%d): HTTP %d", i, volume.Host, volume.Port, resp.StatusCode))
			continue
		}

		successfulConnections++
	}

	if len(failedVolumes) > 0 {
		result.Success = false
		result.Error = fmt.Sprintf("failed to connect to %d volume server(s): %v", len(failedVolumes), failedVolumes)
	} else {
		result.Message = fmt.Sprintf("successfully connected to all %d volume servers", successfulConnections)
	}

	result.Details = map[string]interface{}{
		"total_volumes":          len(cluster.VolumeServers),
		"successful_connections": successfulConnections,
		"failed_volumes":         failedVolumes,
	}

	return result
}

// FilerConnectivityTest tests filer server connectivity
type FilerConnectivityTest struct{}

func (fct *FilerConnectivityTest) Name() string {
	return "filer-connectivity"
}

func (fct *FilerConnectivityTest) Description() string {
	return "Tests HTTP connectivity to all filer servers"
}

func (fct *FilerConnectivityTest) Timeout() time.Duration {
	return 30 * time.Second
}

func (fct *FilerConnectivityTest) RequiresCluster() bool {
	return true
}

func (fct *FilerConnectivityTest) Execute(ctx context.Context, cluster *spec.Specification) testing.TestResult {
	result := testing.TestResult{
		Name:      fct.Name(),
		Success:   true,
		Timestamp: time.Now(),
	}

	if len(cluster.FilerServers) == 0 {
		// Filer servers are optional
		result.Message = "no filer servers configured (optional)"
		return result
	}

	var failedFilers []string
	successfulConnections := 0

	for i, filer := range cluster.FilerServers {
		url := fmt.Sprintf("http://%s:%d/", filer.Host, filer.Port)
		
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			failedFilers = append(failedFilers, fmt.Sprintf("filer-%d: failed to create request", i))
			continue
		}

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			failedFilers = append(failedFilers, fmt.Sprintf("filer-%d (%s:%d): %v", i, filer.Host, filer.Port, err))
			continue
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			failedFilers = append(failedFilers, fmt.Sprintf("filer-%d (%s:%d): HTTP %d", i, filer.Host, filer.Port, resp.StatusCode))
			continue
		}

		successfulConnections++
	}

	if len(failedFilers) > 0 {
		result.Success = false
		result.Error = fmt.Sprintf("failed to connect to %d filer server(s): %v", len(failedFilers), failedFilers)
	} else {
		result.Message = fmt.Sprintf("successfully connected to all %d filer servers", successfulConnections)
	}

	result.Details = map[string]interface{}{
		"total_filers":           len(cluster.FilerServers),
		"successful_connections": successfulConnections,
		"failed_filers":          failedFilers,
	}

	return result
}

// S3ConnectivityTest tests S3 API connectivity
type S3ConnectivityTest struct{}

func (s3ct *S3ConnectivityTest) Name() string {
	return "s3-connectivity"
}

func (s3ct *S3ConnectivityTest) Description() string {
	return "Tests S3 API connectivity on filer servers"
}

func (s3ct *S3ConnectivityTest) Timeout() time.Duration {
	return 30 * time.Second
}

func (s3ct *S3ConnectivityTest) RequiresCluster() bool {
	return true
}

func (s3ct *S3ConnectivityTest) Execute(ctx context.Context, cluster *spec.Specification) testing.TestResult {
	result := testing.TestResult{
		Name:      s3ct.Name(),
		Success:   true,
		Timestamp: time.Now(),
	}

	// Find filer servers with S3 enabled
	var s3Filers []*spec.FilerServerSpec
	for _, filer := range cluster.FilerServers {
		if filer.S3 {
			s3Filers = append(s3Filers, filer)
		}
	}

	if len(s3Filers) == 0 {
		// S3 is optional
		result.Message = "no S3-enabled filer servers configured (optional)"
		return result
	}

	var failedS3 []string
	successfulConnections := 0

	for i, filer := range s3Filers {
		url := fmt.Sprintf("http://%s:%d/", filer.Host, filer.S3Port)
		
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			failedS3 = append(failedS3, fmt.Sprintf("s3-filer-%d: failed to create request", i))
			continue
		}

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			failedS3 = append(failedS3, fmt.Sprintf("s3-filer-%d (%s:%d): %v", i, filer.Host, filer.S3Port, err))
			continue
		}
		resp.Body.Close()

		// S3 API may return different status codes, accept any response as connectivity success
		successfulConnections++
	}

	if len(failedS3) > 0 {
		result.Success = false
		result.Error = fmt.Sprintf("failed to connect to %d S3 endpoint(s): %v", len(failedS3), failedS3)
	} else {
		result.Message = fmt.Sprintf("successfully connected to all %d S3 endpoints", successfulConnections)
	}

	result.Details = map[string]interface{}{
		"total_s3_endpoints":     len(s3Filers),
		"successful_connections": successfulConnections,
		"failed_s3":              failedS3,
	}

	return result
}

// ClusterHealthTest tests overall cluster health
type ClusterHealthTest struct{}

func (cht *ClusterHealthTest) Name() string {
	return "cluster-health"
}

func (cht *ClusterHealthTest) Description() string {
	return "Tests overall cluster health and master-volume communication"
}

func (cht *ClusterHealthTest) Timeout() time.Duration {
	return 45 * time.Second
}

func (cht *ClusterHealthTest) RequiresCluster() bool {
	return true
}

func (cht *ClusterHealthTest) Execute(ctx context.Context, cluster *spec.Specification) testing.TestResult {
	result := testing.TestResult{
		Name:      cht.Name(),
		Success:   true,
		Timestamp: time.Now(),
	}

	if len(cluster.MasterServers) == 0 {
		result.Success = false
		result.Error = "no master servers configured"
		return result
	}

	// Test cluster status via master
	master := cluster.MasterServers[0]
	url := fmt.Sprintf("http://%s:%d/cluster/status", master.Host, master.Port)
	
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		result.Success = false
		result.Error = fmt.Sprintf("failed to create cluster status request: %v", err)
		return result
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		result.Success = false
		result.Error = fmt.Sprintf("failed to get cluster status: %v", err)
		return result
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		result.Success = false
		result.Error = fmt.Sprintf("cluster status returned HTTP %d", resp.StatusCode)
		return result
	}

	// Test volume assignment (basic health check)
	assignUrl := fmt.Sprintf("http://%s:%d/dir/assign", master.Host, master.Port)
	assignReq, err := http.NewRequestWithContext(ctx, "GET", assignUrl, nil)
	if err != nil {
		result.Success = false
		result.Error = fmt.Sprintf("failed to create volume assignment request: %v", err)
		return result
	}

	assignResp, err := client.Do(assignReq)
	if err != nil {
		result.Success = false
		result.Error = fmt.Sprintf("failed to get volume assignment: %v", err)
		return result
	}
	defer assignResp.Body.Close()

	if assignResp.StatusCode != http.StatusOK {
		result.Success = false
		result.Error = fmt.Sprintf("volume assignment returned HTTP %d", assignResp.StatusCode)
		return result
	}

	result.Message = "cluster health check passed"
	result.Details = map[string]interface{}{
		"master_endpoint":        fmt.Sprintf("%s:%d", master.Host, master.Port),
		"cluster_status_check":   "passed",
		"volume_assignment_check": "passed",
	}

	return result
}
