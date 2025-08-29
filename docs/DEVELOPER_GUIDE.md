# SeaweedFS-Up Developer Guide

**Version:** 2.0.0  
**Last Updated:** August 2025

This guide provides comprehensive documentation for developers working on SeaweedFS-up, including architecture overview, code organization, API references, and contribution guidelines.

## 🏗️ Architecture Overview

SeaweedFS-up is built as a modular, enterprise-grade cluster management platform with the following architectural principles:

- **Modular Design**: Clean separation of concerns with well-defined packages
- **Interface-Driven**: Extensive use of interfaces for testability and extensibility  
- **Task Orchestration**: Complex operations broken down into atomic, rollback-capable tasks
- **Security-First**: Enterprise security built into every component
- **Extensible**: Plugin-friendly architecture for custom functionality

```
seaweed-up/
├── cmd/                    # CLI command implementations
├── pkg/                    # Core packages and business logic  
├── docs/                   # Documentation
├── examples/               # Example configurations
├── scripts/                # Build and deployment scripts
└── main.go                # Application entry point
```

## 📦 Package Structure

### Core CLI Commands (`cmd/`)

```
cmd/
├── root.go                 # Root command and global flags
├── cluster.go              # Cluster management commands
├── cluster_impl.go         # Cluster command implementations
├── component.go            # Component management commands
├── security.go             # Security and authentication commands
├── monitoring.go           # Monitoring and alerting commands
├── template.go             # Template management commands
├── env.go                  # Environment management commands
└── version_cmd.go          # Version information command
```

### Business Logic Packages (`pkg/`)

```
pkg/
├── cluster/                # Cluster operations and management
│   ├── spec/              # Cluster specifications and schemas
│   ├── status/            # Status collection and monitoring
│   ├── executor/          # Command execution (local/SSH)
│   ├── manager/           # High-level cluster management
│   ├── task/              # Task orchestration system
│   └── operation/         # Complex cluster operations
├── component/             # Component version management
│   ├── registry/          # Local component registry
│   └── repository/        # Remote component repositories
├── security/              # Security and authentication
│   ├── tls/              # TLS certificate management
│   └── auth/             # Authentication systems
├── monitoring/            # Monitoring and alerting
│   ├── metrics/          # Metrics collection and storage
│   └── alerting/         # Alert rules and notifications
├── template/              # Template engine and management
├── environment/           # Environment and configuration
├── errors/               # Structured error types
└── utils/                # Common utilities and helpers
```

## 🔧 Core Interfaces and Types

### Command Execution Interface

```go
// pkg/cluster/executor/executor.go
type Executor interface {
    Execute(command string) (*ExecutionResult, error)
    ExecuteWithContext(ctx context.Context, command string) (*ExecutionResult, error)
    UploadFile(localPath, remotePath string) error
    DownloadFile(remotePath, localPath string) error
    Close() error
}
```

### Task System Interface

```go
// pkg/cluster/task/task.go
type Task interface {
    Execute() error
    Rollback() error
    Description() string
    Dependencies() []string
}
```

### Component Specification Interface

```go
// pkg/cluster/spec/spec.go
type ComponentSpec interface {
    GetHost() string
    GetPort() int
    GetType() string
    GetDataDir() string
}
```

## 🛠️ Key Systems

### 1. Task Orchestration System

The task orchestration system is the backbone of complex operations like deployment, upgrades, and scaling.

```go
// Example: Creating a custom task
type CustomTask struct {
    *task.BaseTask
    component *spec.ComponentSpec
    executor  executor.Executor
}

func (t *CustomTask) Execute() error {
    // Implementation
    return nil
}

func (t *CustomTask) Rollback() error {
    // Rollback logic
    return nil
}
```

**Key Features:**
- Atomic operations with rollback capability
- Dependency management between tasks
- Parallel execution where safe
- Progress tracking and logging

### 2. Security System

