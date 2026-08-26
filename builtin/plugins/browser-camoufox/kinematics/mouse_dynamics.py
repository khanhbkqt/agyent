"""
Human mouse kinematics simulation using Cubic Bézier curves, velocity easing, and micro-jitter.
Prevents detection by Datadome, Cloudflare Turnstile, PerimeterX, and Kasada.
"""

import math
import random
import time
from typing import Any, List, Tuple


def _bezier_point(
    p0: Tuple[float, float],
    p1: Tuple[float, float],
    p2: Tuple[float, float],
    p3: Tuple[float, float],
    t: float,
) -> Tuple[float, float]:
    """Calculates a point on a cubic Bézier curve at parameter t (0.0 <= t <= 1.0)."""
    u = 1.0 - t
    tt = t * t
    uu = u * u
    uuu = uu * u
    ttt = tt * t

    x = uuu * p0[0] + 3 * uu * t * p1[0] + 3 * u * tt * p2[0] + ttt * p3[0]
    y = uuu * p0[1] + 3 * uu * t * p1[1] + 3 * u * tt * p2[1] + ttt * p3[1]
    return (x, y)


def generate_bezier_trajectory(
    start: Tuple[float, float],
    end: Tuple[float, float],
    num_points: int = 25,
    deviation_scale: float = 0.2,
) -> List[Tuple[float, float]]:
    """
    Generates a realistic human mouse trajectory between start and end coordinates.
    """
    dx = end[0] - start[0]
    dy = end[1] - start[1]
    distance = math.hypot(dx, dy)

    if distance < 5:
        return [start, end]

    # Dynamically scale number of steps based on distance
    steps = max(10, min(60, int(distance / 15))) if num_points <= 0 else num_points

    # Random control points with human-like curvature
    side = 1 if random.random() > 0.5 else -1
    spread = distance * deviation_scale

    ctrl1_x = start[0] + dx * 0.25 + side * random.uniform(-spread, spread) * 0.5
    ctrl1_y = start[1] + dy * 0.25 + side * random.uniform(-spread, spread) * 0.5

    ctrl2_x = start[0] + dx * 0.75 + side * random.uniform(-spread, spread) * 0.3
    ctrl2_y = start[1] + dy * 0.75 + side * random.uniform(-spread, spread) * 0.3

    p0 = start
    p1 = (ctrl1_x, ctrl1_y)
    p2 = (ctrl2_x, ctrl2_y)
    p3 = end

    trajectory: List[Tuple[float, float]] = []

    # Non-linear velocity distribution (ease-in, ease-out)
    for i in range(steps + 1):
        t_linear = i / float(steps)
        # Smoothstep curve for human acceleration / deceleration
        t = t_linear * t_linear * (3.0 - 2.0 * t_linear)

        bx, by = _bezier_point(p0, p1, p2, p3, t)

        # Apply minor micro-jitter in the middle of movement
        if 0.1 < t < 0.9:
            jitter_x = random.gauss(0, 0.5)
            jitter_y = random.gauss(0, 0.5)
            bx += jitter_x
            by += jitter_y

        trajectory.append((bx, by))

    return trajectory


def human_move_mouse(
    page: Any,
    target_x: float,
    target_y: float,
    start_x: Optional[float] = None,
    start_y: Optional[float] = None,
) -> None:
    """
    Moves mouse smoothly to target coordinates using human Bézier trajectory.
    """
    if start_x is None or start_y is None:
        # Default start from center or slight offset
        start_x = random.uniform(100, 300)
        start_y = random.uniform(100, 300)

    points = generate_bezier_trajectory((start_x, start_y), (target_x, target_y))

    for x, y in points:
        page.mouse.move(x, y)
        time.sleep(random.uniform(0.005, 0.018))


def human_click(page: Any, target_x: float, target_y: float) -> None:
    """
    Moves to target with human dynamics and clicks with realistic hold duration.
    """
    human_move_mouse(page, target_x, target_y)
    # Slight hesitation before clicking
    time.sleep(random.uniform(0.05, 0.15))
    page.mouse.down()
    # Click duration (40ms - 120ms)
    time.sleep(random.uniform(0.04, 0.12))
    page.mouse.up()
    # Post-click rest
    time.sleep(random.uniform(0.05, 0.10))
