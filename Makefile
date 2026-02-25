.PHONY: build build-windows test test-verbose clean fmt vet lint

BINARY  = wintmux
VERSION = 0.1.0

# Build for current platform (Linux/macOS — for running unit tests)
build:
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) ./cmd/wintmux/

# Cross-compile for Windows (produces wintmux.exe)
build-windows:
	GOOS=windows GOARCH=amd64 go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY).exe ./cmd/wintmux/

# Run all unit tests (platform-independent modules)
test:
	go test ./internal/scrollback/ ./internal/ipc/ ./internal/cli/

# Run tests with verbose output
test-verbose:
	go test -v ./internal/scrollback/ ./internal/ipc/ ./internal/cli/

# Run tests with race detector
test-race:
	go test -race ./internal/scrollback/ ./internal/ipc/ ./internal/cli/

clean:
	rm -f $(BINARY) $(BINARY).exe

fmt:
	go fmt ./...

vet:
	go vet ./internal/scrollback/ ./internal/ipc/ ./internal/cli/

lint: fmt vet
