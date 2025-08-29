package status

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/executor"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/utils"
)

// StatusCollector collects status information from SeaweedFS clusters
type StatusCollector struct {
	executor executor.Executor
	client   *http.Client
}

// NewStatusCollector creates a new status collector
func NewStatusCollector(executor executor.Executor) *StatusCollector {
	return &StatusCollector{
		executor: executor,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// CollectClusterStatus collects comprehensive cluster status
func (c *StatusCollector) CollectClusterStatus(ctx context.Context, cluster *spec.Specification, opts StatusCollectionOptions) (*ClusterStatus, error) {
	if cluster == nil {
		return nil, fmt.Errorf("cluster specification is nil")
	}

	var components []ComponentStatus
	var wg sync.WaitGroup
	resultsChan := make(chan ComponentStatus, 100)
	errorsChan := make(chan error, 100)

	// Collect master server status
	for _, master := range cluster.MasterServers {
		wg.Add(1)
		go func(m *spec.MasterServerSpec) {
			defer wg.Done()
			status, err := c.collectMasterStatus(ctx, m, opts)
			if err != nil {
				errorsChan <- err
				return
			}
			resultsChan <- *status
		}(master)
	}

	// Collect volume server status
	for _, volume := range cluster.VolumeServers {
		wg.Add(1)
		go func(v *spec.VolumeServerSpec) {
			defer wg.Done()
			status, err := c.collectVolumeStatus(ctx, v, opts)
			if err != nil {
				errorsChan <- err
				return
			}
			resultsChan <- *status
		}(volume)
	}

	// Collect filer status
	for _, filer := range cluster.FilerServers {
		wg.Add(1)
		go func(f *spec.FilerServerSpec) {
			defer wg.Done()
			status, err := c.collectFilerStatus(ctx, f, opts)
			if err != nil {
				errorsChan <- err
				return
			}
			resultsChan <- *status
		}(filer)
	}

	// Wait for all collectors to complete
	go func() {
		wg.Wait()
		close(resultsChan)
		close(errorsChan)
	}()

	// Collect results
	var errors []error
	for {
		select {
		case result, ok := <-resultsChan:
			if !ok {
				resultsChan = nil
			} else {
				components = append(components, result)
			}
		case err, ok := <-errorsChan:
			if !ok {
				errorsChan = nil
			} else {
				errors = append(errors, err)
			}
		}

		if resultsChan == nil && errorsChan == nil {
			break
		}
	}

	// Determine overall cluster state
	clusterState := c.determineClusterState(components)

	clusterStatus := &ClusterStatus{
		Name:       cluster.Name,
		State:      clusterState,
		Components: components,
		UpdatedAt:  time.Now(),
	}

	// Add cluster version if available
	if len(components) > 0 && components[0].Version != "" {
		clusterStatus.Version = components[0].Version
	}

	return clusterStatus, nil
}

// collectMasterStatus collects status from a master server
func (c *StatusCollector) collectMasterStatus(ctx context.Context, master *spec.MasterServerSpec, opts StatusCollectionOptions) (*ComponentStatus, error) {
	baseStatus := ComponentStatus{
		Name:     fmt.Sprintf("master-%s", master.Host),
		Type:     ComponentMaster,
		Host:     master.Host,
		Port:     master.Port,
		LastSeen: time.Now(),
	}

	// Check if process is running
	pid, err := c.findProcessPID(ctx, master.Host, "weed", "master")
	if err != nil {
		baseStatus.Status = "stopped"
		baseStatus.HealthCheck = HealthStatus{
			Status:    "unhealthy",
			Error:     err.Error(),
			LastCheck: time.Now(),
		}
		return &baseStatus, nil
	}

	baseStatus.PID = pid
	baseStatus.Status = "running"

	// Get process info
	if opts.IncludeMetrics {
		if err := c.collectProcessMetrics(ctx, master.Host, pid, &baseStatus); err != nil {
			// Non-fatal error, continue
		}
	}

	// Health check via HTTP API
	if opts.HealthCheck {
		healthStatus := c.checkMasterHealth(ctx, master.Host, master.Port, opts.Timeout)
		baseStatus.HealthCheck = healthStatus

		if healthStatus.Status == "healthy" {
			baseStatus.Status = "healthy"
		}
	}

	return &baseStatus, nil
}

// collectVolumeStatus collects status from a volume server
func (c *StatusCollector) collectVolumeStatus(ctx context.Context, volume *spec.VolumeServerSpec, opts StatusCollectionOptions) (*ComponentStatus, error) {
	baseStatus := ComponentStatus{
		Name:     fmt.Sprintf("volume-%s", volume.Host),
		Type:     ComponentVolume,
		Host:     volume.Host,
		Port:     volume.Port,
		LastSeen: time.Now(),
	}

	// Check if process is running
	pid, err := c.findProcessPID(ctx, volume.Host, "weed", "volume")
	if err != nil {
		baseStatus.Status = "stopped"
		baseStatus.HealthCheck = HealthStatus{
			Status:    "unhealthy",
			Error:     err.Error(),
			LastCheck: time.Now(),
		}
		return &baseStatus, nil
	}

	baseStatus.PID = pid
	baseStatus.Status = "running"

	// Get process info
	if opts.IncludeMetrics {
		if err := c.collectProcessMetrics(ctx, volume.Host, pid, &baseStatus); err != nil {
			// Non-fatal error, continue
		}
	}

	// Health check via HTTP API
	if opts.HealthCheck {
		healthStatus := c.checkVolumeHealth(ctx, volume.Host, volume.Port, opts.Timeout)
		baseStatus.HealthCheck = healthStatus

		if healthStatus.Status == "healthy" {
			baseStatus.Status = "healthy"
		}
	}

	return &baseStatus, nil
}

// collectFilerStatus collects status from a filer server
func (c *StatusCollector) collectFilerStatus(ctx context.Context, filer *spec.FilerServerSpec, opts StatusCollectionOptions) (*ComponentStatus, error) {
	baseStatus := ComponentStatus{
		Name:     fmt.Sprintf("filer-%s", filer.Host),
		Type:     ComponentFiler,
		Host:     filer.Host,
		Port:     filer.Port,
		LastSeen: time.Now(),
	}

	// Check if process is running
	pid, err := c.findProcessPID(ctx, filer.Host, "weed", "filer")
	if err != nil {
		baseStatus.Status = "stopped"
		baseStatus.HealthCheck = HealthStatus{
			Status:    "unhealthy",
			Error:     err.Error(),
			LastCheck: time.Now(),
		}
		return &baseStatus, nil
	}

	baseStatus.PID = pid
	baseStatus.Status = "running"

	// Get process info
	if opts.IncludeMetrics {
		if err := c.collectProcessMetrics(ctx, filer.Host, pid, &baseStatus); err != nil {
			// Non-fatal error, continue
		}
	}

	// Health check via HTTP API
	if opts.HealthCheck {
		healthStatus := c.checkFilerHealth(ctx, filer.Host, filer.Port, opts.Timeout)
		baseStatus.HealthCheck = healthStatus

		if healthStatus.Status == "healthy" {
			baseStatus.Status = "healthy"
		}
	}

	return &baseStatus, nil
}

// findProcessPID finds the PID of a SeaweedFS process
func (c *StatusCollector) findProcessPID(ctx context.Context, host, binary, component string) (int, error) {
	if c.executor == nil {
		// Fallback for local processes
		return c.findLocalProcessPID(binary, component)
	}

	cmd := fmt.Sprintf("pgrep -f '%s.*%s' | head -1", binary, component)
	result, err := c.executor.Execute(ctx, host, cmd)
	if err != nil {
		return 0, fmt.Errorf("process not found")
	}

	pidStr := strings.TrimSpace(result)
	if pidStr == "" {
		return 0, fmt.Errorf("process not found")
	}

	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0, fmt.Errorf("invalid PID: %s", pidStr)
	}

	return pid, nil
}

// findLocalProcessPID finds local process PID (fallback)
func (c *StatusCollector) findLocalProcessPID(binary, component string) (int, error) {
	// Simple approach - in a full implementation, you would use
	// system-specific process enumeration
	cmd := fmt.Sprintf("pgrep -f '%s.*%s'", binary, component)
	if output, err := utils.ExecuteCommand(cmd); err == nil {
		lines := strings.Split(strings.TrimSpace(output), "\n")
		if len(lines) > 0 && lines[0] != "" {
			return strconv.Atoi(lines[0])
		}
	}
	return 0, fmt.Errorf("process not found")
}

// collectProcessMetrics collects metrics from a running process
func (c *StatusCollector) collectProcessMetrics(ctx context.Context, host string, pid int, status *ComponentStatus) error {
	if c.executor == nil {
		return c.collectLocalProcessMetrics(pid, status)
	}

	// Get process stats via remote execution
	cmd := fmt.Sprintf("cat /proc/%d/stat /proc/%d/status 2>/dev/null", pid, pid)
	result, err := c.executor.Execute(ctx, host, cmd)
	if err != nil {
		return err
	}

	return c.parseProcessStats(result, status)
}

// collectLocalProcessMetrics collects metrics from local process
func (c *StatusCollector) collectLocalProcessMetrics(pid int, status *ComponentStatus) error {
	// Read process stats
	statPath := fmt.Sprintf("/proc/%d/stat", pid)
	if data, err := os.ReadFile(statPath); err == nil {
		if err := c.parseProcessStats(string(data), status); err != nil {
			return err
		}
	}

	// Get memory usage
	statusPath := fmt.Sprintf("/proc/%d/status", pid)
	if data, err := os.ReadFile(statusPath); err == nil {
		c.parseProcessStatus(string(data), status)
	}

	return nil
}

// parseProcessStats parses /proc/pid/stat data
func (c *StatusCollector) parseProcessStats(statData string, status *ComponentStatus) error {
	fields := strings.Fields(statData)
	if len(fields) < 22 {
		return fmt.Errorf("invalid stat data")
	}

	// Field 21 is start time (in clock ticks since boot)
	if _, err := strconv.ParseInt(fields[21], 10, 64); err == nil {
		// Convert to time - this is simplified, real implementation would
		// account for system boot time and clock ticks
		status.StartTime = time.Now().Add(-time.Hour) // Placeholder
		status.Uptime = time.Since(status.StartTime)
	}

	return nil
}

// parseProcessStatus parses /proc/pid/status data
func (c *StatusCollector) parseProcessStatus(statusData string, status *ComponentStatus) {
	lines := strings.Split(statusData, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "VmRSS:") {
			// Parse memory usage
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if mem, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
					status.MemoryUsage = mem * 1024 // Convert KB to bytes
				}
			}
		}
	}
}

