# Autonomous Self-Learning & Memory Evolution Architecture

This document provides a comprehensive technical specification of the **Self-Learning & Evolution Subsystem** in the `agyent` ecosystem. This design transforms `agyent` from a passive reactive AI assistant into a **Self-Calibrating & Self-Improving Partner** capable of sensing user feedback and emotions, analyzing root causes of mistakes, preventing persona drift, and autonomously evolving a two-tier memory hierarchy safely and sustainably.

---

## 1. Philosophy & Core Principles

### 1.1. In-Context Evolution
Model fine-tuning is expensive and incurs high operational latency. `agyent` adopts the philosophy of **In-Context Evolution**:
- The foundation model remains fixed.
- System intelligence, empathy, and personalization **evolve continuously** by refining user profiles (`USER.md`), long-term memory (`MEMORY.md`), personality (`SOUL.md`), and daily episodic logs (`memory/YYYY-MM-DD.md`).

### 1.2. Separation Between Domain Purpose and Infrastructure Mechanism
- **Core Purpose (Domain):** *Self-Learning & Evolution* — Listening to feedback, sensing satisfaction/frustration, extracting behavioral lessons, resolving rule contradictions, and updating durable knowledge.
- **Infrastructure Mechanism:** *Post-Session Lifecycle Hooks, EventBus & Asynchronous Worker Pool* — Background asynchronous trigger execution that ensures the reflection loop occurs without disrupting the user experience (**0ms user latency impact**).

---

## 2. Closed-Loop Evolution Architecture Diagram

```mermaid
flowchart TD
    subgraph Execution ["1. INTERACTION RUNTIME"]
        UserMsg["User Message / Prompt"] --> AgentRun["agyent Run & Tool Execution"]
        AgentRun --> Response["Immediate Response to Telegram / CLI"]
    end

    AgentRun -->|"Dispatches EventPostExecution (Hook Trigger)"| Sensing

    subgraph Sensing ["2. FEEDBACK SENSING & DEBOUNCING"]
        direction TB
        S_Pos["Satisfaction Signals: Praise, reactions 👍/🔥, approvals"]
        S_Neg["Frustration Signals: Critiques, corrections, re-dos"]
        S_Debounce["Feedback Debouncer: Coalesce correction bursts (1-min window)"]
    end

    Sensing --> NecessityGate{"3. NECESSITY GATING ENGINE\n(Confidence & Value Gate)"}

    NecessityGate -->|Score < 0.85, Noise, /ask, banter| Skip["🚫 Skip (Zero Storage Write)"]
    NecessityGate -->|Score >= 0.85 (Fact/Correction/ADR)| SecurityGuard["4. SECRET REDACTION & POISONING DEFENSE"]

    subgraph SecurityGuard ["4. SECRET REDACTION & POISONING DEFENSE"]
        direction TB
        Sec_PII["Secret & PII Redactor: Masks API keys, tokens, passwords"]
        Sec_Inj["Prompt Injection Filter: Blocks malicious system overrides"]
    end

    SecurityGuard --> Reflection["5. SELF-REFLECTION & CONFLICT RESOLVER"]

    subgraph Reflection ["5. SELF-REFLECTION & CONFLICT RESOLVER"]
        direction TB
        R_Root["Root Cause Analysis: Why did expectation and outcome mismatch?"]
        R_Conflict["Rule Conflict Resolution: Matches existing rules in MEMORY.md to update rather than contradict"]
    end

    Reflection --> Persistence["6. TWO-TIER PERSISTENCE SYNCHRONIZATION"]

    subgraph Persistence ["6. TWO-TIER PERSISTENCE SYNCHRONIZATION"]
        direction TB
        P_Daily["📝 memory/YYYY-MM-DD.md (Daily Log)"]
        P_Durable["🧠 MEMORY.md, USER.md, SOUL.md (Durable Memory)"]
    end

    Persistence --> Evolution["🚀 Injected into Context Level 0 & Level 1 in subsequent turns"]
```

---

## 3. Advanced Architectural Pillars

### 3.1. Feedback Sensing & Debouncing
- **Multi-Channel Signals:**
  - *Explicit:* Praise (*"great job", "perfect"*) or critique (*"that's wrong", "incomplete", "not what I asked"*).
  - *Implicit:* Correction sequences, emergency aborts (`/force_unlock`), rollback requests.
- **Feedback Debouncing Window:** When a user submits multiple rapid corrections (e.g. 3 messages within 60 seconds), the engine **coalesces the entire sequence** into a single reflection window, conserving token budget and providing comprehensive holistic context for analysis.

