"""
Set-of-Marks (SoM) visual marker injector and overlay generator.
Draws numbered badge markers over interactive elements for multimodal visual grounding.
"""

from typing import Any


def inject_visual_marks(page: Any) -> int:
    """
    Injects visible numbered badges on top of all tagged elements ([data-agy-id]).
    Returns total number of marks injected.
    """
    js_inject = """
    () => {
        // Clean any existing overlay container
        const existing = document.getElementById('agy-som-overlay-container');
        if (existing) existing.remove();

        const container = document.createElement('div');
        container.id = 'agy-som-overlay-container';
        container.style.position = 'absolute';
        container.style.top = '0';
        container.style.left = '0';
        container.style.width = '100%';
        container.style.height = '100%';
        container.style.pointerEvents = 'none';
        container.style.zIndex = '99999999';

        const elements = document.querySelectorAll('[data-agy-id]');
        let count = 0;

        for (const el of elements) {
            const id = el.getAttribute('data-agy-id');
            const rect = el.getBoundingClientRect();
            if (rect.width <= 0 || rect.height <= 0) continue;

            const badge = document.createElement('div');
            badge.innerText = id;
            badge.style.position = 'fixed';
            badge.style.left = `${Math.max(0, rect.left)}px`;
            badge.style.top = `${Math.max(0, rect.top)}px`;
            badge.style.backgroundColor = '#FF0055';
            badge.style.color = '#FFFFFF';
            badge.style.fontSize = '11px';
            badge.style.fontWeight = 'bold';
            badge.style.padding = '1px 4px';
            badge.style.borderRadius = '3px';
            badge.style.boxShadow = '0 0 3px rgba(0,0,0,0.8)';
            badge.style.pointerEvents = 'none';
            badge.style.zIndex = '99999999';

            container.appendChild(badge);
            count++;
        }

        document.body.appendChild(container);
        return count;
    }
    """
    try:
        return page.evaluate(js_inject)
    except Exception:
        return 0


def clear_visual_marks(page: Any) -> None:
    """
    Removes the Set-of-Marks overlay from the page.
    """
    js_clear = """
    () => {
        const container = document.getElementById('agy-som-overlay-container');
        if (container) container.remove();
    }
    """
    try:
        page.evaluate(js_clear)
    except Exception:
        pass
