# Contributing to agyent

Thank you for your interest in contributing to **agyent**! We welcome contributions from the community to make agyent faster, more extensible, and more capable.

---

## Code of Conduct

We are committed to providing a welcoming, inclusive, and harassment-free environment for everyone. Please be respectful and constructive in all interactions across issues, discussions, and pull requests.

---

## Development Setup

### Prerequisites
- **Go 1.22+** (with CGO disabled by default: `CGO_ENABLED=0`)
- **Git**
- **Antigravity CLI (`agy`)** installed and available in `$PATH`
- **Telegram Bot Token** (obtainable via [@BotFather](https://t.me/BotFather))

### Building from Source

```bash
# Clone repository
git clone https://github.com/khanhbkqt/agyent.git
cd agyent

# Download dependencies
go mod download

# Run test suite
go test ./...

# Build standalone binary
go build -o bin/agyent ./cmd/agyent
```

---

## Architectural Principles

When writing code for `agyent`, please adhere to our core design principles:

1. **Hexagonal Architecture (Ports & Adapters):**
   - Core domain models live in `internal/core/domain/`.
   - Business orchestration logic lives in `internal/core/engine/`.
   - External dependencies (Storage, Telegram, AGY CLI Harness, MCP, Plugins) live in `internal/adapters/` and implement interfaces defined in `internal/core/ports/`.
2. **Zero-CGO & Pure-Go SQLite:**
   - Always use `modernc.org/sqlite` with WAL mode enabled (`_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)`).
   - Use `FlexTime` scanner for flexible timestamp deserialization.
3. **Prefix KV-Cache Preservation:**
   - Maintain strict prompt ordering (Level 0 Static Foundation $\rightarrow$ Level 1 Global Directives $\rightarrow$ Level 2 Workspace Directives $\rightarrow$ Level 3 Skills & Plugins $\rightarrow$ Level 4 User Turn).
   - Separate initial turns (`ComposeResolvedTurnPrompt`) from continuation turns (`ComposeContinuationPrompt`).
4. **Structured Logging:**
   - Use Go's standard `log/slog` for structured logging. Avoid unstructured `fmt.Println` in production code paths.

---

## Submitting Pull Requests

1. Fork the repository and create a new feature branch:
   ```bash
   git checkout -b feat/your-feature-name
   ```
2. Commit your changes with clear, semantic commit messages (e.g., `feat(engine): ...`, `fix(telegram): ...`, `docs: ...`).
3. Ensure all tests pass before submitting:
   ```bash
   go test -v ./...
   ```
4. Open a Pull Request against the `main` branch with a clear description of the problem solved and test validation results.

---

## Reporting Issues

If you encounter a bug or have a feature request:
- Search existing issues to verify it hasn't been reported already.
- Open a new issue with detailed reproduction steps, system environment info, and relevant log outputs (with secrets redacted).
