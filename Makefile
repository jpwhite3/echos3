# Makefile for the EchoS3 Go application

# Define the name of the binary
BINARY_NAME=echos3

# ---- Versioning ----
# Get the current version from the latest git tag.
# This creates a descriptive version like: v1.0.0, v1.0.0-5-g123abc, or v1.0.0-dirty
VERSION := $(shell git describe --tags --always --dirty)
# The LDFLAGS variable will be used to inject the version into the Go binary.
LDFLAGS := -ldflags="-X main.Version=$(VERSION)"

# ---- Cross-platform commands ----
# Default to 'open' for macOS, but check for other OSes.
OPEN_CMD := open
ifeq ($(OS),Windows_NT)
    # Windows
    OPEN_CMD := start
else
    UNAME_S := $(shell uname -s)
    ifeq ($(UNAME_S),Linux)
        # Linux
        OPEN_CMD := xdg-open
    endif
endif
# Darwin (macOS) is handled by the initial 'open' default.


# Default target executed when you just run `make`
all: build

# Build the Go application, injecting the version information
build:
	@echo "Building $(BINARY_NAME) version $(VERSION)..."
	@go build $(LDFLAGS) -o $(BINARY_NAME) main.go
	@echo "$(BINARY_NAME) built successfully."

# Run the Go application
# Use `make run ARGS="<your arguments>"` to pass arguments
# Example: make run ARGS="./tmp s3://my-bucket --delete"
run: build
	@echo "Running $(BINARY_NAME) version $(VERSION)..."
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

# Generate and view test coverage report in the browser
coverage:
	@echo "Generating and viewing test coverage report..."
	@go test -coverprofile=coverage.out ./...
	@go tool cover -html=coverage.out -o coverage.html
	@echo "Opening coverage.html in the browser..."
	@$(OPEN_CMD) coverage.html

# Clean the project directory by removing the compiled binary and coverage files
clean:
	@echo "Cleaning up..."
	@if [ -f $(BINARY_NAME) ] ; then rm $(BINARY_NAME) ; fi
	@if [ -f coverage.out ] ; then rm coverage.out ; fi
	@if [ -f coverage.html ] ; then rm coverage.html ; fi
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
install:
	@echo "Installing $(BINARY_NAME) version $(VERSION) to $(GOPATH)/bin..."
	@go install $(LDFLAGS) .

# Phony targets are not actual files. Declaring them avoids conflicts with
# files of the same name and improves performance.
.PHONY: all build run test coverage clean lint install-linter install
