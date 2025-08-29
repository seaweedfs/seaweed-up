package task

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/seaweedfs/seaweed-up/pkg/errors"
)

// Task represents a single operation that can be executed and rolled back
type Task interface {
	Execute(ctx context.Context) error
	Rollback(ctx context.Context) error
	String() string
	GetID() string
}

// TaskResult holds the result of a task execution
type TaskResult struct {
	TaskID    string        `json:"task_id"`
	Success   bool          `json:"success"`
	Error     error         `json:"error,omitempty"`
	Duration  time.Duration `json:"duration"`
	StartTime time.Time     `json:"start_time"`
	EndTime   time.Time     `json:"end_time"`
}

// TaskGroup represents a collection of tasks that can be executed together
type TaskGroup struct {
	ID              string
	Name            string
	Tasks           []Task
	Parallel        bool
	Results         []TaskResult
	MaxRetries      int
	RetryDelay      time.Duration
	ContinueOnError bool
}

// NewTaskGroup creates a new task group
func NewTaskGroup(id, name string, parallel bool) *TaskGroup {
	return &TaskGroup{
		ID:              id,
		Name:            name,
		Tasks:           make([]Task, 0),
		Parallel:        parallel,
		Results:         make([]TaskResult, 0),
		MaxRetries:      3,
		RetryDelay:      time.Second * 5,
		ContinueOnError: false,
	}
}

// AddTask adds a task to the group
func (tg *TaskGroup) AddTask(task Task) {
	tg.Tasks = append(tg.Tasks, task)
}

// Execute runs all tasks in the group
func (tg *TaskGroup) Execute(ctx context.Context) error {
	color.Green("🚀 Executing task group: %s", tg.Name)

	if tg.Parallel {
		return tg.executeParallel(ctx)
	}
	return tg.executeSequential(ctx)
}

// executeSequential runs tasks one by one
func (tg *TaskGroup) executeSequential(ctx context.Context) error {
	for i, task := range tg.Tasks {
		color.Cyan("📋 [%d/%d] %s", i+1, len(tg.Tasks), task.String())

		result := tg.executeTaskWithRetry(ctx, task)
		tg.Results = append(tg.Results, result)

		if !result.Success && !tg.ContinueOnError {
			color.Red("❌ Task failed: %s", task.String())
			return result.Error
		}

		if result.Success {
			color.Green("✅ Task completed: %s (%.2fs)", task.String(), result.Duration.Seconds())
		} else {
			color.Yellow("⚠️  Task failed but continuing: %s", task.String())
		}
	}

	return nil
}

// executeParallel runs all tasks concurrently
func (tg *TaskGroup) executeParallel(ctx context.Context) error {
	var wg sync.WaitGroup
	resultsChan := make(chan TaskResult, len(tg.Tasks))

	// Start all tasks
	for _, task := range tg.Tasks {
		wg.Add(1)
		go func(t Task) {
			defer wg.Done()
			color.Cyan("📋 Starting: %s", t.String())
			result := tg.executeTaskWithRetry(ctx, t)
			resultsChan <- result
		}(task)
	}

	// Wait for all to complete
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// Collect results
	var firstError error
	for result := range resultsChan {
		tg.Results = append(tg.Results, result)

		if result.Success {
			color.Green("✅ Task completed: %s (%.2fs)", result.TaskID, result.Duration.Seconds())
		} else {
			color.Red("❌ Task failed: %s - %v", result.TaskID, result.Error)
			if firstError == nil {
				firstError = result.Error
			}
		}
	}

	// If any task failed and we don't continue on error, return the first error
	if firstError != nil && !tg.ContinueOnError {
		return firstError
	}

	return nil
}

// executeTaskWithRetry executes a task with retry logic
func (tg *TaskGroup) executeTaskWithRetry(ctx context.Context, task Task) TaskResult {
	startTime := time.Now()
	var lastError error

	for attempt := 0; attempt <= tg.MaxRetries; attempt++ {
		if attempt > 0 {
			color.Yellow("🔄 Retrying task: %s (attempt %d/%d)", task.String(), attempt, tg.MaxRetries)
			time.Sleep(tg.RetryDelay)
		}

		err := task.Execute(ctx)
		if err == nil {
			return TaskResult{
				TaskID:    task.GetID(),
				Success:   true,
				Duration:  time.Since(startTime),
				StartTime: startTime,
				EndTime:   time.Now(),
			}
		}

		lastError = err
		color.Yellow("⚠️  Task failed (attempt %d/%d): %v", attempt+1, tg.MaxRetries+1, err)
	}

	return TaskResult{
		TaskID:    task.GetID(),
		Success:   false,
		Error:     lastError,
		Duration:  time.Since(startTime),
		StartTime: startTime,
		EndTime:   time.Now(),
	}
}

