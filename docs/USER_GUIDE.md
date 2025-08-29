# SeaweedFS-Up User Guide

**Version:** 2.0.0  
**Last Updated:** August 2025

Welcome to SeaweedFS-up, the comprehensive enterprise-grade cluster management platform for SeaweedFS. This guide will walk you through all the features and capabilities of the tool.

## 🚀 Quick Start

### Installation

```bash
# Clone and build seaweed-up
git clone https://github.com/seaweedfs/seaweed-up.git
cd seaweed-up
go build -o seaweed-up .

# Initialize global environment
./seaweed-up env init
```

### Your First Cluster

```bash
# 1. Create a cluster configuration
./seaweed-up template generate single-node -o my-cluster.yaml

# 2. Initialize security (optional but recommended)
./seaweed-up security tls init my-cluster
./seaweed-up security auth init my-cluster --method=jwt

# 3. Deploy the cluster
./seaweed-up cluster deploy -f my-cluster.yaml

# 4. Check cluster status
./seaweed-up cluster status my-cluster
```

## 📋 Table of Contents

- [🏗️ Cluster Management](#cluster-management)
- [🔧 Component Management](#component-management)
- [🔒 Security & Authentication](#security--authentication)
- [📊 Monitoring & Alerting](#monitoring--alerting)
- [📝 Template System](#template-system)
- [🌍 Environment Management](#environment-management)
- [⚙️ Configuration Reference](#configuration-reference)
- [🛠️ Advanced Operations](#advanced-operations)
- [📈 Best Practices](#best-practices)
- [🔍 Troubleshooting](#troubleshooting)

---

## 🏗️ Cluster Management

SeaweedFS-up provides comprehensive cluster lifecycle management capabilities.

### Deploy a New Cluster

```bash
# Deploy from configuration file
./seaweed-up cluster deploy -f cluster.yaml

# Deploy with specific options
./seaweed-up cluster deploy -f cluster.yaml --user=admin --identity-file=~/.ssh/id_rsa

# Deploy with TLS enabled
./seaweed-up cluster deploy -f cluster.yaml --tls

# Dry run (validate without deploying)
./seaweed-up cluster deploy -f cluster.yaml --dry-run
```

### Monitor Cluster Status

```bash
# Basic cluster status
./seaweed-up cluster status my-cluster

# Detailed status with resource usage
./seaweed-up cluster status my-cluster --detailed

# Watch status in real-time
./seaweed-up cluster status my-cluster --watch
```

### Upgrade Clusters

```bash
# Rolling upgrade to specific version
./seaweed-up cluster upgrade my-cluster --version=3.55

# Upgrade to latest version
./seaweed-up cluster upgrade my-cluster --version=latest

# Staged upgrade with validation
./seaweed-up cluster upgrade my-cluster --version=3.55 --staged --validate
```

### Scale Clusters

```bash
# Scale out - add volume servers
./seaweed-up cluster scale-out -f cluster.yaml --add-volume=2

# Scale out - add filer servers  
./seaweed-up cluster scale-out -f cluster.yaml --add-filer=1

# Scale in - remove servers (careful!)
./seaweed-up cluster scale-in my-cluster --remove-volume=1
```

### Cluster Operations

```bash
# List all clusters
./seaweed-up cluster list

# Destroy a cluster
./seaweed-up cluster destroy my-cluster

# Backup cluster configuration
./seaweed-up cluster backup my-cluster -o backup.tar.gz

# Restore from backup
./seaweed-up cluster restore -f backup.tar.gz
```

---

## 🔧 Component Management

Manage SeaweedFS binary components with version control and dependency management.

### Install Components

```bash
# Install latest version
./seaweed-up component install weed

# Install specific version
./seaweed-up component install weed:3.55

# Install multiple components
./seaweed-up component install weed:3.55 envoy:1.28.0
```

### Manage Component Versions

```bash
# List installed components
./seaweed-up component list

# List available versions
./seaweed-up component list --available weed

# Update to latest version
./seaweed-up component update weed

# Update all components
./seaweed-up component update --all
```

### Component Operations

```bash
# Uninstall component
./seaweed-up component uninstall weed:3.54

# Check component status
./seaweed-up component status weed

# Clean up old versions
./seaweed-up component cleanup
```

---

## 🔒 Security & Authentication

Enterprise-grade security with TLS certificates and multiple authentication methods.

### TLS Certificate Management

```bash
# Initialize Certificate Authority
./seaweed-up security tls init my-cluster \
  --organization="My Company" \
  --country=US \
  --validity=10

# Generate certificates for all components
./seaweed-up security tls generate -f cluster.yaml

# List certificates
./seaweed-up security tls list

# Validate certificates
./seaweed-up security tls validate

# Clean up expired certificates
./seaweed-up security tls cleanup
```

### Authentication Configuration

#### JWT Authentication
```bash
# Initialize JWT authentication
./seaweed-up security auth init my-cluster --method=jwt

# JWT is automatically configured with:
# - 8-hour token expiration
# - Secure random secret key
# - Refresh token support
```

#### API Key Authentication
```bash
# Initialize API key authentication
./seaweed-up security auth init my-cluster --method=apikey

# Create API keys
./seaweed-up security auth key create my-cluster \
  --name=admin-key \
  --permissions=read,write,admin \
  --expires=720h

# List API keys
./seaweed-up security auth key list my-cluster

# Revoke API key
./seaweed-up security auth key revoke my-cluster ak_123456789
```

#### Basic Authentication
```bash
# Initialize basic authentication
./seaweed-up security auth init my-cluster --method=basic

# Create users
./seaweed-up security auth user create admin \
  --password=secure123 \
  --roles=admin \
  --permissions=read,write,admin
```

#### Mutual TLS (mTLS)
```bash
# Initialize mTLS authentication
./seaweed-up security auth init my-cluster --method=mtls

# mTLS uses existing TLS certificates for client authentication
```

### User Management

```bash
# Create user
./seaweed-up security auth user create username \
  --roles=user,operator \
  --permissions=read,write

# List users
./seaweed-up security auth user list

# Delete user
./seaweed-up security auth user delete username
```

### Security Operations

```bash
# Security audit
./seaweed-up security audit

# Security hardening
./seaweed-up security harden -f cluster.yaml

# Check authentication status
./seaweed-up security auth status my-cluster
```

---

## 📊 Monitoring & Alerting

Real-time monitoring with intelligent alerting and interactive dashboards.

### Metrics Collection

```bash
# Start metrics collection
./seaweed-up monitoring metrics start my-cluster

# List available metrics
./seaweed-up monitoring metrics list

# Query specific metrics
./seaweed-up monitoring metrics query \
  --metric=cpu_usage \
  --host=server1 \
  --duration=1h
```

### Alerting Rules

```bash
# Create alert rule
./seaweed-up monitoring alerts create \
  --name=high-cpu \
  --metric=cpu_usage \
  --condition=">80" \
  --severity=warning \
  --summary="High CPU usage on {{.Host}}"

# List alert rules  
./seaweed-up monitoring alerts list

# Test alert rule
./seaweed-up monitoring alerts test high-cpu
```

### Interactive Dashboard

```bash
# Launch real-time dashboard
./seaweed-up monitoring dashboard

# Dashboard shows:
# - Cluster health overview
# - Resource usage graphs  
# - Active alerts
# - Component status
```

### Alert Notifications

Configure multiple notification channels:

```yaml
# In cluster template or configuration
monitoring:
  alerting:
    notifiers:
      - type: console
        name: console-notify
        
      - type: email
        name: email-notify
        config:
          smtp_host: smtp.company.com
          smtp_port: 587
          from: alerts@company.com
          to: ["ops@company.com"]
          
      - type: slack
        name: slack-notify
        config:
          webhook_url: "https://hooks.slack.com/services/..."
          channel: "#alerts"
```

---

## 📝 Template System

Pre-configured templates for rapid deployment with best practices built-in.

### Built-in Templates

```bash
# List available templates
./seaweed-up template list

# Available templates:
# - single-node: Simple single-node setup for development
# - development: Multi-node development cluster
# - production: Production-ready cluster with HA
# - high-availability: Enterprise HA setup with monitoring
```

### Generate from Templates

```bash
# Generate single-node configuration
./seaweed-up template generate single-node \
  --output my-cluster.yaml \
  --set master.host=192.168.1.10 \
  --set cluster.name=my-cluster

# Generate production configuration
./seaweed-up template generate production \
  --output prod-cluster.yaml \
  --set cluster.name=prod-cluster \
  --set monitoring.enabled=true \
  --set security.tls=true
```

### Template Validation

```bash
# Validate template
./seaweed-up template validate my-template.yaml

# Validate with parameter checking
./seaweed-up template validate my-template.yaml --check-params
```

### Custom Templates

```bash
# Create custom template
./seaweed-up template create \
  --name=custom-template \
  --description="My custom configuration" \
  --base=production \
  --file=custom-template.yaml
```

---

## 🌍 Environment Management

Manage multiple deployment environments and contexts.

### Environment Profiles

```bash
# Initialize environment
./seaweed-up env init

# Create environment profile
./seaweed-up env create development \
  --description="Development environment"

# Switch environment
./seaweed-up env use development

# List environments
./seaweed-up env list

# Current environment status
./seaweed-up env status
```

---

## ⚙️ Configuration Reference

### Cluster Configuration Structure

```yaml
# cluster.yaml
cluster_name: "my-cluster"

global:
  enable_tls: true
  dir:
    conf: "/etc/seaweed"
    data: "/opt/seaweed"
  replication: "001"
  volumeSizeLimitMB: 5000

server_configs:
  master_server:
    default_replication: "001"
    log_level: "info"
  volume_server:
    max_volumes: 100
    log_level: "info"
  filer_server:
    collection: "default"
    log_level: "info"

master_servers:
  - ip: "192.168.1.10"
    port: 9333
    peers: ["192.168.1.10:9333", "192.168.1.11:9333"]
  - ip: "192.168.1.11"  
    port: 9333
    peers: ["192.168.1.10:9333", "192.168.1.11:9333"]

volume_servers:
  - ip: "192.168.1.20"
    port: 8080
    masters: ["192.168.1.10:9333", "192.168.1.11:9333"]
    folders:
      - folder: "/data1"
      - folder: "/data2"
  - ip: "192.168.1.21"
    port: 8080
    masters: ["192.168.1.10:9333", "192.168.1.11:9333"]
    folders:
      - folder: "/data1"
      - folder: "/data2"

filer_servers:
  - ip: "192.168.1.30"
    port: 8888
    masters: ["192.168.1.10:9333", "192.168.1.11:9333"]
    s3: true
    s3_port: 8333
    webdav: true
    webdav_port: 7333

# Optional: Envoy proxy for load balancing
envoy_servers:
  - ip: "192.168.1.40"
    port: 8000
    targets: ["192.168.1.30:8888"]
```

### Security Configuration

```yaml
# Security settings can be embedded in templates
security:
  tls:
    enabled: true
    ca_organization: "My Company"
    validity_years: 5
  
  authentication:
    method: "jwt"  # jwt, basic, apikey, mtls, none
    
    jwt:
      issuer: "seaweed-up-my-cluster"
      expiration_minutes: 480
      
    apikey:
      header_name: "X-API-Key"
      query_param: "api_key"
```

### Monitoring Configuration

```yaml
monitoring:
  metrics:
    enabled: true
    collection_interval: "30s"
    retention: "7d"
    
  alerting:
    enabled: true
    rules:
      - name: "high-cpu"
        metric: "cpu_usage"
        condition: ">85"
        severity: "critical"
        summary: "High CPU usage on {{.Host}}"
        
    notifiers:
      - type: "console"
        name: "console-alerts"
      - type: "email"
        name: "email-alerts"
        config:
          smtp_host: "smtp.company.com"
          from: "alerts@company.com"
          to: ["ops@company.com"]
```

---

## 🛠️ Advanced Operations

### Task Orchestration

SeaweedFS-up uses an advanced task orchestration system for complex operations:

```bash
# All cluster operations use task orchestration:
# - Deploy: Creates and executes deployment tasks
# - Upgrade: Orchestrates rolling upgrade tasks
# - Scale: Manages scaling operation tasks
# - Each task supports rollback on failure
```

### Rolling Upgrades

```bash
# Rolling upgrade with validation
./seaweed-up cluster upgrade my-cluster --version=3.55 \
  --staged \
  --validate \
  --max-unavailable=1 \
  --timeout=300s

# Upgrade stages:
# 1. Pre-upgrade validation
# 2. Component-by-component upgrade
# 3. Health verification at each stage
# 4. Automatic rollback on failure
```

### Dynamic Scaling

```bash
# Scale out with load balancing
./seaweed-up cluster scale-out -f cluster.yaml \
  --add-volume=2 \
  --rebalance \
  --wait-for-ready

# Scale operations include:
# - Resource validation
# - Load balancing integration  
# - Health monitoring
# - Gradual traffic shifting
```

### Backup and Recovery

```bash
# Create cluster backup
./seaweed-up cluster backup my-cluster \
  --output backup-$(date +%Y%m%d).tar.gz \
  --include-data \
  --compress

# Restore cluster
./seaweed-up cluster restore \
  --file backup-20250829.tar.gz \
  --target-cluster restored-cluster \
  --validate-before-restore
```

---

## 📈 Best Practices

### Security Best Practices

1. **Always Enable TLS**
   ```bash
   # Initialize TLS for all clusters
   ./seaweed-up security tls init my-cluster
   ```

2. **Use Strong Authentication**
   ```bash
   # Prefer JWT or mTLS over basic auth
   ./seaweed-up security auth init my-cluster --method=jwt
   ```

3. **Regular Security Audits**
   ```bash
   # Run security audits regularly
   ./seaweed-up security audit
   ```

4. **Certificate Rotation**
   ```bash
   # Set up certificate rotation
   ./seaweed-up security tls cleanup  # Remove expired
   ./seaweed-up security tls generate --force  # Regenerate
   ```

### Deployment Best Practices

1. **Use Templates**
   ```bash
   # Start with production template
   ./seaweed-up template generate production -o cluster.yaml
   ```

2. **Validate Before Deploy**
   ```bash
   # Always dry-run first
   ./seaweed-up cluster deploy -f cluster.yaml --dry-run
   ```

3. **Enable Monitoring**
   ```bash
   # Set up monitoring from day one
   ./seaweed-up monitoring metrics start my-cluster
   ```

4. **Plan for Scaling**
   ```bash
   # Design for horizontal scaling
   ./seaweed-up cluster scale-out -f cluster.yaml --add-volume=2
   ```

### Operational Best Practices

1. **Regular Status Checks**
   ```bash
   # Monitor cluster health
   ./seaweed-up cluster status my-cluster --watch
   ```

2. **Staged Upgrades**
   ```bash
   # Use staged rolling upgrades
   ./seaweed-up cluster upgrade my-cluster --staged --validate
   ```

3. **Backup Regularly**
   ```bash
   # Automated backup schedule
   ./seaweed-up cluster backup my-cluster -o daily-backup.tar.gz
   ```

---

## 🔍 Troubleshooting

### Common Issues and Solutions

#### Connection Issues

**Problem:** SSH connection failures
```bash
# Solution: Check SSH configuration
./seaweed-up cluster deploy -f cluster.yaml --user=root --identity-file=~/.ssh/id_rsa
```

**Problem:** Certificate validation errors
```bash
# Solution: Regenerate certificates
./seaweed-up security tls generate -f cluster.yaml --force
```

#### Deployment Issues

**Problem:** Component startup failures
```bash
# Solution: Check component status and logs
./seaweed-up cluster status my-cluster --detailed
./seaweed-up component status weed
```

**Problem:** Authentication failures
```bash
# Solution: Verify authentication configuration
./seaweed-up security auth status my-cluster
```

#### Performance Issues

**Problem:** High resource usage
```bash
# Solution: Check metrics and scale if needed
./seaweed-up monitoring metrics query --metric=cpu_usage
./seaweed-up cluster scale-out -f cluster.yaml --add-volume=1
```

### Debug Mode

```bash
# Enable verbose logging for troubleshooting
./seaweed-up --verbose cluster deploy -f cluster.yaml

# Check logs location
ls ~/.seaweed-up/logs/
```

### Support and Community

- **GitHub Issues:** [https://github.com/seaweedfs/seaweed-up/issues](https://github.com/seaweedfs/seaweed-up/issues)
- **SeaweedFS Community:** [https://github.com/seaweedfs/seaweedfs](https://github.com/seaweedfs/seaweedfs)
- **Documentation:** [https://github.com/seaweedfs/seaweed-up/docs](https://github.com/seaweedfs/seaweed-up/docs)

---

## 🎯 Next Steps

After reading this guide:

1. **Try the Quick Start** - Deploy your first cluster
2. **Explore Templates** - Use production-ready configurations  
3. **Set up Security** - Enable TLS and authentication
4. **Configure Monitoring** - Set up alerts and dashboards
5. **Plan for Production** - Review best practices and scaling

Happy clustering with SeaweedFS-up! 🚀

---

*This user guide covers SeaweedFS-up v2.0.0. For the latest updates and features, check the project repository.*
