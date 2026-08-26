"""
Human scroll controller simulation with inertial momentum, variable acceleration, and dwell pauses.
"""

import random
import time
from typing import Any


def human_scroll(
    page: Any,
    delta_y: int,
    steps: int = 0,
    pause_after: float = 0.2,
) -> None:
    """
    Scrolls the page smoothly using multiple inertial sub-scrolls.
    """
    if delta_y == 0:
        return

    sign = 1 if delta_y > 0 else -1
    total_abs = abs(delta_y)

    if steps <= 0:
        # Determine steps based on distance
        steps = max(3, min(20, int(total_abs / 80)))

    # Distribute delta_y with acceleration and deceleration
    remaining = total_abs
    for i in range(steps):
        if remaining <= 0:
            break

        is_last = (i == steps - 1)
        if is_last:
            step_amount = remaining
        else:
            # Chunk fraction with random variance
            portion = total_abs / float(steps)
            step_amount = int(portion * random.uniform(0.7, 1.3))
            step_amount = min(step_amount, remaining)

        chunk_y = step_amount * sign
        page.mouse.wheel(0, chunk_y)
        remaining -= step_amount

        # Inertial interval between scroll events (15ms - 45ms)
        time.sleep(random.uniform(0.015, 0.045))

    if pause_after > 0:
        time.sleep(pause_after * random.uniform(0.8, 1.2))


def scroll_feed_interactive(
    page: Any,
    scroll_cycles: int = 3,
    distance_per_cycle: int = 600,
    dwell_sec: float = 1.5,
) -> None:
    """
    Scrolls down a dynamic feed repeatedly with human dwell/reading times to trigger XHR/GraphQL lazy loading.
    """
    for _ in range(scroll_cycles):
        human_scroll(page, distance_per_cycle, pause_after=0.1)
        # Dwell time simulating user reading the content
        time.sleep(random.uniform(dwell_sec * 0.7, dwell_sec * 1.3))
