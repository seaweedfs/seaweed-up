package testing

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/status"
)

// TestFramework provides comprehensive testing capabilities for SeaweedFS clusters
type TestFramework struct {
	cluster         *spec.Specification
	statusCollector *status.StatusCollector
	testSuites      map[string]TestSuite
	results         *TestResults
	config          TestConfig
	mu              sync.RWMutex
}

// TestConfig contains testing configuration
type TestConfig struct {
	Parallel            bool               `yaml:"parallel"`
	Timeout             time.Duration      `yaml:"timeout"`
	RetryAttempts       int                `yaml:"retry_attempts"`
	BenchmarkDuration   time.Duration      `yaml:"benchmark_duration"`
	BenchmarkFileSize   string             `yaml:"benchmark_file_size"`
	BenchmarkThreads    int                `yaml:"benchmark_threads"`
	ValidatePerformance bool               `yaml:"validate_performance"`
	PerformanceTargets  PerformanceTargets `yaml:"performance_targets"`
}

// PerformanceTargets defines expected performance metrics
type PerformanceTargets struct {
	MinThroughputMBps float64 `yaml:"min_throughput_mbps"`
	MaxLatencyMs      float64 `yaml:"max_latency_ms"`
	MinIOPS           float64 `yaml:"min_iops"`
	MaxErrorRate      float64 `yaml:"max_error_rate"`
}

// TestSuite represents a collection of related tests
type TestSuite interface {
	Name() string
	Description() string
	Tests() []Test
	Setup(ctx context.Context, cluster *spec.Specification) error
	Teardown(ctx context.Context, cluster *spec.Specification) error
}

// Test represents a single test case
type Test interface {
	Name() string
	Description() string
	Execute(ctx context.Context, cluster *spec.Specification) TestResult
	Timeout() time.Duration
	RequiresCluster() bool
}

// TestResult contains the result of a single test
type TestResult struct {
	Name      string        `json:"name"`
	Success   bool          `json:"success"`
	Error     string        `json:"error,omitempty"`
	Duration  time.Duration `json:"duration"`
	Message   string        `json:"message"`
	Details   interface{}   `json:"details,omitempty"`
	Metrics   TestMetrics   `json:"metrics,omitempty"`
	Timestamp time.Time     `json:"timestamp"`
}

// TestMetrics contains performance metrics from tests
type TestMetrics struct {
	ThroughputMBps float64 `json:"throughput_mbps,omitempty"`
	LatencyMs      float64 `json:"latency_ms,omitempty"`
	IOPS           float64 `json:"iops,omitempty"`
	ErrorRate      float64 `json:"error_rate,omitempty"`
	FilesProcessed int     `json:"files_processed,omitempty"`
	BytesProcessed int64   `json:"bytes_processed,omitempty"`
}

// TestResults contains results from all test executions
type TestResults struct {
	TotalTests   int                    `json:"total_tests"`
	PassedTests  int                    `json:"passed_tests"`
	FailedTests  int                    `json:"failed_tests"`
	SkippedTests int                    `json:"skipped_tests"`
	Duration     time.Duration          `json:"duration"`
	Results      []TestResult           `json:"results"`
	SuiteResults map[string]SuiteResult `json:"suite_results"`
	Summary      TestSummary            `json:"summary"`
	Timestamp    time.Time              `json:"timestamp"`
}

// SuiteResult contains results for a test suite
type SuiteResult struct {
	Name         string        `json:"name"`
	TotalTests   int           `json:"total_tests"`
	PassedTests  int           `json:"passed_tests"`
	FailedTests  int           `json:"failed_tests"`
	SkippedTests int           `json:"skipped_tests"`
	Duration     time.Duration `json:"duration"`
	Success      bool          `json:"success"`
	Results      []TestResult  `json:"-"` // Internal use only
}

// TestSummary provides a high-level test summary
type TestSummary struct {
	OverallSuccess    bool    `json:"overall_success"`
	SuccessRate       float64 `json:"success_rate"`
	AverageDuration   float64 `json:"average_duration_ms"`
	PerformancePassed bool    `json:"performance_passed,omitempty"`
}

// NewTestFramework creates a new test framework
func NewTestFramework(cluster *spec.Specification, statusCollector *status.StatusCollector, config TestConfig) *TestFramework {
	// Set default values
	if config.Timeout == 0 {
		config.Timeout = 5 * time.Minute
	}
	if config.RetryAttempts == 0 {
		config.RetryAttempts = 3
	}
	if config.BenchmarkDuration == 0 {
		config.BenchmarkDuration = 60 * time.Second
	}
	if config.BenchmarkFileSize == "" {
		config.BenchmarkFileSize = "1MB"
	}
	if config.BenchmarkThreads == 0 {
		config.BenchmarkThreads = 10
	}

	return &TestFramework{
		cluster:         cluster,
		statusCollector: statusCollector,
		testSuites:      make(map[string]TestSuite),
		config:          config,
	}
}

