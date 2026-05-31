#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
# Copyright (C) 2026 openccu-loom authors.
"""
parity_const_diff.py — Konstanten-Diff aiohomematic ↔ openccu-loom.

Extrahiert konstanten-ähnliche Werte aus aiohomematic/aiohomematic/const.py
und vergleicht sie gegen Konstanten in openccu-loom/internal/ + pkg/.

Output: Tabelle „nur in Python" / „nur in Go" / „beidseitig".

Usage:
    ./script/parity_const_diff.py            # Markdown nach stdout
    ./script/parity_const_diff.py --json     # JSON-Output für Tooling

Adresses parity_audit.md L2.4 (P2-Cleanup).

Methodik:
- Python: regex auf `^_?[A-Z_]+\\s*[:=]` in const.py
- Go:     regex auf `\\b[A-Z][a-zA-Z0-9_]*\\s*=` in const-Blöcken
  (rekursiv über internal/ + pkg/, ohne _test.go)
- Schnittmenge: Python-NAME in {Camelize(Python-NAME), Python-NAME, ...}
- Drift: Python-NAME ohne Go-Pendant; Go-NAME ohne Python-Pendant.

Keine externen Abhängigkeiten — nur stdlib.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path
from typing import Iterable

ROOT = Path(__file__).resolve().parent.parent
WORKSPACE = ROOT.parent

PY_CONST = WORKSPACE / "aiohomematic" / "aiohomematic" / "const.py"

GO_DIRS = [
    ROOT / "internal",
    ROOT / "pkg",
]

# Match `_?NAME: Final = ...`, `_?NAME = ...`, `_?NAME: int = ...` etc.
PY_CONST_RE = re.compile(
    r"^(?P<name>_?[A-Z][A-Z0-9_]+)\s*(?::[^=]+)?=", re.MULTILINE
)

# Match `NAME = value` inside a `const ( ... )` block.
GO_CONST_RE = re.compile(
    r"^\s*(?P<name>[A-Z][A-Za-z0-9_]+)\s+(?:[A-Za-z0-9_.\[\]]+\s+)?=", re.MULTILINE
)


def extract_python_constants(path: Path) -> set[str]:
    if not path.exists():
        return set()
    text = path.read_text(encoding="utf-8")
    out: set[str] = set()
    for m in PY_CONST_RE.finditer(text):
        name = m.group("name").lstrip("_")
        # filter out lower-case-ish leftovers (the regex is greedy);
        # require >=2 capital letters to avoid e.g. `T = TypeVar(...)`.
        if sum(1 for c in name if c.isupper()) >= 2:
            out.add(name)
    return out


def extract_go_constants(roots: Iterable[Path]) -> set[str]:
    out: set[str] = set()
    for root in roots:
        if not root.exists():
            continue
        for path in root.rglob("*.go"):
            if path.name.endswith("_test.go"):
                continue
            try:
                text = path.read_text(encoding="utf-8", errors="replace")
            except OSError:
                continue
            # only scan inside const ( ) blocks to avoid catching
            # arbitrary `var Name = ...` and method definitions
            for m in re.finditer(r"const\s*\(([^)]*)\)", text, re.DOTALL):
                block = m.group(1)
                for c in GO_CONST_RE.finditer(block):
                    out.add(c.group("name"))
            # Also accept top-level `const Name = ...` declarations.
            for m in re.finditer(
                r"^const\s+([A-Z][A-Za-z0-9_]+)\s+(?:[A-Za-z0-9_.\[\]]+\s+)?=",
                text,
                re.MULTILINE,
            ):
                out.add(m.group(1))
    return out


def py_to_go_camelcase(name: str) -> str:
    """Translate `FOO_BAR_BAZ` → `FooBarBaz` for cross-stack matching."""
    parts = name.split("_")
    return "".join(p.capitalize() for p in parts if p)


def diff(py: set[str], go: set[str]) -> dict[str, list[str]]:
    py_camel = {py_to_go_camelcase(n): n for n in py}
    only_py: list[str] = []
    only_go: list[str] = []
    both: list[str] = []
    for name in sorted(py):
        cam = py_to_go_camelcase(name)
        if cam in go or name in go:
            both.append(name)
        else:
            only_py.append(name)
    py_camel_set = set(py_camel.keys())
    for name in sorted(go):
        if name in py or name in py_camel_set:
            continue
        only_go.append(name)
    return {"both": both, "only_py": only_py, "only_go": only_go}


def render_markdown(d: dict[str, list[str]]) -> str:
    out: list[str] = []
    out.append("# Parity Const Diff — aiohomematic ↔ openccu-loom")
    out.append("")
    out.append(f"- Beide: {len(d['both'])}")
    out.append(f"- Nur in aiohomematic: {len(d['only_py'])}")
    out.append(f"- Nur in openccu-loom: {len(d['only_go'])}")
    out.append("")
    if d["only_py"]:
        out.append("## Nur in Python")
        out.append("")
        for n in d["only_py"]:
            out.append(f"- `{n}`")
        out.append("")
    if d["only_go"]:
        out.append("## Nur in Go")
        out.append("")
        for n in d["only_go"]:
            out.append(f"- `{n}`")
        out.append("")
    return "\n".join(out)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--json", action="store_true", help="emit JSON instead of Markdown")
    ap.add_argument("--out", type=Path, default=None, help="write to this file (default: stdout)")
    args = ap.parse_args()

    py = extract_python_constants(PY_CONST)
    if not py:
        print(f"warning: {PY_CONST} not readable; treating as empty", file=sys.stderr)
    go = extract_go_constants(GO_DIRS)

    d = diff(py, go)
    if args.json:
        text = json.dumps(d, indent=2, sort_keys=True)
    else:
        text = render_markdown(d)

    if args.out:
        args.out.write_text(text + "\n", encoding="utf-8")
    else:
        print(text)
    return 0


if __name__ == "__main__":
    sys.exit(main())