// checkMasterHealth performs health check on master server
func (c *StatusCollector) checkMasterHealth(ctx context.Context, host string, port int, timeout time.Duration) HealthStatus {
	start := time.Now()
	url := fmt.Sprintf("http://%s:%d/dir/status", host, port)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return HealthStatus{
			Status:    "unhealthy",
			Error:     err.Error(),
			LastCheck: time.Now(),
		}
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return HealthStatus{
			Status:    "unhealthy",
			Error:     err.Error(),
			Latency:   time.Since(start),
			LastCheck: time.Now(),
		}
	}
	defer resp.Body.Close()

	latency := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		return HealthStatus{
			Status:    "unhealthy",
			Error:     fmt.Sprintf("HTTP %d", resp.StatusCode),
			Latency:   latency,
			LastCheck: time.Now(),
		}
	}

	// Try to parse the response
	body, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	if json.Unmarshal(body, &result) == nil {
		return HealthStatus{
			Status:    "healthy",
			Latency:   latency,
			LastCheck: time.Now(),
			Metadata:  result,
		}
	}

	return HealthStatus{
		Status:    "healthy",
		Latency:   latency,
		LastCheck: time.Now(),
	}
}

// checkVolumeHealth performs health check on volume server
func (c *StatusCollector) checkVolumeHealth(ctx context.Context, host string, port int, timeout time.Duration) HealthStatus {
	start := time.Now()
	url := fmt.Sprintf("http://%s:%d/status", host, port)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return HealthStatus{
			Status:    "unhealthy",
			Error:     err.Error(),
			LastCheck: time.Now(),
		}
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return HealthStatus{
			Status:    "unhealthy",
			Error:     err.Error(),
			Latency:   time.Since(start),
			LastCheck: time.Now(),
		}
	}
	defer resp.Body.Close()

	latency := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		return HealthStatus{
			Status:    "unhealthy",
			Error:     fmt.Sprintf("HTTP %d", resp.StatusCode),
			Latency:   latency,
			LastCheck: time.Now(),
		}
	}

	return HealthStatus{
		Status:    "healthy",
		Latency:   latency,
		LastCheck: time.Now(),
	}
}

