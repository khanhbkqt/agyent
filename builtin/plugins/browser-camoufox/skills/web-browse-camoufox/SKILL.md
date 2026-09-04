---
name: web-browse-camoufox
description: >-
  Continuous Stealth Web Perception & Automation Engine powered by Camoufox C++ anti-detect kernel, background session daemon, human kinematics, and stealth media sniffer/downloader. Use this skill for external web search, multi-turn continuous browsing, trend analysis, SPA scraping, form filling, interactive tasks, and downloading high-resolution media (YouTube 1080p/4K, TikTok, Facebook, Instagram, HLS/DASH) with lossless FFmpeg remuxing and trimming.
---

# Autonomous Continuous Stealth Web Browsing with Camoufox

This skill gives agents full autonomous web capabilities without losing session state between turns, and without triggering Cloudflare, Datadome, Akamai, Kasada, or PerimeterX anti-bot challenges.

---

## 1. Core Continuous Browsing Concepts

1. **Background Browser Daemon**: Browser tabs and live DOMs stay open in a detached background daemon across turns. You can execute actions across multiple user messages without reloading the page or losing logins.
2. **Persistent Profile Vault (`user_data_dir`)**: When using a named profile (e.g. `profile_name="shopee_vn"`, `profile_name="tiktok_de"`), all cookies (`cookies.sqlite`), IndexedDB, and localStorage are persisted to disk in real time.
3. **Session Auto-Rehydration & Self-Healing**: If a session was closed or the machine restarted, passing `session_id` or `profile_name` to ANY tool automatically restores the persistent browser context and resumes at the last known URL.

> [!IMPORTANT]
> **Native Tool Calling Invariant**: All `camoufox_*` tools (`camoufox_session_start`, `camoufox_inspect_dom`, `camoufox_act`, `camoufox_screenshot`, `camoufox_search`, `camoufox_fetch_page`) are native tools provided via Model Context Protocol (MCP). Always call them directly as native tool calls. NEVER execute inline python scripts via `run_command` or shell commands to call `dispatch_tool` or `server.py`.


---

## 2. Tool Selection Strategy & Workflows

### Pattern A: Stealth Search & Fast Lookup (100% Replaces `web_search`)
To search the public web without getting blocked:
1. Call `camoufox_search(query="...", engine="duckduckgo"|"google", locale="en-US"|"de-DE")`.
2. Inspect the returned list of results (`title`, `snippet`, `url`).
3. To read a target result, call `camoufox_fetch_page(url="...", extract_mode="markdown")`.

### Pattern B: Single-Page Extraction & Authenticated Scraping
- **Article / Documentation**: Call `camoufox_fetch_page(url="...")` to receive token-optimized Readability Markdown with cookie banners auto-dismissed.
- **Authenticated Page**: Call `camoufox_fetch_page(url="...", profile_name="my_account")` to fetch data behind login walls.
- **Product / E-commerce / Schema**: Call `camoufox_extract_json_ld(url="...", profile_name="my_account")` to extract Schema.org JSON-LD directly.
- **Repeating Lists / Tables**: Call `camoufox_scrape_selector(url="...", selector=".item", fields={"name": "h3", "link": "a@href", "price": ".price"}, profile_name="my_account")`.

### Pattern C: Dynamic SPA & API Interception (Zero DOM Scraping)
For dynamic platforms (TikTok, Pinterest, Amazon BSR, Spreadshirt, GraphQL SPAs):
1. Call `camoufox_intercept_api(url="...", url_pattern="/api/|/graphql", profile_name="...")`.
2. Get the exact server JSON payloads directly from network responses without parsing messy DOM elements.
3. For trend discovery, use `camoufox_discover_trends(platform="tiktok"|"pinterest"|"google_trends", country="DE"|"US")`.

### Pattern D: Multi-Turn Continuous Interactive Agent Loop
For multi-step tasks (logging in, filling forms, multi-turn shopping, pagination, OAuth popups):
1. **Start or Resume Session**:
   `session_id = camoufox_session_start(profile_name="shopee_vn", initial_url="https://...")`
2. **Inspect Elements**:
   Call `camoufox_inspect_dom(session_id=session_id, mode="a11y_tree")`. This outputs numbered elements (`[1] BUTTON: "Search"`, `[2] INPUT: "Keywords"`).
3. **Execute Actions**:
   - **Type in input**: `camoufox_act(session_id=session_id, action="type", target_id=2, value="Mechanical Keyboard")`
   - **Click button**: `camoufox_act(session_id=session_id, action="click", target_id=1)`
   - **Click link expecting popup/OAuth login**: `camoufox_act(session_id=session_id, action="click", target_id=5, expects_popup=True)` (automatically waits for and focuses popup tab)
   - **Switch tabs**: `camoufox_act(session_id=session_id, action="switch_tab", value="1")`
   - **Select dropdown**: `camoufox_act(session_id=session_id, action="select_option", target_id=3, value="OptionValue")`
   - **Check / Uncheck box**: `camoufox_act(session_id=session_id, action="check", target_id=4)`
   - **Scroll down**: `camoufox_act(session_id=session_id, action="scroll", value="600")`
   - **Navigate / History**: `camoufox_act(session_id=session_id, action="go_back")` or `action="reload"`
4. **List Live Sessions & Tabs**:
   Call `camoufox_session_list()` to see all open tabs and stored profiles.
5. **Save State Checkpoint**:
   Call `camoufox_session_save(profile_name="shopee_vn")` to checkpoint state without closing the browser.
