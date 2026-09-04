"""
Handler for media sniffing and downloading tools.
Coordinates Camoufox BrowserManager, MediaSniffer, DOM/Player quality escalation,
and cross-platform lossless FFmpeg muxing & clipping.
"""

import json
import os
import re
import shutil
import subprocess
import sys
import time
from typing import Any, Dict, List, Optional, Tuple, Union
from urllib.parse import urlparse

from core.browser_manager import BrowserManager
from core.media_sniffer import MediaSniffer


ESCALATION_JS = """
(() => {
  try {
    // 1. YouTube HTML5 Player API
    const ytPlayer = document.getElementById('movie_player') || document.querySelector('.html5-video-player');
    if (ytPlayer) {
      if (typeof ytPlayer.mute === 'function') ytPlayer.mute();
      if (typeof ytPlayer.playVideo === 'function') ytPlayer.playVideo();

      let levels = [];
      if (typeof ytPlayer.getAvailableQualityLevels === 'function') {
        levels = ytPlayer.getAvailableQualityLevels();
      }

      const qualityMap = {
        '4k': ['hd2160', 'hd1440', 'hd1080'],
        '1440p': ['hd1440', 'hd1080'],
        '1080p': ['hd1080', 'hd720'],
        '720p': ['hd720', 'large'],
        'highest': ['hd2160', 'hd1440', 'hd1080', 'hd720', 'large', 'medium']
      };

      const desired = qualityMap[arguments[0] || 'highest'] || qualityMap['highest'];
      const chosen = desired.find(q => levels.includes(q)) || levels[0] || 'hd1080';

      if (typeof ytPlayer.setPlaybackQualityRange === 'function') {
        ytPlayer.setPlaybackQualityRange(chosen, chosen);
      }
      if (typeof ytPlayer.setPlaybackQuality === 'function') {
        ytPlayer.setPlaybackQuality(chosen);
      }

      // Small seek forward to bust 360p buffer and force CDN chunk fetch
      if (typeof ytPlayer.getCurrentTime === 'function' && typeof ytPlayer.seekTo === 'function') {
        const cur = ytPlayer.getCurrentTime();
        ytPlayer.seekTo(cur + 1, true);
      }

      return { status: "escalated_youtube", target: chosen, available: levels };
    }

    // 2. Generic HTML5 Video Tag (TikTok, Facebook, Twitter, HTML5)
    const video = document.querySelector('video');
    if (video) {
      video.muted = true;
      video.play().catch(() => {});
      return {
        status: "playing_html5",
        src: video.currentSrc || video.src,
        duration: video.duration
      };
    }

    return { status: "no_player_found" };
  } catch (err) {
    return { status: "error", error: String(err) };
  }
})
"""

JS_EXTRACT_METADATA = """
(() => {
  try {
    let result = {
      title: document.title || "",
      streamingData: null,
      videoDetails: null
    };

    if (window.ytInitialPlayerResponse) {
      if (window.ytInitialPlayerResponse.videoDetails) {
        result.videoDetails = window.ytInitialPlayerResponse.videoDetails;
      }
      if (window.ytInitialPlayerResponse.streamingData) {
        result.streamingData = window.ytInitialPlayerResponse.streamingData;
      }
    }

    // Fallback: search script tags for ytInitialPlayerResponse
    if (!result.streamingData) {
      const scripts = Array.from(document.querySelectorAll('script'));
      for (const s of scripts) {
        const text = s.textContent || "";
        if (text.includes('ytInitialPlayerResponse =')) {
          const match = text.match(/ytInitialPlayerResponse\\s*=\\s*({.+?});/);
          if (match) {
            try {
              const data = JSON.parse(match[1]);
              if (data.streamingData) result.streamingData = data.streamingData;
              if (data.videoDetails) result.videoDetails = data.videoDetails;
              break;
            } catch (e) {}
          }
        }
      }
    }

    return result;
  } catch (err) {
    return { error: String(err) };
  }
})()
"""


