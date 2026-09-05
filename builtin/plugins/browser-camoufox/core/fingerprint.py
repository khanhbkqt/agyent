"""
Fingerprint and anti-detect spoofing configuration for Camoufox.
Configures OS, WebGL, Canvas, Audio, Geo-IP, Timezone, and Locale matching.
"""

import os
import sys
from typing import Any, Dict, List, Optional, Union

try:
    from camoufox import DefaultAddons
except Exception:
    DefaultAddons = None

# Locale to Timezone & Geo-coordinates mapping
LOCALE_TIMEZONE_MAP = {
    "de-DE": {
        "locale": "de-DE",
        "timezone": "Europe/Berlin",
        "geolocation": {"latitude": 52.5200, "longitude": 13.4050, "accuracy": 10},
        "languages": ["de-DE", "de", "en-US", "en"],
    },
    "de": {
        "locale": "de-DE",
        "timezone": "Europe/Berlin",
        "geolocation": {"latitude": 52.5200, "longitude": 13.4050, "accuracy": 10},
        "languages": ["de-DE", "de", "en-US", "en"],
    },
    "en-US": {
        "locale": "en-US",
        "timezone": "America/New_York",
        "geolocation": {"latitude": 40.7128, "longitude": -74.0060, "accuracy": 10},
        "languages": ["en-US", "en"],
    },
    "en-GB": {
        "locale": "en-GB",
        "timezone": "Europe/London",
        "geolocation": {"latitude": 51.5074, "longitude": -0.1278, "accuracy": 10},
        "languages": ["en-GB", "en", "en-US"],
    },
    "vi-VN": {
        "locale": "vi-VN",
        "timezone": "Asia/Ho_Chi_Minh",
        "geolocation": {"latitude": 21.0285, "longitude": 105.8542, "accuracy": 10},
        "languages": ["vi-VN", "vi", "en-US", "en"],
    },
}

COUNTRY_TO_LOCALE = {
    "DE": "de-DE",
    "US": "en-US",
    "GB": "en-GB",
    "UK": "en-GB",
    "VN": "vi-VN",
}


def parse_headless_option(val: Any) -> Union[bool, str]:
    """
    Normalizes headless option from boolean, string, or environment variable.
    Supports:
      - False / "false" / "0" / "headful" -> False (visible GUI window for user interaction)
      - "virtual" / "xvfb" -> "virtual" (Xvfb virtual display on Linux)
      - True / "true" / "1" -> True (headless)
      - None -> checks CAMOUFOX_HEADLESS or AGYENT_BROWSER_HEADLESS, defaulting to True.
    On Linux, if headful (False) is requested but neither DISPLAY nor WAYLAND_DISPLAY is set,
    gracefully falls back to 'virtual' to prevent fatal startup crashes.
    """
    if val is None:
        env_val = os.environ.get("CAMOUFOX_HEADLESS", os.environ.get("AGYENT_BROWSER_HEADLESS"))
        if env_val is not None:
            val = env_val
        else:
            val = True

    if isinstance(val, str):
        v = val.strip().lower()
        if v in ("false", "0", "no", "off", "headful", "gui", "headed"):
            val = False
        elif v in ("virtual", "xvfb"):
            val = "virtual"
        elif v in ("true", "1", "yes", "on", "headless"):
            val = True
        else:
            val = True
    else:
        val = bool(val)

    # Linux headless safety guard
    if val is False and sys.platform.startswith("linux"):
        has_display = bool(os.environ.get("DISPLAY") or os.environ.get("WAYLAND_DISPLAY"))
        if not has_display:
            sys.stderr.write(
                "[CAMOUFOX] Warning: Headful mode requested on Linux, but neither DISPLAY nor WAYLAND_DISPLAY was detected. "
                "Falling back to headless='virtual' (Xvfb virtual display) to prevent startup crash.\n"
            )
            val = "virtual"

    return val


def resolve_locale_config(locale_or_country: Optional[str] = None) -> Dict[str, Any]:
    """
    Resolves locale, timezone, geolocation, and accepted languages from a locale or country code.
    Defaults to 'en-US' if unspecified or unknown.
    """
    if not locale_or_country:
        return LOCALE_TIMEZONE_MAP["en-US"]

    target = locale_or_country.strip()
    if target.upper() in COUNTRY_TO_LOCALE:
        target = COUNTRY_TO_LOCALE[target.upper()]

    return LOCALE_TIMEZONE_MAP.get(target, LOCALE_TIMEZONE_MAP["en-US"])


