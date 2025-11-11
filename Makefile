.PHONY: build run clean test install deps

# Binary names
SERVER_BINARY=doorbell-server
CLI_BINARY=doorbell-cli
SERVER_PATH=./cmd/server
CLI_PATH=./cmd/cli

# Build all applications
build: build-server build-cli

# Build the server
build-server:
	go build -o $(SERVER_BINARY) $(SERVER_PATH)

# Build the CLI
build-cli:
	go build -o $(CLI_BINARY) $(CLI_PATH)

# Run the server
run: build-server
	./$(SERVER_BINARY) -config config.yaml

# Run with custom config
run-config:
	./$(SERVER_BINARY) -config $(CONFIG)

# Send test audio file
test-send: build-cli
	./$(CLI_BINARY) send -f test_audio.mp3

# Install dependencies
deps:
	go mod download
	go mod tidy

# Run tests
test:
	go test -v ./...

# Run tests with coverage
test-coverage:
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Clean build artifacts
clean:
	rm -f $(SERVER_BINARY) $(CLI_BINARY)
	rm -f coverage.out coverage.html
	rm -f *.raw *.pcm

# Install binaries to $GOPATH/bin
install: install-server install-cli

install-server:
	go install $(SERVER_PATH)

install-cli:
	go install $(CLI_PATH)

# Run with race detector
run-race:
	go run -race $(SERVER_PATH)/main.go -config config.yaml

# Format code
fmt:
	go fmt ./...

# Lint code
lint:
	golangci-lint run

# Development mode (auto-restart on changes, requires air)
dev:
	air

# Initialize config from example
init-config:
	cp config.yaml.example config.yaml
	@echo "Created config.yaml from example. Please edit it with your settings."

# Quick start - build everything and test
quickstart: build
	@echo "Built server and CLI successfully!"
	@echo ""
	@echo "Next steps:"
	@echo "  1. Start the server: make run"
	@echo "  2. Send audio file: ./doorbell-cli send -f your_audio.mp3"
	@echo "  3. Two-way audio: ./doorbell-cli speak"
