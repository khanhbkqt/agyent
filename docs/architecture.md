# System Architecture Overview (agyent Architecture)

This document provides a comprehensive technical description of the **`agyent`** system, written in **Go (Golang)** and driven by the **Antigravity CLI (`agy`)**, supporting both **Batch Execution** and **Real-Time Streaming (`stream-json`)**.

---

## 1. Comprehensive System Architecture Diagram

```mermaid
flowchart TB
    subgraph Channels ["Tier 1: Messaging Surface"]
        TG_DM["Telegram 1-on-1 Direct Messages"]
        TG_GRP["Telegram Groups / Forum Topics"]
        PLUG["Pluggable Adapters: Discord / Slack / Webhook"]
        Throttler["Delivery Throttler - 1.5s Buffer"]
    end

    subgraph CoreDaemon ["Tier 2: agyent Core Daemon - Go Binary"]
        direction TB
        Filter["Auth & Whitelist Filter\nAdmin IDs & Approved Groups"]
        Mention["Group Mention/Reply Detector"]
        Norm["Canonical Normalizer"]
        Debounce["Debouncer Queue - 2.0s Window"]
        Router["Router: User/Group -> Active Agent -> Project"]
        LockMgr["Session Mutex Lock Manager"]
        EvBus["Central EventBus: SyncEmit & AsyncEmit"]
    end

    subgraph HarnessLayer ["Tier 3: Subprocess Harness & Monitoring"]
        Harness["AGY Subprocess Controller\nBatch: json | Stream: stream-json"]
        Scanner["NDJSON Stream Scanner"]
        Heartbeat["Typing Heartbeat Loop - 4.0s"]
        MediaIn["Auto Inbound Media Downloader"]
        MediaOut["Auto Outbound Artifacts Detector\n~/.gemini/antigravity/brain/ & workspace/"]
    end

    subgraph StorageLayer ["Tier 4: Persistence & Storage Engine"]
        DB[("Pure-Go SQLite: agyent.db\nSessions, Agents, Projects, Audit Log")]
        AgentWS["Agent Universes: ~/.agyent/agents/<name>/\nIDENTITY.md, SOUL.md, USER.md, MEMORY.md"]
        ProjWS["Project Directories: /path/to/projects/<name>/\nCodebase, PROJECT.md"]
        AGYBrain["AGY Brain Native: ~/.gemini/antigravity/brain/<id>/"]
    end

    Channels <--> Filter
    Filter --> Mention
    Mention --> Norm
    Norm --> Debounce
    Debounce --> Router
    Router <--> LockMgr
    Router <--> DB
    Router --> Harness
    
    Harness <--> MediaIn
    Harness <--> MediaOut
    Harness --> Heartbeat
    Harness --> Scanner
    Scanner --> EvBus
    EvBus --> Throttler
    Throttler --> Channels
    
    Harness <-->|Spawn agy stream-json| AGYBrain
    Harness <-->|CWD / Tools| AgentWS
    Harness <-->|CWD / Code| ProjWS
```

---

## 2. Core Architectural Components

### A. Inbound Ingestion & Filtering Tier
- **Whitelist Enforcement:** Instantly blocks messages from unauthorized users not present in the Admin IDs list or unapproved group chats.
- **Group-Aware Mention Filter:** In group chats and forum topics, only processes messages when:
  1. The message explicitly mentions `@agyent_bot`.
  2. The message is a direct reply to a bot message.
  3. The message is a supported Slash Command (`/p`, `/use`, `/status`, `/reset`).

### B. Normalization & Queueing Tier (Debounce, EventBus & Session Routing)
- **Canonical Message Model:** All messages (text, photos, code attachments) are normalized into a unified `CanonicalMessage` structure.
- **2.0s Debounce Channel:** Automatically aggregates consecutive fragmented messages from a user into a complete, unified prompt turn before invoking `agy`.
- **Central EventBus:** Dispatches lifecycle events (`message.received`, `stream.init`, `stream.delta`, `stream.tool`, `stream.result`, `artifact.detected`) via thread-safe publish/subscribe channels.
- **Session Locking:** Every active session is protected by a dedicated mutex. If a user submits new messages while `agy` is actively processing, the new messages queue sequentially in FIFO order.

### C. Execution & Monitoring Tier (Harness & Streaming Protocol)
- **Subprocess Controller:** Manages the lifecycle of the `agy` CLI binary, enforcing safe timeouts and supporting dual modes:
  - *Streaming Mode (`stream-json`):* Real-time NDJSON stream parsing, streaming text deltas and tool events live.
  - *Batch Mode (`json`):* Parses a single JSON envelope upon process completion.
- **Live Tool & Chat Action Dispatcher:** When tool calls like `generate_image` enter `ACTIVE` state, automatically triggers `sendChatAction(upload_photo)` on Telegram.
- **Auto Outbound Artifacts Watcher:** Monitors the AGY brain directory (`~/.gemini/antigravity/brain/<conv_id>/`) and workspace to detect newly created images and files, delivering them to Telegram immediately.

### D. Storage & State Persistence Tier (Storage & Metadata)
- **Pure-Go SQLite (`modernc.org/sqlite`):** Persists configuration tables, session routing (`user_id` $\rightarrow$ `agent` $\rightarrow$ `project`), audit logs, and token usage metrics.
- **Workspace Markdown Files:** Persists agent persona (`SOUL.md`), identity definition (`IDENTITY.md`), and long-term memory (`MEMORY.md`).