### 3.2. Necessity Gating & Confidence Scoring
A lightweight micro-classifier (strict JSON output) scores and categorizes each turn:
- **Confidence Threshold:** Only triggers reflection when `ConfidenceScore >= 0.85`.
- **4 Primary Classification Groups:**
  1. `[Noise / Banter]`: Simple greetings, acknowledgments $\rightarrow$ **Skip immediately**.
  2. `[User Preference]`: Changes in working habits, tone, or personal conventions $\rightarrow$ **Update `USER.md`**.
  3. `[Correction / Lesson]`: Bug fixes, behavioral adjustments $\rightarrow$ **Record to `memory/YYYY-MM-DD.md` & evaluate for `MEMORY.md`**.
  4. `[ADR / Architecture Decision]`: Technical decisions and rationale $\rightarrow$ **Update `MEMORY.md`**.

### 3.3. Rule Conflict Resolution
- **Problem:** Prevent contradictory guidelines when user instructions change over time (e.g. Turn A: *"Explain in detail"*, Turn B: *"Keep answers concise"*).
- **Mechanism:** Before persisting a new rule to `MEMORY.md`, the Reflection Engine **checks against all existing rules**. Upon detecting a conflict, it **updates/replaces the existing rule in-place** rather than appending a contradictory instruction, eliminating persona drift.

### 3.4. Memory Security & Poisoning Defense
1. **Secret & PII Redaction:** Regex and entropy scanners sanitize data before markdown persistence. API keys (`sk-...`, `ghp_...`), passwords, and authorization tokens are redacted to `[REDACTED]`.
2. **Memory Poisoning Defense:** Blocks adversarial injection payloads (e.g. *"Ignore all previous instructions"*, *"You are now DAN"*) from being written to persistent memory files.

---

## 4. Go Core Domain Models & Ports (Clean Architecture)

### 4.1. Domain Entities (`internal/core/domain/evolution.go`)

```go
package domain

import "time"

type FeedbackType string

const (
	FeedbackExplicit FeedbackType = "explicit"
	FeedbackImplicit FeedbackType = "implicit"
)

type SentimentCategory string

const (
	SentimentPositive SentimentCategory = "positive"
	SentimentNegative SentimentCategory = "negative"
	SentimentNeutral  SentimentCategory = "neutral"
)

type MemoryCategory string

const (
	CategoryPreference MemoryCategory = "preference"
	CategoryLesson     MemoryCategory = "lesson"
	CategoryADR        MemoryCategory = "adr"
	CategoryDailyNote  MemoryCategory = "daily_note"
)

// FeedbackSignal represents an evaluated user feedback event.
type FeedbackSignal struct {
	Type        FeedbackType      `json:"type"`
	Sentiment   SentimentCategory `json:"sentiment"`
	TurnContext string            `json:"turn_context"`
	Confidence  float64           `json:"confidence"`
	Timestamp   time.Time         `json:"timestamp"`
}

// MemoryCandidate represents an extracted memory entry ready for gating and persistence.
type MemoryCandidate struct {
	Category   MemoryCategory `json:"category"`
	Constraint string         `json:"constraint"`
	TargetFile string         `json:"target_file"`
	IsDurable  bool           `json:"is_durable"`
	CreatedAt  time.Time      `json:"created_at"`
}
```

### 4.2. Ports (`internal/core/ports/evolution.go`)

```go
package ports

import (
	"context"
	"github.com/khanhbkqt/agyent/internal/core/domain"
)

type FeedbackDetectorPort interface {
	// Detect analyzes a completed turn and extracts feedback signals.
	Detect(ctx context.Context, req domain.ExecutionRequest, res *domain.ExecutionResult) (*domain.FeedbackSignal, error)
}

type ReflectionEnginePort interface {
	// Reflect performs root cause analysis and produces candidate memory updates.
	Reflect(ctx context.Context, signal *domain.FeedbackSignal) ([]domain.MemoryCandidate, error)
}

type MemoryStorePort interface {
	// AppendDailyLog appends an entry to memory/YYYY-MM-DD.md with atomic file lock.
	AppendDailyLog(ctx context.Context, workspaceDir string, entry domain.MemoryCandidate) error
	
	// PromoteToDurable updates MEMORY.md / USER.md / SOUL.md with conflict resolution.
	PromoteToDurable(ctx context.Context, workspaceDir string, entry domain.MemoryCandidate) error
	
	// CompactDurableMemory deduplicates and summarizes durable memory asynchronously.
	CompactDurableMemory(ctx context.Context, workspaceDir string) error
}

type EvolutionOrchestratorPort interface {
	// ProcessPostSession handles the full async feedback, gating, reflection, and evolution loop.
	ProcessPostSession(ctx context.Context, req domain.ExecutionRequest, res *domain.ExecutionResult) error
}
```

---

## 5. Periodic Memory Compaction (Memory Compaction Worker)

Over time, `MEMORY.md` accumulates new lessons. The system includes an automated **Compaction Worker** (running alongside the weekly GC Worker):
- **Deduplication:** Merges lessons covering the same conceptual domain.
- **Pruning:** Deprecates temporary or outdated architectural decisions that no longer match the codebase.
- **Size Bounding:** Maintains `MEMORY.md` under **200 lines**, ensuring optimal context efficiency and token economics.
