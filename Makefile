.PHONY: build build-cli build-gui run test clean install lint images image-pg image-backup gui gui-dev

# Variables
BINARY_NAME := aifs
BUILD_DIR := build
GO := go
GOFMT := gofmt

# Version info
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)"

# Default target: build both CLI and GUI
all: build

# Build CLI + GUI together, output both to build/
build: build-cli build-gui

# Build CLI only
build-cli:
	@echo "  • Building CLI ($(VERSION))..."
	@$(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/aifs
	@echo "  • CLI built: $(BUILD_DIR)/$(BINARY_NAME)"

# Build GUI only (requires wails: go install github.com/wailsapp/wails/v2/cmd/wails@latest)
# wails always outputs to gui/build/bin/; copy to build/ next to the CLI so
# resolveAifsBin() can find the aifs sibling at runtime.
build-gui:
	@echo "  • Building GUI..."
	@cd gui && wails build -ldflags "-X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)"
	@mkdir -p $(BUILD_DIR)
	@if [ -f gui/build/bin/aifs-gui ]; then \
		cp gui/build/bin/aifs-gui $(BUILD_DIR)/$(BINARY_NAME)-gui; \
	elif [ -f gui/build/bin/aifs-gui.app/Contents/MacOS/aifs-gui ]; then \
		cp gui/build/bin/aifs-gui.app/Contents/MacOS/aifs-gui $(BUILD_DIR)/$(BINARY_NAME)-gui; \
	else \
		echo "ERROR: cannot find aifs-gui binary"; exit 1; \
	fi
	@echo "  • GUI built: $(BUILD_DIR)/$(BINARY_NAME)-gui"

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

# ─── Single-arch dev builds (fast, no QEMU) ────────────────────────
# Build the PostgreSQL + pgBackRest image for the host architecture only.
image-pg:
	$(PODMAN) build -t $(PG_TAG) -f $(EMBED_DIR)/Containerfile $(EMBED_DIR)

# Build the pgBackRest backup image for the host architecture only.
image-backup:
	$(PODMAN) build -t $(BACKUP_TAG) -f $(EMBED_DIR)/backup.Containerfile $(EMBED_DIR)

# Build both container images (single arch).
images: image-pg image-backup
	@echo "Built images:"
	@$(PODMAN) images --format '  {{.Repository}}:{{.Tag}}  ({{.Size}})' | grep -E 'aifs-pg|aifs-backup' || true

# ─── Multi-arch builds (linux/amd64 + linux/arm64) ───────────────────
# Requires qemu-user-static for cross-platform emulation:
#   macOS:  brew install qemu
#   Linux:  apt install qemu-user-static  (or equivalent)
MULTI_PLATFORMS := linux/amd64,linux/arm64

# Build multi-arch PG image (manifest list).
image-pg-multi:
	$(PODMAN) build --platform $(MULTI_PLATFORMS) \
		--manifest $(PG_TAG) \
		-f $(EMBED_DIR)/Containerfile $(EMBED_DIR)

# Build multi-arch backup image (manifest list).
image-backup-multi:
	$(PODMAN) build --platform $(MULTI_PLATFORMS) \
		--manifest $(BACKUP_TAG) \
		-f $(EMBED_DIR)/backup.Containerfile $(EMBED_DIR)

# Build both multi-arch images.
images-multi: image-pg-multi image-backup-multi
	@echo "Built multi-arch manifests:"
	@$(PODMAN) manifest inspect $(PG_TAG) 2>/dev/null | grep -E '"architecture"' || true
	@$(PODMAN) manifest inspect $(BACKUP_TAG) 2>/dev/null | grep -E '"architecture"' || true

# Push multi-arch manifest lists to registry.
images-push: images-multi
	$(PODMAN) manifest push --all $(PG_TAG) docker://$(PG_TAG)
	$(PODMAN) manifest push --all $(BACKUP_TAG) docker://$(BACKUP_TAG)
	@echo "Pushed multi-arch images to registry"

# ── GUI dev ───────────────────────────────────────────────────────────────────

# Run the GUI in dev mode with hot-reload (frontend + backend live reload)
gui-dev:
	cd gui && wails dev
