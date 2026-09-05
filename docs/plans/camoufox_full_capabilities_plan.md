# Master Architecture Plan: Camoufox Full Capabilities Engine

> **Document status:** Historical
> **Code authority:** current Camoufox plugin code, manifest, MCP schema and skill
> **Last verified:** 2026-09-05
**Universal Stealth Web & Autonomous Perception Architecture for `agyent`**

* **Document Version**: 1.0.0
* **Target Plugin**: `builtin/plugins/browser-camoufox`
* **Status**: Approved for Implementation
* **Author**: Bé Na & Khánh Nguyễn (@khanhbkqt)

---

## 1. Executive Summary & Problem Statement

### 1.1. Bối cảnh & Thách thức
Hệ thống Gateway `agyent` đòi hỏi năng lực tự động hóa trình duyệt cấp cao để phục vụ nghiên cứu thị trường (POD Trends, TikTok Creative Center, Pinterest Trends, Amazon BSR, Etsy, Spreadshirt) và kiểm thử AI POCs.
Tuy nhiên, việc sử dụng các công cụ tìm kiếm truyền thống (`web_search`) hoặc các bộ cào HTML tĩnh gặp các rào cản chí tử:
1. **Dễ bị phát hiện & Block:** Các hệ thống bảo vệ hiện đại (**Cloudflare Turnstile, Datadome, Akamai, Kasada, PerimeterX**) phát hiện ngay lập tức bot thông qua TLS fingerprint, canvas hash, và bất thường trong hành vi chuột.
2. **SPA & Client-side Rendering (CSR):** Các nền tảng hiện đại không trả về dữ liệu trong HTML ban đầu mà tải động qua luồng XHR/GraphQL ngầm.
3. **Tiêu tốn Token & Nhiễu thông tin:** Đưa trực tiếp HTML thô vào Context Window gây lãng phí hàng chục nghìn token và làm giảm độ chính xác của AI.

### 1.2. Mục tiêu kiến trúc
Nâng cấp plugin `browser-camoufox` thành một **Autonomous Stealth Web Engine** hoàn chỉnh, mang lại:
* **Khả năng tàng hình tuyệt đối (100% Zero-Leak):** Dựa trên nhân Firefox C++ tùy biến của Camoufox với Hardware & TLS spoofing hoàn hảo.
* **Cào dữ liệu trực tiếp từ API Stream (Network Sniffer):** Lắng nghe và gom payload JSON từ XHR/GraphQL mà không cần parse DOM.
* **Mắt nhìn & Định vị ngữ nghĩa (Perception & Grounding):** Trích xuất Accessibility Tree và Set-of-Marks giúp AI tương tác trực quan, chính xác.
* **Mô phỏng sinh học con người (Human Kinematics):** Chuột Bézier, gõ phím phân phối Gaussian, cuộn trang quán tính.
* **Kho lưu trữ danh tính bền vững (Profile Vault):** Quản lý phiên đăng nhập và cookie độc lập cho từng thị trường (DE, US, EU).

---

## 2. High-Level Architecture

```
+---------------------------------------------------------------------------------------------------------+
|                                      AGYENT AI AGENT / BRAIN LAYER                                      |
|                 (Bé Na & Autonomous Subagents — 100% Zero-Leak, No web_search)                          |
+----------------------------------------------------+----------------------------------------------------+
                                                     | JSON-RPC 2.0 (MCP Protocol via stdio)
+----------------------------------------------------v----------------------------------------------------+
|                                    CAMOUFOX PLUGIN CORE ENGINE                                          |
|                                                                                                         |
|  [1. PERCEPTION & GROUNDING]      [2. HUMAN KINEMATICS]           [3. NETWORK & API HARVEST]           |
|  - Semantic Accessibility Tree    - Bézier Curve Mouse Trajectory - Raw XHR / GraphQL Sniffer          |
|  - Set-of-Marks (SoM) Visual Box  - Gaussian Typing & Jitter WPM  - WebSocket Stream Interceptor       |
|  - Token-Optimized Markdown       - Inertial Momentum Scroll      - Response Payload Capture (No HTML) |
|  - Visual Multi-Modal Snapshot    - Auto-Captcha/Turnstile Solver - AdBlock / Tracker Strip (uBlock)   |
|                                                                                                         |
|  +---------------------------------------------------------------------------------------------------+  |
|  |                            [4. VIRTUAL CONTEXT & PROFILE VAULT]                                  |  |
|  |  - Persistent Encrypted Profiles: ~/.agyent/camoufox/profiles/{tiktok_de, amazon_de, default}     |  |
|  |  - Session Cookies, LocalStorage, IndexedDB State, Fingerprint Cache                              |  |
|  +---------------------------------------------------------------------------------------------------+  |
|                                                                                                         |
|  +---------------------------------------------------------------------------------------------------+  |
|  |                         [5. ENGINE-LEVEL ANTI-DETECT HARDWARE SPOOFING]                           |  |
|  |  - C++ Modified Firefox Kernel: WebGL Vendor/Renderer, Canvas Noise, AudioContext Invariant       |  |
|  |  - Hardware Concurrency, Screen Depth, Font Fingerprint Masking, Battery API, WebRTC Leak Shield  |  |
|  |  - TLS / JA3 / JA4 Fingerprint Matching exact Firefox Release Builds                               |  |
|  |  - Geo-IP, Timezone (e.g. Europe/Berlin) & Locale (de-DE) Synced with Proxy Exit Nodes             |  |
|  +---------------------------------------------------------------------------------------------------+  |
+---------------------------------------------------------------------------------------------------------+
```