// Rollback attempts to rollback all successfully executed tasks in reverse order
func (tg *TaskGroup) Rollback(ctx context.Context) error {
	color.Yellow("🔄 Rolling back task group: %s", tg.Name)

	// Rollback in reverse order
	for i := len(tg.Results) - 1; i >= 0; i-- {
		result := tg.Results[i]
		if !result.Success {
			continue // Skip tasks that didn't succeed
		}

		// Find the task by ID
		var taskToRollback Task
		for _, task := range tg.Tasks {
			if task.GetID() == result.TaskID {
				taskToRollback = task
				break
			}
		}

		if taskToRollback != nil {
			color.Cyan("🔙 Rolling back: %s", taskToRollback.String())
			if err := taskToRollback.Rollback(ctx); err != nil {
				color.Red("❌ Rollback failed for task %s: %v", taskToRollback.String(), err)
				return err
			}
			color.Green("✅ Rollback completed: %s", taskToRollback.String())
		}
	}

	return nil
}

// GetSuccessfulTasks returns the count of successful tasks
func (tg *TaskGroup) GetSuccessfulTasks() int {
	count := 0
	for _, result := range tg.Results {
		if result.Success {
			count++
		}
	}
	return count
}

// GetFailedTasks returns the count of failed tasks
func (tg *TaskGroup) GetFailedTasks() int {
	count := 0
	for _, result := range tg.Results {
		if !result.Success {
			count++
		}
	}
	return count
}

// GetTotalDuration returns the total duration of all tasks
func (tg *TaskGroup) GetTotalDuration() time.Duration {
	var total time.Duration
	for _, result := range tg.Results {
		total += result.Duration
	}
	return total
}

// PrintSummary prints a summary of task execution
func (tg *TaskGroup) PrintSummary() {
	color.Green("\n📊 Task Group Summary: %s", tg.Name)
	fmt.Printf("Total Tasks: %d\n", len(tg.Tasks))
	fmt.Printf("Successful: %d\n", tg.GetSuccessfulTasks())
	fmt.Printf("Failed: %d\n", tg.GetFailedTasks())
	fmt.Printf("Total Duration: %.2f seconds\n", tg.GetTotalDuration().Seconds())

	if tg.GetFailedTasks() > 0 {
		color.Yellow("\n⚠️  Failed Tasks:")
		for _, result := range tg.Results {
			if !result.Success {
				fmt.Printf("  - %s: %v\n", result.TaskID, result.Error)
			}
		}
	}
}

// TaskOrchestrator manages complex operations using task groups
type TaskOrchestrator struct {
	taskGroups []TaskGroup
}

// NewTaskOrchestrator creates a new task orchestrator
func NewTaskOrchestrator() *TaskOrchestrator {
	return &TaskOrchestrator{
		taskGroups: make([]TaskGroup, 0),
	}
}

// AddTaskGroup adds a task group to the orchestrator
func (to *TaskOrchestrator) AddTaskGroup(group *TaskGroup) {
	to.taskGroups = append(to.taskGroups, *group)
}

// Execute runs all task groups in sequence
func (to *TaskOrchestrator) Execute(ctx context.Context) error {
	color.Green("🎭 Starting task orchestration")

	for i, group := range to.taskGroups {
		color.Cyan("📋 Phase %d: %s", i+1, group.Name)

		if err := group.Execute(ctx); err != nil {
			color.Red("❌ Task group failed: %s", group.Name)

			// Attempt rollback
			color.Yellow("🔄 Attempting rollback...")
			if rollbackErr := group.Rollback(ctx); rollbackErr != nil {
				return fmt.Errorf("task group failed and rollback failed: %w (rollback error: %v)", err, rollbackErr)
			}

			return fmt.Errorf("task group failed: %w", err)
		}

		color.Green("✅ Phase completed: %s", group.Name)
		group.PrintSummary()
	}

	color.Green("🎉 Task orchestration completed successfully!")
	return nil
}

// Base task implementation helpers

// BaseTask provides common functionality for all tasks
type BaseTask struct {
	ID          string
	Name        string
	Description string
}

// GetID returns the task ID
func (bt *BaseTask) GetID() string {
	return bt.ID
}

// String returns the task description
func (bt *BaseTask) String() string {
	if bt.Description != "" {
		return bt.Description
	}
	return bt.Name
}

// NoOpTask is a task that does nothing (useful for testing)
type NoOpTask struct {
	BaseTask
}

// Execute does nothing
func (t *NoOpTask) Execute(ctx context.Context) error {
	// Simulate some work
	time.Sleep(100 * time.Millisecond)
	return nil
}

// Rollback does nothing
func (t *NoOpTask) Rollback(ctx context.Context) error {
	return nil
}

// ErrorTask always fails (useful for testing)
type ErrorTask struct {
	BaseTask
	ErrorMessage string
}

// Execute always returns an error
func (t *ErrorTask) Execute(ctx context.Context) error {
	return errors.NewClusterOperationError("test", "test-cluster", "test-node", fmt.Errorf(t.ErrorMessage))
}

// Rollback does nothing
func (t *ErrorTask) Rollback(ctx context.Context) error {
	return nil
}
