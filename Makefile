BINARY_NAME=agyent
VERSION?=1.0.0
GIT_COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_DATE?=$(shell date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo "unknown")
LDFLAGS=-s -w -X main.Version=$(VERSION) -X main.GitCommit=$(GIT_COMMIT) -X main.BuildDate=$(BUILD_DATE)

.PHONY: all build build-all test test-short test-coverage lint-plugins docs-check workflow-check architecture-check verify cross-compile release clean

all: build

build-all: cross-compile

build:
	@echo "==> Building local binary..."
	go build -ldflags="$(LDFLAGS)" -o bin/$(BINARY_NAME) ./cmd/agyent

lint-plugins:
	@echo "==> Verifying Python plugins syntax and typing integrity..."
	@command -v python3 >/dev/null 2>&1 || { echo "python3 is required"; exit 1; }
	@python3 -m py_compile builtin/plugins/*/*.py builtin/plugins/*/*/*.py
	@echo "==> All Python plugin files verified cleanly!"

docs-check:
	@echo "==> Checking documentation, plugin manifests, and skills..."
	python3 scripts/check_docs.py

workflow-check:
	@echo "==> Checking engineering workflow graphs..."
	python3 scripts/check_workflows.py
	python3 -m unittest scripts/check_workflows_test.py

architecture-check:
	@echo "==> Checking package boundaries and architecture invariants..."
	python3 scripts/check_architecture.py

verify: docs-check workflow-check architecture-check lint-plugins test-short

test: lint-plugins
	@echo "==> Running all unit & concurrency tests..."
	go test -v -race ./...

test-short:
	@echo "==> Running fast tests in short mode..."
	go test -v -short ./...

test-coverage:
	@echo "==> Running tests with coverage..."
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

test-e2e: build
	@echo "==> Running hermetic E2E tests with compiled binary..."
	go test -v -tags e2e ./cmd/agyent/...

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