Multi-layered security with TLS and authentication.

```go
// TLS Certificate Management
certManager := tls.NewCertificateManager(certsDir, caConfig)
err := certManager.InitializeCA(clusterName)
certificates, err := certManager.GenerateClusterCertificates(cluster)

// Authentication Management
authManager := auth.NewAuthManager(configDir)
authConfig, err := authManager.InitializeAuth(clusterName, auth.AuthMethodJWT)
apiKey, err := authManager.CreateAPIKey(clusterName, keyName, permissions, expiresAt)
```

### 3. Monitoring System

Real-time metrics collection and intelligent alerting.

```go
// Metrics Collection
collector := metrics.NewMetricsCollector()
storage := metrics.NewInMemoryMetricsStorage()
err := collector.CollectMetrics(clusterSpec, storage)

// Alerting
alertManager := alerting.NewAlertManager(storage)
rule := &alerting.AlertRule{
    Name:      "high-cpu",
    Metric:    "cpu_usage", 
    Condition: alerting.ConditionGreaterThan,
    Threshold: 85.0,
    Severity:  alerting.SeverityCritical,
}
alertManager.AddRule(rule)
```

### 4. Template System

Dynamic template generation with parameter validation.

```go
// Template Management  
templateManager := templates.NewTemplateManager(templatesDir)
err := templateManager.LoadTemplates()

// Generate from template
template, err := templateManager.GetTemplate("production")
spec, err := templateManager.GenerateCluster(template, parameters)
```

## 🔌 Adding New Features

### Adding a New CLI Command

1. **Create command file** in `cmd/`:
```go
// cmd/mycmd.go
func newMyCmd() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "mycmd",
        Short: "My new command",
        RunE:  runMyCmd,
    }
    return cmd
}

func runMyCmd(cmd *cobra.Command, args []string) error {
    // Implementation
    return nil
}
```

2. **Register command** in `cmd/root.go`:
```go
func init() {
    rootCmd.AddCommand(newMyCmd())
}
```

### Adding a New Task Type

1. **Implement Task interface**:
```go
// pkg/cluster/task/my_task.go
type MyTask struct {
    *BaseTask
    // Task-specific fields
}

func (t *MyTask) Execute() error {
    // Execution logic
    return nil
}

func (t *MyTask) Rollback() error {
    // Rollback logic  
    return nil
}
```

2. **Use in operations**:
```go
// pkg/cluster/operation/operations.go
func (om *ClusterOperationManager) MyOperation(spec *spec.Specification) error {
    orchestrator := task.NewTaskOrchestrator()
    
    myTask := &task.MyTask{
        BaseTask: task.NewBaseTask("my-task", "Description"),
        // Initialize task-specific fields
    }
    
    orchestrator.AddTask(myTask)
    return orchestrator.Execute()
}
```

### Adding a New Authentication Method

1. **Extend AuthMethod enum**:
```go
// pkg/security/auth/auth.go
const (
    AuthMethodNone   AuthMethod = "none"
    AuthMethodJWT    AuthMethod = "jwt"
    AuthMethodBasic  AuthMethod = "basic"
    AuthMethodMTLS   AuthMethod = "mtls"
    AuthMethodAPIKey AuthMethod = "apikey"
    AuthMethodOAuth  AuthMethod = "oauth"  // New method
)
```

2. **Add configuration struct**:
```go
type OAuthConfig struct {
    ClientID     string `json:"client_id"`
    ClientSecret string `json:"client_secret"`
    AuthURL      string `json:"auth_url"`
    TokenURL     string `json:"token_url"`
}
```

3. **Implement in AuthManager**:
```go
func (am *AuthManager) InitializeAuth(clusterName string, method AuthMethod) (*AuthConfig, error) {
    // ... existing code ...
    
    case AuthMethodOAuth:
        oauthConfig := &OAuthConfig{
            // Default configuration
        }
        authConfig.OAuthConfig = oauthConfig
        
    // ... rest of method ...
}
```

