# Binary name
BINARY_NAME=local-agent-on-steroids

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOFMT=$(GOCMD) fmt
GOVET=$(GOCMD) vet

# Build parameters
LDFLAGS=-ldflags "-s -w"

# Output directories
BUILD_DIR=build
MACOS_DIR=$(BUILD_DIR)/macos
LINUX_DIR=$(BUILD_DIR)/linux

.DEFAULT_GOAL := help
.PHONY: all build build-macos build-linux clean test fmt vet help

all: clean build

## help: Display this help message
help:
	@echo "Available targets:"
	@echo "  make build         - Build binary for current platform"
	@echo "  make build-macos   - Build binary for macOS (arm64 and amd64)"
	@echo "  make build-linux   - Build binary for Linux (amd64 and arm64)"
	@echo "  make all           - Clean and build for current platform"
	@echo "  make clean         - Remove build artifacts"
	@echo "  make test          - Run tests"
	@echo "  make fmt           - Format code"
	@echo "  make vet           - Run go vet"

## build: Build binary for current platform
build:
	@echo "Building for current platform..."
	$(GOBUILD) $(LDFLAGS) -o $(BINARY_NAME) .
	@echo "Build complete: $(BINARY_NAME)"

## build-macos: Build binaries for macOS
build-macos:
	@echo "Building for macOS..."
	@mkdir -p $(MACOS_DIR)
	@echo "  - Building macOS ARM64..."
	GOOS=darwin GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(MACOS_DIR)/$(BINARY_NAME)-darwin-arm64 .
	@echo "  - Building macOS AMD64..."
	GOOS=darwin GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(MACOS_DIR)/$(BINARY_NAME)-darwin-amd64 .
	@echo "macOS builds complete:"
	@ls -lh $(MACOS_DIR)

## build-linux: Build binaries for Linux
build-linux:
	@echo "Building for Linux..."
	@mkdir -p $(LINUX_DIR)
	@echo "  - Building Linux AMD64..."
	GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(LINUX_DIR)/$(BINARY_NAME)-linux-amd64 .
	@echo "  - Building Linux ARM64..."
	GOOS=linux GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(LINUX_DIR)/$(BINARY_NAME)-linux-arm64 .
	@echo "Linux builds complete:"
	@ls -lh $(LINUX_DIR)

## build-all: Build binaries for all platforms
build-all: build-macos build-linux
	@echo "All builds complete!"
	@echo ""
	@echo "macOS binaries:"
	@ls -lh $(MACOS_DIR)
	@echo ""
	@echo "Linux binaries:"
	@ls -lh $(LINUX_DIR)

## clean: Remove build artifacts
clean:
	@echo "Cleaning build artifacts..."
	$(GOCLEAN)
	@rm -f $(BINARY_NAME)
	@rm -rf $(BUILD_DIR)
	@echo "Clean complete"

## test: Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -v ./...

## fmt: Format Go code
fmt:
	@echo "Formatting code..."
	$(GOFMT) ./...

## vet: Run go vet
vet:
	@echo "Running go vet..."
	$(GOVET) ./...

## deps: Download dependencies
deps:
	@echo "Downloading dependencies..."
	$(GOCMD) mod download
	$(GOCMD) mod tidy

## install: Install binary to GOPATH/bin
install: build
	@echo "Installing $(BINARY_NAME)..."
	$(GOCMD) install

## run: Build and run for current platform
run: build
	@echo "Running $(BINARY_NAME)..."
	./$(BINARY_NAME) --help
