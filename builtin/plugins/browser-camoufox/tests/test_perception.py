import os
import sys
import unittest

plugin_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
if plugin_dir not in sys.path:
    sys.path.insert(0, plugin_dir)

from perception.a11y_tree import InteractiveElement, format_a11y_tree
from perception.readability_cleaner import extract_clean_markdown


class TestPerception(unittest.TestCase):

    def test_readability_cleaner(self):
        sample_html = """
        <!DOCTYPE html>
        <html>
        <head><title>Test Page</title><style>body { color: red; }</style></head>
        <body>
            <nav><a href="/home">Home</a></nav>
            <div class="cookie-consent-modal">
                <p>We use cookies</p>
                <button>Accept</button>
            </div>
            <main>
                <h1>Main Heading</h1>
                <p>This is a <strong>bold</strong> paragraph with a <a href="https://example.com">link</a>.</p>
                <ul>
                    <li>First item</li>
                    <li>Second item</li>
                </ul>
                <pre><code>def hello():\n    return "world"</code></pre>
            </main>
            <footer>Copyright 2026</footer>
        </body>
        </html>
        """
        res = extract_clean_markdown(sample_html)
        md = res["content"]

        # Assert main content is converted to Markdown
        self.assertIn("# Main Heading", md)
        self.assertIn("**bold**", md)
        self.assertIn("[link](https://example.com)", md)
        self.assertIn("* First item", md)
        self.assertIn("```", md)

        # Assert noise/cookie banner/footer are stripped
        self.assertNotIn("We use cookies", md)
        self.assertNotIn("Copyright 2026", md)
        self.assertNotIn("color: red", md)
        self.assertGreater(res["word_count"], 0)

    def test_a11y_tree_formatting(self):
        elements = [
            InteractiveElement(
                index=1,
                tag="button",
                role="button",
                text="Submit Form",
                selector="[data-agy-id='1']",
                bounds={"x": 10, "y": 20, "width": 100, "height": 30},
                center={"x": 60, "y": 35},
                attributes={"type": "submit"},
            ),
            InteractiveElement(
                index=2,
                tag="input",
                role="input:text",
                text="",
                selector="[data-agy-id='2']",
                bounds={"x": 10, "y": 60, "width": 200, "height": 30},
                center={"x": 110, "y": 75},
                attributes={"placeholder": "Search query", "value": "test"},
            ),
        ]

        formatted = format_a11y_tree(elements, page_title="Search Page", current_url="https://example.com")
        self.assertIn("[1] BUTTON \"Submit Form\"", formatted)
        self.assertIn("[2] INPUT:TEXT", formatted)
        self.assertIn("placeholder=Search query", formatted)
        self.assertIn("@(60, 35)", formatted)


if __name__ == "__main__":
    unittest.main()