### Adding a New Template

1. **Create template function**:
```go
// pkg/template/templates.go
func (tm *TemplateManager) createMyTemplate() ClusterTemplate {
    return ClusterTemplate{
        Name:        "my-template",
        Description: "My custom template",
        Version:     "1.0.0",
        Author:      "Developer",
        Tags:        []string{"custom", "production"},
        
        Parameters: []TemplateParameter{
            {
                Name:         "cluster_size",
                Type:         "int",
                Description:  "Number of nodes",
                DefaultValue: "3",
                Required:     true,
            },
        },
        
        Spec: spec.Specification{
            // Template specification
        },
    }
}
```

2. **Register in built-in templates**:
```go
func (tm *TemplateManager) createBuiltInTemplates() error {
    templates := []ClusterTemplate{
        tm.createSingleNodeTemplate(),
        tm.createDevelopmentTemplate(), 
        tm.createProductionTemplate(),
        tm.createHighAvailabilityTemplate(),
        tm.createMyTemplate(),  // Add new template
    }
    // ... rest of method ...
}
```

## 🧪 Testing Guidelines

### Unit Testing Structure

```
pkg/
└── mypackage/
    ├── mycode.go
    └── mycode_test.go
```

### Testing Best Practices

1. **Use interface mocking**:
```go
type MockExecutor struct {
    Commands []string
    Results  []*ExecutionResult
}

func (m *MockExecutor) Execute(command string) (*ExecutionResult, error) {
    m.Commands = append(m.Commands, command)
    if len(m.Results) > 0 {
        result := m.Results[0]
        m.Results = m.Results[1:]
        return result, nil
    }
    return &ExecutionResult{}, nil
}
```

2. **Test error conditions**:
```go
func TestClusterDeploy_SSHError(t *testing.T) {
    mockExecutor := &MockExecutor{}
    mockExecutor.Results = []*ExecutionResult{
        {ExitCode: 1, Error: "SSH connection failed"},
    }
    
    manager := NewManager(mockExecutor)
    err := manager.DeployCluster(spec)
    
    assert.Error(t, err)
    assert.Contains(t, err.Error(), "SSH connection failed")
}
```

3. **Integration testing**:
```go
func TestFullDeployment(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping integration test")
    }
    
    // Test with real SSH connections in CI environment
}
```

## 🔒 Security Considerations

### Input Validation

Always validate user inputs:

```go
func ValidateClusterName(name string) error {
    if name == "" {
        return fmt.Errorf("cluster name cannot be empty")
    }
    if len(name) > 63 {
        return fmt.Errorf("cluster name too long")
    }
    if !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(name) {
        return fmt.Errorf("cluster name contains invalid characters")
    }
    return nil
}
```

### SSH Security

```go
// Use secure SSH configuration
sshConfig := &ssh.ClientConfig{
    User: user,
    Auth: []ssh.AuthMethod{
        ssh.PublicKeys(privateKey),
    },
    HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Use proper host key checking in production
    Timeout:         30 * time.Second,
}
```

### File Permissions

```go
// Secure file permissions for sensitive data
err := os.WriteFile(certPath, certData, 0600)  // Only owner can read/write
err := os.WriteFile(configPath, configData, 0644)  // Standard config permissions
```

## 📝 Code Style and Standards

### Naming Conventions

- **Packages**: lowercase, single words when possible
- **Types**: PascalCase
- **Functions**: PascalCase for exported, camelCase for private
- **Variables**: camelCase
- **Constants**: PascalCase or SCREAMING_SNAKE_CASE for package-level

### Error Handling

Use structured errors from `pkg/errors/`:

