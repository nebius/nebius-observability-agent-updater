.PHONY: all clean

# Default target
all: build-deb

# Run the application
run:
	go run cmd/nebius-observability-agent-updater/main.go

# Build the application
build:
	go build -o nebius-observability-agent-updater cmd/nebius-observability-agent-updater/main.go

build-deb: build
	scripts/build_deb.sh

tidy:
	go mod tidy

lint:
	golangci-lint run -c .golangci.yaml

test:
	go test -v ./...

