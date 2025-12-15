# SeaweedFS seaweed-up Integration Tests

This directory contains integration tests for the `seaweed-up` CLI tool. The tests verify that `seaweed-up` can successfully deploy, manage, and destroy SeaweedFS clusters.

## Overview

The integration tests use Docker containers to simulate target servers. Each container runs Ubuntu with SSH enabled, allowing `seaweed-up` to connect and deploy SeaweedFS components as it would on real servers.

## Prerequisites

- Docker and Docker Compose
- Go 1.21+
- SSH client tools (for debugging)
- netcat (`nc`) for connectivity checks

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

The Docker Compose setup creates three Ubuntu containers:

| Host   | IP Address    | Purpose                    |
|--------|---------------|----------------------------|
| host1  | 172.28.0.10   | Master, Filer (single-node tests) |
| host2  | 172.28.0.11   | Volume server              |
| host3  | 172.28.0.12   | Volume server              |

All hosts are configured with:
- SSH server (port 22)
- Root login enabled
- Password authentication (password: `testpassword`)
- Automatically generated SSH keys for tests

## Test Structure

```
test/integration/
├── docker-compose.yml      # Docker environment definition
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

## Known Limitations

1. **systemd**: The containers use a simplified init system, not full systemd. Services are managed via direct process control.
2. **Network**: Containers use a custom bridge network (172.28.0.0/16). This may conflict with existing Docker networks.
3. **Privileged mode**: Containers run in privileged mode to support certain operations.

## Troubleshooting

### Containers won't start
```bash
# Check Docker logs
docker logs seaweed-up-host1

# Verify network
docker network inspect seaweed-up_seaweed-up-net
```

### SSH connection refused
```bash
# Wait for SSH to be ready
./scripts/wait-for-hosts.sh

# Check SSH is running in container
docker exec seaweed-up-host1 ps aux | grep sshd
```

### Tests fail with timeout
- Increase `TEST_TIMEOUT` in Makefile
- Check if hosts are accessible: `make status`
- Review container logs: `make logs`

