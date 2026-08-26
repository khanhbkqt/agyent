# Milestone 1: Core Foundation & Microkernel Architecture

> **Technical Specification & Evidence Dashboard**  
> **Milestone ID:** 1  
> **Milestone Name:** Core Foundation  
> **Status:** Completed & Verified (100%)  

---

## 1. Milestone 1 Objectives

1. **Go Module & Project Structure:** Establish standard Go directory layout following Microkernel / Ports & Adapters architecture (`internal/core/domain`, `internal/core/ports`, `internal/config`, `internal/wizard`, `cmd/agyent`).
2. **Domain Models & Entities:** Define core entities (`CanonicalMessage`, `OutboundMessage`, `User`, `Group`, `Session`, `Agent`, `Project`, `AuditLog`, `ExecutionRequest`, `ExecutionResult`, `Event`, `TokenUsage`) with deduplication, reply ID, and attachment fields.
3. **Ports & Contracts (Interface Segregation):** Establish specialized repository interfaces (`SessionRepository`, `AgentRepository`, `ProjectRepository`, `UserRepository`, `AuditRepository` $\rightarrow$ `StoragePort`, `ChannelPort`, `RunnerPort`, `SecurityPort`, `EventBusPort`).
4. **Configuration Engine:** Ingest, validate, and merge configuration from file (`config.yaml`), environment variables (`AGYENT_*`), and default values, supporting path expansion via `os.UserHomeDir()` and `path/filepath`.
5. **Interactive Setup Wizard (`agyent init`):** Interactive terminal wizard using `charmbracelet/huh` and `lipgloss`, supporting `--non-interactive` flags, 5s timeout Telegram token validation, and graceful abort handling.
6. **CLI Entrypoint (`agyent`):** CLI command tree using `spf13/cobra` (`init`, `run`, `version`, `status`).
7. **Comprehensive Unit Tests:** Automated testing for Domain Models, Config Loader, Validator, and Wizard Helpers.

---

## 2. Review & Sign-Off Dashboard

| Review Role | Status | Key Technical Requirements |
| :--- | :--- | :--- |
| 🛡️ **Tech Lead** | **APPROVED** ✅ | - **Interface Segregation**: Split `StoragePort` into granular repository interfaces (`SessionRepository`, `AgentRepository`, `ProjectRepository`, `UserRepository`, `GroupRepository`, `AuditRepository`).<br>- **Domain Models**: Added `CanonicalMessage` (ID, Timestamp, SessionKey), `ExecutionRequest` (Files, Attachments), `OutboundMessage` (ReplyToMessageID).<br>- **Cross-Platform**: Strict use of `path/filepath` and `os.UserHomeDir()`.<br>- **CLI Wizard**: Handled `huh.ErrUserAborted`, `--non-interactive` flag, 5s timeout for Telegram token verification. |
| 🧪 **QA Lead** | **APPROVED WITH TEST MATRIX** ✅ | - **Test Isolation & Sandbox**: 100% of file I/O tests use `t.TempDir()`, environment variables use `t.Setenv()`, Telegram API mocked via `httptest.NewServer`.<br>- **Table-Driven Testing**: Standard `[]struct{name, input, expected, wantErr}` applied across all unit tests.<br>- **Race Detector**: Executed under `go test -v -race ./...`. |
| 🧠 **Domain Expert** | **APPROVED** ✅ | - **AGY CLI Protocol**: Added `Model string` to `ExecutionRequest` for dynamic model overrides; ready for long STDIN prompt delivery.<br>- **Dual-Scope Session**: Confirmed `GlobalConversationID` and `ProjectConversationID` alongside `SessionKey` supporting Telegram Supergroup Topics.<br>- **Outbound Artifacts Structure**: Defined explicit `OutboundAttachment` (`FilePath`, `FileName`, `MIMEType`, `Caption`, `Type`).<br>- **Harness Risk Mitigation**: Process Group termination required to avoid zombie processes. |

---

## 3. Architecture & Code Specification

### Layer 0: Project Setup & Module Initialization

#### `go.mod`
- Initialize Go module for `agyent`.
- Declare dependencies:
  - `github.com/spf13/cobra` (CLI Framework)
  - `github.com/spf13/viper` or `gopkg.in/yaml.v3` (Config management)
  - `github.com/charmbracelet/huh` & `github.com/charmbracelet/lipgloss` (Interactive CLI Wizard & UI)
  - `github.com/stretchr/testify` (Test assertions & mocking)

---

### Layer 1: Domain Models (`internal/core/domain/`)

