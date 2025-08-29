package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/executor"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

// MetricsCollector collects and stores metrics from SeaweedFS components
type MetricsCollector struct {
	executor    executor.Executor
	client      *http.Client
	storage     MetricsStorage
	interval    time.Duration
	running     bool
	stopChan    chan struct{}
}

// MetricPoint represents a single metric measurement
type MetricPoint struct {
	Timestamp   time.Time              `json:"timestamp"`
	Component   string                 `json:"component"`
	Host        string                 `json:"host"`
	Port        int                    `json:"port"`
	MetricName  string                 `json:"metric_name"`
	Value       float64                `json:"value"`
	Tags        map[string]string      `json:"tags,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// MetricsStorage interface for storing metrics
type MetricsStorage interface {
	Store(ctx context.Context, metrics []MetricPoint) error
	Query(ctx context.Context, query MetricsQuery) ([]MetricPoint, error)
	GetLatest(ctx context.Context, component, host string, metricName string) (*MetricPoint, error)
	Close() error
}

// MetricsQuery represents a metrics query
type MetricsQuery struct {
	Component   string            `json:"component,omitempty"`
	Host        string            `json:"host,omitempty"`
	MetricName  string            `json:"metric_name,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	StartTime   time.Time         `json:"start_time"`
	EndTime     time.Time         `json:"end_time"`
	Limit       int               `json:"limit,omitempty"`
}

// ComponentMetrics represents metrics for a component
type ComponentMetrics struct {
	Component string                 `json:"component"`
	Host      string                 `json:"host"`
	Port      int                    `json:"port"`
	Timestamp time.Time              `json:"timestamp"`
	Metrics   map[string]interface{} `json:"metrics"`
}

// NewMetricsCollector creates a new metrics collector
func NewMetricsCollector(executor executor.Executor, storage MetricsStorage, interval time.Duration) *MetricsCollector {
	return &MetricsCollector{
		executor: executor,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		storage:  storage,
		interval: interval,
		stopChan: make(chan struct{}),
	}
}

// Start begins metrics collection
func (mc *MetricsCollector) Start(ctx context.Context) error {
	if mc.running {
		return fmt.Errorf("metrics collector is already running")
	}
	
	mc.running = true
	ticker := time.NewTicker(mc.interval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			if err := mc.collectMetrics(ctx); err != nil {
				fmt.Printf("Error collecting metrics: %v\n", err)
			}
		case <-mc.stopChan:
			mc.running = false
			return nil
		case <-ctx.Done():
			mc.running = false
			return ctx.Err()
		}
	}
}

// Stop stops metrics collection
func (mc *MetricsCollector) Stop() {
	if mc.running {
		close(mc.stopChan)
	}
}

// CollectFromCluster collects metrics from all components in a cluster
func (mc *MetricsCollector) CollectFromCluster(ctx context.Context, cluster *spec.Specification) ([]ComponentMetrics, error) {
	var allMetrics []ComponentMetrics
	
	// Collect from master servers
	for _, master := range cluster.MasterServers {
		metrics, err := mc.collectFromMaster(ctx, master)
		if err != nil {
			fmt.Printf("Error collecting from master %s: %v\n", master.Host, err)
			continue
		}
		allMetrics = append(allMetrics, *metrics)
	}
	
	// Collect from volume servers
	for _, volume := range cluster.VolumeServers {
		metrics, err := mc.collectFromVolume(ctx, volume)
		if err != nil {
			fmt.Printf("Error collecting from volume %s: %v\n", volume.Host, err)
			continue
		}
		allMetrics = append(allMetrics, *metrics)
	}
	
	// Collect from filer servers
	for _, filer := range cluster.FilerServers {
		metrics, err := mc.collectFromFiler(ctx, filer)
		if err != nil {
			fmt.Printf("Error collecting from filer %s: %v\n", filer.Host, err)
			continue
		}
		allMetrics = append(allMetrics, *metrics)
	}
	
	return allMetrics, nil
}

// collectMetrics is the internal metrics collection method
func (mc *MetricsCollector) collectMetrics(ctx context.Context) error {
	// For now, this is a placeholder that would be called periodically
	// In a real implementation, you would maintain a list of clusters to monitor
	return nil
}

