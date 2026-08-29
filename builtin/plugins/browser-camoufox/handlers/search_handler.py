"""
Handler for camoufox_search and camoufox_discover_trends.
Provides stealth search and trend discovery without relying on legacy web_search.
"""

import urllib.parse
from typing import Any, Dict, List, Optional

from core.browser_manager import BrowserManager
from perception.readability_cleaner import dismiss_cookie_banners


def _search_duckduckgo(page: Any, query: str, max_results: int) -> List[Dict[str, Any]]:
    """Executes search via DuckDuckGo HTML/Lite interface."""
    encoded_query = urllib.parse.quote_plus(query)
    search_url = f"https://html.duckduckgo.com/html/?q={encoded_query}"
    page.goto(search_url, wait_until="domcontentloaded", timeout=25000)

    js_extract = """
    (maxCount) => {
        const items = [];
        const links = document.querySelectorAll('.result__body');
        let pos = 1;
        for (const item of links) {
            const titleEl = item.querySelector('.result__title a');
            const snippetEl = item.querySelector('.result__snippet');
            if (titleEl) {
                const title = titleEl.innerText.trim();
                let href = titleEl.getAttribute('href') || '';
                // Duckduckgo wrap url decoding
                if (href.includes('uddg=')) {
                    const match = href.match(/uddg=([^&]+)/);
                    if (match) {
                        try { href = decodeURIComponent(match[1]); } catch(e) {}
                    }
                }
                const snippet = snippetEl ? snippetEl.innerText.trim() : '';
                items.push({
                    position: pos,
                    title: title,
                    snippet: snippet,
                    url: href
                });
                pos++;
                if (items.length >= maxCount) break;
            }
        }
        return items;
    }
    """
    return page.evaluate(js_extract, max_results)


def _search_google(page: Any, query: str, locale: str, max_results: int) -> List[Dict[str, Any]]:
    """Executes search via Google with stealth banner handling."""
    encoded_query = urllib.parse.quote_plus(query)
    lang = locale.split("-")[0] if "-" in locale else "en"
    search_url = f"https://www.google.com/search?q={encoded_query}&hl={lang}"
    page.goto(search_url, wait_until="domcontentloaded", timeout=25000)
    dismiss_cookie_banners(page)

    js_extract = """
    (maxCount) => {
        const items = [];
        const entries = document.querySelectorAll('div.g, div[data-hveid]');
        let pos = 1;
        for (const entry of entries) {
            const titleEl = entry.querySelector('h3');
            const linkEl = entry.querySelector('a');
            if (titleEl && linkEl) {
                const title = titleEl.innerText.trim();
                const href = linkEl.getAttribute('href') || '';
                if (!href.startsWith('http') || href.includes('google.com/search')) continue;

                // Find snippet text
                let snippet = '';
                const snippetEl = entry.querySelector('div[data-sncf], div[style*="-webkit-line-clamp"], .VwiC3b');
                if (snippetEl) {
                    snippet = snippetEl.innerText.trim();
                }

                items.push({
                    position: pos,
                    title: title,
                    snippet: snippet,
                    url: href
                });
                pos++;
                if (items.length >= maxCount) break;
            }
        }
        return items;
    }
    """
    return page.evaluate(js_extract, max_results)


def _search_bing(page: Any, query: str, max_results: int) -> List[Dict[str, Any]]:
    """Executes search via Bing."""
    encoded_query = urllib.parse.quote_plus(query)
    search_url = f"https://www.bing.com/search?q={encoded_query}"
    page.goto(search_url, wait_until="domcontentloaded", timeout=25000)
    dismiss_cookie_banners(page)

    js_extract = """
    (maxCount) => {
        const items = [];
        const entries = document.querySelectorAll('li.b_algo');
        let pos = 1;
        for (const entry of entries) {
            const titleEl = entry.querySelector('h2 a');
            const snippetEl = entry.querySelector('.b_caption p, .b_snippet');
            if (titleEl) {
                const title = titleEl.innerText.trim();
                const href = titleEl.getAttribute('href') || '';
                const snippet = snippetEl ? snippetEl.innerText.trim() : '';
                items.push({
                    position: pos,
                    title: title,
                    snippet: snippet,
                    url: href
                });
                pos++;
                if (items.length >= maxCount) break;
            }
        }
        return items;
    }
    """
    return page.evaluate(js_extract, max_results)


