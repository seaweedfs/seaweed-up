# SeaweedFS seaweed-up Integration Testing Makefile

# Configuration
ifndef DOCKER_COMPOSE
DOCKER_COMPOSE := $(if $(shell command -v docker-compose 2>/dev/null),docker-compose,docker compose)
endif
TEST_TIMEOUT ?= 15m

# Colors for output
BLUE := \033[36m
GREEN := \033[32m
YELLOW := \033[33m
RED := \033[31m
NC := \033[0m # No Color

.PHONY: help build setup test test-single test-multi clean logs status

help: ## Show this help message
	@echo "$(BLUE)seaweed-up Integration Testing$(NC)"
	@echo ""
	@echo "Available targets:"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  $(GREEN)%-20s$(NC) %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# Build
build: ## Build seaweed-up binary
	@echo "$(YELLOW)Building seaweed-up...$(NC)"
	@cd ../.. && go build -o seaweed-up .
	@echo "$(GREEN)Build complete!$(NC)"

# Environment Setup
setup: ## Set up test environment (Docker containers)
	@echo "$(YELLOW)Setting up integration test environment...$(NC)"
	@$(DOCKER_COMPOSE) up -d
	@echo "$(BLUE)Waiting for all hosts to be ready...$(NC)"
	@./scripts/wait-for-hosts.sh
	@echo "$(GREEN)Test environment ready!$(NC)"

# Test Targets
test: build ## Run all integration tests
	@echo "$(YELLOW)Running all integration tests...$(NC)"
	@go test -v -tags=integration -timeout=$(TEST_TIMEOUT) ./...

test-single: build setup ## Run single-node deployment test
	@echo "$(YELLOW)Running single-node deployment test...$(NC)"
	@go test -v -tags=integration -timeout=$(TEST_TIMEOUT) -run TestDeploySingleNode ./...

test-multi: build setup ## Run multi-node deployment test
	@echo "$(YELLOW)Running multi-node deployment test...$(NC)"
	@go test -v -tags=integration -timeout=$(TEST_TIMEOUT) -run TestDeployMultiNode ./...

test-destroy: build setup ## Run cluster destroy test
	@echo "$(YELLOW)Running cluster destroy test...$(NC)"
	@go test -v -tags=integration -timeout=$(TEST_TIMEOUT) -run TestClusterDestroy ./...

# Cleanup
clean: ## Clean up test environment
	@echo "$(YELLOW)Cleaning up test environment...$(NC)"
	@$(DOCKER_COMPOSE) down -v --remove-orphans
	@rm -rf .ssh
	@echo "$(GREEN)Environment cleaned up!$(NC)"

# Monitoring and debugging
logs: ## Show logs from all containers
	@$(DOCKER_COMPOSE) logs --tail=50 -f

logs-host1: ## Show logs from host1
	@$(DOCKER_COMPOSE) logs --tail=100 -f target-host-1

logs-host2: ## Show logs from host2
	@$(DOCKER_COMPOSE) logs --tail=100 -f target-host-2

logs-host3: ## Show logs from host3
	@$(DOCKER_COMPOSE) logs --tail=100 -f target-host-3

status: ## Show status of all containers
	@echo "$(BLUE)Container Status:$(NC)"
	@$(DOCKER_COMPOSE) ps
	@echo ""
	@echo "$(BLUE)Host Connectivity:$(NC)"
	@nc -z 172.28.0.10 22 && echo "host1 (172.28.0.10:22): accessible" || echo "host1 (172.28.0.10:22): not accessible"
	@nc -z 172.28.0.11 22 && echo "host2 (172.28.0.11:22): accessible" || echo "host2 (172.28.0.11:22): not accessible"
	@nc -z 172.28.0.12 22 && echo "host3 (172.28.0.12:22): accessible" || echo "host3 (172.28.0.12:22): not accessible"

# Development targets
shell-host1: ## Open shell in host1 container
	@docker exec -it seaweed-up-host1 bash

shell-host2: ## Open shell in host2 container
	@docker exec -it seaweed-up-host2 bash

shell-host3: ## Open shell in host3 container
	@docker exec -it seaweed-up-host3 bash

# SSH targets (for debugging)
ssh-host1: ## SSH into host1
	@ssh -i .ssh/id_rsa_test -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null root@172.28.0.10

ssh-host2: ## SSH into host2
	@ssh -i .ssh/id_rsa_test -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null root@172.28.0.11

ssh-host3: ## SSH into host3
	@ssh -i .ssh/id_rsa_test -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null root@172.28.0.12

# CI targets
ci-test: ## Run tests in CI environment
	@echo "$(YELLOW)Running CI integration tests...$(NC)"
	@make build
	@make setup
	@make test || (make clean && exit 1)
	@make clean

# Quick development cycle
dev: build setup ## Quick development setup
	@echo "$(GREEN)Development environment ready!$(NC)"
	@echo "Run 'make test' to run integration tests"
	@echo "Run 'make shell-host1' to access host1"
	@echo "Run 'make clean' when done"

