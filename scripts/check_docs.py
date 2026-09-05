#!/usr/bin/env python3
"""Validate documentation status, links, code references, plugins, and skills."""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path
from urllib.parse import unquote


ROOT = Path(__file__).resolve().parents[1]
ALLOWED_STATUSES = {"Canonical", "Normative", "Reference", "Proposed", "Historical"}
ACTIVE_STATUSES = {"Canonical", "Normative", "Reference"}
STATUS_RE = re.compile(r"\*\*Document status:\*\*\s*([A-Za-z]+)")
LINK_RE = re.compile(r"!?\[[^\]]*\]\(([^)]+)\)")
CODE_PATH_RE = re.compile(
    r"`((?:cmd|internal|builtin|scripts)/[^`\s]+?\.(?:go|py|json|sql|md|sh|ps1))(?::\d+)?`"
)
NAME_RE = re.compile(r"^name:\s*[\"']?([^\"'\s]+)[\"']?\s*$", re.MULTILINE)
DESCRIPTION_RE = re.compile(r"^description:\s*(?:.+)?$", re.MULTILINE)


def parse_status(path: Path, errors: list[str]) -> str | None:
    text = path.read_text(encoding="utf-8-sig")
    match = STATUS_RE.search("\n".join(text.splitlines()[:15]))
    rel = path.relative_to(ROOT)
    if not match:
        errors.append(f"{rel}: missing Document status in the first 15 lines")
        return None
    status = match.group(1)
    if status not in ALLOWED_STATUSES:
        errors.append(f"{rel}: unsupported Document status {status!r}")
    if "**Code authority:**" not in "\n".join(text.splitlines()[:15]):
        errors.append(f"{rel}: missing Code authority in the first 15 lines")
    if "**Last verified:**" not in "\n".join(text.splitlines()[:15]):
        errors.append(f"{rel}: missing Last verified in the first 15 lines")
    return status


def clean_link_target(raw: str) -> str:
    target = raw.strip()
    if target.startswith("<") and ">" in target:
        target = target[1 : target.index(">")]
    elif " " in target:
        target = target.split(" ", 1)[0]
    return unquote(target.split("#", 1)[0])


def validate_doc(path: Path, status: str | None, errors: list[str]) -> None:
    text = path.read_text(encoding="utf-8-sig")
    rel = path.relative_to(ROOT)

    for raw in LINK_RE.findall(text):
        target = clean_link_target(raw)
        if not target or target.startswith(("http://", "https://", "mailto:", "file:", "/")):
            continue
        resolved = (path.parent / target).resolve()
        if not resolved.exists():
            errors.append(f"{rel}: broken local link {raw!r}")

    if status in ACTIVE_STATUSES:
        if "file://" in text:
            errors.append(f"{rel}: active documentation must not use file:// links")
        for code_path in CODE_PATH_RE.findall(text):
            if not (ROOT / code_path).exists():
                errors.append(f"{rel}: referenced repository file does not exist: {code_path}")


def frontmatter(text: str, rel: Path, errors: list[str]) -> str | None:
    if not text.startswith("---\n"):
        errors.append(f"{rel}: SKILL.md must start with YAML frontmatter")
        return None
    end = text.find("\n---\n", 4)
    if end == -1:
        errors.append(f"{rel}: SKILL.md frontmatter is not closed")
        return None
    return text[4:end]


def validate_skill(path: Path, errors: list[str]) -> None:
    rel = path.relative_to(ROOT)
    text = path.read_text(encoding="utf-8")
    header = frontmatter(text, rel, errors)
    if header is None:
        return
    name_match = NAME_RE.search(header)
    if not name_match:
        errors.append(f"{rel}: skill frontmatter is missing a simple name")
    elif name_match.group(1) != path.parent.name:
        errors.append(
            f"{rel}: skill name {name_match.group(1)!r} must match directory {path.parent.name!r}"
        )
    if not DESCRIPTION_RE.search(header):
        errors.append(f"{rel}: skill frontmatter is missing description")
    elif re.search(r"^description:\s*(?:[>|]-?)?\s*$", header, re.MULTILINE):
        lines = header.splitlines()
        desc_index = next((i for i, line in enumerate(lines) if line.startswith("description:")), -1)
        continuation = lines[desc_index + 1 :] if desc_index >= 0 else []
        if not any(line.startswith(("  ", "\t")) and line.strip() for line in continuation):
            errors.append(f"{rel}: skill description is empty")


def validate_plugin(plugin_dir: Path, errors: list[str]) -> None:
    manifest_path = plugin_dir / "plugin.json"
    rel = plugin_dir.relative_to(ROOT)
    if not manifest_path.exists():
        errors.append(f"{rel}: missing plugin.json")
        return
    try:
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        errors.append(f"{manifest_path.relative_to(ROOT)}: invalid JSON: {exc}")
        return
    if manifest.get("name") != plugin_dir.name:
        errors.append(
            f"{manifest_path.relative_to(ROOT)}: manifest name must match directory {plugin_dir.name!r}"
        )
    mcp_path = plugin_dir / "mcp_config.json"
    if mcp_path.exists():
        try:
            config = json.loads(mcp_path.read_text(encoding="utf-8"))
            if not isinstance(config.get("mcpServers"), dict):
                errors.append(f"{mcp_path.relative_to(ROOT)}: mcpServers must be an object")
        except (OSError, json.JSONDecodeError) as exc:
            errors.append(f"{mcp_path.relative_to(ROOT)}: invalid JSON: {exc}")


def main() -> int:
    errors: list[str] = []

    skill_paths = list((ROOT / ".agents" / "skills").glob("*/SKILL.md"))
    skill_paths.extend((ROOT / "builtin" / "plugins").glob("*/skills/*/SKILL.md"))

    for path in sorted((ROOT / "docs").rglob("*.md")):
        if path.name == "AGENTS.md":
            continue
        status = parse_status(path, errors)
        validate_doc(path, status, errors)

    for path in sorted(skill_paths):
        validate_skill(path, errors)

    auxiliary_docs = {
        ROOT / "AGENTS.md",
        ROOT / "README.md",
        ROOT / "CONTRIBUTING.md",
        ROOT / ".agents" / "workflows" / "README.md",
        *ROOT.rglob("AGENTS.md"),
    }
    for skill_path in skill_paths:
        auxiliary_docs.update(skill_path.parent.rglob("*.md"))
    for path in sorted(auxiliary_docs):
        validate_doc(path, "Normative", errors)

    for plugin_dir in sorted(path for path in (ROOT / "builtin" / "plugins").iterdir() if path.is_dir()):
        validate_plugin(plugin_dir, errors)

    if errors:
        print("Documentation/skill check failed:")
        for error in errors:
            print(f"  - {error}")
        return 1

    print(
        f"Documentation/skill check passed: "
        f"{len(list((ROOT / 'docs').rglob('*.md')))} docs, "
        f"{len(skill_paths)} skills."
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
