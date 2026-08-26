---
name: web-browse-camoufox
description: >-
  Use this skill when the user asks to browse a website, fetch live web content, or scrape a URL stealthily.
---

# Stealth Web Browsing with Camoufox

To read web pages without getting blocked:
1. Call tool `camoufox_fetch_page` with the target URL.
2. Parse the returned JSON containing `title` and `content`.
3. Provide a concise, structured markdown summary to the user.
