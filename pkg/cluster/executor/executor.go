package executor

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"github.com/seaweedfs/seaweed-up/pkg/errors"
	"github.com/seaweedfs/seaweed-up/pkg/utils"
)

// Executor interface for running commands on remote hosts
type Executor interface {
	Execute(ctx context.Context, host, command string) (string, error)
	ExecuteWithTimeout(ctx context.Context, host, command string, timeout time.Duration) (string, error)
	Close() error
}

// SSHExecutor executes commands via SSH
type SSHExecutor struct {
	user         string
	port         int
	identityFile string
	timeout      time.Duration
	connections  map[string]*ssh.Client
}

// NewSSHExecutor creates a new SSH executor
func NewSSHExecutor(user string, port int, identityFile string) *SSHExecutor {
	return &SSHExecutor{
		user:         user,
		port:         port,
		identityFile: identityFile,
		timeout:      30 * time.Second,
		connections:  make(map[string]*ssh.Client),
	}
}

// Execute runs a command on the specified host
func (e *SSHExecutor) Execute(ctx context.Context, host, command string) (string, error) {
	return e.ExecuteWithTimeout(ctx, host, command, e.timeout)
}

// ExecuteWithTimeout runs a command with custom timeout
func (e *SSHExecutor) ExecuteWithTimeout(ctx context.Context, host, command string, timeout time.Duration) (string, error) {
	client, err := e.getConnection(host)
	if err != nil {
		return "", err
	}
	
	session, err := client.NewSession()
	if err != nil {
		return "", errors.NewSSHConnectionError(host, e.port, e.user, err)
	}
	defer session.Close()
	
	// Create a context with timeout
	ctxWithTimeout, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	
	// Channel to receive command result
	resultChan := make(chan commandResult, 1)
	
	go func() {
		output, err := session.CombinedOutput(command)
		resultChan <- commandResult{
			output: string(output),
			err:    err,
		}
	}()
	
	select {
	case result := <-resultChan:
		if result.err != nil {
			return result.output, fmt.Errorf("command failed: %w", result.err)
		}
		return result.output, nil
	case <-ctxWithTimeout.Done():
		return "", fmt.Errorf("command timed out after %v", timeout)
	}
}

// getConnection gets or creates an SSH connection to the host
func (e *SSHExecutor) getConnection(host string) (*ssh.Client, error) {
	if client, exists := e.connections[host]; exists {
		// Test if connection is still alive
		if _, err := client.NewSession(); err == nil {
			return client, nil
		}
		// Connection is dead, remove it
		delete(e.connections, host)
	}
	
	// Create new connection
	config, err := e.createSSHConfig()
	if err != nil {
		return nil, err
	}
	
	address := fmt.Sprintf("%s:%d", host, e.port)
	client, err := ssh.Dial("tcp", address, config)
	if err != nil {
		return nil, errors.NewSSHConnectionError(host, e.port, e.user, err)
	}
	
	// Cache the connection
	e.connections[host] = client
	return client, nil
}

// createSSHConfig creates SSH client configuration
func (e *SSHExecutor) createSSHConfig() (*ssh.ClientConfig, error) {
	config := &ssh.ClientConfig{
		User:            e.user,
		Timeout:         10 * time.Second,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // In production, use proper host key verification
	}
	
	if e.identityFile != "" {
		// Load private key
		key, err := utils.LoadPrivateKey(e.identityFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load private key: %w", err)
		}
		
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("failed to parse private key: %w", err)
		}
		
		config.Auth = []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		}
	} else {
		// Try SSH agent
		if authMethod, err := utils.SSHAgent(); err == nil {
			config.Auth = []ssh.AuthMethod{authMethod}
		} else {
			return nil, fmt.Errorf("no SSH authentication method available")
		}
	}
	
	return config, nil
}

// Close closes all SSH connections
func (e *SSHExecutor) Close() error {
	for host, client := range e.connections {
		if err := client.Close(); err != nil {
			// Log error but don't fail
		}
		delete(e.connections, host)
	}
	return nil
}

// LocalExecutor executes commands locally
type LocalExecutor struct {
	timeout time.Duration
}

// NewLocalExecutor creates a new local executor
func NewLocalExecutor() *LocalExecutor {
	return &LocalExecutor{
		timeout: 30 * time.Second,
	}
}

// Execute runs a command locally
func (e *LocalExecutor) Execute(ctx context.Context, host, command string) (string, error) {
	return e.ExecuteWithTimeout(ctx, host, command, e.timeout)
}

// ExecuteWithTimeout runs a command locally with timeout
func (e *LocalExecutor) ExecuteWithTimeout(ctx context.Context, host, command string, timeout time.Duration) (string, error) {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	
	// Split command into parts
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return "", fmt.Errorf("empty command")
	}
	
	cmd := exec.CommandContext(ctxWithTimeout, parts[0], parts[1:]...)
	output, err := cmd.CombinedOutput()
	
	if err != nil {
		return string(output), fmt.Errorf("command failed: %w", err)
	}
	
	return string(output), nil
}

// Close is a no-op for local executor
func (e *LocalExecutor) Close() error {
	return nil
}

// MockExecutor is a mock executor for testing
type MockExecutor struct {
	responses map[string]string
	errors    map[string]error
	callLog   []ExecutorCall
}

// ExecutorCall represents a logged executor call
type ExecutorCall struct {
	Host    string
	Command string
	Time    time.Time
}

// NewMockExecutor creates a new mock executor
func NewMockExecutor() *MockExecutor {
	return &MockExecutor{
		responses: make(map[string]string),
		errors:    make(map[string]error),
		callLog:   make([]ExecutorCall, 0),
	}
}

// SetResponse sets a response for a specific command
func (e *MockExecutor) SetResponse(command, response string) {
	e.responses[command] = response
}

// SetError sets an error for a specific command
func (e *MockExecutor) SetError(command string, err error) {
	e.errors[command] = err
}

// Execute runs a mocked command
func (e *MockExecutor) Execute(ctx context.Context, host, command string) (string, error) {
	return e.ExecuteWithTimeout(ctx, host, command, time.Second)
}

// ExecuteWithTimeout runs a mocked command with timeout
func (e *MockExecutor) ExecuteWithTimeout(ctx context.Context, host, command string, timeout time.Duration) (string, error) {
	// Log the call
	e.callLog = append(e.callLog, ExecutorCall{
		Host:    host,
		Command: command,
		Time:    time.Now(),
	})
	
	// Check for error first
	if err, exists := e.errors[command]; exists {
		return "", err
	}
	
	// Check for response
	if response, exists := e.responses[command]; exists {
		return response, nil
	}
	
	// Default response
	return "mock-output", nil
}

// GetCallLog returns the log of all executor calls
func (e *MockExecutor) GetCallLog() []ExecutorCall {
	return e.callLog
}

// Close is a no-op for mock executor
func (e *MockExecutor) Close() error {
	return nil
}

// commandResult holds the result of a command execution
type commandResult struct {
	output string
	err    error
}