```go
func DeployComponent(spec ComponentSpec) error {
    if spec.GetHost() == "" {
        return &errors.ConfigurationError{
            Component: spec.GetType(),
            Field:     "host", 
            Message:   "host cannot be empty",
        }
    }
    
    if err := connectSSH(spec.GetHost()); err != nil {
        return &errors.SSHConnectionError{
            Host:  spec.GetHost(),
            Cause: err,
        }
    }
    
    return nil
}
```

### Logging

Use structured logging with levels:

```go
import "github.com/fatih/color"

// Info level
color.Green("✅ Component deployed successfully: %s", componentName)

// Warning level  
color.Yellow("⚠️  Certificate expires in 30 days: %s", certPath)

// Error level
color.Red("❌ Deployment failed: %v", err)

// Debug level (with --verbose flag)
if verbose {
    fmt.Printf("🔍 Debug: SSH command executed: %s\n", command)
}
```

## 🚀 Build and Deployment

### Development Build

```bash
# Standard build
go build -o seaweed-up .

# Build with version info
go build -ldflags "-X main.version=$(git describe --tags)" -o seaweed-up .

# Build for multiple platforms
GOOS=linux GOARCH=amd64 go build -o seaweed-up-linux-amd64 .
GOOS=darwin GOARCH=amd64 go build -o seaweed-up-darwin-amd64 .
GOOS=windows GOARCH=amd64 go build -o seaweed-up-windows-amd64.exe .
```

### Testing

```bash
# Run all tests
go test ./...

# Run tests with coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Run specific tests
go test ./pkg/cluster/...

# Integration tests (requires infrastructure)
go test -tags=integration ./...
```

### Code Quality

```bash
# Linting
golangci-lint run

# Format code
go fmt ./...

# Vet code
go vet ./...

# Check dependencies
go mod tidy
go mod verify
```

## 🤝 Contributing Guidelines

### Development Workflow

1. **Fork and Clone**
   ```bash
   git clone https://github.com/your-username/seaweed-up.git
   cd seaweed-up
   ```

2. **Create Feature Branch**
   ```bash
   git checkout -b feature/my-new-feature
   ```

3. **Development**
   - Write code following style guidelines
   - Add comprehensive tests
   - Update documentation as needed

4. **Testing**
   ```bash
   go test ./...
   go vet ./...
   golangci-lint run
   ```

5. **Commit and Push**
   ```bash
   git commit -m "feat: add new feature description"
   git push origin feature/my-new-feature
   ```

6. **Pull Request**
   - Create PR with clear description
   - Ensure CI passes
   - Address review feedback

### Commit Message Convention

Follow conventional commit format:

```
<type>(<scope>): <description>

<body>

<footer>
```

Types:
- `feat`: New features
- `fix`: Bug fixes
- `docs`: Documentation changes
- `style`: Code style changes
- `refactor`: Code refactoring
- `test`: Test additions/changes
- `chore`: Build process or tooling changes

Examples:
```
feat(security): add OAuth authentication support
fix(cluster): resolve SSH connection timeout issue  
docs(api): update deployment examples
test(monitoring): add metrics collection tests
```

## 📚 API Documentation

### Core Types Reference

#### Cluster Specification
```go
type Specification struct {
    Name          string              `yaml:"cluster_name,omitempty"`
    GlobalOptions GlobalOptions       `yaml:"global,omitempty"`
    ServerConfigs ServerConfigs       `yaml:"server_configs,omitempty"`
    MasterServers []*MasterServerSpec `yaml:"master_servers"`
    VolumeServers []*VolumeServerSpec `yaml:"volume_servers"`
    FilerServers  []*FilerServerSpec  `yaml:"filer_servers"`
    EnvoyServers  []*EnvoyServerSpec  `yaml:"envoy_servers"`
}
```

#### Task Interface
```go
type Task interface {
    Execute() error
    Rollback() error
    Description() string
    Dependencies() []string
}
```