6. **Capture Proof**:
   Call `camoufox_screenshot(session_id=session_id)`.
7. **Close When Completely Done**:
   Call `camoufox_session_close(session_id=session_id)` when the entire multi-turn task is finished.

### Pattern E: Stealth Media Sniffing & Downloader (YouTube, TikTok, Facebook, HLS/DASH)
Use this pattern whenever the user asks to download, extract, inspect, or clip videos/audio from media sites (YouTube, TikTok, Facebook, Instagram, Twitter/X, Vimeo, HLS/DASH streams, or direct HTML5 media).

#### Step 1: Sniff Media Streams
Call `camoufox_sniff_media` to let Camoufox navigate to the page, execute JS player scripts, bypass anti-bot tokens, escalate resolution to 1080p/4K, and capture pre-authenticated decrypted CDN URLs:
```json
{
  "name": "camoufox_sniff_media",
  "arguments": {
    "url": "https://www.youtube.com/watch?v=VIDEO_ID",
    "target_quality": "highest",
    "wait_time_ms": 4000
  }
}
```

The tool returns structured metadata and stream categories:
- `title`: Video title from DOM or player response.
- `stream_topology`: `"dual_adaptive"`, `"single_muxed"`, or `"manifest"`.
- `recommended_pairs`: Ready-to-download pairs of best video track + best audio track (for YouTube/DASH).
- `video_streams`: List of available video formats (resolutions, itags, codecs, bitrates).
- `audio_streams`: List of available audio tracks.
- `direct_streams`: List of standalone media container URLs (TikTok, Instagram, direct MP4).
- `manifest_streams`: List of HLS (`.m3u8`) or DASH (`.mpd`) playlists.

---

#### Step 2: Download & Multiplex via FFmpeg

Select the appropriate download strategy based on `stream_topology`:

##### Scenario A: YouTube 1080p / 2K / 4K (Dual Adaptive Muxing)
YouTube separates video and audio tracks at 1080p and higher. Pass both `video_url` and `audio_url` from `recommended_pairs[0]`:
```json
{
  "name": "camoufox_download_media",
  "arguments": {
    "video_url": "https://rr3---sn-....googlevideo.com/videoplayback?...",
    "audio_url": "https://rr3---sn-....googlevideo.com/videoplayback?...",
    "output_filename": "my_video_1080p.mp4"
  }
}
```
*FFmpeg automatically runs lossless stream copy (`-c:v copy -c:a copy -map 0:v:0 -map 1:a:0 -avoid_negative_ts make_zero`), completing the download and remuxing in seconds without CPU re-encoding.*

##### Scenario B: TikTok, Facebook Reels, Direct MP4 (Single Muxed)
Platforms like TikTok provide a single container URL containing both audio and video:
```json
{
  "name": "camoufox_download_media",
  "arguments": {
    "video_url": "https://v16-webapp-prime.tiktokcdn.com/...",
    "output_filename": "tiktok_clip.mp4"
  }
}
```

##### Scenario C: Extract Audio Track Only
To download just the audio (podcast, song, speech):
```json
{
  "name": "camoufox_download_media",
  "arguments": {
    "video_url": "https://rr3---sn-....googlevideo.com/videoplayback?mime=audio%2Fmp4...",
    "output_filename": "audio_track.m4a"
  }
}
```

##### Scenario D: Precise Highlight Trimming & Clipping
To cut a specific snippet (e.g. from second 30 to second 50):
```json
{
  "name": "camoufox_download_media",
  "arguments": {
    "video_url": "https://...",
    "audio_url": "https://...",
    "start_time": "00:00:30",
    "duration": "20",
    "accurate_trim": true,
    "output_filename": "highlight_clip.mp4"
  }
}
```
> [!TIP]
> - `accurate_trim: false` (default): Fast lossless seek and copy (`-avoid_negative_ts make_zero`).
> - `accurate_trim: true`: Re-encodes with ultrafast preset (`-preset ultrafast -crf 20`) to prevent frozen initial frames or audio desync when cutting on non-keyframes.

##### Scenario E: Authenticated / Age-Restricted / Member Videos
If a video requires login (YouTube Premium, age confirmation, member-only):
1. First, authenticate in a named profile (e.g. `profile_name="google_main"`).
2. Pass `profile_name="google_main"` to both `camoufox_sniff_media` and `camoufox_download_media`. Camoufox will automatically pass authenticated session cookies and headers to both the browser and FFmpeg.

---

## 3. Invariants & Best Practices
- **Never Worry About Subprocess Exits**: The browser lives in the background daemon. Your session remains open across turns.
- **Profile Reuse**: Always use specific `profile_name`s (e.g. `shopee_vn`, `tiktok_de`, `amazon_us`, `google_main`) so logins and auth cookies persist across days.
- **Auto-Rehydration Recovery**: If a session was auto-rehydrated after a system reboot, run `camoufox_inspect_dom` once to check current page state.
- **Token Efficiency**: Use default `extract_mode="markdown"` to save ~75% tokens.
- **FFmpeg Zero-Setup**: FFmpeg is automatically discovered from PATH or Python's `imageio-ffmpeg` static binary bundle.
- **Corporate Firewall Handling**: If a target returns `"Application Control Violation"` or `"The URL you requested has been blocked"`, an enterprise firewall (FortiGate / Palo Alto) is blocking social media/streaming at the gateway layer. Advise the user to use a VPN, home network, or VPS connection.


