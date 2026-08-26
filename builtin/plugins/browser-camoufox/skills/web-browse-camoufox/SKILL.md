---
name: web-browse-camoufox
description: >-
  Autonomous Stealth Web Perception & Automation Engine powered by Camoufox C++ anti-detect kernel, human kinematics, and network sniffer. Use this skill for all external web search, trend analysis, SPA scraping, form filling, and interactive multi-step browsing.
---

# Autonomous Stealth Web Browsing with Camoufox

This skill gives agents full autonomous web capabilities without triggering Cloudflare, Datadome, Akamai, Kasada, or PerimeterX anti-bot challenges.

---

## 1. Tool Selection Strategy & Workflows

### Pattern A: Stealth Search & Fast Lookup (100% Replaces `web_search`)
To search the public web without getting blocked:
1. Call `camoufox_search(query="...", engine="duckduckgo"|"google", locale="en-US"|"de-DE")`.
2. Inspect the returned list of results (`title`, `snippet`, `url`).
3. To read a target result, call `camoufox_fetch_page(url="...", extract_mode="markdown")`.

### Pattern B: Single-Page Extraction & Structured Scraping
- **Article / Documentation**: Call `camoufox_fetch_page(url="...")` to receive token-optimized Readability Markdown with cookie banners auto-dismissed.
- **Product / E-commerce / Schema**: Call `camoufox_extract_json_ld(url="...")` to extract Schema.org JSON-LD, OpenGraph, and metadata directly.
- **Repeating Lists / Tables**: Call `camoufox_scrape_selector(url="...", selector=".item", fields={"name": "h3", "link": "a@href", "price": ".price"})`.

### Pattern C: Dynamic SPA & API Interception (Zero DOM Scraping)
For complex dynamic platforms (TikTok, Pinterest, Amazon BSR, Spreadshirt, GraphQL SPAs):
1. Call `camoufox_intercept_api(url="...", url_pattern="/api/|/graphql")`.
2. Get the exact server JSON payloads directly from network responses without parsing messy DOM elements.
3. For trend discovery, use `camoufox_discover_trends(platform="tiktok"|"pinterest"|"google_trends", country="DE"|"US")`.

### Pattern D: Multi-Step Stateful Interactive Agent Loop
For interactive tasks requiring login, clicking buttons, typing in search bars, or navigating pagination:
1. **Start Session**: `session_id = camoufox_session_start(profile_name="market_de", initial_url="https://...")`.
2. **Inspect Elements**: Call `camoufox_inspect_dom(session_id=session_id, mode="a11y_tree")`. This outputs numbered elements (`[1] BUTTON: "Search"`, `[2] INPUT: "Keywords"`).
3. **Execute Action**:
   - Type in input: `camoufox_act(session_id=session_id, action="type", target_id=2, value="Vintage T-Shirt")`.
   - Click submit: `camoufox_act(session_id=session_id, action="click", target_id=1)`.
   - Scroll down: `camoufox_act(session_id=session_id, action="scroll", value="600")`.
4. **Capture Evidence / Visual Proof**: Call `camoufox_screenshot(session_id=session_id)`.
5. **Persist & Close**: Call `camoufox_session_close(session_id=session_id)`. Cookies and session state are automatically stored in the `Profile Vault` (~/.agyent/camoufox/profiles/).

---

## 2. Invariants & Best Practices
- **Token Efficiency**: Never fetch `raw_html` unless specifically debugging HTML tags. Default `markdown` saves ~75% tokens.
- **Profile Reuse**: Use dedicated profile names (e.g. `de_researcher`, `tiktok_de`, `amazon_shopper`) so login sessions and cookies persist across tasks.
- **Human Kinematics**: All clicks and typing automatically use Bézier curves and Gaussian delays to bypass behavioral bot detection.