// checkFilerHealth performs health check on filer server
func (c *StatusCollector) checkFilerHealth(ctx context.Context, host string, port int, timeout time.Duration) HealthStatus {
	start := time.Now()
	url := fmt.Sprintf("http://%s:%d/", host, port)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return HealthStatus{
			Status:    "unhealthy",
			Error:     err.Error(),
			LastCheck: time.Now(),
		}
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return HealthStatus{
			Status:    "unhealthy",
			Error:     err.Error(),
			Latency:   time.Since(start),
			LastCheck: time.Now(),
		}
	}
	defer resp.Body.Close()

	latency := time.Since(start)

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusForbidden {
		// Filer might return 403 for root access, which is still healthy
		return HealthStatus{
			Status:    "healthy",
			Latency:   latency,
			LastCheck: time.Now(),
		}
	}

	return HealthStatus{
		Status:    "unhealthy",
		Error:     fmt.Sprintf("HTTP %d", resp.StatusCode),
		Latency:   latency,
		LastCheck: time.Now(),
	}
}

// determineClusterState determines overall cluster state from component states
func (c *StatusCollector) determineClusterState(components []ComponentStatus) ClusterState {
	if len(components) == 0 {
		return StateUnknown
	}

	healthy := 0
	running := 0
	total := len(components)

	for _, comp := range components {
		if comp.Status == "running" || comp.Status == "healthy" {
			running++
		}
		if comp.HealthCheck.Status == "healthy" {
			healthy++
		}
	}

	// All components healthy
	if healthy == total {
		return StateRunning
	}

	// No components running
	if running == 0 {
		return StateStopped
	}

	// Some components running but not all healthy
	if running > 0 {
		return StateDegraded
	}

	return StateError
}