def _resolve_downloads_dir(agent_name: str, workspace_dir: Optional[str] = None, output_dir: Optional[str] = None) -> str:
    """Safely determines the target downloads directory."""
    if output_dir:
        target = os.path.abspath(output_dir)
        os.makedirs(target, exist_ok=True)
        return target

    if workspace_dir and os.path.isdir(workspace_dir):
        target = os.path.join(os.path.abspath(workspace_dir), "downloads")
        os.makedirs(target, exist_ok=True)
        return target

    base = os.path.join(os.path.expanduser("~"), ".agyent", "camoufox", "agents", agent_name, "downloads")
    os.makedirs(base, exist_ok=True)
    return base


def _build_ffmpeg_headers(
    user_agent: Optional[str] = None,
    cookies: Optional[List[Dict[str, Any]]] = None,
    referer: Optional[str] = None,
    custom_headers: Optional[Dict[str, str]] = None,
) -> str:
    """
    Constructs an FFmpeg -headers argument formatted strictly with \\r\\n line endings.
    FFmpeg requires every header followed by \\r\\n, including the last line.
    """
    lines: List[str] = []
    if user_agent:
        lines.append(f"User-Agent: {user_agent}")
    if referer:
        lines.append(f"Referer: {referer}")

    if cookies:
        cookie_parts = []
        for c in cookies:
            name = c.get("name")
            val = c.get("value")
            if name and val is not None:
                cookie_parts.append(f"{name}={val}")
        if cookie_parts:
            lines.append(f"Cookie: {'; '.join(cookie_parts)}")

    if custom_headers:
        for k, v in custom_headers.items():
            lines.append(f"{k}: {v}")

    if not lines:
        return ""
    return "\r\n".join(lines) + "\r\n"


