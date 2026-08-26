# Camoufox Autonomous Stealth Web Rules

- **Zero-Leak Invariant**: Always route external browsing through Camoufox stealth engine to maintain consistent TLS fingerprints, canvas noise, and spoofed hardware invariants.
- **Zero `web_search` Dependency**: When researching information, searching for products, or gathering live market data, always use `camoufox_search` or `camoufox_discover_trends`.
- **Token Efficiency Invariant**: Always prefer `extract_mode: "markdown"` or `camoufox_extract_json_ld` over raw HTML. Do not dump massive unparsed DOM trees into the conversation context.
- **Stateful Isolation**: For shopping or social platforms, always use `camoufox_session_start` with an appropriate `profile_name` to preserve cookies and session state across turns.