// RegisterTestSuite registers a test suite
func (tf *TestFramework) RegisterTestSuite(suite TestSuite) {
	tf.mu.Lock()
	defer tf.mu.Unlock()

	tf.testSuites[suite.Name()] = suite
}

// RunAllTests runs all registered test suites
func (tf *TestFramework) RunAllTests(ctx context.Context) (*TestResults, error) {
	color.Green("🧪 Starting comprehensive test execution...")

	start := time.Now()
	results := &TestResults{
		Results:      make([]TestResult, 0),
		SuiteResults: make(map[string]SuiteResult),
		Timestamp:    start,
	}

	// Run all test suites
	for _, suite := range tf.testSuites {
		color.Cyan("📋 Running test suite: %s", suite.Name())

		suiteResult, err := tf.runTestSuite(ctx, suite)
		if err != nil {
			color.Red("❌ Test suite %s failed: %v", suite.Name(), err)
			continue
		}

		results.SuiteResults[suite.Name()] = SuiteResult{
			Name:         suiteResult.Name,
			TotalTests:   suiteResult.TotalTests,
			PassedTests:  suiteResult.PassedTests,
			FailedTests:  suiteResult.FailedTests,
			SkippedTests: suiteResult.SkippedTests,
			Duration:     suiteResult.Duration,
			Success:      suiteResult.Success,
		}
		results.Results = append(results.Results, suiteResult.Results...)
		results.TotalTests += suiteResult.TotalTests
		results.PassedTests += suiteResult.PassedTests
		results.FailedTests += suiteResult.FailedTests
		results.SkippedTests += suiteResult.SkippedTests
	}

	results.Duration = time.Since(start)
	results.Summary = tf.generateSummary(results)

	tf.results = results
	tf.printResults(results)

	return results, nil
}

// RunTestSuite runs a specific test suite
func (tf *TestFramework) RunTestSuite(ctx context.Context, suiteName string) (*SuiteResult, error) {
	tf.mu.RLock()
	suite, exists := tf.testSuites[suiteName]
	tf.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("test suite %s not found", suiteName)
	}

	return tf.runTestSuite(ctx, suite)
}

// runTestSuite executes a test suite
func (tf *TestFramework) runTestSuite(ctx context.Context, suite TestSuite) (*SuiteResult, error) {
	start := time.Now()

	// Setup suite
	if err := suite.Setup(ctx, tf.cluster); err != nil {
		return nil, fmt.Errorf("suite setup failed: %w", err)
	}

	// Ensure teardown runs
	defer func() {
		if err := suite.Teardown(ctx, tf.cluster); err != nil {
			color.Yellow("⚠️  Suite teardown failed: %v", err)
		}
	}()

	suiteResult := &SuiteResult{
		Name:    suite.Name(),
		Results: make([]TestResult, 0),
	}

	tests := suite.Tests()
	suiteResult.TotalTests = len(tests)

	// Run tests
	if tf.config.Parallel {
		suiteResult.Results = tf.runTestsParallel(ctx, tests)
	} else {
		suiteResult.Results = tf.runTestsSequential(ctx, tests)
	}

	// Calculate results
	for _, result := range suiteResult.Results {
		if result.Success {
			suiteResult.PassedTests++
		} else {
			suiteResult.FailedTests++
		}
	}

	suiteResult.Duration = time.Since(start)
	suiteResult.Success = suiteResult.FailedTests == 0

	return suiteResult, nil
}

// runTestsSequential runs tests sequentially
func (tf *TestFramework) runTestsSequential(ctx context.Context, tests []Test) []TestResult {
	var results []TestResult

	for _, test := range tests {
		result := tf.runSingleTest(ctx, test)
		results = append(results, result)

		// Print immediate result
		tf.printTestResult(result)
	}

	return results
}

// runTestsParallel runs tests in parallel
func (tf *TestFramework) runTestsParallel(ctx context.Context, tests []Test) []TestResult {
	results := make([]TestResult, len(tests))
	var wg sync.WaitGroup

	for i, test := range tests {
		wg.Add(1)
		go func(index int, t Test) {
			defer wg.Done()
			results[index] = tf.runSingleTest(ctx, t)
		}(i, test)
	}

	wg.Wait()

	// Print results after all complete
	for _, result := range results {
		tf.printTestResult(result)
	}

	return results
}

