package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// InMemoryStorage implements MetricsStorage using in-memory storage
type InMemoryStorage struct {
	mu      sync.RWMutex
	metrics []MetricPoint
	maxSize int
}

// NewInMemoryStorage creates a new in-memory metrics storage
func NewInMemoryStorage(maxSize int) *InMemoryStorage {
	if maxSize <= 0 {
		maxSize = 10000 // Default to 10k metrics
	}
	
	return &InMemoryStorage{
		metrics: make([]MetricPoint, 0),
		maxSize: maxSize,
	}
}

// Store stores metrics in memory
func (ims *InMemoryStorage) Store(ctx context.Context, metrics []MetricPoint) error {
	ims.mu.Lock()
	defer ims.mu.Unlock()
	
	// Add new metrics
	ims.metrics = append(ims.metrics, metrics...)
	
	// Keep only the most recent metrics if we exceed maxSize
	if len(ims.metrics) > ims.maxSize {
		// Sort by timestamp and keep the most recent
		sort.Slice(ims.metrics, func(i, j int) bool {
			return ims.metrics[i].Timestamp.Before(ims.metrics[j].Timestamp)
		})
		
		// Keep only the last maxSize metrics
		excess := len(ims.metrics) - ims.maxSize
		ims.metrics = ims.metrics[excess:]
	}
	
	return nil
}

// Query retrieves metrics based on the query parameters
func (ims *InMemoryStorage) Query(ctx context.Context, query MetricsQuery) ([]MetricPoint, error) {
	ims.mu.RLock()
	defer ims.mu.RUnlock()
	
	var result []MetricPoint
	
	for _, metric := range ims.metrics {
		if ims.matchesQuery(metric, query) {
			result = append(result, metric)
		}
	}
	
	// Sort by timestamp (newest first)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Timestamp.After(result[j].Timestamp)
	})
	
	// Apply limit if specified
	if query.Limit > 0 && len(result) > query.Limit {
		result = result[:query.Limit]
	}
	
	return result, nil
}

// GetLatest retrieves the latest metric for a specific component and metric name
func (ims *InMemoryStorage) GetLatest(ctx context.Context, component, host string, metricName string) (*MetricPoint, error) {
	ims.mu.RLock()
	defer ims.mu.RUnlock()
	
	var latest *MetricPoint
	
	for _, metric := range ims.metrics {
		if metric.Component == component && 
		   metric.Host == host && 
		   metric.MetricName == metricName {
			if latest == nil || metric.Timestamp.After(latest.Timestamp) {
				latest = &metric
			}
		}
	}
	
	if latest == nil {
		return nil, fmt.Errorf("no metric found for %s/%s/%s", component, host, metricName)
	}
	
	return latest, nil
}

// Close closes the storage (no-op for in-memory)
func (ims *InMemoryStorage) Close() error {
	return nil
}

// matchesQuery checks if a metric matches the query criteria
func (ims *InMemoryStorage) matchesQuery(metric MetricPoint, query MetricsQuery) bool {
	// Check component filter
	if query.Component != "" && metric.Component != query.Component {
		return false
	}
	
	// Check host filter
	if query.Host != "" && metric.Host != query.Host {
		return false
	}
	
	// Check metric name filter
	if query.MetricName != "" && metric.MetricName != query.MetricName {
		return false
	}
	
	// Check time range
	if !query.StartTime.IsZero() && metric.Timestamp.Before(query.StartTime) {
		return false
	}
	if !query.EndTime.IsZero() && metric.Timestamp.After(query.EndTime) {
		return false
	}
	
	// Check tags
	if len(query.Tags) > 0 {
		for key, value := range query.Tags {
			if metricValue, exists := metric.Tags[key]; !exists || metricValue != value {
				return false
			}
		}
	}
	
	return true
}

