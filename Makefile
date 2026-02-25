.PHONY: build test clean install help lint fmt run

# Build variables
BINARY_NAME=tgh
GO=go
GOFLAGS=-v
LDFLAGS=-ldflags="-s -w"
REPO?=

# Targets

help:
	@echo "Available targets:"
	@echo "  make build           - Build the tgh binary"
	@echo "  make test            - Run tests"
	@echo "  make clean           - Remove build artifacts"
	@echo "  make install         - Build and install tgh"
	@echo "  make lint            - Run linter (if available)"
	@echo "  make fmt             - Format code"
	@echo "  make run [REPO=...]  - Build and run tgh (optional repo: owner/repo)"
	@echo "  make deps            - Download dependencies"
	@echo "  make tidy            - Tidy dependencies"
	@echo ""
	@echo "Examples:"
	@echo "  make run"
	@echo "  make run REPO=github/cli"

build:
	@echo "Building $(BINARY_NAME)..."
	$(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BINARY_NAME)

test:
	@echo "Running tests..."
	$(GO) test -v ./...

clean:
	@echo "Cleaning build artifacts..."
	$(GO) clean
	rm -f $(BINARY_NAME)

install: build
	@echo "Installing $(BINARY_NAME)..."
	$(GO) install

run: build
	@echo "Running $(BINARY_NAME)..."
	@if [ -n "$(REPO)" ]; then \
		echo "  Repository: $(REPO)"; \
		cd "$(REPO)" && ../../tgh || true; \
	else \
		./$(BINARY_NAME); \
	fi

lint:
	@echo "Running linter..."
	@command -v golangci-lint >/dev/null 2>&1 || { echo "golangci-lint not installed"; exit 1; }
	golangci-lint run

fmt:
	@echo "Formatting code..."
	$(GO) fmt ./...

deps:
	@echo "Downloading dependencies..."
	$(GO) mod download

tidy:
	@echo "Tidying dependencies..."
	$(GO) mod tidy

.DEFAULT_GOAL := help