#### `internal/core/domain/message.go`
- `CanonicalMessage`: `ID string`, `Timestamp time.Time`, `Channel string`, `Sender SenderUser`, `Chat ChatContext`, `Text string`, `RawText string`, `Attachments []Attachment`, `IsMentioned bool`, `IsReplyToBot bool`, `ReplyToMessageID string`.
- `Attachment`: `ID string`, `FileName string`, `FilePath string`, `MIMEType string`, `Size int64`, `Type string`.
- `OutboundMessage`: `ChatID string`, `ThreadID int64`, `Text string`, `ParseMode string`, `Attachments []OutboundAttachment`, `ReplyToMessageID string`.
- `OutboundAttachment`: `FilePath string`, `FileName string`, `MIMEType string`, `Caption string`, `Type string`.
- Helper methods: `IsCommand()`, `CommandArgs()`, `CleanText()`.

#### `internal/core/domain/session.go`
- `Session`: `SessionKey string`, `ActiveAgent string`, `ActiveProject string`, `GlobalConversationID string`, `ProjectConversationID string`, `UpdatedAt time.Time`.
- Helper `FormatSessionKey(channel, chatID, threadID string) string`.

#### `internal/core/domain/agent.go`
- `Agent`: `Name string`, `Description string`, `Status AgentStatus`, `WorkspacePath string`, `CreatedAt time.Time`, `UpdatedAt time.Time`.
- `AgentStatus` constants: `StatusUninitialized = "uninitialized"`, `StatusInitialized = "initialized"`.

#### `internal/core/domain/project.go`
- `Project`: `ID string`, `AgentName string`, `ProjectName string`, `ProjectPath string`, `CreatedAt time.Time`.
- Helper `FormatProjectID(agentName, projectName string) string`.

#### `internal/core/domain/user.go`
- `User`: `ID string`, `Username string`, `FullName string`, `Role string`, `CreatedAt time.Time`.
- `Group`: `GroupID string`, `GroupTitle string`, `IsActive bool`, `CreatedAt time.Time`.

#### `internal/core/domain/audit.go`
- `AuditLog`: `ID int64`, `SessionKey string`, `AgentName string`, `ProjectName string`, `ConversationID string`, `PromptLength int`, `ResponseLength int`, `DurationSeconds float64`, `Usage TokenUsage`, `Status string`, `ErrorMessage string`, `CreatedAt time.Time`.
- `TokenUsage`: `InputTokens int`, `OutputTokens int`, `ThinkingTokens int`, `TotalTokens int`.

#### `internal/core/domain/execution.go`
- `ExecutionRequest`: `Prompt string`, `ConversationID string`, `WorkspaceDir string`, `Files []string`, `Attachments []Attachment`, `Model string`, `Mode string`, `Effort string`, `Timeout time.Duration`, `DangerouslySkipPermissions bool`.
- `ExecutionResult`: `Success bool`, `ConversationID string`, `ResponseText string`, `DurationSec float64`, `Usage TokenUsage`, `Error string`.

#### `internal/core/domain/events.go`
- `EventType` constants: `EventMessageReceived`, `EventPreExecution`, `EventPostExecution`, `EventArtifactDetected`, `EventErrorOccurred`.
- `Event` struct: `Type EventType`, `Payload any`, `Timestamp time.Time`.

---

### Layer 2: Ports & Contracts (`internal/core/ports/`)

#### `internal/core/ports/storage.go`
- Interface Segregation:
  - `SessionRepository`: `GetSession(ctx, key) (*Session, error)`, `SaveSession(ctx, *Session) error`, `DeleteSession(ctx, key) error`.
  - `AgentRepository`: `GetAgent(ctx, name) (*Agent, error)`, `ListAgents(ctx) ([]Agent, error)`, `SaveAgent(ctx, *Agent) error`, `DeleteAgent(ctx, name) error`.
  - `ProjectRepository`: `GetProject(ctx, id) (*Project, error)`, `ListProjects(ctx, agentName) ([]Project, error)`, `SaveProject(ctx, *Project) error`, `DeleteProject(ctx, id) error`.
  - `UserRepository`: `GetUser(ctx, id) (*User, error)`, `ListUsers(ctx) ([]User, error)`, `SaveUser(ctx, *User) error`, `IsUserAllowed(ctx, id) (bool, error)`.
  - `GroupRepository`: `GetGroup(ctx, groupID) (*Group, error)`, `ListGroups(ctx) ([]Group, error)`, `SaveGroup(ctx, *Group) error`, `IsGroupAllowed(ctx, groupID) (bool, error)`.
  - `AuditRepository`: `LogAudit(ctx, *AuditLog) error`.
  - `StoragePort`: Composed interface encompassing all repositories above plus `Close() error`.

#### `internal/core/ports/channel.go`
- `ChannelPort`:
  - `Name() string`
  - `Start(ctx context.Context, inbound chan<- domain.CanonicalMessage) error`
  - `Send(ctx context.Context, msg domain.OutboundMessage) error`
  - `SendTyping(ctx context.Context, chatID string) error`
  - `SendFile(ctx context.Context, chatID string, filePath string, caption string) error`
  - `Stop() error`

#### `internal/core/ports/runner.go`
- `RunnerPort`:
  - `Name() string`
  - `Execute(ctx context.Context, req domain.ExecutionRequest) (*domain.ExecutionResult, error)`
  - `HealthCheck(ctx context.Context) error`

