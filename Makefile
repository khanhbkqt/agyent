BINARY_NAME=agyent
VERSION?=1.0.0
GIT_COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_DATE?=$(shell date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo "unknown")
LDFLAGS=-s -w -X main.Version=$(VERSION) -X main.GitCommit=$(GIT_COMMIT) -X main.BuildDate=$(BUILD_DATE)

.PHONY: all build test test-short test-coverage cross-compile release clean

all: build

build:
	@echo "==> Building local binary..."
	go build -ldflags="$(LDFLAGS)" -o bin/$(BINARY_NAME) ./cmd/agyent

test:
	@echo "==> Running all unit & concurrency tests..."
	go test -v -race ./...

test-short:
	@echo "==> Running fast tests in short mode..."
	go test -v -short ./...

test-coverage:
	@echo "==> Running tests with coverage..."
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

cross-compile: clean
	@echo "==> Cross-compiling zero-CGO static binaries for 6 target platforms..."
	mkdir -p dist/agyent-linux-amd64 dist/agyent-linux-arm64 dist/agyent-darwin-amd64 dist/agyent-darwin-arm64 dist/agyent-windows-amd64 dist/agyent-windows-arm64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o dist/agyent-linux-amd64/$(BINARY_NAME) ./cmd/agyent
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o dist/agyent-linux-arm64/$(BINARY_NAME) ./cmd/agyent
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o dist/agyent-darwin-amd64/$(BINARY_NAME) ./cmd/agyent
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o dist/agyent-darwin-arm64/$(BINARY_NAME) ./cmd/agyent
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o dist/agyent-windows-amd64/$(BINARY_NAME).exe ./cmd/agyent
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o dist/agyent-windows-arm64/$(BINARY_NAME).exe ./cmd/agyent
	@echo "==> Cross-compilation completed successfully!"

release: cross-compile
	@echo "==> Packaging release archives into dist/archives/..."
	mkdir -p dist/archives
	cp README.md LICENSE scripts/deploy/agyent.service dist/agyent-linux-amd64/
	cp README.md LICENSE scripts/deploy/agyent.service dist/agyent-linux-arm64/
	cp README.md LICENSE dist/agyent-darwin-amd64/
	cp README.md LICENSE dist/agyent-darwin-arm64/
	cp README.md LICENSE dist/agyent-windows-amd64/
	cp README.md LICENSE dist/agyent-windows-arm64/
	tar -czf dist/archives/agyent-v$(VERSION)-linux-amd64.tar.gz -C dist/agyent-linux-amd64 .
	tar -czf dist/archives/agyent-v$(VERSION)-linux-arm64.tar.gz -C dist/agyent-linux-arm64 .
	tar -czf dist/archives/agyent-v$(VERSION)-darwin-amd64.tar.gz -C dist/agyent-darwin-amd64 .
	tar -czf dist/archives/agyent-v$(VERSION)-darwin-arm64.tar.gz -C dist/agyent-darwin-arm64 .
	zip -j dist/archives/agyent-v$(VERSION)-windows-amd64.zip dist/agyent-windows-amd64/*
	zip -j dist/archives/agyent-v$(VERSION)-windows-arm64.zip dist/agyent-windows-arm64/*
	@echo "==> Generating SHA256 Checksums..."
	cd dist/archives && sha256sum * > checksums.txt 2>/dev/null || shasum -a 256 * > checksums.txt 2>/dev/null || true
	@echo "==> Release packaging complete in dist/archives/ directory:"
	ls -la dist/archives/ 2>/dev/null || true

clean:
	@echo "==> Cleaning build artifacts..."
	rm -rf bin/ dist/ coverage.out coverage.html