// GetStats returns storage statistics
func (ims *InMemoryStorage) GetStats() map[string]interface{} {
	ims.mu.RLock()
	defer ims.mu.RUnlock()
	
	componentCounts := make(map[string]int)
	hostCounts := make(map[string]int)
	metricCounts := make(map[string]int)
	
	var oldestTime, newestTime time.Time
	for i, metric := range ims.metrics {
		componentCounts[metric.Component]++
		hostCounts[metric.Host]++
		metricCounts[metric.MetricName]++
		
		if i == 0 || metric.Timestamp.Before(oldestTime) {
			oldestTime = metric.Timestamp
		}
		if i == 0 || metric.Timestamp.After(newestTime) {
			newestTime = metric.Timestamp
		}
	}
	
	return map[string]interface{}{
		"total_metrics":    len(ims.metrics),
		"max_size":         ims.maxSize,
		"components":       componentCounts,
		"hosts":            hostCounts,
		"metric_types":     metricCounts,
		"oldest_timestamp": oldestTime,
		"newest_timestamp": newestTime,
		"time_span":        newestTime.Sub(oldestTime).String(),
	}
}

// FileStorage implements MetricsStorage using file-based storage
type FileStorage struct {
	mu       sync.RWMutex
	dataDir  string
	maxFiles int
}

// NewFileStorage creates a new file-based metrics storage
func NewFileStorage(dataDir string, maxFiles int) *FileStorage {
	if maxFiles <= 0 {
		maxFiles = 100 // Default to 100 files
	}
	
	return &FileStorage{
		dataDir:  dataDir,
		maxFiles: maxFiles,
	}
}

// Store stores metrics in files organized by date
func (fs *FileStorage) Store(ctx context.Context, metrics []MetricPoint) error {
	if len(metrics) == 0 {
		return nil
	}
	
	fs.mu.Lock()
	defer fs.mu.Unlock()
	
	// Ensure data directory exists
	if err := os.MkdirAll(fs.dataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}
	
	// Group metrics by date
	metricsByDate := make(map[string][]MetricPoint)
	for _, metric := range metrics {
		dateKey := metric.Timestamp.Format("2006-01-02")
		metricsByDate[dateKey] = append(metricsByDate[dateKey], metric)
	}
	
	// Write metrics to files
	for dateKey, dayMetrics := range metricsByDate {
		filename := fmt.Sprintf("metrics-%s.jsonl", dateKey)
		filePath := filepath.Join(fs.dataDir, filename)
		
		// Append to existing file or create new one
		file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return fmt.Errorf("failed to open metrics file %s: %w", filePath, err)
		}
		
		// Write each metric as a JSON line
		for _, metric := range dayMetrics {
			data, err := json.Marshal(metric)
			if err != nil {
				file.Close()
				return fmt.Errorf("failed to marshal metric: %w", err)
			}
			
			if _, err := file.WriteString(string(data) + "\n"); err != nil {
				file.Close()
				return fmt.Errorf("failed to write metric: %w", err)
			}
		}
		
		file.Close()
	}
	
	// Clean up old files if we exceed maxFiles
	return fs.cleanupOldFiles()
}

// Query retrieves metrics from files based on query parameters
func (fs *FileStorage) Query(ctx context.Context, query MetricsQuery) ([]MetricPoint, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	
	var result []MetricPoint
	
	// Determine which files to search based on time range
	files, err := fs.getRelevantFiles(query.StartTime, query.EndTime)
	if err != nil {
		return nil, fmt.Errorf("failed to get relevant files: %w", err)
	}
	
	// Read and filter metrics from each file
	for _, filename := range files {
		filePath := filepath.Join(fs.dataDir, filename)
		metrics, err := fs.readMetricsFile(filePath)
		if err != nil {
			continue // Skip files with errors
		}
		
		// Filter metrics
		for _, metric := range metrics {
			if fs.matchesQuery(metric, query) {
				result = append(result, metric)
			}
		}
		
		// Early exit if we have enough results
		if query.Limit > 0 && len(result) >= query.Limit {
			break
		}
	}
	
	// Sort by timestamp (newest first)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Timestamp.After(result[j].Timestamp)
	})
	
	// Apply limit if specified
	if query.Limit > 0 && len(result) > query.Limit {
		result = result[:query.Limit]
	}
	
	return result, nil
}

