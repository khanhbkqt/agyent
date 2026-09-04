"""
Fingerprint and anti-detect spoofing configuration for Camoufox.
Configures OS, WebGL, Canvas, Audio, Geo-IP, Timezone, and Locale matching.
"""

from typing import Any, Dict, List, Optional

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
    headless: bool = True,
    locale: Optional[str] = "en-US",
    proxy: Optional[Dict[str, str]] = None,
    extra_options: Optional[Dict[str, Any]] = None,
) -> Dict[str, Any]:
    """
    Builds browser launch options dictionary for Camoufox instantiation.
    """
    loc_cfg = resolve_locale_config(locale)

    options: Dict[str, Any] = {
        "headless": headless,
        "locale": loc_cfg["locale"],
        "enable_cache": True,
        "config": {"prefs": {"security.enterprise_roots.enabled": True}},
    }

    if proxy:
        options["proxy"] = proxy

    if extra_options:
        options.update(extra_options)

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