def handle_search(
    query: str,
    engine: str = "duckduckgo",
    locale: str = "en-US",
    max_results: int = 5,
    timeout_ms: int = 30000,
    profile_name: Optional[str] = None,
) -> Dict[str, Any]:
    """
    Performs a stealth search query and returns cleaned search items.
    """
    mgr = BrowserManager.get_instance()
    engine_clean = engine.lower().strip()

    def run(page: Any) -> List[Dict[str, Any]]:
        if engine_clean == "google":
            return _search_google(page, query, locale, max_results)
        elif engine_clean == "bing":
            return _search_bing(page, query, max_results)
        else:
            # Default to DuckDuckGo
            return _search_duckduckgo(page, query, max_results)

    try:
        results = mgr.run_stateless(run, headless=True, locale=locale, timeout_ms=timeout_ms, profile_name=profile_name)
        return {
            "query": query,
            "engine": engine_clean,
            "count": len(results),
            "results": results,
        }
    except Exception as e:
        return {
            "query": query,
            "engine": engine_clean,
            "count": 0,
            "results": [],
            "error": str(e),
        }


def handle_discover_trends(
    platform: str,
    country: str = "US",
    category: Optional[str] = None,
    timeout_ms: int = 35000,
) -> Dict[str, Any]:
    """
    Fetches trending topics, hashtags, and keywords from major creative and search platforms.
    """
    mgr = BrowserManager.get_instance()
    platform_clean = platform.lower().strip()

    def run(page: Any) -> Dict[str, Any]:
        if platform_clean == "tiktok":
            url = f"https://ads.tiktok.com/business/creativecenter/inspiration/popular/hashtag/pc/en?region={country.upper()}"
            page.goto(url, wait_until="domcontentloaded", timeout=30000)
            page.wait_for_timeout(3000)
            dismiss_cookie_banners(page)

            js_tiktok = """
            () => {
                const trends = [];
                const cards = document.querySelectorAll('[class*="rankingItem"], [class*="cardWrapper"], tr');
                let rank = 1;
                for (const c of cards) {
                    const text = c.innerText.trim();
                    if (text && text.includes('#')) {
                        const lines = text.split('\\n').map(l => l.trim()).filter(l => l.length > 0);
                        const tag = lines.find(l => l.startsWith('#')) || lines[0];
                        trends.push({
                            rank: rank,
                            keyword: tag,
                            details: lines.join(' | ')
                        });
                        rank++;
                        if (trends.length >= 15) break;
                    }
                }
                return trends;
            }
            """
            items = page.evaluate(js_tiktok)
            return {"platform": "tiktok", "country": country, "trends": items}

        elif platform_clean == "google_trends":
            geo = country.upper()
            url = f"https://trends.google.com/trending?geo={geo}"
            page.goto(url, wait_until="domcontentloaded", timeout=30000)
            dismiss_cookie_banners(page)
            page.wait_for_timeout(2500)

            js_gtrends = """
            () => {
                const trends = [];
                const rows = document.querySelectorAll('tr, div[role="row"], .mFe1ob');
                let rank = 1;
                for (const row of rows) {
                    const title = row.querySelector('.title, .mFe1ob, .mZRIEd, td:nth-child(2)');
                    const searchVol = row.querySelector('.search-count-title, td:nth-child(3)');
                    if (title) {
                        const name = title.innerText.trim();
                        if (name && name !== 'Title' && name !== 'Search term') {
                            trends.push({
                                rank: rank,
                                keyword: name,
                                volume: searchVol ? searchVol.innerText.trim() : 'N/A'
                            });
                            rank++;
                            if (trends.length >= 20) break;
                        }
                    }
                }
                return trends;
            }
            """
            items = page.evaluate(js_gtrends)
            return {"platform": "google_trends", "country": country, "trends": items}

        elif platform_clean == "pinterest":
            geo = country.upper()
            url = f"https://trends.pinterest.com/?country={geo}"
            page.goto(url, wait_until="domcontentloaded", timeout=30000)
            dismiss_cookie_banners(page)
            page.wait_for_timeout(3000)

            js_pin = """
            () => {
                const trends = [];
                const rows = document.querySelectorAll('tr, [data-test-id*="trend-row"]');
                let rank = 1;
                for (const r of rows) {
                    const text = r.innerText.trim();
                    if (text && !text.includes('Search term')) {
                        const parts = text.split('\\n').filter(p => p.trim().length > 0);
                        if (parts.length >= 1) {
                            trends.push({
                                rank: rank,
                                keyword: parts[0],
                                metrics: parts.slice(1).join(' | ')
                            });
                            rank++;
                            if (trends.length >= 15) break;
                        }
                    }
                }
                return trends;
            }
            """
            items = page.evaluate(js_pin)
            return {"platform": "pinterest", "country": country, "trends": items}

        else:
            return {"platform": platform, "country": country, "trends": [], "error": f"Unsupported platform: {platform}"}

    try:
        return mgr.run_stateless(run, headless=True, locale=f"en-{country.upper()}", timeout_ms=timeout_ms)
    except Exception as e:
        return {
            "platform": platform,
            "country": country,
            "trends": [],
            "error": str(e),
        }
