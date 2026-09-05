#!/usr/bin/env python3
"""Enforce repository dependency and portability invariants."""

from __future__ import annotations

import re
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
CORE = ROOT / "internal" / "core"

IMPORT_RE = re.compile(r'^\s*(?:[A-Za-z_][A-Za-z0-9_]*\s+)?"([^"]+)"\s*$', re.MULTILINE)


def go_imports(path: Path) -> set[str]:
    return set(IMPORT_RE.findall(path.read_text(encoding="utf-8")))


def main() -> int:
    errors: list[str] = []

    for path in CORE.rglob("*.go"):
        if path.name.endswith("_test.go"):
            continue
        rel = path.relative_to(ROOT)
        imports = go_imports(path)

        for imported in imports:
            if imported.startswith("agyent/internal/adapters") or imported.startswith("agyent/cmd"):
                errors.append(f"{rel}: core must not import outward dependency {imported!r}")

        if rel.parts[:3] == ("internal", "core", "domain"):
            for imported in imports:
                if imported.startswith("agyent/"):
                    errors.append(f"{rel}: domain must be infrastructure-independent, found {imported!r}")

        if rel.parts[:3] == ("internal", "core", "ports"):
            for imported in imports:
                if imported.startswith("agyent/") and imported != "agyent/internal/core/domain":
                    errors.append(f"{rel}: ports may depend only on core/domain, found {imported!r}")

    for path in (ROOT / "internal" / "adapters").rglob("*.go"):
        rel = path.relative_to(ROOT)
        for imported in go_imports(path):
            if imported.startswith("agyent/cmd"):
                errors.append(f"{rel}: adapters must not import the CLI composition root {imported!r}")

    searchable = [ROOT / "go.mod"]
    searchable.extend((ROOT / "internal").rglob("*.go"))
    searchable.extend((ROOT / "cmd").rglob("*.go"))
    for path in searchable:
        text = path.read_text(encoding="utf-8")
        if "github.com/mattn/go-sqlite3" in text:
            errors.append(f"{path.relative_to(ROOT)}: CGO SQLite driver is forbidden; use modernc.org/sqlite")

    composition = (ROOT / "cmd" / "agyent" / "run.go").read_text(encoding="utf-8")
    for required in ("execution.NewService(", "eng.SetExecutionService(execSvc)", "ipcServer.SetPolicyEngine(policyEngine)"):
        if required not in composition:
            errors.append(f"cmd/agyent/run.go: missing required security/execution wiring {required!r}")

    sqlite_source = (ROOT / "internal" / "adapters" / "storage" / "sqlite" / "sqlite.go").read_text(encoding="utf-8")
    for required in ("writeDB.SetMaxOpenConns(1)", "readDB.SetMaxOpenConns(20)", "journal_mode(WAL)", "busy_timeout(5000)"):
        if required not in sqlite_source:
            errors.append(f"internal/adapters/storage/sqlite/sqlite.go: missing storage invariant {required!r}")

    if errors:
        print("Architecture check failed:")
        for error in errors:
            print(f"  - {error}")
        return 1

    print("Architecture check passed.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