// GenerateStatusSummary generates a summary of cluster status
func (c *StatusCollector) GenerateStatusSummary(status *ClusterStatus) StatusSummary {
	summary := StatusSummary{
		TotalComponents:    len(status.Components),
		ComponentsByType:   make(map[ComponentType]int),
		ComponentsByStatus: make(map[string]int),
		ClusterVersion:     status.Version,
		LastUpdated:        status.UpdatedAt,
	}

	var totalLatency time.Duration
	var latencyCount int

	for _, comp := range status.Components {
		// Count by type
		summary.ComponentsByType[comp.Type]++

		// Count by status
		summary.ComponentsByStatus[comp.Status]++

		if comp.Status == "running" || comp.Status == "healthy" {
			summary.RunningComponents++
		}

		if comp.HealthCheck.Status == "healthy" {
			summary.HealthyComponents++

			if comp.HealthCheck.Latency > 0 {
				totalLatency += comp.HealthCheck.Latency
				latencyCount++
			}
		}

		// Aggregate metrics
		summary.TotalMemoryUsage += comp.MemoryUsage
		summary.TotalCPUUsage += comp.CPUUsage
		summary.TotalDiskUsage += comp.DiskUsage
	}

	if latencyCount > 0 {
		summary.AverageResponseTime = totalLatency / time.Duration(latencyCount)
	}

	return summary
}

// killProcess attempts to stop a process (for cluster shutdown operations)
func (c *StatusCollector) killProcess(ctx context.Context, host string, pid int, signal syscall.Signal) error {
	if c.executor == nil {
		// Local process
		if process, err := os.FindProcess(pid); err == nil {
			return process.Signal(signal)
		}
		return fmt.Errorf("process not found")
	}

	// Remote process
	cmd := fmt.Sprintf("kill -%d %d", signal, pid)
	_, err := c.executor.Execute(ctx, host, cmd)
	return err
}