// collectFromMaster collects metrics from a master server
func (mc *MetricsCollector) collectFromMaster(ctx context.Context, master *spec.MasterServerSpec) (*ComponentMetrics, error) {
	url := fmt.Sprintf("http://%s:%d/stats/memory", master.Host, master.Port)
	
	resp, err := mc.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	
	var stats map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return nil, err
	}
	
	// Also collect topology information
	topologyURL := fmt.Sprintf("http://%s:%d/dir/status", master.Host, master.Port)
	topologyResp, err := mc.client.Get(topologyURL)
	if err == nil && topologyResp.StatusCode == http.StatusOK {
		var topologyStats map[string]interface{}
		json.NewDecoder(topologyResp.Body).Decode(&topologyStats)
		topologyResp.Body.Close()
		
		// Merge topology stats
		for k, v := range topologyStats {
			stats[k] = v
		}
	}
	
	return &ComponentMetrics{
		Component: "master",
		Host:      master.Host,
		Port:      master.Port,
		Timestamp: time.Now(),
		Metrics:   stats,
	}, nil
}

// collectFromVolume collects metrics from a volume server
func (mc *MetricsCollector) collectFromVolume(ctx context.Context, volume *spec.VolumeServerSpec) (*ComponentMetrics, error) {
	url := fmt.Sprintf("http://%s:%d/stats/memory", volume.Host, volume.Port)
	
	resp, err := mc.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	
	var stats map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return nil, err
	}
	
	// Also collect disk usage information
	diskURL := fmt.Sprintf("http://%s:%d/stats/disk", volume.Host, volume.Port)
	diskResp, err := mc.client.Get(diskURL)
	if err == nil && diskResp.StatusCode == http.StatusOK {
		var diskStats map[string]interface{}
		json.NewDecoder(diskResp.Body).Decode(&diskStats)
		diskResp.Body.Close()
		
		// Merge disk stats
		for k, v := range diskStats {
			stats[k] = v
		}
	}
	
	return &ComponentMetrics{
		Component: "volume",
		Host:      volume.Host,
		Port:      volume.Port,
		Timestamp: time.Now(),
		Metrics:   stats,
	}, nil
}

// collectFromFiler collects metrics from a filer server
func (mc *MetricsCollector) collectFromFiler(ctx context.Context, filer *spec.FilerServerSpec) (*ComponentMetrics, error) {
	url := fmt.Sprintf("http://%s:%d/stats/memory", filer.Host, filer.Port)
	
	resp, err := mc.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	
	var stats map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return nil, err
	}
	
	return &ComponentMetrics{
		Component: "filer",
		Host:      filer.Host,
		Port:      filer.Port,
		Timestamp: time.Now(),
		Metrics:   stats,
	}, nil
}

// StoreMetrics stores collected metrics using the configured storage
func (mc *MetricsCollector) StoreMetrics(ctx context.Context, componentMetrics []ComponentMetrics) error {
	var metricPoints []MetricPoint
	
	for _, cm := range componentMetrics {
		points := mc.convertToMetricPoints(cm)
		metricPoints = append(metricPoints, points...)
	}
	
	return mc.storage.Store(ctx, metricPoints)
}

// convertToMetricPoints converts ComponentMetrics to MetricPoints
func (mc *MetricsCollector) convertToMetricPoints(cm ComponentMetrics) []MetricPoint {
	var points []MetricPoint
	
	for metricName, value := range cm.Metrics {
		// Convert various types to float64
		var floatValue float64
		var ok bool
		
		switch v := value.(type) {
		case float64:
			floatValue = v
			ok = true
		case float32:
			floatValue = float64(v)
			ok = true
		case int:
			floatValue = float64(v)
			ok = true
		case int64:
			floatValue = float64(v)
			ok = true
		case string:
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				floatValue = f
				ok = true
			}
		}
		
		if !ok {
			// Skip non-numeric values or convert them to metadata
			continue
		}
		
		point := MetricPoint{
			Timestamp:  cm.Timestamp,
			Component:  cm.Component,
			Host:       cm.Host,
			Port:       cm.Port,
			MetricName: metricName,
			Value:      floatValue,
			Tags: map[string]string{
				"component": cm.Component,
				"host":      cm.Host,
			},
		}
		
		points = append(points, point)
	}
	
	return points
}

