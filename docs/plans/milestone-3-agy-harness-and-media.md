# Milestone 3: AGY Harness & Media Watcher

> **Document status:** Historical
> **Code authority:** AGY harness/watcher, Telegram media adapter and current references
> **Last verified:** 2026-09-05

> **Production-Grade Technical Specification & Evidence Dashboard (Audited by TechLead & QA/QC Lead & Real AGY Verification)**  
> **Milestone ID:** 3  
> **Milestone Name:** AGY Harness & Media Watcher  
> **Status:** Completed & Verified (100%)  

---

## 1. Milestone 3 Objectives

1. **Go Subprocess Controller (`internal/adapters/harness/agy/`):**
   - Implement `ports.RunnerPort` interface (`Execute`, `HealthCheck`, `Name`).
   - Drive the `agy` CLI binary using `os/exec.CommandContext`, passing flags:
     `--output-format json --dangerously-skip-permissions --conversation <id> --mode <mode> --effort <effort> --model <model>`.
   - Set current working directory (`cmd.Dir`) accurately to the Agent or Project workspace.

2. **STDIN Prompt Streaming (Bypassing 32KB OS CLI Limits):**
   - Pipe prompt content directly via standard input (`cmd.Stdin = strings.NewReader(req.Prompt)`), completely eliminating long command-line arguments via `-p`.
   - Support arbitrary prompt sizes, line breaks, special characters, UTF-8 Unicode, and large source code blocks.

3. **Process Tree Lifecycle & Zombie Subprocess Prevention:**
   - **Linux/macOS:** Group processes via `SysProcAttr: &syscall.SysProcAttr{Setpgid: true}` and dispatch `syscall.SIGKILL` to the entire group (`-PID`) upon timeout or context cancellation, with safe `Pid > 0` checks.
   - **Windows:** Use `CREATE_NO_WINDOW (0x08000000)` to eliminate black console popups, combined with process tree termination via `taskkill /F /T /PID <pid>` backed by a 2s timeout guard to eliminate all spawned child processes (`python`, `node`, `git`...).

4. **JSON Boundary & ANSI Escape Parser:**
   - Extract the final valid JSON envelope from the stdout buffer (`{ ... }`) using a Backwards Scanner (`lastClose-1` down to `0`), ignoring auxiliary logs, CLI warnings (`warning: conversation "..." not found`), or ANSI TrueColor escape sequences.
   - Convert safely to `domain.ExecutionResult` (`Success`, `ConversationID`, `ResponseText`, `DurationSec`, `Usage`, `Artifacts`, `Error`).

5. **Snapshot Diff Media Watcher (Automated Artifact Detection & Filtering):**
   - Take pre-execution directory snapshots (`map[string]fileEntry` storing `ModTimeUnixMs` and `Size`) using optimized `filepath.WalkDir`.
   - Compare post-execution snapshots to detect newly created or modified files.
   - **Artifact Format Whitelist:** Automatically upload charts (`.png`, `.jpg`, `.jpeg`, `.svg`, `.webp`, `.gif`), documents (`.pdf`, `.csv`, `.xlsx`, `.docx`, `.json`), archives (`.zip`, `.tar.gz`, `.tar`, `.gz`, `.7z`), media (`.mp4`, `.mp3`, `.wav`), or files in `exports/`, `output/`.
   - **Directory Exclusion Filter:** Immediately prune (`filepath.SkipDir`) heavy directories: `.git`, `node_modules`, `.venv`, `vendor`, `.gemini`, `dist`, `build`, `bin`, `obj`, `.agyent`, `__pycache__`.
   - **Guardrails:** 50MB file size ceiling (Telegram Bot API limit) and deterministic ID sorting.

6. **Sentinel Error Propagation (Session Loss Auto-Detection):**
   - Detect AGY conversation loss patterns via regex `convNotFoundRegex` and return standard `ports.ErrConversationNotFound` for automated self-healing at the Orchestrator layer.

7. **Dummy AGY Binary Mock & Real CLI Test Suite:**
   - Build dummy CLI mock binary simulating `agy` for testing (simulating delays, streaming stdout/stderr, JSON output, ANSI codes, and errors).
   - Validate directly against real `agy` binary in the environment.

---

## 2. Review & Sign-Off Dashboard

| Review Role | Status | Key Technical Requirements |
| :--- | :--- | :--- |
| 🛡️ **Principal Architect / Tech Lead** | **APPROVED** ✅ | - Mandatory `cmd.Stdin` for prompts.<br>- Apply `cmd.Cancel` & `cmd.WaitDelay = 3*time.Second` to prevent hung pipes.<br>- Process Tree cleanup on Windows (`CREATE_NO_WINDOW`, `taskkill` 2s timeout) and Linux (`Setpgid`, `Pid > 0`).<br>- Fast `WalkDir` with exclusion pruning without pruning `rootDir`.<br>- Backwards JSON scanner (`lastClose-1` down to 0). |
| 🧪 **QA/QC Lead** | **APPROVED WITH FULL TEST MATRIX** ✅ | - 22 test scenarios covering Parser, Watcher, Runner, Concurrency Stress, and Real AGY CLI.<br>- Stress test 50 goroutines under `-race`.<br>- Regex `convNotFoundRegex` matches real `agy` output logs.<br>- Target coverage $\ge 92\%$. |
| 🧠 **Domain Expert** | **APPROVED** ✅ | - Added `Artifacts []Attachment` to `domain.ExecutionResult`.<br>- Clean Architecture boundary: Harness returns `ports.ErrConversationNotFound`, Engine manages auto-retry. |