def handle_sniff_media(
    url: str,
    session_id: Optional[str] = None,
    profile_name: Optional[str] = None,
    target_quality: str = "highest",
    wait_time_ms: int = 4000,
    timeout_ms: int = 30000,
    agent_name: Optional[str] = None,
    workspace_dir: Optional[str] = None,
    headless: Optional[Union[bool, str]] = None,
) -> Dict[str, Any]:
    """
    Navigates to URL, escalates player quality, captures clean CDN streams,
    and returns a structured catalog of media streams and topology.
    """
    mgr = BrowserManager.get_instance()
    agent = agent_name or os.environ.get("AGYENT_AGENT_NAME", "default")
    ws_dir = workspace_dir or os.environ.get("AGYENT_AGENT_WORKSPACE")

    target_profile = profile_name
    if session_id and not target_profile:
        sess = mgr.get_session(session_id, agent_name=agent, workspace_dir=ws_dir)
        if sess:
            target_profile = sess.profile_name

    def run(page: Any) -> Dict[str, Any]:
        sniffer = MediaSniffer(max_records=200)
        sniffer.attach_to_page(page)

        # 1. Navigate to target URL
        page.goto(url, wait_until="domcontentloaded", timeout=timeout_ms)

        # 2. Trigger Active Quality Escalation
        try:
            page.evaluate(ESCALATION_JS, target_quality)
        except Exception as e:
            sys.stderr.write(f"[CAMOUFOX_MEDIA] Escalation script error: {e}\n")

        # 3. Allow player buffer playback and CDN chunk requests
        wait_sec = max(1.5, wait_time_ms / 1000.0)
        page.wait_for_timeout(int(wait_sec * 1000))

        # 4. Extract JS Context & Metadata
        dom_meta = {}
        try:
            dom_meta = page.evaluate(JS_EXTRACT_METADATA) or {}
        except Exception:
            pass

        # 5. Extract Session Context for Downloader Auth
        auth_context = {
            "user_agent": page.evaluate("() => navigator.userAgent"),
            "referer": page.url,
            "cookies": page.context.cookies(),
        }

        # 6. Gather Sniffed Network Streams
        all_records = sniffer.get_all_records()
        yt_streams = all_records.get("youtube", {})
        manifests = all_records.get("manifests", [])
        direct_streams = all_records.get("direct_streams", [])

        # Process YouTube adaptive streams & match with DOM metadata
        recommended_pairs: List[Dict[str, Any]] = []
        video_streams: List[Dict[str, Any]] = []
        audio_streams: List[Dict[str, Any]] = []

        streaming_data = dom_meta.get("streamingData") or {}
        adaptive_formats = streaming_data.get("adaptiveFormats", [])
        combined_formats = streaming_data.get("formats", [])

        # Map sniffed clean URLs by itag
        sniffed_video_by_itag = {v["itag"]: v for v in yt_streams.get("videos", [])}
        sniffed_audio_by_itag = {a["itag"]: a for a in yt_streams.get("audios", [])}

        # Correlate adaptive video formats
        for fmt in adaptive_formats:
            itag = fmt.get("itag")
            mime = fmt.get("mimeType", "")
            is_video = "video/" in mime
            is_audio = "audio/" in mime

            # Prefer sniffed decrypted CDN URL over raw URL
            sniffed_item = sniffed_video_by_itag.get(itag) or sniffed_audio_by_itag.get(itag)
            stream_url = (sniffed_item["clean_url"] if sniffed_item else fmt.get("url")) or ""

            if is_video:
                video_streams.append({
                    "itag": itag,
                    "resolution": f"{fmt.get('width')}x{fmt.get('height')}",
                    "quality_label": fmt.get("qualityLabel", ""),
                    "fps": fmt.get("fps"),
                    "bitrate": fmt.get("bitrate"),
                    "codec": mime,
                    "url": stream_url,
                    "has_sniffed_url": bool(sniffed_item),
                })
            elif is_audio:
                audio_streams.append({
                    "itag": itag,
                    "bitrate": fmt.get("bitrate"),
                    "audio_quality": fmt.get("audioQuality", ""),
                    "codec": mime,
                    "url": stream_url,
                    "has_sniffed_url": bool(sniffed_item),
                })

        # Add any captured streams that weren't in adaptiveFormats
        for itag, v in sniffed_video_by_itag.items():
            if not any(item["itag"] == itag for item in video_streams):
                video_streams.append({
                    "itag": itag,
                    "resolution": "Unknown",
                    "quality_label": f"itag_{itag}",
                    "codec": v.get("mime", "video/mp4"),
                    "url": v["clean_url"],
                    "has_sniffed_url": True,
                })

        for itag, a in sniffed_audio_by_itag.items():
            if not any(item["itag"] == itag for item in audio_streams):
                audio_streams.append({
                    "itag": itag,
                    "codec": a.get("mime", "audio/mp4"),
                    "url": a["clean_url"],
                    "has_sniffed_url": True,
                })

        # Determine Stream Topology
        topology = "single_muxed"
        if video_streams and audio_streams:
            topology = "dual_adaptive"
        elif manifests:
            topology = "manifest"

        # Build recommended pair for dual_adaptive
        if topology == "dual_adaptive":
            # Pick best video with valid URL
            best_video = None
            for v in sorted(video_streams, key=lambda x: x.get("bitrate", 0) or 0, reverse=True):
                if v.get("url"):
                    best_video = v
                    break

            # Pick best audio with valid URL
            best_audio = None
            for a in sorted(audio_streams, key=lambda x: x.get("bitrate", 0) or 0, reverse=True):
                if a.get("url"):
                    best_audio = a
                    break

            if best_video and best_audio:
                recommended_pairs.append({
                    "label": f"{best_video.get('quality_label', 'HD')} + HQ Audio",
                    "video_itag": best_video.get("itag"),
                    "audio_itag": best_audio.get("itag"),
                    "video_url": best_video["url"],
                    "audio_url": best_audio["url"],
                    "resolution": best_video.get("resolution"),
                    "fps": best_video.get("fps"),
                })

        video_details = dom_meta.get("videoDetails") or {}
        return {
            "title": video_details.get("title") or dom_meta.get("title") or page.title(),
            "duration_seconds": int(video_details.get("lengthSeconds", 0)) if video_details.get("lengthSeconds") else None,
            "page_url": page.url,
            "stream_topology": topology,
            "recommended_pairs": recommended_pairs,
            "video_streams": video_streams[:15],
            "audio_streams": audio_streams[:10],
            "manifest_streams": manifests,
            "direct_streams": direct_streams,
            "auth_context": {
                "user_agent": auth_context["user_agent"],
                "referer": auth_context["referer"],
                "cookies_count": len(auth_context["cookies"]),
            },
        }

    try:
        return mgr.run_stateless(
            run,
            headless=headless,
            timeout_ms=timeout_ms + wait_time_ms,
            profile_name=target_profile,
            agent_name=agent,
            workspace_dir=ws_dir,
        )
    except Exception as e:
        return {
            "title": "",
            "page_url": url,
            "stream_topology": "unknown",
            "error": str(e),
            "video_streams": [],
            "audio_streams": [],
            "recommended_pairs": [],
        }


