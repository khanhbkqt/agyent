import os
import sys
import unittest

plugin_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
if plugin_dir not in sys.path:
    sys.path.insert(0, plugin_dir)

from kinematics.keyboard_dynamics import calculate_key_delay
from kinematics.mouse_dynamics import generate_bezier_trajectory


class TestKinematics(unittest.TestCase):

    def test_bezier_trajectory_generation(self):
        start = (100.0, 100.0)
        end = (500.0, 400.0)
        points = generate_bezier_trajectory(start, end, num_points=20)

        self.assertGreaterEqual(len(points), 10)
        # First point should start at start coords (approx)
        self.assertAlmostEqual(points[0][0], start[0], delta=2.0)
        self.assertAlmostEqual(points[0][1], start[1], delta=2.0)
        # Last point should be close to end coords
        self.assertAlmostEqual(points[-1][0], end[0], delta=2.0)
        self.assertAlmostEqual(points[-1][1], end[1], delta=2.0)

    def test_calculate_key_delay(self):
        # Normal typing delay should be within reasonable human boundaries (20ms - 250ms)
        delay = calculate_key_delay("a", "b")
        self.assertGreater(delay, 0.01)
        self.assertLess(delay, 0.3)

        # Sentence punctuation should introduce natural thinking pause
        delay_punct = calculate_key_delay("T", ".")
        self.assertGreater(delay_punct, delay)


if __name__ == "__main__":
    unittest.main()
