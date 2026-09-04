"""
Core Media Sniffer for Camoufox Browser.
Intercepts live media requests, HLS playlists, DASH manifests, and video/audio chunks directly from network traffic.
Captures pre-authenticated, decrypted CDN URLs (e.g. googlevideo.com/videoplayback) without requiring reverse engineering.
"""

import re
import sys
import threading
from typing import Any, Dict, List, Optional
from urllib.parse import parse_qs, urlencode, urlparse, urlunparse


class MediaSniffer:
    """Network sniffer specialized for intercepting and categorizing streaming media."""

    # Common media patterns
    RE_YOUTUBE_PLAYBACK = re.compile(r"googlevideo\.com/videoplayback", re.IGNORECASE)
    RE_HLS_MANIFEST = re.compile(r"\.m3u8(\?.*)?$", re.IGNORECASE)
    RE_DASH_MANIFEST = re.compile(r"\.mpd(\?.*)?$", re.IGNORECASE)
    RE_DIRECT_MEDIA = re.compile(r"\.(mp4|webm|m4s|m4a|mp3|aac|flv)(\?.*)?$", re.IGNORECASE)
    RE_CDN_MEDIA = re.compile(r"(tiktokcdn\.com|fbcdn\.net|twimg\.com|cdninstagram\.com)", re.IGNORECASE)

    def __init__(self, max_records: int = 200):
        self.max_records = max_records
        self.lock = threading.Lock()
        self.captured_requests: List[Dict[str, Any]] = []
        self.captured_responses: List[Dict[str, Any]] = []
        self.headers_by_domain: Dict[str, Dict[str, str]] = {}

    def attach_to_page(self, page: Any) -> None:
        """Attaches request and response listeners to Playwright page."""
        page.on("request", self._handle_request)
        page.on("response", self._handle_response)

    def _handle_request(self, request: Any) -> None:
        """Captures media requests, decrypted CDN URLs, and auth headers."""
        try:
            url = request.url
            method = request.method
            headers = request.headers or {}

            # Cache domain-level headers (User-Agent, Cookie, Referer)
            try:
                domain = urlparse(url).netloc
                if domain:
                    with self.lock:
                        if domain not in self.headers_by_domain:
                            self.headers_by_domain[domain] = {}
                        for h in ["user-agent", "cookie", "referer", "origin"]:
                            if h in headers:
                                self.headers_by_domain[domain][h] = headers[h]
            except Exception:
                pass

            # Detect streaming media patterns
            is_yt = bool(self.RE_YOUTUBE_PLAYBACK.search(url))
            is_hls = bool(self.RE_HLS_MANIFEST.search(url))
            is_dash = bool(self.RE_DASH_MANIFEST.search(url))
            resource_type = getattr(request, "resource_type", "")
            if resource_type in ["document", "stylesheet", "image", "font"] and not (is_hls or is_dash or is_yt):
                return

            is_direct = bool(self.RE_DIRECT_MEDIA.search(url) or self.RE_CDN_MEDIA.search(url))

            if not (is_yt or is_hls or is_dash or is_direct or resource_type == "media"):
                return

            record = {
                "url": url,
                "method": method,
                "resource_type": resource_type,
                "headers": headers,
                "is_youtube": is_yt,
                "is_hls": is_hls,
                "is_dash": is_dash,
                "is_direct": is_direct,
            }

            if is_yt:
                record["yt_params"] = self._parse_youtube_url(url)

            with self.lock:
                if len(self.captured_requests) >= self.max_records:
                    self.captured_requests.pop(0)
                self.captured_requests.append(record)

        except Exception as e:
            sys.stderr.write(f"[CAMOUFOX_MEDIA_SNIFFER] Error handling request: {e}\n")

    def _handle_response(self, response: Any) -> None:
        """Captures media responses for content-type verification."""
        try:
            url = response.url
            content_type = response.headers.get("content-type", "").lower()
            status = response.status

            is_media_ct = any(m in content_type for m in [
                "video/", "audio/", "application/vnd.apple.mpegurl",
                "application/x-mpegurl", "application/dash+xml",
            ])
            is_yt = bool(self.RE_YOUTUBE_PLAYBACK.search(url))
            is_hls = bool(self.RE_HLS_MANIFEST.search(url))
            is_dash = bool(self.RE_DASH_MANIFEST.search(url))

            if not (is_media_ct or is_yt or is_hls or is_dash):
                return

            with self.lock:
                if len(self.captured_responses) >= self.max_records:
                    self.captured_responses.pop(0)
                self.captured_responses.append({
                    "url": url,
                    "status": status,
                    "content_type": content_type,
                    "headers": response.headers,
                })
        except Exception as e:
            sys.stderr.write(f"[CAMOUFOX_MEDIA_SNIFFER] Error handling response: {e}\n")

    @staticmethod
    def _parse_youtube_url(url: str) -> Dict[str, Any]:
        """Extracts itag, mime, signature, and constructs a clean un-ranged URL."""
        parsed = urlparse(url)
        params = parse_qs(parsed.query)

        itag = params.get("itag", [""])[0]
        mime = params.get("mime", [""])[0]
        sig = params.get("sig", [""])[0] or params.get("lsig", [""])[0]
        sparams = params.get("sparams", [""])[0]
        dur = params.get("dur", [""])[0]
        range_param = params.get("range", [""])[0]

        # Construct a clean URL without range query for full stream downloads
        clean_params = {k: v for k, v in params.items() if k != "range"}
        # Ensure ratebypass is set if available
        if "ratebypass" not in clean_params:
            clean_params["ratebypass"] = ["yes"]
        clean_query = urlencode(clean_params, doseq=True)
        clean_url = urlunparse((
            parsed.scheme,
            parsed.netloc,
            parsed.path,
            parsed.params,
            clean_query,
            parsed.fragment,
        ))

        return {
            "itag": int(itag) if itag.isdigit() else itag,
            "mime": mime,
            "is_video": mime.startswith("video/"),
            "is_audio": mime.startswith("audio/"),
            "sig": sig,
            "sparams": sparams,
            "dur": float(dur) if dur else None,
            "range": range_param,
            "clean_url": clean_url,
        }

    def get_youtube_streams(self) -> Dict[str, List[Dict[str, Any]]]:
        """Groups captured YouTube requests by video vs audio and by itag."""
        videos: Dict[int, Dict[str, Any]] = {}
        audios: Dict[int, Dict[str, Any]] = {}

        with self.lock:
            for req in self.captured_requests:
                if not req.get("is_youtube") or "yt_params" not in req:
                    continue
                yp = req["yt_params"]
                itag = yp.get("itag")
                if not itag:
                    continue

                item = {
                    "itag": itag,
                    "mime": yp.get("mime", ""),
                    "raw_url": req["url"],
                    "clean_url": yp.get("clean_url", req["url"]),
                    "dur": yp.get("dur"),
                    "headers": req.get("headers", {}),
                }

                if yp.get("is_video"):
                    videos[itag] = item
                elif yp.get("is_audio"):
                    audios[itag] = item

        return {
            "videos": list(videos.values()),
            "audios": list(audios.values()),
        }

    def get_manifests(self) -> List[Dict[str, Any]]:
        """Returns captured HLS or DASH manifests."""
        manifests = []
        with self.lock:
            for req in self.captured_requests:
                if req.get("is_hls") or req.get("is_dash"):
                    manifests.append({
                        "url": req["url"],
                        "type": "hls" if req.get("is_hls") else "dash",
                        "headers": req.get("headers", {}),
                    })
        return manifests

    def get_direct_streams(self) -> List[Dict[str, Any]]:
        """Returns direct video/audio URLs (e.g. TikTok, direct MP4)."""
        direct = []
        with self.lock:
            for req in self.captured_requests:
                if req.get("is_direct") and not req.get("is_youtube"):
                    direct.append({
                        "url": req["url"],
                        "headers": req.get("headers", {}),
                        "resource_type": req.get("resource_type", ""),
                    })
        return direct

    def get_all_records(self) -> Dict[str, Any]:
        """Returns a snapshot of all captured media."""
        return {
            "requests_count": len(self.captured_requests),
            "responses_count": len(self.captured_responses),
            "youtube": self.get_youtube_streams(),
            "manifests": self.get_manifests(),
            "direct_streams": self.get_direct_streams(),
        }

    def clear(self) -> None:
        """Clears captured requests and responses."""
        with self.lock:
            self.captured_requests.clear()
            self.captured_responses.clear()
            self.headers_by_domain.clear()
