"""
Network Sniffer for intercepting XHR, Fetch, and GraphQL background responses.
Captures raw JSON payloads directly from the network layer without DOM overhead.
"""

import json
import re
import sys
from typing import Any, Dict, List, Optional


class NetworkSniffer:
    """Interceptors for page-level network requests and responses."""

    def __init__(self, url_pattern: Optional[str] = None, max_payloads: int = 50):
        self.max_payloads = max_payloads
        self.pattern: Optional[re.Pattern] = None
        if url_pattern:
            try:
                self.pattern = re.compile(url_pattern, re.IGNORECASE)
            except re.error as e:
                sys.stderr.write(f"[CAMOUFOX_SNIFFER] Invalid regex pattern '{url_pattern}': {e}\n")
                self.pattern = None

        self.captured_payloads: List[Dict[str, Any]] = []

    def attach_to_page(self, page: Any) -> None:
        """Attaches response handler to the given Playwright page."""
        page.on("response", self._handle_response)

    def _handle_response(self, response: Any) -> None:
        """Processes each network response and saves matching JSON payloads."""
        try:
            url = response.url
            if self.pattern and not self.pattern.search(url):
                return

            # Skip common non-API static resource types
            content_type = response.headers.get("content-type", "").lower()
            if any(ext in content_type for ext in ["image/", "video/", "audio/", "font/", "text/css"]):
                return

            status = response.status
            if status >= 400:
                return

            # Attempt to parse response body as JSON
            payload: Optional[Any] = None
            try:
                body_bytes = response.body()
                if body_bytes:
                    text = body_bytes.decode("utf-8", errors="replace").strip()
                    if (text.startswith("{") and text.endswith("}")) or (text.startswith("[") and text.endswith("]")):
                        payload = json.loads(text)
            except Exception:
                pass

            if payload is not None:
                if len(self.captured_payloads) >= self.max_payloads:
                    # Drop oldest to prevent unbounded memory growth
                    self.captured_payloads.pop(0)

                self.captured_payloads.append({
                    "url": url,
                    "status": status,
                    "content_type": content_type,
                    "payload": payload,
                })
        except Exception as e:
            sys.stderr.write(f"[CAMOUFOX_SNIFFER] Error handling response: {e}\n")

    def get_captured(self) -> List[Dict[str, Any]]:
        """Returns a copy of all captured JSON payloads."""
        return list(self.captured_payloads)

    def clear(self) -> None:
        """Clears all captured payloads."""
        self.captured_payloads.clear()