def _get_ffmpeg_bin() -> Optional[str]:
    """Locates FFmpeg binary in system PATH or via imageio_ffmpeg fallback."""
    bin_path = shutil.which("ffmpeg")
    if bin_path:
        return bin_path
    try:
        import imageio_ffmpeg
        exe = imageio_ffmpeg.get_ffmpeg_exe()
        if exe and os.path.exists(exe):
            return exe
    except Exception:
        pass
    return None


def handle_download_media(
    video_url: str,
    audio_url: Optional[str] = None,
    output_filename: Optional[str] = None,
    output_dir: Optional[str] = None,
    start_time: Optional[Union[str, int]] = None,
    duration: Optional[Union[str, int]] = None,
    accurate_trim: bool = False,
    custom_headers: Optional[Dict[str, str]] = None,
    session_id: Optional[str] = None,
    profile_name: Optional[str] = None,
    agent_name: Optional[str] = None,
    workspace_dir: Optional[str] = None,
) -> Dict[str, Any]:
    """
    Downloads and multiplexes video/audio streams via FFmpeg.
    Supports lossless stream copy (-c copy) or ultrafast re-encoding for frame-accurate cuts.
    """
    # 1. Pre-flight verification: Check if FFmpeg is available
    ffmpeg_bin = _get_ffmpeg_bin()
    if not ffmpeg_bin:
        return {
            "success": False,
            "error": (
                "FFmpeg executable not found in system PATH. FFmpeg is required for downloading, "
                "muxing adaptive streams, and trimming media. "
                "Installation instructions: "
                "Windows: winget install Gyan.FFmpeg or choco install ffmpeg; "
                "Ubuntu/Debian: sudo apt update && sudo apt install -y ffmpeg; "
                "macOS: brew install ffmpeg."
            )
        }

    agent = agent_name or os.environ.get("AGYENT_AGENT_NAME", "default")
    ws_dir = workspace_dir or os.environ.get("AGYENT_AGENT_WORKSPACE")

    # 2. Determine target output path
    target_dir = _resolve_downloads_dir(agent_name=agent, workspace_dir=ws_dir, output_dir=output_dir)
    if not output_filename:
        ts = int(time.time())
        output_filename = f"media_{ts}.mp4"
    elif not (output_filename.endswith(".mp4") or output_filename.endswith(".mkv") or output_filename.endswith(".webm")):
        output_filename += ".mp4"

    # Sanitize output filename
    safe_name = re.sub(r'[\\/*?:"<>|]', "_", output_filename)
    dest_path = os.path.abspath(os.path.join(target_dir, safe_name))

    # 3. Extract Session Auth (User-Agent, Cookies) if available
    user_agent: Optional[str] = None
    cookies: Optional[List[Dict[str, Any]]] = None
    referer: Optional[str] = None

    try:
        mgr = BrowserManager.get_instance()
        target_profile = profile_name
        if session_id and not target_profile:
            sess = mgr.get_session(session_id, agent_name=agent, workspace_dir=ws_dir)
            if sess:
                target_profile = sess.profile_name

        if target_profile or session_id:
            # Rehydrate context briefly to get cookies
            def get_auth(page: Any) -> Dict[str, Any]:
                return {
                    "ua": page.evaluate("() => navigator.userAgent"),
                    "cookies": page.context.cookies(),
                }
            auth_res = mgr.run_stateless(get_auth, headless=True, timeout_ms=8000, profile_name=target_profile, agent_name=agent, workspace_dir=ws_dir)
            if isinstance(auth_res, dict):
                user_agent = auth_res.get("ua")
                cookies = auth_res.get("cookies")
    except Exception:
        pass

    # Fallback standard User-Agent if none extracted
    if not user_agent:
        user_agent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:132.0) Gecko/20100101 Firefox/132.0"

    headers_str = _build_ffmpeg_headers(
        user_agent=user_agent,
        cookies=cookies,
        referer=referer,
        custom_headers=custom_headers,
    )

    # 4. Construct FFmpeg command
    cmd: List[str] = [ffmpeg_bin, "-y"]

    # Handle trimming start_time
    st_val = str(start_time).strip() if start_time is not None else None
    dur_val = str(duration).strip() if duration is not None else None

    # Input 0: Video stream
    if headers_str:
        cmd.extend(["-headers", headers_str])
    if st_val:
        cmd.extend(["-ss", st_val])
    cmd.extend(["-i", video_url])

    # Input 1: Audio stream (if dual_adaptive)
    has_audio_input = bool(audio_url)
    if has_audio_input:
        if headers_str:
            cmd.extend(["-headers", headers_str])
        if st_val:
            cmd.extend(["-ss", st_val])
        cmd.extend(["-i", audio_url])

    # Trimming duration
    if dur_val:
        cmd.extend(["-t", dur_val])

    # Codec and mapping configuration
    if has_audio_input:
        cmd.extend(["-map", "0:v:0", "-map", "1:a:0"])
        if accurate_trim and (st_val or dur_val):
            # Re-encode ultrafast for sample-accurate cuts without freeze
            cmd.extend(["-c:v", "libx264", "-preset", "ultrafast", "-crf", "20", "-c:a", "aac", "-b:a", "192k"])
        else:
            # Lossless copy mode
            cmd.extend(["-c:v", "copy", "-c:a", "copy", "-avoid_negative_ts", "make_zero"])
    else:
        # Single stream
        if accurate_trim and (st_val or dur_val):
            cmd.extend(["-c:v", "libx264", "-preset", "ultrafast", "-crf", "20", "-c:a", "aac"])
        else:
            cmd.extend(["-c", "copy", "-avoid_negative_ts", "make_zero"])

    cmd.append(dest_path)

    # 5. Execute Subprocess (shell=False to prevent CRLF & escaping corruption)
    start_time_exec = time.time()
    try:
        proc = subprocess.Popen(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            shell=False,
        )
        _, stderr_bytes = proc.communicate(timeout=300)  # 5 min timeout
        returncode = proc.returncode

        stderr_text = stderr_bytes.decode("utf-8", errors="replace") if stderr_bytes else ""
        elapsed = round(time.time() - start_time_exec, 2)

        if returncode != 0:
            return {
                "success": False,
                "error": f"FFmpeg execution failed with exit code {returncode}",
                "details": stderr_text[-1500:] if stderr_text else "",
                "command": " ".join(cmd[:6]) + " ...",
            }

        if not os.path.exists(dest_path) or os.path.getsize(dest_path) == 0:
            return {
                "success": False,
                "error": "Output file was not created or has 0 bytes.",
                "details": stderr_text[-1500:] if stderr_text else "",
            }

        file_size = os.path.getsize(dest_path)
        return {
            "success": True,
            "file_path": dest_path,
            "file_name": safe_name,
            "file_size_bytes": file_size,
            "file_size_mb": round(file_size / (1024 * 1024), 2),
            "elapsed_seconds": elapsed,
            "topology": "dual_adaptive" if has_audio_input else "single_muxed",
            "trimmed": bool(st_val or dur_val),
        }

    except subprocess.TimeoutExpired:
        if proc:
            proc.kill()
        return {
            "success": False,
            "error": "FFmpeg download process timed out after 300 seconds.",
        }
    except Exception as e:
        return {
            "success": False,
            "error": f"Failed to execute FFmpeg download: {str(e)}",
        }
