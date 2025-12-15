# SeaweedFS seaweed-up Integration Tests

This directory contains integration tests for the `seaweed-up` CLI tool. The tests verify that `seaweed-up` can successfully deploy, manage, and destroy SeaweedFS clusters.

## Overview

The integration tests use Docker containers with **systemd** to simulate real servers. Each container runs Ubuntu 22.04 with full systemd support, allowing `seaweed-up` to deploy SeaweedFS components using systemd service files exactly as it would on real servers.

## Prerequisites

- **Docker** (with Docker Compose v2)
- **Go 1.21+**
- **SSH client tools** (for debugging)
- **netcat** (`nc`) for connectivity checks

### Docker Requirements

The tests use `jrei/systemd-ubuntu:22.04` images which require:
- **Privileged mode**: Containers run with `--privileged` flag
- **cgroup access**: Mounts `/sys/fs/cgroup` for systemd
- **tmpfs mounts**: For `/run` and `/run/lock`

### Why Systemd?

SeaweedFS installation via `seaweed-up` creates systemd service files to manage the SeaweedFS processes. The containers must have full systemd support (running as PID 1) to:
- Create and enable systemd service files
- Start/stop/restart SeaweedFS services
- Manage service dependencies
- Handle process supervision

## Quick Start

```bash
# Run all integration tests
make test

# Run specific test
make test-single    # Single-node deployment test
make test-multi     # Multi-node deployment test
make test-destroy   # Cluster destroy test

# Clean up
make clean
```

## Test Environment

The Docker Compose setup creates three systemd-enabled Ubuntu containers:

| Host   | IP Address    | Purpose                           |
|--------|---------------|-----------------------------------|
| host1  | 172.28.0.10   | Master, Filer (single-node tests) |
| host2  | 172.28.0.11   | Volume server                     |
| host3  | 172.28.0.12   | Volume server                     |

### Container Configuration

Each container is configured with:
- **Ubuntu 22.04** with systemd as PID 1
- **SSH server** managed by systemd (port 22)
- **Root login** enabled
- **Privileged mode** for systemd support
- **Automatically generated SSH keys** for passwordless access

### Container Initialization

The containers go through this initialization sequence:
1. Start with systemd as PID 1
2. Wait for systemd to reach "running" or "degraded" state
3. Install SSH server via apt
4. Configure SSH for root login
5. Start SSH service via systemctl
6. Setup SSH keys for passwordless authentication

## Test Structure

```
test/integration/
├── docker-compose.yml      # Systemd-enabled Docker environment
├── framework.go            # Test utilities and helpers
├── deploy_test.go          # Deployment tests
├── Makefile                # Test automation
├── scripts/
│   └── wait-for-hosts.sh   # Wait for containers to be ready
├── testdata/
│   ├── cluster-single.yaml # Single-node cluster config
│   └── cluster-multi.yaml  # Multi-node cluster config
└── README.md               # This file
```

## Test Cases

### TestDeploySingleNode

Tests deploying a single-node SeaweedFS cluster:
1. Deploy cluster using `cluster-single.yaml`
2. Verify master server is running on port 9333
3. Verify volume server is running on port 8382
4. Verify filer server is running on port 8888
5. Check cluster status

### TestDeployMultiNode

Tests deploying a multi-node SeaweedFS cluster:
1. Deploy cluster using `cluster-multi.yaml`
2. Verify master server on host1
3. Verify volume servers on host2 and host3
4. Verify filer server on host1
5. List clusters and verify output

### TestClusterDestroy

Tests the cluster destroy functionality:
1. Deploy a cluster
2. Destroy the cluster
3. Verify services are stopped

## Debugging

### View container logs
```bash
make logs           # All containers
make logs-host1     # Just host1
```

### Access container shell
```bash
make shell-host1    # Docker exec into host1
make ssh-host1      # SSH into host1 (requires setup first)
```

### Check systemd status
```bash
# Check if systemd is running
docker exec seaweed-up-host1 systemctl is-system-running

# List all services
docker exec seaweed-up-host1 systemctl list-units --type=service

# Check SeaweedFS service status
docker exec seaweed-up-host1 systemctl status seaweed_master0
```

### Check status
```bash
make status
```

## Manual Testing

```bash
# Setup the environment
make setup

# Build seaweed-up
make build

# Run seaweed-up manually
../../seaweed-up cluster deploy test \
  -f testdata/cluster-single.yaml \
  -u root \
  --identity .ssh/id_rsa_test \
  --yes

# Clean up when done
make clean
```

## CI Integration

For CI/CD pipelines:

```bash
# Run all tests with automatic cleanup
make ci-test
```

### GitHub Actions

The CI workflow (`.github/workflows/integration-tests.yml`) automatically:
1. Starts the Docker containers
2. Waits for systemd to initialize (~30-60 seconds)
3. Installs and configures SSH
4. Sets up SSH keys
5. Runs deployment tests
6. Collects logs on failure
7. Cleans up containers

## Known Limitations

1. **Systemd initialization time**: Containers take 30-60 seconds for systemd to fully initialize. The tests include appropriate waits.
2. **Network**: Containers use a custom bridge network (172.28.0.0/16). This may conflict with existing Docker networks.
3. **Privileged mode**: Containers run in privileged mode to support systemd, which has security implications.
4. **Resource usage**: Each container with systemd uses more memory than a minimal container (~200-300MB per container).

## Troubleshooting

### Containers won't start
```bash
# Check Docker logs
docker logs seaweed-up-host1

# Verify network
docker network inspect seaweed-up_seaweed-up-net

# Check if cgroups v2 is available (required for systemd)
cat /sys/fs/cgroup/cgroup.controllers
```

### Systemd not starting
```bash
# Check systemd status
docker exec seaweed-up-host1 systemctl is-system-running

# View systemd journal
docker exec seaweed-up-host1 journalctl -xe

# Check for failed services
docker exec seaweed-up-host1 systemctl --failed
```

### SSH connection refused
```bash
# Wait for SSH to be ready
./scripts/wait-for-hosts.sh

# Check if SSH service is running
docker exec seaweed-up-host1 systemctl status ssh

# Check SSH is listening
docker exec seaweed-up-host1 ss -tlnp | grep 22
```

### SeaweedFS service not starting
```bash
# Check service status
docker exec seaweed-up-host1 systemctl status seaweed_master0

# View service logs
docker exec seaweed-up-host1 journalctl -u seaweed_master0

# Check service file
docker exec seaweed-up-host1 cat /etc/systemd/system/seaweed_master0.service
```

### Tests fail with timeout
- Increase `TEST_TIMEOUT` in Makefile
- Check if hosts are accessible: `make status`
- Review container logs: `make logs`
- Ensure Docker has enough resources (memory, CPU)

## Docker Image

The tests use `jrei/systemd-ubuntu:22.04` from Docker Hub. This image:
- Runs systemd as PID 1
- Requires privileged mode
- Requires cgroup mounts
- Is based on Ubuntu 22.04 LTS

For more information: https://github.com/j8r/dockerfiles
