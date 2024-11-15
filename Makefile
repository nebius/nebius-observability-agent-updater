.PHONY: all clean proto

# Default target
all: proto

# Clean generated files
clean:
	rm -f generated/*.pb.go

# Generate Go code from Protocol Buffers
proto:
	protoc --go_out=generated --go_opt=paths=source_relative \
		--go-grpc_out=generated --go-grpc_opt=paths=source_relative \
		--go-grpc_opt=Mproto/version_service.proto=github.com/nebius/nebius-observability-agent-updater/generated \
		--go_opt=Mproto/version_service.proto=github.com/nebius/nebius-observability-agent-updater/generated \
		proto/*.proto

# Install necessary tools
install-tools:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

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

