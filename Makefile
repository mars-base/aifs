.PHONY: build run test clean install lint

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
