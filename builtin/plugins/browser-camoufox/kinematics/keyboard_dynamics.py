"""
Human keyboard dynamics simulation using Gaussian WPM distribution, natural pauses, and shift timings.
"""

import random
import time
from typing import Any


def calculate_key_delay(char: str, prev_char: str = "") -> float:
    """
    Calculates delay in seconds before pressing the next character.
    Mimics human keystroke variability and hesitation before punctuation.
    """
    # Base delay from Gaussian distribution: ~70-90ms average
    base_delay = random.gauss(0.08, 0.025)
    base_delay = max(0.02, min(0.25, base_delay))

    # Punctuation pauses (human thinking/sentence boundary pause)
    if prev_char in (".", "!", "?", "\n"):
        base_delay += random.uniform(0.15, 0.35)
    elif prev_char in (",", ";", ":", "-"):
        base_delay += random.uniform(0.08, 0.18)

    # Upper-case / shift modifier hesitation
    if char.isupper() and not prev_char.isupper():
        base_delay += random.uniform(0.05, 0.12)

    # Spacebar rhythm pause
    if char == " ":
        base_delay += random.uniform(0.02, 0.06)

    return base_delay


def human_type(target: Any, text: str) -> None:
    """
    Types text into a Playwright page or ElementHandle/Locator with human keystroke intervals.
    """
    prev_char = ""
    for char in text:
        delay = calculate_key_delay(char, prev_char)
        time.sleep(delay)

        if hasattr(target, "type"):
            # Target is locator / element
            target.type(char, delay=0)
        elif hasattr(target, "keyboard"):
            # Target is page
            target.keyboard.type(char, delay=0)
        else:
            time.sleep(0.01)

        prev_char = char