def build_camoufox_launch_options(
    headless: Optional[Union[bool, str]] = None,
    locale: Optional[str] = "en-US",
    proxy: Optional[Dict[str, str]] = None,
    enable_adblock: bool = True,
    geoip: Optional[bool] = None,
    disable_coop: bool = True,
    block_webrtc: bool = True,
    block_images: bool = False,
    humanize: Union[bool, float] = True,
    os_target: Optional[Union[str, List[str]]] = None,
    addons: Optional[List[Any]] = None,
    extra_options: Optional[Dict[str, Any]] = None,
) -> Dict[str, Any]:
    """
    Builds comprehensive browser launch options dictionary for Camoufox instantiation.
    Enables native uBlock Origin, Geo-IP harmonization, anti-bot COOP bypass, WebRTC shields,
    and BrowserForge hardware synthesis.
    """
    norm_headless = parse_headless_option(headless)
    loc_cfg = resolve_locale_config(locale)

    # Media suppression prefs to avoid CPU spikes and infinite video decoding loops (e.g. TikTok, Etsy)
    media_prefs = {
        "security.enterprise_roots.enabled": True,
        "media.autoplay.default": 5,                         # 5 = block all audio and video autoplay
        "media.autoplay.blocking_policy": 2,                 # 2 = strict autoplay blocking
        "media.autoplay.allow-extension-background-pages": False,
        "media.autoplay.block-event.enabled": True,
        "media.block-autoplay-until-in-viewport": True,
        "media.volume_scale": "0.0",
        "media.rdd-process.enabled": False,                  # Disable Remote Data Decoder process
        "media.rdd-v4l2.enabled": False,
        "media.rdd-ffmpeg.enabled": False,
        "media.ffmpeg.vaapi.enabled": False,
        "media.hardware-video-decoding.enabled": False,
        "dom.media.silence_playback": True,
    }

    options: Dict[str, Any] = {
        "headless": norm_headless,
        "locale": loc_cfg["locale"],
        "enable_cache": True,
        "disable_coop": disable_coop,
        "block_webrtc": block_webrtc,
        "block_images": block_images,
        "humanize": humanize,
        "i_know_what_im_doing": True,
        "config": {"prefs": media_prefs},
        "args": [
            "--disable-software-rasterizer",
            "--disable-dev-shm-usage",
            "--no-remote",
        ],
    }

    # Proxy and Geo-IP Harmonization
    if proxy:
        options["proxy"] = proxy
        if geoip is None:
            geoip = True

    if geoip is not None:
        options["geoip"] = geoip

    # Operating System & Hardware Profile
    if os_target:
        options["os"] = os_target

    # Addons (Native uBlock Origin control + custom addons)
    if not enable_adblock:
        exclude_list = []
        if DefaultAddons and hasattr(DefaultAddons, "UBO"):
            exclude_list.append(DefaultAddons.UBO)
        options["exclude_addons"] = exclude_list
    else:
        options["exclude_addons"] = []

    if addons:
        options["addons"] = list(addons)

    if extra_options:
        extra_copy = dict(extra_options)
        if "args" in extra_copy:
            for a in extra_copy.pop("args"):
                if a not in options["args"]:
                    options["args"].append(a)
        if "config" in extra_copy and isinstance(extra_copy["config"], dict):
            extra_cfg = extra_copy.pop("config")
            if "prefs" in extra_cfg and isinstance(extra_cfg["prefs"], dict):
                options["config"]["prefs"].update(extra_cfg["prefs"])
        options.update(extra_copy)

    return options


def build_context_options(
    locale: Optional[str] = "en-US",
    storage_state: Optional[str] = None,
) -> Dict[str, Any]:
    """
    Builds Playwright BrowserContext options.
    """
    loc_cfg = resolve_locale_config(locale)
    ctx_opts: Dict[str, Any] = {
        "timezone_id": loc_cfg["timezone"],
        "geolocation": loc_cfg["geolocation"],
        "permissions": ["geolocation"],
        "viewport": {"width": 1280, "height": 800},
        "ignore_https_errors": True,
    }
    if storage_state:
        ctx_opts["storage_state"] = storage_state
    return ctx_opts