---

## 3. The 12-Tool MCP Specification

### 3.1. Nhóm 1: Search & Discovery (Thay thế triệt để web_search)
1. **`camoufox_search`**:
   * *Mô tả*: Mở phiên stealth tìm kiếm qua DuckDuckGo, Startpage, Google hoặc Bing.
   * *Đầu vào*: `query` (string), `engine` ("duckduckgo" | "google" | "startpage" | "bing"), `locale` ("de-DE" | "en-US"), `max_results` (int, default: 5).
   * *Đầu ra*: Mảng JSON chứa `{title, snippet, url, position}` đã được làm sạch.
2. **`camoufox_discover_trends`**:
   * *Mô tả*: Driver chuyên biệt cho các trung tâm dữ liệu xu hướng (TikTok Creative Center, Pinterest Trends, Google Trends).
   * *Đầu vào*: `platform` ("tiktok" | "pinterest" | "google_trends"), `country` ("DE" | "US"), `category` (string, optional).
   * *Đầu ra*: Danh sách ranked keywords, hashtags, video volume, growth index.

### 3.2. Nhóm 2: Direct Extraction & Content Cleaning
3. **`camoufox_fetch_page`**:
   * *Mô tả*: Tải trang và trích xuất nội dung bài viết dưới dạng Readability Markdown hoặc Clean Text.
   * *Đầu vào*: `url` (string), `extract_mode` ("markdown" | "text" | "raw_html"), `auto_dismiss_banners` (bool, default: true), `timeout_ms` (int).
   * *Đầu ra*: `{url, title, content, word_count, truncated}`.
4. **`camoufox_extract_json_ld`**:
   * *Mô tả*: Trích xuất Schema.org JSON-LD, OpenGraph, Twitter Cards, Product Metadata từ DOM.
   * *Đầu vào*: `url` (string).
   * *Đầu ra*: Object JSON cấu trúc chuẩn mực của trang.
5. **`camoufox_scrape_selector`**:
   * *Mô tả*: Bóc tách dữ liệu theo mảng phần tử CSS Selector / XPath.
   * *Đầu vào*: `url` (string), `selector` (string), `fields` (map tên trường -> sub-selector/attribute), `limit` (int).
   * *Đầu ra*: Mảng các items có cấu trúc.

### 3.3. Nhóm 3: Network & API Sniffer
6. **`camoufox_intercept_api`**:
   * *Mô tả*: Tải trang đích đồng thời chặn bắt toàn bộ response XHR/GraphQL thỏa mãn regex/URL pattern.
   * *Đầu vào*: `url` (string), `url_pattern` (string regex, ví dụ: `/api/v1/trend/|/graphql`), `wait_time_ms` (int).
   * *Đầu ra*: Danh sách các payload JSON gốc do máy chủ trả về.

### 3.4. Nhóm 4: Stateful Interactive Agent Loop
7. **`camoufox_session_start`**:
   * *Mô tả*: Khởi tạo một phiên duyệt web có trạng thái (Stateful Tab Session) và profile cụ thể.
   * *Đầu vào*: `profile_name` (string, ví dụ: "de_shopper"), `headless` (bool, default: true).
   * *Đầu ra*: `session_id`.
8. **`camoufox_inspect_dom`**:
   * *Mô tả*: Trích xuất cây Accessibility Tree tối giản (AOM) hoặc Set-of-Marks bounding boxes của tab hiện tại.
   * *Đầu vào*: `session_id` (string), `mode` ("a11y_tree" | "visual_marks").
   * *Đầu ra*: Bản đồ tương tác gồm các Element ID đánh số (`[1]`, `[2]`, `[3]...`).
9. **`camoufox_act`**:
   * *Mô tả*: Thực thi hành vi người dùng lên phần tử tương tác.
   * *Đầu vào*: `session_id` (string), `action` ("click" | "type" | "hover" | "scroll" | "press_key" | "wait"), `target_id` (string/int), `value` (string, optional).
   * *Đầu ra*: Trạng thái thực thi và sự thay đổi của trang.