#### `internal/core/ports/security.go`
- `SecurityPort`:
  - `AuthorizeUser(ctx context.Context, user domain.User) (bool, error)`
  - `AuthorizeGroup(ctx context.Context, group domain.Group) (bool, error)`
  - `InspectInput(ctx context.Context, text string) error`

#### `internal/core/ports/eventbus.go`
- `EventBusPort`:
  - `SyncEmit(ctx context.Context, evt domain.Event) error`
  - `AsyncEmit(evt domain.Event)`
  - `SubscribeSync(eventType domain.EventType, handler func(context.Context, domain.Event) error)`
  - `SubscribeAsync(eventType domain.EventType, handler func(context.Context, domain.Event))`

---

### Layer 3: Configuration Engine (`internal/config/`)

#### `internal/config/config.go`
- `Config` struct with tags `yaml` & `mapstructure`.
- `ServerConfig`, `TelegramConfig`, `AGYConfig`, `StorageConfig`.
- Functions: `DefaultConfig() *Config`, `Load(configPath string) (*Config, error)`, `Save(configPath string, cfg *Config) error`, `Validate() error`, `ExpandPaths() error`.

---

### Layer 4: Interactive Setup Wizard (`internal/wizard/`)

#### `internal/wizard/wizard.go`
- Interactive terminal wizard using `charmbracelet/huh`:
  1. Welcome guide.
  2. Input Telegram Bot Token (masked).
  3. Input Telegram Admin User ID.
  4. Confirm Agent Workspaces directory (default `~/.agyent/agents`).
  5. Confirm AGY CLI binary path (default `agy`).
  6. Debounce duration (default 2.0s).
  7. Confirm create default agent `dev_expert`.
- Validation logic:
  - Validates Telegram token via HTTP API `getMe` with `context.WithTimeout(ctx, 5*time.Second)`.
  - Validates presence of `agy` binary (`exec.LookPath`).
  - Handles `huh.ErrUserAborted` gracefully on `Ctrl+C`.
  - Supports `--non-interactive` flag for CI/CD environments.
  - Automatically creates directories and writes `config.yaml`.

---

### Layer 5: CLI Framework & Entrypoints (`cmd/agyent/`)

- `cmd/agyent/main.go`: Application entrypoint.
- `cmd/agyent/root.go`: Root command setup with Cobra, `--config` and `--verbose` flags.
- `cmd/agyent/init.go`: `agyent init` command with interactive wizard.
- `cmd/agyent/run.go`: `agyent run` daemon runner.
- `cmd/agyent/version.go`: `agyent version` metadata printer.

---

### Layer 6: Unit Tests & Test Matrices

#### `internal/core/domain/domain_test.go`
- Table-driven tests for:
  - `FormatSessionKey`: Private chat (`telegram:123`), Topic group (`telegram:-1001:42`).
  - `FormatProjectID`: Whitespace trimming, normalizations.
  - `CleanText`: Excess whitespace handling, UTF-8 emojis.
  - `IsCommand` & `CommandArgs`: Parsing `/cmd arg1 arg2`, ignoring invalid leading spaces.

#### `internal/config/config_test.go`
- Table-driven tests in isolated `t.TempDir()` & `t.Setenv()` sandbox:
  - `Load_ValidYAML`: Correctly parses server, telegram, agy, storage configs.
  - `Load_MalformedYAML`: Gracefully handles YAML syntax errors without panic.
  - `Load_EnvOverrides`: Successfully overrides values via `AGYENT_TELEGRAM_BOT_TOKEN`.
  - `ExpandPaths_CrossPlatform`: Safely expands `~` to user home directory.
  - `Validate_MissingRequired`: Asserts required Token and Admin IDs.
  - `Save_Config`: Writes to new YAML file in `t.TempDir()` and re-reads for parity.

#### `internal/wizard/wizard_test.go`
- Table-driven tests with `httptest.NewServer`:
  - `ValidateToken_Regex`: Regex format check for Telegram Bot Token.
  - `VerifyTokenHTTP_Success`: Simulates HTTP 200 `{ok: true, result: {username: "bot"}}`.
  - `VerifyTokenHTTP_Timeout`: Simulates server sleep 6s, triggers 5s timeout.
  - `ParseAdminID`: Parses valid integers vs invalid alphanumeric strings.

---

## 4. Verification Plan

### Automated Tests
1. **Compile Check:**
   ```bash
   go build -v ./cmd/agyent
   ```
2. **Run Unit Tests with Race Detector:**
   ```bash
   go test -v -race ./...
   ```
3. **Lint & Code Quality:**
   ```bash
   go vet ./...
   gofmt -s -w .
   ```

### Manual Verification
1. **Version Command:**
   ```bash
   ./agyent version
   ```
2. **Init Help Command:**
   ```bash
   ./agyent init --help
   ```

---

## 5. Milestone Completion Status
- **Status:** 100% Completed & Verified.