// runSingleTest runs a single test with retry logic
func (tf *TestFramework) runSingleTest(ctx context.Context, test Test) TestResult {
	var lastResult TestResult

	for attempt := 1; attempt <= tf.config.RetryAttempts; attempt++ {
		// Create timeout context
		testCtx, cancel := context.WithTimeout(ctx, test.Timeout())

		// Execute test
		start := time.Now()
		result := test.Execute(testCtx, tf.cluster)
		result.Duration = time.Since(start)
		result.Timestamp = time.Now()

		cancel()

		if result.Success {
			return result
		}

		lastResult = result
		if attempt < tf.config.RetryAttempts {
			time.Sleep(time.Second * time.Duration(attempt))
		}
	}

	return lastResult
}

// generateSummary generates a test summary
func (tf *TestFramework) generateSummary(results *TestResults) TestSummary {
	summary := TestSummary{
		OverallSuccess: results.FailedTests == 0,
	}

	if results.TotalTests > 0 {
		summary.SuccessRate = float64(results.PassedTests) / float64(results.TotalTests) * 100

		var totalDuration time.Duration
		for _, result := range results.Results {
			totalDuration += result.Duration
		}
		summary.AverageDuration = float64(totalDuration.Nanoseconds()) / float64(results.TotalTests) / 1000000 // Convert to ms
	}

	// Check performance targets if enabled
	if tf.config.ValidatePerformance {
		summary.PerformancePassed = tf.validatePerformanceTargets(results)
	}

	return summary
}

// validatePerformanceTargets validates performance against targets
func (tf *TestFramework) validatePerformanceTargets(results *TestResults) bool {
	targets := tf.config.PerformanceTargets

	for _, result := range results.Results {
		if result.Metrics.ThroughputMBps > 0 && result.Metrics.ThroughputMBps < targets.MinThroughputMBps {
			return false
		}
		if result.Metrics.LatencyMs > 0 && result.Metrics.LatencyMs > targets.MaxLatencyMs {
			return false
		}
		if result.Metrics.IOPS > 0 && result.Metrics.IOPS < targets.MinIOPS {
			return false
		}
		if result.Metrics.ErrorRate > targets.MaxErrorRate {
			return false
		}
	}

	return true
}

// printResults prints formatted test results
func (tf *TestFramework) printResults(results *TestResults) {
	color.Green("\n🎯 Test Execution Complete")
	color.Cyan(strings.Repeat("=", 60))

	fmt.Printf("Total Tests: %d\n", results.TotalTests)
	fmt.Printf("Passed: %s\n", color.GreenString("%d", results.PassedTests))
	fmt.Printf("Failed: %s\n", color.RedString("%d", results.FailedTests))
	fmt.Printf("Skipped: %s\n", color.YellowString("%d", results.SkippedTests))
	fmt.Printf("Success Rate: %.1f%%\n", results.Summary.SuccessRate)
	fmt.Printf("Total Duration: %v\n", results.Duration)
	fmt.Printf("Average Test Duration: %.1f ms\n", results.Summary.AverageDuration)

	if tf.config.ValidatePerformance {
		if results.Summary.PerformancePassed {
			color.Green("✅ Performance targets met")
		} else {
			color.Red("❌ Performance targets not met")
		}
	}

	// Print suite summaries
	color.Cyan("\n📊 Test Suite Summary:")
	for name, suite := range results.SuiteResults {
		status := "✅"
		if !suite.Success {
			status = "❌"
		}
		fmt.Printf("%s %s: %d/%d tests passed (%.1f%%)\n",
			status, name, suite.PassedTests, suite.TotalTests,
			float64(suite.PassedTests)/float64(suite.TotalTests)*100)
	}

	// Print failed tests
	if results.FailedTests > 0 {
		color.Red("\n❌ Failed Tests:")
		for _, result := range results.Results {
			if !result.Success {
				fmt.Printf("  • %s: %s\n", result.Name, result.Error)
			}
		}
	}
}

// printTestResult prints individual test result
func (tf *TestFramework) printTestResult(result TestResult) {
	status := "✅"
	statusColor := color.GreenString
	if !result.Success {
		status = "❌"
		statusColor = color.RedString
	}

	fmt.Printf("%s %s (%v)\n", status, statusColor(result.Name), result.Duration)
	if !result.Success && result.Error != "" {
		fmt.Printf("    Error: %s\n", color.RedString(result.Error))
	}
}

// GetResults returns the latest test results
func (tf *TestFramework) GetResults() *TestResults {
	tf.mu.RLock()
	defer tf.mu.RUnlock()
	return tf.results
}

// ListTestSuites returns all registered test suites
func (tf *TestFramework) ListTestSuites() []string {
	tf.mu.RLock()
	defer tf.mu.RUnlock()

	var names []string
	for name := range tf.testSuites {
		names = append(names, name)
	}
	return names
}