// GetLatest retrieves the latest metric for a specific component and metric name
func (fs *FileStorage) GetLatest(ctx context.Context, component, host string, metricName string) (*MetricPoint, error) {
	// Query for the latest metric
	query := MetricsQuery{
		Component:  component,
		Host:       host,
		MetricName: metricName,
		StartTime:  time.Now().Add(-24 * time.Hour), // Look back 24 hours
		EndTime:    time.Now(),
		Limit:      1,
	}
	
	results, err := fs.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	
	if len(results) == 0 {
		return nil, fmt.Errorf("no metric found for %s/%s/%s", component, host, metricName)
	}
	
	return &results[0], nil
}

// Close closes the file storage
func (fs *FileStorage) Close() error {
	return nil
}

// getRelevantFiles returns filenames that might contain metrics for the given time range
func (fs *FileStorage) getRelevantFiles(startTime, endTime time.Time) ([]string, error) {
	files, err := os.ReadDir(fs.dataDir)
	if err != nil {
		return nil, err
	}
	
	var relevantFiles []string
	
	for _, file := range files {
		if !file.IsDir() && filepath.Ext(file.Name()) == ".jsonl" {
			// Extract date from filename (metrics-2023-12-25.jsonl)
			if len(file.Name()) >= 18 && file.Name()[:8] == "metrics-" {
				dateStr := file.Name()[8:18] // Extract "2023-12-25"
				fileDate, err := time.Parse("2006-01-02", dateStr)
				if err != nil {
					continue
				}
				
				// Check if file date overlaps with query range
				if (startTime.IsZero() || fileDate.After(startTime.Add(-24*time.Hour))) &&
				   (endTime.IsZero() || fileDate.Before(endTime.Add(24*time.Hour))) {
					relevantFiles = append(relevantFiles, file.Name())
				}
			}
		}
	}
	
	// Sort files by name (which corresponds to date)
	sort.Strings(relevantFiles)
	
	return relevantFiles, nil
}

// readMetricsFile reads metrics from a JSON lines file
func (fs *FileStorage) readMetricsFile(filePath string) ([]MetricPoint, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	
	var metrics []MetricPoint
	decoder := json.NewDecoder(file)
	
	for decoder.More() {
		var metric MetricPoint
		if err := decoder.Decode(&metric); err != nil {
			continue // Skip invalid lines
		}
		metrics = append(metrics, metric)
	}
	
	return metrics, nil
}

// matchesQuery checks if a metric matches the query criteria (same as in-memory)
func (fs *FileStorage) matchesQuery(metric MetricPoint, query MetricsQuery) bool {
	// Same implementation as InMemoryStorage
	if query.Component != "" && metric.Component != query.Component {
		return false
	}
	
	if query.Host != "" && metric.Host != query.Host {
		return false
	}
	
	if query.MetricName != "" && metric.MetricName != query.MetricName {
		return false
	}
	
	if !query.StartTime.IsZero() && metric.Timestamp.Before(query.StartTime) {
		return false
	}
	if !query.EndTime.IsZero() && metric.Timestamp.After(query.EndTime) {
		return false
	}
	
	if len(query.Tags) > 0 {
		for key, value := range query.Tags {
			if metricValue, exists := metric.Tags[key]; !exists || metricValue != value {
				return false
			}
		}
	}
	
	return true
}

// cleanupOldFiles removes old metric files if we exceed maxFiles
func (fs *FileStorage) cleanupOldFiles() error {
	files, err := os.ReadDir(fs.dataDir)
	if err != nil {
		return err
	}
	
	var metricFiles []os.DirEntry
	for _, file := range files {
		if !file.IsDir() && filepath.Ext(file.Name()) == ".jsonl" && 
		   len(file.Name()) >= 18 && file.Name()[:8] == "metrics-" {
			metricFiles = append(metricFiles, file)
		}
	}
	
	if len(metricFiles) <= fs.maxFiles {
		return nil
	}
	
	// Sort by name (oldest first) and remove excess files
	sort.Slice(metricFiles, func(i, j int) bool {
		return metricFiles[i].Name() < metricFiles[j].Name()
	})
	
	filesToRemove := len(metricFiles) - fs.maxFiles
	for i := 0; i < filesToRemove; i++ {
		filePath := filepath.Join(fs.dataDir, metricFiles[i].Name())
		os.Remove(filePath) // Ignore errors
	}
	
	return nil
}
