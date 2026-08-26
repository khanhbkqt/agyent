"""
Token-optimized HTML-to-Markdown cleaner and Cookie Banner eliminator.
Zero external C-dependencies, highly robust and efficient.
"""

from html.parser import HTMLParser
import re
from typing import Any, Dict, List, Optional, Tuple


# Heuristic patterns for noise tags and container classes to drop
NOISE_TAGS = {"script", "style", "noscript", "svg", "iframe", "object", "embed", "header", "footer", "nav", "aside"}

NOISE_CLASS_PATTERNS = re.compile(
    r"(cookie|consent|banner|popup|modal|overlay|gdpr|privacy-policy|advert|sponsor|promo|newsletter|subscribe)",
    re.IGNORECASE,
)


class SimpleHTMLCleaner(HTMLParser):
    """Parses HTML into a clean, hierarchical Markdown document."""

    def __init__(self):
        super().__init__()
        self.result: List[str] = []
        self.tag_stack: List[str] = []
        self.skip_stack: List[str] = []
        self.current_link: Optional[str] = None
        self.list_depth: int = 0
        self.list_type: List[str] = []  # 'ul' or 'ol'
        self.list_item_index: List[int] = []

    def handle_starttag(self, tag: str, attrs: List[Tuple[str, Optional[str]]]) -> None:
        tag = tag.lower()
        attr_dict = {k.lower(): (v or "") for k, v in attrs}

        # Check if tag is noisy
        if tag in NOISE_TAGS:
            self.skip_stack.append(tag)
            return

        # Check class/id for noise keywords in container tags (div, section)
        if tag in ("div", "section", "aside", "dialog"):
            classes = attr_dict.get("class", "")
            elem_id = attr_dict.get("id", "")
            if NOISE_CLASS_PATTERNS.search(classes) or NOISE_CLASS_PATTERNS.search(elem_id):
                self.skip_stack.append(tag)
                return

        if self.skip_stack:
            return

        self.tag_stack.append(tag)

        if tag in ("h1", "h2", "h3", "h4", "h5", "h6"):
            level = int(tag[1])
            self.result.append("\n\n" + "#" * level + " ")
        elif tag == "p":
            self.result.append("\n\n")
        elif tag == "br":
            self.result.append("\n")
        elif tag == "hr":
            self.result.append("\n\n---\n\n")
        elif tag == "blockquote":
            self.result.append("\n\n> ")
        elif tag == "pre":
            self.result.append("\n\n```\n")
        elif tag == "code" and "pre" not in self.tag_stack:
            self.result.append("`")
        elif tag == "strong" or tag == "b":
            self.result.append("**")
        elif tag == "em" or tag == "i":
            self.result.append("*")
        elif tag == "a":
            href = attr_dict.get("href", "").strip()
            if href and not href.startswith("javascript:"):
                self.current_link = href
                self.result.append("[")
        elif tag == "ul":
            self.list_depth += 1
            self.list_type.append("ul")
            self.result.append("\n")
        elif tag == "ol":
            self.list_depth += 1
            self.list_type.append("ol")
            self.list_item_index.append(1)
            self.result.append("\n")
        elif tag == "li":
            indent = "  " * max(0, self.list_depth - 1)
            if self.list_type and self.list_type[-1] == "ol":
                idx = self.list_item_index[-1] if self.list_item_index else 1
                self.result.append(f"\n{indent}{idx}. ")
                if self.list_item_index:
                    self.list_item_index[-1] += 1
            else:
                self.result.append(f"\n{indent}* ")

    def handle_endtag(self, tag: str) -> None:
        tag = tag.lower()

        if self.skip_stack:
            if self.skip_stack[-1] == tag:
                self.skip_stack.pop()
            return

        if self.tag_stack and self.tag_stack[-1] == tag:
            self.tag_stack.pop()

        if tag in ("h1", "h2", "h3", "h4", "h5", "h6", "p"):
            self.result.append("\n")
        elif tag == "pre":
            self.result.append("\n```\n")
        elif tag == "code" and "pre" not in self.tag_stack:
            self.result.append("`")
        elif tag == "strong" or tag == "b":
            self.result.append("**")
        elif tag == "em" or tag == "i":
            self.result.append("*")
        elif tag == "a":
            if self.current_link:
                self.result.append(f"]({self.current_link})")
                self.current_link = None
        elif tag == "ul":
            if self.list_depth > 0:
                self.list_depth -= 1
            if self.list_type:
                self.list_type.pop()
            self.result.append("\n")
        elif tag == "ol":
            if self.list_depth > 0:
                self.list_depth -= 1
            if self.list_type:
                self.list_type.pop()
            if self.list_item_index:
                self.list_item_index.pop()
            self.result.append("\n")

    def handle_data(self, data: str) -> None:
        if self.skip_stack:
            return
        if not data:
            return

        # Normalize inner whitespaces
        cleaned = re.sub(r"[ \t]+", " ", data)
        self.result.append(cleaned)

    def get_markdown(self) -> str:
        raw = "".join(self.result)
        # Collapse multiple blank lines
        cleaned = re.sub(r"\n{3,}", "\n\n", raw).strip()
        return cleaned


def extract_clean_markdown(html_content: str, max_chars: int = 15000) -> Dict[str, Any]:
    """
    Parses raw HTML and returns clean token-optimized markdown with metadata.
    """
    if not html_content or not html_content.strip():
        return {
            "content": "",
            "word_count": 0,
            "char_count": 0,
            "truncated": False,
        }

    parser = SimpleHTMLCleaner()
    try:
        parser.feed(html_content)
        md = parser.get_markdown()
    except Exception:
        # Fallback to regex strip if HTML is severely corrupted
        clean_text = re.sub(r"<[^>]+>", " ", html_content)
        md = re.sub(r"\s+", " ", clean_text).strip()

    char_count = len(md)
    words = md.split()
    word_count = len(words)
    truncated = False

    if char_count > max_chars:
        md = md[:max_chars] + "\n\n... [Content Truncated for Token Optimization]"
        truncated = True

    return {
        "content": md,
        "word_count": word_count,
        "char_count": len(md),
        "truncated": truncated,
    }


def dismiss_cookie_banners(page: Any) -> None:
    """
    Attempts to dismiss common Cookie & Consent banners via DOM evaluation.
    """
    try:
        js_script = """
        () => {
            const selectors = [
                'button[id*="accept"]',
                'button[class*="accept"]',
                'button[data-testid*="accept"]',
                'button[id*="cookie"]',
                'button[class*="cookie"]',
                'button:has-text("Accept All")',
                'button:has-text("Alle akzeptieren")',
                'button:has-text("Allow all")',
                'button:has-text("I agree")',
                'button:has-text("Got it")',
                'button:has-text("Accepter")',
                'a[id*="accept"]',
                'div[role="dialog"] button:has-text("OK")',
                'div[role="dialog"] button:has-text("Accept")'
            ];
            for (const sel of selectors) {
                try {
                    const btn = document.querySelector(sel);
                    if (btn && btn.offsetParent !== null) {
                        btn.click();
                        return true;
                    }
                } catch(e) {}
            }
            return false;
        }
        """
        page.evaluate(js_script)
    except Exception:
        pass
