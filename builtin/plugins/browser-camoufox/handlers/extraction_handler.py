"""
Handler for camoufox_fetch_page, camoufox_extract_json_ld, and camoufox_scrape_selector.
Provides clean, structured, and token-optimized extraction from modern websites.
"""

import json
from typing import Any, Dict, List, Optional

from core.browser_manager import BrowserManager
from perception.readability_cleaner import dismiss_cookie_banners, extract_clean_markdown


def handle_fetch_page(
    url: str,
    extract_mode: str = "markdown",
    auto_dismiss_banners: bool = True,
    timeout_ms: int = 30000,
) -> Dict[str, Any]:
    """
    Fetches a web page using Camoufox stealth browser and returns cleaned content.
    """
    mgr = BrowserManager.get_instance()

    def run(page: Any) -> Dict[str, Any]:
        page.goto(url, wait_until="domcontentloaded", timeout=timeout_ms)
        if auto_dismiss_banners:
            dismiss_cookie_banners(page)

        title = page.title()
        current_url = page.url

        if extract_mode == "raw_html":
            html = page.content()
            return {
                "url": current_url,
                "title": title,
                "content": html[:30000],
                "word_count": len(html.split()),
                "char_count": len(html),
                "truncated": len(html) > 30000,
            }
        elif extract_mode == "text":
            text = page.inner_text("body")
            truncated = False
            if len(text) > 15000:
                text = text[:15000] + "\n... [Content Truncated]"
                truncated = True
            return {
                "url": current_url,
                "title": title,
                "content": text,
                "word_count": len(text.split()),
                "char_count": len(text),
                "truncated": truncated,
            }
        else:
            # Default to Readability Markdown
            html = page.content()
            res = extract_clean_markdown(html)
            return {
                "url": current_url,
                "title": title,
                "content": res["content"],
                "word_count": res["word_count"],
                "char_count": res["char_count"],
                "truncated": res["truncated"],
            }

    try:
        return mgr.run_stateless(run, headless=True, timeout_ms=timeout_ms)
    except Exception as e:
        return {
            "url": url,
            "title": "",
            "content": "",
            "error": str(e),
            "word_count": 0,
            "truncated": False,
        }


def handle_extract_json_ld(url: str, timeout_ms: int = 30000) -> Dict[str, Any]:
    """
    Extracts Schema.org JSON-LD scripts, OpenGraph, and Twitter metadata from a page.
    """
    mgr = BrowserManager.get_instance()

    def run(page: Any) -> Dict[str, Any]:
        page.goto(url, wait_until="domcontentloaded", timeout=timeout_ms)
        title = page.title()

        js_extract = """
        () => {
            const meta = {};
            const jsonLd = [];

            // 1. JSON-LD scripts
            const scripts = document.querySelectorAll('script[type="application/ld+json"]');
            for (const s of scripts) {
                try {
                    const parsed = JSON.parse(s.innerText);
                    jsonLd.push(parsed);
                } catch(e) {}
            }

            // 2. OpenGraph & Twitter Meta tags
            const metaTags = document.querySelectorAll('meta');
            for (const m of metaTags) {
                const prop = m.getAttribute('property') || m.getAttribute('name');
                const content = m.getAttribute('content');
                if (prop && content) {
                    if (prop.startsWith('og:') || prop.startsWith('twitter:') || prop === 'description' || prop === 'keywords') {
                        meta[prop] = content;
                    }
                }
            }

            // 3. Canonical link
            const canonical = document.querySelector('link[rel="canonical"]');
            if (canonical) {
                meta['canonical'] = canonical.getAttribute('href');
            }

            return {
                json_ld: jsonLd,
                meta: meta
            };
        }
        """
        data = page.evaluate(js_extract)
        return {
            "url": page.url,
            "title": title,
            "json_ld": data.get("json_ld", []),
            "meta": data.get("meta", {}),
        }

    try:
        return mgr.run_stateless(run, headless=True, timeout_ms=timeout_ms)
    except Exception as e:
        return {
            "url": url,
            "title": "",
            "json_ld": [],
            "meta": {},
            "error": str(e),
        }


def handle_scrape_selector(
    url: str,
    selector: str,
    fields: Optional[Dict[str, str]] = None,
    limit: int = 20,
    timeout_ms: int = 30000,
) -> Dict[str, Any]:
    """
    Extracts structured items from a page using CSS selectors and field mappings.
    """
    mgr = BrowserManager.get_instance()

    def run(page: Any) -> Dict[str, Any]:
        page.goto(url, wait_until="domcontentloaded", timeout=timeout_ms)
        dismiss_cookie_banners(page)

        field_map = fields or {}
        js_scrape = """
        (args) => {
            const { selector, fields, limit } = args;
            const elements = document.querySelectorAll(selector);
            const items = [];

            for (const el of elements) {
                if (items.length >= limit) break;

                const item = {};
                if (Object.keys(fields).length === 0) {
                    // Extract full text if no field map provided
                    item['text'] = el.innerText.trim();
                    if (el.tagName.toLowerCase() === 'a' && el.getAttribute('href')) {
                        item['href'] = el.getAttribute('href');
                    }
                } else {
                    for (const [fieldKey, fieldSelector] of Object.entries(fields)) {
                        try {
                            const trimmedSel = fieldSelector.trim();
                            if (trimmedSel.startsWith('@')) {
                                // Extract attribute from root element (e.g. '@href', '@src')
                                const attrName = trimmedSel.substring(1);
                                item[fieldKey] = el.getAttribute(attrName) || '';
                            } else if (trimmedSel.includes('@')) {
                                // Sub-selector + attribute (e.g. 'a@href', 'img@src')
                                const parts = trimmedSel.split('@');
                                const subEl = (parts[0] === '' || parts[0] === '.') ? el : el.querySelector(parts[0]);
                                item[fieldKey] = subEl ? (subEl.getAttribute(parts[1]) || '') : '';
                            } else if (trimmedSel === '' || trimmedSel === '.' || trimmedSel === selector || (el.matches && el.matches(trimmedSel))) {
                                // Self element text
                                item[fieldKey] = el.innerText.trim();
                            } else {
                                // Sub-selector text
                                const subEl = el.querySelector(trimmedSel);
                                item[fieldKey] = subEl ? subEl.innerText.trim() : (el.matches && el.matches(trimmedSel) ? el.innerText.trim() : '');
                            }
                        } catch(e) {
                            item[fieldKey] = '';
                        }
                    }
                }
                items.push(item);
            }

            return items;
        }
        """
        items = page.evaluate(js_scrape, {"selector": selector, "fields": field_map, "limit": limit})
        return {
            "url": page.url,
            "selector": selector,
            "count": len(items),
            "items": items,
        }

    try:
        return mgr.run_stateless(run, headless=True, timeout_ms=timeout_ms)
    except Exception as e:
        return {
            "url": url,
            "selector": selector,
            "count": 0,
            "items": [],
            "error": str(e),
        }
