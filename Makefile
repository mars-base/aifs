.PHONY: build run test clean install lint images image-pg image-backup

# Variables
BINARY_NAME := aifs
BUILD_DIR := build
GO := go
GOFMT := gofmt

# Version info
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)"

# Default target
all: build

# Build
build:
	$(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/aifs

# Install to $GOPATH/bin
install:
	$(GO) install $(LDFLAGS) ./cmd/aifs

# Run
run: build
	./$(BUILD_DIR)/$(BINARY_NAME)

# Test
test:
	$(GO) test -v -race ./...

# Test coverage
cover:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

# Clean
clean:
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html

# Code formatting
fmt:
	$(GOFMT) -s -w .

# Lint
lint:
	golangci-lint run ./...

# Cross-compile (Linux/macOS/Windows amd64)
release:
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64   ./cmd/aifs
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64  ./cmd/aifs
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe ./cmd/aifs

# Dev: build and run setup
dev-setup: build
	./$(BUILD_DIR)/$(BINARY_NAME) setup

# Dev: build and run status
dev-status: build
	./$(BUILD_DIR)/$(BINARY_NAME) status

# ─── Container images ────────────────────────────────────────────────
# Tags must match the defaults in internal/config/config.go so that the
# running aifs binary picks them up via imageExists (no rebuild on start):
#   PG:     ghcr.io/mars-base/aifs/aifs-pg:18-2.58.0
#   backup: ghcr.io/mars-base/aifs/aifs-backup:2.58.0
# Override with: make images PG_TAG=... BACKUP_TAG=...
PG_TAG     ?= ghcr.io/mars-base/aifs/aifs-pg:18-2.58.0
BACKUP_TAG ?= ghcr.io/mars-base/aifs/aifs-backup:2.58.0
EMBED_DIR  := embed

# On rootless Linux podman needs XDG_RUNTIME_DIR to reach the user API socket.
# On macOS podman runs inside a machine VM and does not need this.
PODMAN := $(shell command -v podman 2>/dev/null)
ifeq ($(PODMAN),)
$(error podman not found in PATH — install it first (run scripts/install.sh))
endif

# Export XDG_RUNTIME_DIR for rootless Linux only (skip on macOS where it is
# already set by the login session / not used by the machine VM).
UNAME_S := $(shell uname -s)
ifneq ($(UNAME_S),Darwin)
export XDG_RUNTIME_DIR := $(shell echo $${XDG_RUNTIME_DIR:-/run/user/$$(id -u)})
endif

# Build the PostgreSQL + pgBackRest image.
image-pg:
	$(PODMAN) build -t $(PG_TAG) -f $(EMBED_DIR)/Containerfile $(EMBED_DIR)

# Build the pgBackRest backup image (runs as the postgres user, uid 999).
image-backup:
	$(PODMAN) build -t $(BACKUP_TAG) -f $(EMBED_DIR)/backup.Containerfile $(EMBED_DIR)

# Build both container images.
images: image-pg image-backup
	@echo "Built images:"
	@$(PODMAN) images --format '  {{.Repository}}:{{.Tag}}  ({{.Size}})' | grep -E 'aifs-pg|aifs-backup' || true