#### Authentication Configuration
```go
type AuthConfig struct {
    Method       AuthMethod                 `json:"method"`
    Enabled      bool                       `json:"enabled"`
    JWTConfig    *JWTConfig                 `json:"jwt_config,omitempty"`
    BasicConfig  *BasicAuthConfig           `json:"basic_config,omitempty"`
    MTLSConfig   *MTLSConfig                `json:"mtls_config,omitempty"`
    APIKeyConfig *APIKeyConfig              `json:"apikey_config,omitempty"`
    Settings     map[string]interface{}     `json:"settings,omitempty"`
}
```

### Function Reference

#### Cluster Operations
```go
// Deploy a cluster
func (om *ClusterOperationManager) DeployCluster(spec *spec.Specification) error

// Upgrade cluster components
func (om *ClusterOperationManager) UpgradeCluster(clusterName, version string) error

// Scale cluster
func (om *ClusterOperationManager) ScaleOut(spec *spec.Specification, addVolume, addFiler int) error
```

#### Security Operations
```go
// Initialize CA
func (cm *CertificateManager) InitializeCA(clusterName string) error

// Generate certificates
func (cm *CertificateManager) GenerateClusterCertificates(cluster *spec.Specification) (map[string]*CertificateInfo, error)

// Initialize authentication
func (am *AuthManager) InitializeAuth(clusterName string, method AuthMethod) (*AuthConfig, error)
```

## 🐛 Debugging and Troubleshooting

### Debug Mode

Enable verbose logging:
```bash
./seaweed-up --verbose cluster deploy -f cluster.yaml
```

### Common Debug Scenarios

1. **SSH Connection Issues**
   ```go
   // Add debug logging
   if verbose {
       fmt.Printf("🔍 Connecting to %s with user %s\n", host, user)
       fmt.Printf("🔍 Using identity file: %s\n", identityFile)
   }
   ```

2. **Task Execution Failures**
   ```go
   // Task orchestrator provides detailed logs
   orchestrator.SetVerbose(true)
   err := orchestrator.Execute()
   ```

3. **Certificate Issues**
   ```bash
   # Validate certificates
   ./seaweed-up security tls validate
   
   # Check certificate details
   openssl x509 -in cert.pem -text -noout
   ```

### Profiling

```go
import _ "net/http/pprof"
import "net/http"

// Add to main function for profiling
go func() {
    log.Println(http.ListenAndServe("localhost:6060", nil))
}()
```

## 📈 Performance Considerations

### Memory Management

- Use streaming for large file operations
- Implement proper cleanup in defer statements
- Consider memory pools for frequent allocations

### Concurrency

- Use goroutines for parallel SSH operations
- Implement proper context cancellation
- Use sync.WaitGroup for coordinated operations

### Caching

- Cache SSH connections when possible
- Cache template parsing results
- Implement local component registry caching

## 🔮 Future Enhancements

### Planned Features

1. **Plugin System**: Allow custom plugins for specialized deployments
2. **Web UI**: Browser-based cluster management interface
3. **GitOps Integration**: Git-based configuration management
4. **Multi-Cloud Support**: Deploy across different cloud providers
5. **Advanced Analytics**: Historical trend analysis and capacity planning

### Extension Points

The architecture provides several extension points:

- **Custom Tasks**: Implement Task interface for new operations
- **Custom Executors**: Implement Executor interface for new deployment targets
- **Custom Auth Methods**: Add new authentication mechanisms
- **Custom Templates**: Create domain-specific templates
- **Custom Metrics**: Add application-specific monitoring

---

## 📞 Development Support

- **GitHub Issues**: Report bugs and request features
- **Discussions**: Architecture discussions and design proposals  
- **Pull Requests**: Code contributions and improvements
- **Documentation**: Help improve developer guides and examples

Thank you for contributing to SeaweedFS-up! 🚀

---

*This developer guide covers SeaweedFS-up v2.0.0 architecture. For the latest code organization and APIs, refer to the source code and inline documentation.*