10. **`camoufox_session_close`**:
    * *Mô tả*: Đóng phiên làm việc, lưu trữ trạng thái cookies/localStorage vào Profile Vault.
    * *Đầu vào*: `session_id` (string).

### 3.5. Nhóm 5: Visual Inspection & Media Export
11. **`camoufox_screenshot`**:
    * *Mô tả*: Chụp ảnh màn hình Full Page hoặc Element cụ thể, tự động tối ưu hóa và lưu vào artifacts.
    * *Đầu vào*: `url` hoặc `session_id`, `selector` (optional), `full_page` (bool).
    * *Đầu ra*: File path của ảnh PNG trong artifacts.
12. **`camoufox_pdf_export`**:
    * *Mô tả*: Kết xuất trang web thành tài liệu PDF hoàn chỉnh.
    * *Đầu vào*: `url` hoặc `session_id`.
    * *Đầu ra*: File path của file PDF.

---

## 4. Codebase Directory Blueprint

```
builtin/plugins/browser-camoufox/
├── plugin.json
├── mcp_config.json
├── server.py                        # MCP JSON-RPC Server & Fast Router
│
├── core/
│   ├── browser_manager.py           # Quản lý vòng đời Camoufox, tab pool & garbage collector
│   ├── profile_vault.py             # Quản lý storage cookies/state tại ~/.agyent/camoufox/profiles/
│   ├── fingerprint.py               # Cấu hình OS, WebGL, Canvas, Audio, Geo/Locale
│   └── network_sniffer.py           # Bộ chặn bắt XHR / Fetch / GraphQL responses
│
├── kinematics/
│   ├── mouse_dynamics.py            # Quỹ đạo Bézier cong, gia tốc, micro-jitter
│   ├── keyboard_dynamics.py         # Gaussian WPM distribution, natural pauses & typo correction
│   └── scroll_controller.py         # Cuộn quán tính, dwell time, infinite feed trigger
│
├── perception/
│   ├── a11y_tree.py                 # Parser Accessibility Tree & đánh chỉ mục Element ID
│   ├── visual_markers.py            # Set-of-Marks overlay & bounding box generator
│   └── readability_cleaner.py       # Chuyển đổi HTML sang Clean Markdown tối ưu token
│
├── handlers/                        # Handlers ánh xạ trực tiếp cho 12 công cụ MCP
│   ├── search_handler.py            # Xử lý camoufox_search & discover_trends
│   ├── extraction_handler.py        # Xử lý fetch_page, extract_json_ld, scrape_selector
│   ├── network_handler.py           # Xử lý intercept_api
│   ├── interactive_handler.py       # Xử lý session_start, inspect_dom, act, session_close
│   └── visual_handler.py            # Xử lý screenshot, pdf_export
│
├── skills/
│   └── web-browse-camoufox/
│       └── SKILL.md                 # Hướng dẫn chiến lược gọi chuỗi tool cho Agent
└── rules/
    └── AGENTS.md                    # Quy tắc bất di bất dịch (Tuyệt đối không web_search)
```

---

## 5. Implementation Phases & Milestones

### Phase 1: Core Foundation & Search Engine *(Ưu tiên 1)*
* **Mục tiêu**: Xây dựng cấu trúc module hóa, hoàn thiện `camoufox_search` và `camoufox_fetch_page` với bộ lọc Readability Markdown & Cookie Banner Killer.
* **Thời gian dự kiến**: 1 Ngày.
* **Kết quả**: Agent sở hữu công cụ search stealth cực mạnh, thay thế vĩnh viễn `web_search`.

### Phase 2: Network Sniffer & Structured Scraping *(Ưu tiên 2)*
* **Mục tiêu**: Xây dựng `network_sniffer.py`, `camoufox_intercept_api` và `camoufox_scrape_selector`.
* **Kết quả**: Khả năng cào dữ liệu gốc từ TikTok Creative Center, Spreadshirt, Amazon, Pinterest trực tiếp từ luồng API ngầm.

### Phase 3: Autonomous Interaction Loop & Profile Vault *(Ưu tiên 3)*
* **Mục tiêu**: Xây dựng `a11y_tree.py`, `mouse_dynamics.py`, `camoufox_act` và `profile_vault.py`.
* **Kết quả**: Hỗ trợ Agent tương tác sâu đa bước, tự giải challenge, duy trì phiên đăng nhập theo từng quốc gia (DE/US).

---

## 6. Verification & Quality Invariants
1. **Zero Data Leak**: Tuyệt đối không để rò rỉ WebRTC IP thật, múi giờ hệ thống không khớp với IP proxy.
2. **Zero `web_search` Dependency**: Mọi truy vấn thông tin bên ngoài đều được giải quyết tự chủ qua Camoufox.
3. **Token Efficiency**: Kết quả trả về cho LLM luôn được làm sạch qua bộ lọc Readability hoặc Schema JSON chuẩn, tiết kiệm tối thiểu 70% Context Tokens so với HTML thô.