---

## 3. Source Code Structure for Milestone 3

```
internal/
├── core/
│   └── domain/
│       └── execution.go          <-- Added Artifacts []Attachment to ExecutionResult
└── adapters/
    └── harness/
        └── agy/
            ├── runner.go          <-- Implements ports.RunnerPort
            ├── runner_test.go     <-- Subprocess runner & Real agy CLI tests
            ├── parser.go          <-- Backwards JSON Boundary Parser & ANSI Stripper
            ├── parser_test.go     <-- JSON parsing, ANSI codes & Error matching tests
            ├── watcher.go         <-- Fast WalkDir Snapshot Diff Media Watcher
            ├── watcher_test.go    <-- Artifact detection, whitelist, exclusions tests
            ├── process_unix.go    <-- Process Group setup for Linux/macOS
            ├── process_windows.go <-- CREATE_NO_WINDOW & Taskkill for Windows
            └── mock/
                └── dummy_cli.go   <-- Test helper mock CLI binary
```

---

## 4. 22-Scenario QA Verification Matrix

```
+---------------------------------------------------------------------------------------------------------+
|                                        AGY HARNESS & MEDIA TEST MATRIX                                  |
+--------------------------+----------------------------------------------------+-------------------------+
| Test Case Category       | Test Scenario & Assertion Details                  | Target Verification     |
+--------------------------+----------------------------------------------------+-------------------------+
| 1. Parser Engine         | - TC-PARS-01: Parse clean AGY JSON output.         | Match ExecutionResult   |
|                          | - TC-PARS-02: Real AGY warning log + JSON.         | Match conv not found    |
|                          | - TC-PARS-03: Logs before & after JSON envelope.   | Backwards scan correct  |
|                          | - TC-PARS-04: ANSI 24-bit TrueColor & OSC codes.   | Strip ANSI 100%         |
|                          | - TC-PARS-05: Corrupt / Truncated JSON.            | Return ErrOutputParse   |
|                          | - TC-PARS-06: Zero / Nil Token Usage.              | Safe zero struct        |
+--------------------------+----------------------------------------------------+-------------------------+
| 2. Snapshot Watcher      | - TC-WAT-01: New files .png, .pdf, .zip, .csv.     | Whitelist match + MIME  |
|                          | - TC-WAT-02: Source files .go, .py, .js, .md root. | Ignored (empty)         |
|                          | - TC-WAT-03: Excluded Dirs (Build/, node_modules/).| Pruned via SkipDir      |
|                          | - TC-WAT-04: Special folder (exports/data.bin).    | Detected as artifact    |
|                          | - TC-WAT-05: Unicode filenames & spaces handling.  | Safe path parsing       |
|                          | - TC-WAT-06: 50MB file size boundary checks.       | Telegram size ceiling   |
|                          | - TC-WAT-07: Locked file O_EXCL handling.          | Safe walk, no panic     |
|                          | - TC-WAT-08: Compound extension (.tar.gz).         | Gzip MIME & archive     |
+--------------------------+----------------------------------------------------+-------------------------+
| 3. Runner & Process Tree | - TC-RUN-01: Large prompt >2MB via STDIN.          | Zero CLI limit errors   |
|                          | - TC-RUN-02: Timeout 500ms -> kill tree <1.0s.     | No zombie subprocesses  |
|                          | - TC-RUN-03: Context cancel unblock immediately.   | Immediate cleanup       |
|                          | - TC-RUN-04: Invalid binary path handling.         | Return ErrProcessExec   |
+--------------------------+----------------------------------------------------+-------------------------+
| 4. Real AGY Integration  | - TC-REAL-01: Sandbox execution prompt "say 999".  | Response contains 999   |
|                          | - TC-REAL-02: Resume conversation turn 2.          | Consistent convID       |
|                          | - TC-REAL-03: HealthCheck with real agy.           | Return nil              |
+--------------------------+----------------------------------------------------+-------------------------+
| 5. Concurrency & Stress  | - TC-CONC-01: 50 Subprocess workers concurrent.    | 0 race conditions       |
|                          | - TC-CONC-02: 30 Watchers + 10 File Mutators race. | Concurrency safe        |
+--------------------------+----------------------------------------------------+-------------------------+
```

---

## 5. Milestone Completion Status
- **Status:** 100% Completed & Verified.