// GetSystemMetrics collects system-level metrics (CPU, memory, disk, network)
func (mc *MetricsCollector) GetSystemMetrics(ctx context.Context, host string) (*SystemMetrics, error) {
	if mc.executor == nil {
		return nil, fmt.Errorf("no executor available for system metrics")
	}
	
	metrics := &SystemMetrics{
		Host:      host,
		Timestamp: time.Now(),
	}
	
	// Get CPU usage
	cpuCmd := "cat /proc/loadavg"
	if output, err := mc.executor.Execute(ctx, host, cpuCmd); err == nil {
		fields := strings.Fields(output)
		if len(fields) >= 3 {
			if load1, err := strconv.ParseFloat(fields[0], 64); err == nil {
				metrics.CPULoad1 = load1
			}
			if load5, err := strconv.ParseFloat(fields[1], 64); err == nil {
				metrics.CPULoad5 = load5
			}
			if load15, err := strconv.ParseFloat(fields[2], 64); err == nil {
				metrics.CPULoad15 = load15
			}
		}
	}
	
	// Get memory usage
	memCmd := "cat /proc/meminfo"
	if output, err := mc.executor.Execute(ctx, host, memCmd); err == nil {
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "MemTotal:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if val, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
						metrics.MemoryTotal = val * 1024 // Convert KB to bytes
					}
				}
			} else if strings.HasPrefix(line, "MemAvailable:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if val, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
						metrics.MemoryAvailable = val * 1024 // Convert KB to bytes
					}
				}
			}
		}
		
		if metrics.MemoryTotal > 0 && metrics.MemoryAvailable > 0 {
			metrics.MemoryUsed = metrics.MemoryTotal - metrics.MemoryAvailable
			metrics.MemoryUsagePercent = float64(metrics.MemoryUsed) / float64(metrics.MemoryTotal) * 100
		}
	}
	
	// Get disk usage for common SeaweedFS directories
	diskDirs := []string{"/opt/seaweedfs", "/etc/seaweedfs", "/var/log/seaweedfs", "/"}
	for _, dir := range diskDirs {
		diskCmd := fmt.Sprintf("df -B1 %s 2>/dev/null | tail -1", dir)
		if output, err := mc.executor.Execute(ctx, host, diskCmd); err == nil {
			fields := strings.Fields(output)
			if len(fields) >= 6 {
				if total, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
					if used, err := strconv.ParseInt(fields[2], 10, 64); err == nil {
						diskInfo := DiskUsage{
							Path:         dir,
							Total:        total,
							Used:         used,
							Available:    total - used,
							UsagePercent: float64(used) / float64(total) * 100,
						}
						metrics.DiskUsage = append(metrics.DiskUsage, diskInfo)
					}
				}
			}
		}
	}
	
	return metrics, nil
}

// SystemMetrics represents system-level metrics
type SystemMetrics struct {
	Host                string      `json:"host"`
	Timestamp           time.Time   `json:"timestamp"`
	CPULoad1            float64     `json:"cpu_load_1m"`
	CPULoad5            float64     `json:"cpu_load_5m"`
	CPULoad15           float64     `json:"cpu_load_15m"`
	MemoryTotal         int64       `json:"memory_total_bytes"`
	MemoryUsed          int64       `json:"memory_used_bytes"`
	MemoryAvailable     int64       `json:"memory_available_bytes"`
	MemoryUsagePercent  float64     `json:"memory_usage_percent"`
	DiskUsage           []DiskUsage `json:"disk_usage"`
}

// DiskUsage represents disk usage information
type DiskUsage struct {
	Path         string  `json:"path"`
	Total        int64   `json:"total_bytes"`
	Used         int64   `json:"used_bytes"`
	Available    int64   `json:"available_bytes"`
	UsagePercent float64 `json:"usage_percent"`
}
