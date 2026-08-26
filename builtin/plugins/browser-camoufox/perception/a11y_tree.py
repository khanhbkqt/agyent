"""
Accessibility Tree Parser and Interactive Element Indexer.
Extracts clean, numbered semantic landmarks ([1], [2], [3]...) for autonomous AI grounding.
"""

from typing import Any, Dict, List, Optional


class InteractiveElement:
    """Represents a discovered clickable or interactive element on the page."""

    def __init__(
        self,
        index: int,
        tag: str,
        role: str,
        text: str,
        selector: str,
        bounds: Dict[str, float],
        center: Dict[str, float],
        attributes: Dict[str, str],
    ):
        self.index = index
        self.tag = tag
        self.role = role
        self.text = text
        self.selector = selector
        self.bounds = bounds
        self.center = center
        self.attributes = attributes

    def to_dict(self) -> Dict[str, Any]:
        return {
            "id": self.index,
            "tag": self.tag,
            "role": self.role,
            "text": self.text,
            "selector": self.selector,
            "bounds": self.bounds,
            "center": self.center,
            "attributes": self.attributes,
        }

    def to_formatted_line(self) -> str:
        """Returns a compact line for LLM reasoning."""
        desc_parts = [f"[{self.index}] {self.role.upper()}"]
        if self.text:
            desc_parts.append(f'"{self.text}"')

        attr_info = []
        if self.attributes.get("type"):
            attr_info.append(f"type={self.attributes['type']}")
        if self.attributes.get("placeholder"):
            attr_info.append(f"placeholder={self.attributes['placeholder']}")
        if self.attributes.get("href"):
            attr_info.append(f"href={self.attributes['href']}")
        if self.attributes.get("value"):
            attr_info.append(f"val={self.attributes['value']}")

        if attr_info:
            desc_parts.append(f"({', '.join(attr_info)})")

        desc_parts.append(f"@({int(self.center['x'])}, {int(self.center['y'])})")
        return " ".join(desc_parts)


def extract_interactive_elements(page: Any, max_elements: int = 100) -> List[InteractiveElement]:
    """
    Evaluates JavaScript in the browser to extract all visible, actionable elements.
    """
    js_extract = """
    () => {
        const interactiveSelectors = [
            'button',
            'a[href]',
            'input',
            'select',
            'textarea',
            '[role="button"]',
            '[role="link"]',
            '[role="checkbox"]',
            '[role="tab"]',
            '[role="menuitem"]',
            '[role="searchbox"]',
            '[tabindex="0"]',
            'summary'
        ];

        const elements = document.querySelectorAll(interactiveSelectors.join(','));
        const results = [];
        let counter = 1;

        for (const el of elements) {
            // Check visibility
            const rect = el.getBoundingClientRect();
            if (rect.width <= 0 || rect.height <= 0) continue;
            const style = window.getComputedStyle(el);
            if (style.display === 'none' || style.visibility === 'hidden' || style.opacity === '0') continue;

            // Compute center
            const centerX = rect.left + rect.width / 2;
            const centerY = rect.top + rect.height / 2;

            // Ignore elements outside viewport
            if (centerY < 0 || centerX < 0 || centerX > window.innerWidth || centerY > window.innerHeight * 2) {
                continue;
            }

            // Extract role & text
            const tag = el.tagName.toLowerCase();
            let role = el.getAttribute('role') || tag;
            if (tag === 'input') {
                role = 'input:' + (el.getAttribute('type') || 'text');
            }

            let text = (el.innerText || el.textContent || el.getAttribute('aria-label') || el.getAttribute('placeholder') || el.getAttribute('title') || '').trim();
            // Truncate long text
            if (text.length > 60) {
                text = text.substring(0, 57) + '...';
            }

            const attributes = {};
            if (el.getAttribute('type')) attributes['type'] = el.getAttribute('type');
            if (el.getAttribute('placeholder')) attributes['placeholder'] = el.getAttribute('placeholder');
            if (el.getAttribute('href')) attributes['href'] = el.getAttribute('href');
            if (el.getAttribute('name')) attributes['name'] = el.getAttribute('name');
            if (el.getAttribute('id')) attributes['id'] = el.getAttribute('id');
            if (el.value !== undefined && el.value !== '') attributes['value'] = String(el.value);

            // Generate unique selector
            let selector = '';
            if (el.id) {
                selector = '#' + CSS.escape(el.id);
            } else {
                selector = tag;
                if (el.className && typeof el.className === 'string') {
                    const firstClass = el.className.split(' ').filter(c => c.trim().length > 0)[0];
                    if (firstClass) selector += '.' + CSS.escape(firstClass);
                }
            }

            // Tag DOM element with internal data-agy-id for instant targeting
            el.setAttribute('data-agy-id', String(counter));

            results.push({
                index: counter,
                tag: tag,
                role: role,
                text: text,
                selector: `[data-agy-id="${counter}"]`,
                bounds: {
                    x: Math.round(rect.left),
                    y: Math.round(rect.top),
                    width: Math.round(rect.width),
                    height: Math.round(rect.height)
                },
                center: {
                    x: Math.round(centerX),
                    y: Math.round(centerY)
                },
                attributes: attributes
            });

            counter++;
            if (counter > 100) break;
        }

        return results;
    }
    """
    raw_list = page.evaluate(js_extract)
    elements = []
    for item in raw_list:
        elem = InteractiveElement(
            index=item["index"],
            tag=item["tag"],
            role=item["role"],
            text=item["text"],
            selector=item["selector"],
            bounds=item["bounds"],
            center=item["center"],
            attributes=item["attributes"],
        )
        elements.append(elem)
    return elements


def format_a11y_tree(elements: List[InteractiveElement], page_title: str = "", current_url: str = "") -> str:
    """
    Formats the interactive elements list into a clean structured AOM representation for LLMs.
    """
    lines = []
    if page_title or current_url:
        lines.append(f"### Current Page: {page_title} ({current_url})")
        lines.append(f"Found {len(elements)} interactive elements:\n")

    for elem in elements:
        lines.append(elem.to_formatted_line())

    return "\n".join(lines)
