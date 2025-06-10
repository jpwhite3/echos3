# Makefile for the EchoS3 Go application

# Define the name of the binary
BINARY_NAME=echos3

# Get the Go version
GO_VERSION := $(shell go version)

# Default target executed when you just run `make`
all: build

# Build the Go application
build:
	@echo "Building $(BINARY_NAME)..."
	@go build -o $(BINARY_NAME) main.go
	@echo "$(BINARY_NAME) built successfully."

# Run the Go application
# Use `make run ARGS="<your arguments>"` to pass arguments
# Example: make run ARGS="./tmp s3://my-bucket --delete"
run: build
	@echo "Running $(BINARY_NAME)..."
	@./$(BINARY_NAME) $(ARGS)

# Test the application
# Use `make test` to run all tests
# Use `make test V=1` for verbose output
test:
	@echo "Running tests..."
	@if [ "$(V)" = "1" ]; then \
		go test -v ./...; \
	else \
		go test ./...; \
	fi

# Clean the project directory by removing the compiled binary
clean:
	@echo "Cleaning up..."
	@if [ -f $(BINARY_NAME) ] ; then rm $(BINARY_NAME) ; fi
	@echo "Cleanup complete."

# Lint the Go files
# This requires golangci-lint to be installed.
# Run `make install-linter` to install it.
lint:
	@echo "Linting Go files..."
	@# Check if golangci-lint is installed
	@if ! command -v golangci-lint &> /dev/null; then \
		echo "golangci-lint not found. Please run 'make install-linter'."; \
		exit 1; \
	fi
	@golangci-lint run

# Install golangci-lint
install-linter:
	@echo "Installing golangci-lint..."
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Install the application to your GOPATH/bin directory
install: build
	@echo "Installing $(BINARY_NAME) to $(GOPATH)/bin..."
	@go install .

# Phony targets are not actual files. Declaring them avoids conflicts with
# files of the same name and improves performance.
.PHONY: all build run test clean lint install-linter install