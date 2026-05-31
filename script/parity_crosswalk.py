#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
# Copyright (C) 2026 openccu-loom authors.
"""
Datei-/Symbol-Crosswalk Python ↔ Go.

Walks every .py file in the aiohomematic family, maps each to its
expected counterpart in the openccu-loom tree, computes file-level
LOC ratios, and grep-checks top-level symbols (classes, functions,
module-level CONSTANTS) for presence in any Go source file under
the candidate directories.

Output: a single Markdown document (default ``parity_crosswalk.md``
at the openccu-loom repo root). The document is fully regenerated on
every run — *no* manual edits should live in it. Curated assessment
(roadmap IDs, status flags, owners, done-dates) lives in
``parity_audit.md`` and references this file.

Usage:
    ./script/parity_crosswalk.py            # generate parity_crosswalk.md
    ./script/parity_crosswalk.py --out=...  # custom path

The script has no third-party dependencies; only stdlib.
"""

from __future__ import annotations

import argparse
import ast
import datetime as _dt
import re
import subprocess
import sys
from dataclasses import dataclass, field
from pathlib import Path

# ---------------------------------------------------------------------------
# Paths — anchored to the developer's local layout (see CLAUDE.md).
# ---------------------------------------------------------------------------

OPENCCU_LOOM_ROOT = Path(__file__).resolve().parent.parent
WORKSPACE_ROOT = OPENCCU_LOOM_ROOT.parent

PY_REPOS: dict[str, Path] = {
    "aiohomematic": WORKSPACE_ROOT / "aiohomematic" / "aiohomematic",
    "aiohomematic-config": WORKSPACE_ROOT / "aiohomematic-config" / "aiohomematic_config",
    "aiohomematic2mqtt": WORKSPACE_ROOT / "aiohomematic2mqtt" / "aiohomematic2mqtt",
}

GO_ROOTS: list[Path] = [
    OPENCCU_LOOM_ROOT / "internal",
    OPENCCU_LOOM_ROOT / "pkg",
    OPENCCU_LOOM_ROOT / "cmd",
]

# ---------------------------------------------------------------------------
# Path mapping. Order matters: most specific prefix first. Each entry maps a
# Python directory prefix (relative to its repo root) to one or more candidate
# Go directories (relative to openccu-loom repo root). A Python file is matched
# against the *first* rule whose prefix it begins with.
# ---------------------------------------------------------------------------

PathRule = tuple[str, str, list[str]]  # (repo_label, py_prefix, [go_dirs...])

PATH_RULES: list[PathRule] = [
    # aiohomematic — most specific first.
    ("aiohomematic", "central/coordinators/", ["internal/central/coordinators"]),
    ("aiohomematic", "central/events/", ["internal/central/events"]),
    ("aiohomematic", "central/", ["internal/central", "internal/central/registry", "internal/central/statemachine", "internal/central/adapter", "internal/central/rpcserver"]),
    ("aiohomematic", "client/backends/", ["internal/client/backends"]),
    ("aiohomematic", "client/", ["internal/client", "internal/client/transport", "internal/client/transport/xmlrpc", "internal/client/transport/binrpc", "internal/client/transport/jsonrpc", "internal/client/reliability", "internal/client/rega"]),
    ("aiohomematic", "interfaces/", ["pkg/interfaces"]),
    ("aiohomematic", "metrics/", ["internal/metrics", "internal/observability"]),
    ("aiohomematic", "model/calculated/", ["internal/model/calculated"]),
    ("aiohomematic", "model/combined/", ["internal/model/combined"]),
    ("aiohomematic", "model/custom/capabilities/", ["internal/model/custom"]),
    ("aiohomematic", "model/custom/", ["internal/model/custom", "internal/model/custom/climate", "internal/model/custom/cover", "internal/model/custom/light", "internal/model/custom/lock", "internal/model/custom/siren", "internal/model/custom/switch", "internal/model/custom/textdisplay", "internal/model/custom/valve"]),
    ("aiohomematic", "model/generic/", ["internal/model/generic"]),
    ("aiohomematic", "model/hub/", ["internal/model/hub"]),
    ("aiohomematic", "model/mixins/", ["internal/model", "internal/model/optimistic"]),
    ("aiohomematic", "model/", ["internal/model", "internal/model/device", "internal/model/event", "internal/model/schedule", "internal/model/weekprofile"]),
    ("aiohomematic", "rega_scripts/", ["internal/client/rega/scripts"]),
    ("aiohomematic", "schemas/", ["pkg/hmproto", "internal/parameter", "internal/payload"]),
    ("aiohomematic", "store/dynamic/", ["internal/store/dynamic"]),
    ("aiohomematic", "store/patches/", ["internal/store/patches"]),
    ("aiohomematic", "store/persistent/", ["internal/store/sqlite"]),
    ("aiohomematic", "store/visibility/", ["internal/store/visibility"]),
    ("aiohomematic", "store/", ["internal/store", "internal/store/masterprofile"]),
    ("aiohomematic", "support/", ["pkg/hmtypes", "pkg/hmerr", "pkg/hmenum", "pkg/hmevent", "internal/observability", "internal/clock", "internal/parameter", "internal/boundary"]),
    ("aiohomematic", "translations/", ["internal/i18n", "internal/i18n/catalogs"]),
    ("aiohomematic", "", ["internal", "pkg"]),
    # aiohomematic-config — Config UI logic.
    ("aiohomematic-config", "", ["internal/configui", "internal/configui/easymode"]),
    # aiohomematic2mqtt — MQTT bridge reference.
    ("aiohomematic2mqtt", "bridge/", ["internal/north/mqtt"]),
    ("aiohomematic2mqtt", "discovery/", ["internal/north/mqtt"]),
    ("aiohomematic2mqtt", "interfaces/", ["internal/north/mqtt", "pkg/interfaces"]),
    ("aiohomematic2mqtt", "metrics/", ["internal/metrics", "internal/north/mqtt"]),
    ("aiohomematic2mqtt", "payloads/", ["internal/north/mqtt", "internal/north/mqtt/protocol"]),
    ("aiohomematic2mqtt", "platforms/", ["internal/north/mqtt"]),
    ("aiohomematic2mqtt", "runner/", ["internal/north/mqtt", "cmd/openccu-loom"]),
    ("aiohomematic2mqtt", "", ["internal/north/mqtt", "internal/north/mqtt/protocol", "cmd/openccu-loom"]),
]

# Filenames that exist in Python without a meaningful Go counterpart.
# Anything matching is reported as ``➖ skipped`` rather than missing.
SKIP_FILES = {
    "__init__.py",
    "py.typed",
    "_version.py",
    "version.py",
}

# Top-level symbols whose absence in Go is not a finding (e.g. Python-only
# helpers, tests, ``__all__`` lists). Currently empty; populate as needed.
SYMBOL_IGNORE: set[str] = set()

# ---------------------------------------------------------------------------
# Architecture-Divergence Ignore-List (Improvement B)
#
# Files matching these entries are architectural differences, not missing
# implementations. They get status "🟢 (architecture)" and are listed in a
# separate section. They do NOT count as ❌ in the summary.
#
# Each entry: (repo_label, py_path_substring, reason)
# PATH_RULES mappings take precedence over this list — an entry that matches
# a specific PATH_RULES mapping will be classified by that rule first.
# To mark a specific file as NOT an architecture divergence (so that a
# PATH_RULES or symbol match can classify it properly), add its rel_path
# to ARCHITECTURE_DIVERGENCE_EXCLUDES.
# ---------------------------------------------------------------------------

ArchDivergence = tuple[str, str, str]  # (repo, py_path_substring, reason)

ARCHITECTURE_DIVERGENCES: list[ArchDivergence] = [
    (
        "aiohomematic",
        "interfaces/",
        "Python Protocol classes → Go implicit interface satisfaction (no 1:1 file needed)",
    ),
    (
        "aiohomematic",
        "support/mixins.py",
        "Mixin pattern → Go struct embedding (no dedicated file needed)",
    ),
    (
        "aiohomematic",
        "type_aliases.py",
        "Python type aliases bundle → Go uses pkg/hmtypes for typed primitives",
    ),
    (
        "aiohomematic",
        "model/calculated/field.py",
        "CalculatedDataPointField descriptor → Go uses explicit struct fields (no descriptor pattern)",
    ),
    (
        "aiohomematic",
        "model/custom/field.py",
        "DataPointField descriptor → Go uses explicit struct fields (no descriptor pattern)",
    ),
    (
        "aiohomematic2mqtt",
        "platforms/",
        "Per-platform classes → generic discovery_aggregate.go layer",
    ),
    (
        "aiohomematic2mqtt",
        "exceptions.py",
        "Python exception hierarchy → Go pkg/hmerr sentinel errors",
    ),
    (
        "aiohomematic2mqtt",
        "interfaces/",
        "Python Protocol classes → Go implicit interface satisfaction",
    ),
    (
        "aiohomematic2mqtt",
        "config.py",
        "Module init / version marker only (1 LOC)",
    ),
    (
        "aiohomematic2mqtt",
        "discovery/envelope.py",
        "build_origin_info / build_device_info → inlined in Go discovery_aggregate.go / deviceDescriptor()",
    ),
    (
        "aiohomematic2mqtt",
        "metrics/counters.py",
        "BridgeMetrics → covered by internal/metrics provider interfaces (F11.F P1-11.x)",
    ),
    (
        "aiohomematic2mqtt",
        "runner/config_loader.py",
        "Required-keys validation → covered by F11 QW-20 Validate() + validateMQTT() in internal/config/",
    ),
]

# Files that match an ARCHITECTURE_DIVERGENCES entry but should be excluded
# from the architecture classification (because a real Go implementation
# already covers them via PATH_RULES or symbol match).
ARCHITECTURE_DIVERGENCE_EXCLUDES: set[str] = {
    # QW-21: text_display.py is implemented via discovery_aggregate.go
    "aiohomematic2mqtt/platforms/text_display.py",
}

# ---------------------------------------------------------------------------
# Symbol-Synonym Table (Improvement C)
#
# When a Python symbol is looked up in Go and not found, the synonym is also
# searched. This handles cases where naming conventions differ structurally
# (suffix-drop, prefix-drop, etc.).
# ---------------------------------------------------------------------------

SYMBOL_SYNONYMS: dict[str, str] = {
    # client/backends/protocol.py::BackendOperationsProtocol → backend.go::Operations
    "BackendOperationsProtocol": "Operations",
    # model/combined/hs_color.py::CombinedDpHsColor → hscolor.go::HSColor
    "CombinedDpHsColor": "HSColor",
    # platforms/text_display.py::MqttTextDisplay → discovery_aggregate.go::buildTextDisplay (QW-21)
    "MqttTextDisplay": "buildTextDisplay",
    # discovery/envelope.py::build_origin_info → inlined in channelBaseBody (origin block)
    "build_origin_info": "channelBaseBody",
    # discovery/envelope.py::build_device_info → discovery.go::deviceDescriptor
    "build_device_info": "deviceDescriptor",
}

# ---------------------------------------------------------------------------
# Models
# ---------------------------------------------------------------------------


@dataclass
class PyFile:
    repo: str
    rel_path: str  # relative to repo root (e.g. "store/visibility/rules.py")
    abs_path: Path
    lines: int  # source lines (non-blank, non-comment-only)
    symbols: list[str] = field(default_factory=list)  # top-level identifiers


@dataclass
class GoFile:
    rel_path: str  # relative to openccu-loom root
    abs_path: Path
    lines: int


@dataclass
class FileMatch:
    py: PyFile
    go_dirs: list[str]
    go_match: GoFile | None  # primary file-level match (best filename overlap)
    go_dir_files: list[GoFile]  # all .go files in the candidate dirs
    symbol_hits: dict[str, list[str]]  # symbol -> list of go file paths where found
    skipped: bool = False
    arch_divergence: str = ""  # non-empty → architecture divergence reason
    note: str = ""

    @property
    def py_loc(self) -> int:
        return self.py.lines

    @property
    def go_loc(self) -> int:
        return self.go_match.lines if self.go_match else 0

    @property
    def go_dir_loc(self) -> int:
        """Aggregated LOC of all .go files in the candidate dirs (excl. tests).

        Used to defeat the false-positive ``skeleton`` label that arises when
        Python's per-file logic is split across multiple Go files. If the
        directory total is healthy, the file is not a skeleton.
        """
        return sum(
            f.lines for f in self.go_dir_files
            if not f.rel_path.endswith("_test.go")
        )

    @property
    def loc_ratio(self) -> float | None:
        if self.py_loc == 0:
            return None
        if self.go_loc == 0:
            return 0.0
        return self.go_loc / self.py_loc

    @property
    def dir_loc_ratio(self) -> float | None:
        if self.py_loc == 0:
            return None
        return self.go_dir_loc / self.py_loc if self.go_dir_loc else 0.0

    @property
    def symbols_total(self) -> int:
        return len(self.py.symbols)

    @property
    def symbols_found(self) -> int:
        return sum(1 for s in self.py.symbols if self.symbol_hits.get(s))

    @property
    def status(self) -> str:
        if self.skipped:
            return "➖"
        if self.arch_divergence:
            return "🟢 (architecture)"
        if not self.go_match and not any(self.symbol_hits.values()):
            return "❌"
        if not self.go_match:
            # File missing but symbols found elsewhere in candidate dir.
            return "⚠️ (renamed)"
        # File exists. Check skeleton vs implemented.
        ratio = self.loc_ratio or 0.0
        dir_ratio = self.dir_loc_ratio or 0.0
        sym_ratio = (
            self.symbols_found / self.symbols_total
            if self.symbols_total
            else 1.0
        )
        # Real skeleton: per-file ratio low AND directory aggregate also low.
        # Logic split across multiple Go files dissolves the per-file penalty.
        if (ratio < 0.3 and dir_ratio < 0.3) or sym_ratio < 0.5:
            return "⚠️ (skeleton)"
        return "✅"


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

_PY_COMMENT = re.compile(r"^\s*#")
_GO_COMMENT = re.compile(r"^\s*//")


def count_source_lines(path: Path, kind: str) -> int:
    """Count non-blank, non-comment-only lines."""
    pattern = _PY_COMMENT if kind == "py" else _GO_COMMENT
    n = 0
    try:
        with path.open(encoding="utf-8", errors="replace") as fh:
            for raw in fh:
                line = raw.strip()
                if not line:
                    continue
                if pattern.match(line):
                    continue
                n += 1
    except OSError:
        return 0
    return n


def py_top_level_symbols(path: Path) -> list[str]:
    """Return top-level class names, function names, and CONSTANT_NAMES."""
    try:
        tree = ast.parse(path.read_text(encoding="utf-8", errors="replace"))
    except (SyntaxError, OSError):
        return []
    out: list[str] = []
    for node in tree.body:
        if isinstance(node, (ast.ClassDef, ast.FunctionDef, ast.AsyncFunctionDef)):
            out.append(node.name)
        elif isinstance(node, ast.Assign):
            for tgt in node.targets:
                if isinstance(tgt, ast.Name) and tgt.id.isupper() and len(tgt.id) > 2:
                    out.append(tgt.id)
        elif isinstance(node, ast.AnnAssign) and isinstance(node.target, ast.Name):
            name = node.target.id
            if name.isupper() and len(name) > 2:
                out.append(name)
    return out


def snake_to_camel(name: str) -> str:
    """Convert ``foo_bar_baz`` to ``FooBarBaz``; strip leading underscores."""
    stripped = name.lstrip("_")
    parts = stripped.split("_")
    return "".join(p[:1].upper() + p[1:] for p in parts if p)


def symbol_variants(name: str) -> list[str]:
    """Possible Go identifier spellings of a Python identifier."""
    stripped = name.lstrip("_")
    variants = {name, stripped}
    if "_" in stripped:
        camel = snake_to_camel(stripped)
        variants.add(camel)
        # exported → unexported (private Go) variant
        if camel:
            variants.add(camel[:1].lower() + camel[1:])
    return [v for v in variants if v]


def collect_py_files(repo_root: Path) -> list[Path]:
    out: list[Path] = []
    for p in repo_root.rglob("*.py"):
        if "__pycache__" in p.parts:
            continue
        out.append(p)
    return sorted(out)


def collect_go_files(roots: list[Path]) -> list[Path]:
    out: list[Path] = []
    for root in roots:
        if not root.exists():
            continue
        for p in root.rglob("*.go"):
            out.append(p)
    return sorted(out)


def match_path_rule(repo: str, rel_path: str) -> list[str]:
    """Return list of candidate Go directories for a given Python file."""
    rel_dir = str(Path(rel_path).parent) + "/" if Path(rel_path).parent != Path() else ""
    for r_repo, prefix, dirs in PATH_RULES:
        if r_repo != repo:
            continue
        if rel_dir.startswith(prefix):
            return dirs
    return []


def best_filename_match(py_basename: str, go_files: list[GoFile]) -> GoFile | None:
    """Pick the closest Go file for a given Python basename.

    Strategy (in order):
      1. Exact stem match (e.g. ``timer.py`` ↔ ``timer.go``).
      2. Underscore-stripped stem match: strip all underscores from both
         the Python stem and the Go stem before comparing (e.g.
         ``hs_color.py`` → ``hscolor`` ↔ ``hscolor.go``).
      3. Substring containment in either direction.
    """
    stem = Path(py_basename).stem
    stem_nounderscore = stem.replace("_", "")
    candidates = [f for f in go_files if not f.rel_path.endswith("_test.go")]

    # 1. Exact stem match.
    for f in candidates:
        if Path(f.abs_path.name).stem == stem:
            return f

    # 2. Underscore-stripped stem match (Improvement A).
    for f in candidates:
        gstem_nounderscore = Path(f.abs_path.name).stem.replace("_", "")
        if stem_nounderscore and gstem_nounderscore and stem_nounderscore == gstem_nounderscore:
            return f

    # 3. Substring containment in either direction.
    for f in candidates:
        gstem = Path(f.abs_path.name).stem
        if stem in gstem or gstem in stem:
            return f
    return None


def build_go_text_index(go_files: list[Path]) -> dict[str, str]:
    """Concatenated content per file, for grep-style symbol search."""
    return {
        str(p.relative_to(OPENCCU_LOOM_ROOT)): p.read_text(
            encoding="utf-8", errors="replace"
        )
        for p in go_files
    }


def find_symbol(
    sym: str, candidate_dirs: list[str], go_index: dict[str, str]
) -> list[str]:
    """Return Go files (rel paths) that contain any spelling of ``sym``.

    Candidate dirs are searched first, then the rest as fallback.
    Also checks SYMBOL_SYNONYMS for alternative Go spellings (Improvement C).
    """
    # Collect all name variants including synonyms.
    all_names = [sym]
    if sym in SYMBOL_SYNONYMS:
        all_names.append(SYMBOL_SYNONYMS[sym])
    variants: list[str] = []
    for name in all_names:
        variants.extend(symbol_variants(name))
    # Deduplicate while preserving order.
    seen: set[str] = set()
    unique_variants: list[str] = []
    for v in variants:
        if v not in seen:
            seen.add(v)
            unique_variants.append(v)

    if not unique_variants:
        return []
    patterns = [
        re.compile(rf"\b{re.escape(v)}\b") for v in unique_variants
    ]

    def _search(rel: str, text: str) -> bool:
        return any(p.search(text) for p in patterns)

    primary: list[str] = []
    fallback: list[str] = []
    for rel, text in go_index.items():
        in_dir = any(rel.startswith(d.rstrip("/") + "/") for d in candidate_dirs)
        if not _search(rel, text):
            continue
        if in_dir:
            primary.append(rel)
        else:
            fallback.append(rel)
    return primary if primary else fallback


# ---------------------------------------------------------------------------
# Main pipeline
# ---------------------------------------------------------------------------


def git_sha(repo: Path) -> str:
    try:
        out = subprocess.run(
            ["git", "rev-parse", "--short=10", "HEAD"],
            cwd=repo,
            check=True,
            capture_output=True,
            text=True,
        )
        return out.stdout.strip()
    except (subprocess.CalledProcessError, FileNotFoundError):
        return "unknown"


def check_arch_divergence(repo: str, rel_path: str) -> str:
    """Return the divergence reason if this file is an architecture divergence.

    Returns empty string if the file should be classified normally.
    Specific excludes (ARCHITECTURE_DIVERGENCE_EXCLUDES) take full priority.
    """
    key = f"{repo}/{rel_path}"
    if key in ARCHITECTURE_DIVERGENCE_EXCLUDES:
        return ""
    for ad_repo, substring, reason in ARCHITECTURE_DIVERGENCES:
        if ad_repo != repo:
            continue
        if substring in rel_path:
            return reason
    return ""


def build_match(py: PyFile, go_index: dict[str, str], all_go: list[GoFile]) -> FileMatch:
    if py.abs_path.name in SKIP_FILES:
        return FileMatch(
            py=py,
            go_dirs=[],
            go_match=None,
            go_dir_files=[],
            symbol_hits={},
            skipped=True,
            note="Modul-Init / Version-Marker — kein Go-Pendant erwartet",
        )

    candidate_dirs = match_path_rule(py.repo, py.rel_path)
    dir_files = [
        f
        for f in all_go
        if any(f.rel_path.startswith(d.rstrip("/") + "/") for d in candidate_dirs)
    ]
    go_match = best_filename_match(Path(py.rel_path).name, dir_files)
    sym_hits = {
        s: find_symbol(s, candidate_dirs, go_index)
        for s in py.symbols
        if s not in SYMBOL_IGNORE
    }

    # Improvement B: classify as architecture divergence when:
    # - no file match AND no symbol hits (would be ❌ otherwise), OR
    # - the file is in ARCHITECTURE_DIVERGENCES regardless of match status
    #   (because the whole directory is structurally different in Go).
    # ARCHITECTURE_DIVERGENCE_EXCLUDES bypasses this for specific files.
    arch_reason = check_arch_divergence(py.repo, py.rel_path)
    # Only apply arch classification when there is genuinely no Go counterpart
    # found (would otherwise be ❌) OR when the entire directory is divergent.
    # If a file match or symbol hit exists, let normal classification proceed so
    # that real implementations are not hidden behind the architecture label.
    has_any_match = bool(go_match) or any(sym_hits.values())
    if arch_reason and not has_any_match:
        return FileMatch(
            py=py,
            go_dirs=candidate_dirs,
            go_match=go_match,
            go_dir_files=dir_files,
            symbol_hits=sym_hits,
            arch_divergence=arch_reason,
        )

    return FileMatch(
        py=py,
        go_dirs=candidate_dirs,
        go_match=go_match,
        go_dir_files=dir_files,
        symbol_hits=sym_hits,
    )


def group_key(rel_path: str) -> str:
    parts = Path(rel_path).parts
    return "/".join(parts[:-1]) if len(parts) > 1 else "(root)"


def render_markdown(matches: list[FileMatch]) -> str:
    now = _dt.datetime.now(_dt.UTC).strftime("%Y-%m-%dT%H:%MZ")
    shas = {
        "aiohomematic": git_sha(WORKSPACE_ROOT / "aiohomematic"),
        "aiohomematic-config": git_sha(WORKSPACE_ROOT / "aiohomematic-config"),
        "openccu-loom": git_sha(OPENCCU_LOOM_ROOT),
    }

    out: list[str] = []
    out.append("# parity_crosswalk.md — Datei-/Symbol-Crosswalk Python ↔ Go")
    out.append("")
    out.append(
        "> **Mechanisch generiert** von `script/parity_crosswalk.py`. "
        "Wird bei jedem Lauf vollständig überschrieben — keine manuellen Edits."
    )
    out.append(
        "> Kuratiertes Audit (Roadmap-IDs, Owner, Done-Dates) siehe `parity_audit.md`."
    )
    out.append("")
    out.append(f"**Generiert:** {now}")
    out.append(f"**aiohomematic:** `{shas['aiohomematic']}`")
    out.append(f"**aiohomematic-config:** `{shas['aiohomematic-config']}`")
    out.append(f"**openccu-loom:** `{shas['openccu-loom']}`")
    out.append("")
    out.append("## Legende")
    out.append("")
    out.append("| Symbol | Bedeutung |")
    out.append("|---|---|")
    out.append("| ✅ | Go-Pendant existiert, Datei- *oder* Verzeichnis-LOC-Verhältnis ≥ 30 %, ≥ 50 % Symbole gefunden |")
    out.append("| ⚠️ (skeleton) | Sowohl Datei- als auch Verzeichnis-Aggregat < 30 % der Python-LOC, oder < 50 % Symbole |")
    out.append("| ⚠️ (renamed) | Keine gleichnamige Go-Datei, aber ≥ 1 Symbol im Kandidatenverzeichnis gefunden |")
    out.append("| ❌ | Weder gleichnamige Datei noch Symbole im Kandidatenverzeichnis |")
    out.append("| 🟢 (architecture) | Architektur-Divergenz — kein 1:1 Go-Pendant erwartet (zählt nicht als ❌) |")
    out.append("| ➖ | Übersprungen (`__init__.py` etc.) |")
    out.append("")
    out.append(
        'Die 30 %-Schwelle wird **zuerst gegen die Einzeldatei** und **dann gegen '
        'die LOC-Summe aller `.go`-Dateien im Kandidatenverzeichnis** geprüft. '
        'Erst wenn beide Werte unter 30 % liegen, wird ⚠️ (skeleton) vergeben — '
        'das fängt das Muster „Logik in viele Geschwister-Files verteilt" ab. '
        'Symbole werden mit Snake-→Camel-Konvertierung gesucht; Treffer in '
        'Test-Dateien zählen ebenfalls. Hinweis: Python-`Protocol`-Klassen '
        'haben in Go oft kein 1:1-Pendant, weil Go implicit interface '
        'satisfaction nutzt — solche Treffer können False Positives sein.'
    )
    out.append("")

    # ----- Übersicht je Subsystem -----
    out.append("## Übersicht je Subsystem")
    out.append("")
    out.append("| Subsystem | Dateien | ✅ | ⚠️ | ❌ | 🟢 | ➖ |")
    out.append("|---|---:|---:|---:|---:|---:|---:|")
    by_group: dict[str, list[FileMatch]] = {}
    for m in matches:
        key = f"{m.py.repo}/{group_key(m.py.rel_path)}"
        by_group.setdefault(key, []).append(m)

    for key in sorted(by_group):
        ms = by_group[key]
        n_ok = sum(1 for m in ms if m.status == "✅")
        n_warn = sum(1 for m in ms if m.status.startswith("⚠️"))
        n_miss = sum(1 for m in ms if m.status == "❌")
        n_arch = sum(1 for m in ms if m.status == "🟢 (architecture)")
        n_skip = sum(1 for m in ms if m.status == "➖")
        out.append(
            f"| `{key}` | {len(ms)} | {n_ok} | {n_warn} | {n_miss} | {n_arch} | {n_skip} |"
        )
    out.append("")

    # ----- Detail-Tabellen -----
    out.append("## Detail")
    out.append("")
    for key in sorted(by_group):
        ms = sorted(by_group[key], key=lambda m: m.py.rel_path)
        out.append(f"### `{key}/`")
        out.append("")
        # Take dirs from any non-skipped match (skipped files have empty go_dirs).
        dirs_for_header: list[str] = next(
            (m.go_dirs for m in ms if m.go_dirs), []
        )
        if dirs_for_header:
            joined = ", ".join(f"`{d}/`" for d in dirs_for_header)
            out.append(f"Erwartete Go-Verzeichnisse: {joined}")
        else:
            out.append("**Kein Pfad-Mapping definiert** — Eintrag in `PATH_RULES` fehlt.")
        out.append("")
        out.append(
            "| Status | Python-Datei | py-LOC | Go-Pendant | go-LOC | Verhältnis | Sym (gef./ges.) | Notiz |"
        )
        out.append("|---|---|---:|---|---:|---:|---:|---|")
        for m in ms:
            py_name = Path(m.py.rel_path).name
            go_pendant = (
                f"`{m.go_match.rel_path}`"
                if m.go_match
                else "—"
            )
            ratio = (
                f"{m.loc_ratio:.0%}"
                if m.loc_ratio is not None and m.go_match
                else "—"
            )
            sym_cell = (
                f"{m.symbols_found}/{m.symbols_total}"
                if m.symbols_total
                else "—"
            )
            note = m.note
            if not note and m.arch_divergence:
                note = m.arch_divergence
            elif not note and m.status == "❌":
                # List a few missing symbols as a hint.
                missing = [s for s in m.py.symbols if not m.symbol_hits.get(s)]
                if missing:
                    note = "fehlend: " + ", ".join(f"`{s}`" for s in missing[:4])
                    if len(missing) > 4:
                        note += f" (+{len(missing) - 4})"
            elif not note and m.status.startswith("⚠️"):
                missing = [s for s in m.py.symbols if not m.symbol_hits.get(s)]
                if missing:
                    note = "fehlend: " + ", ".join(f"`{s}`" for s in missing[:3])
                    if len(missing) > 3:
                        note += f" (+{len(missing) - 3})"
            out.append(
                f"| {m.status} | `{py_name}` | {m.py_loc} | {go_pendant} | "
                f"{m.go_loc or '—'} | {ratio} | {sym_cell} | {note} |"
            )
        out.append("")

    # ----- Fehlend / Skeleton-Top-Liste (für schnelle Triage) -----
    out.append("## Top-Befunde — automatisch sortiert")
    out.append("")
    missing = sorted(
        (m for m in matches if m.status == "❌"),
        key=lambda m: -m.py.lines,
    )
    if missing:
        out.append("### ❌ Vollständig fehlend (Top 20 nach py-LOC)")
        out.append("")
        out.append("| Datei | py-LOC | Top-Symbole |")
        out.append("|---|---:|---|")
        for m in missing[:20]:
            syms = ", ".join(f"`{s}`" for s in m.py.symbols[:3])
            if len(m.py.symbols) > 3:
                syms += f" (+{len(m.py.symbols) - 3})"
            out.append(
                f"| `{m.py.repo}/{m.py.rel_path}` | {m.py.lines} | {syms} |"
            )
        out.append("")

    skeletons = sorted(
        (m for m in matches if m.status == "⚠️ (skeleton)"),
        key=lambda m: (m.loc_ratio or 0.0),
    )
    if skeletons:
        out.append("### ⚠️ Skelette (Top 20, kleinstes Datei-Verhältnis zuerst)")
        out.append("")
        out.append("| Datei | py-LOC | go-Datei-LOC | Datei-% | go-Dir-LOC | Dir-% | Symbole |")
        out.append("|---|---:|---:|---:|---:|---:|---:|")
        for m in skeletons[:20]:
            dir_pct = (
                f"{m.dir_loc_ratio:.0%}" if m.dir_loc_ratio is not None else "—"
            )
            out.append(
                f"| `{m.py.repo}/{m.py.rel_path}` | {m.py.lines} | "
                f"{m.go_loc} | {m.loc_ratio:.0%} | {m.go_dir_loc} | {dir_pct} | "
                f"{m.symbols_found}/{m.symbols_total} |"
            )
        out.append("")

    # ----- Architektur-Divergenzen — kein Code-Pendant erwartet -----
    arch_divs = sorted(
        (m for m in matches if m.status == "🟢 (architecture)"),
        key=lambda m: (m.py.repo, m.py.rel_path),
    )
    if arch_divs:
        out.append("## Architektur-Divergenzen — kein Code-Pendant erwartet")
        out.append("")
        out.append(
            "Diese Dateien implementieren Python-spezifische Konzepte "
            "(Protocols, Mixins, Exception-Hierarchien, Per-Plattform-Klassen), "
            "die in Go durch andere Sprachmittel abgedeckt werden. "
            "Sie zählen **nicht** als ❌ in der Übersicht."
        )
        out.append("")
        out.append("| Datei | py-LOC | Grund |")
        out.append("|---|---:|---|")
        for m in arch_divs:
            out.append(
                f"| `{m.py.repo}/{m.py.rel_path}` | {m.py.lines} | {m.arch_divergence} |"
            )
        out.append("")

    return "\n".join(out) + "\n"


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n", 1)[0])
    parser.add_argument(
        "--out",
        type=Path,
        default=OPENCCU_LOOM_ROOT / "parity_crosswalk.md",
        help="Output Markdown path (default: ./parity_crosswalk.md).",
    )
    args = parser.parse_args(argv)

    # 1. Collect Python sources.
    py_files: list[PyFile] = []
    for repo, root in PY_REPOS.items():
        if not root.exists():
            print(f"warning: {root} does not exist; skipping", file=sys.stderr)
            continue
        for path in collect_py_files(root):
            rel = str(path.relative_to(root))
            py_files.append(
                PyFile(
                    repo=repo,
                    rel_path=rel,
                    abs_path=path,
                    lines=count_source_lines(path, "py"),
                    symbols=py_top_level_symbols(path),
                )
            )

    # 2. Collect Go sources.
    go_paths = collect_go_files(GO_ROOTS)
    go_files = [
        GoFile(
            rel_path=str(p.relative_to(OPENCCU_LOOM_ROOT)),
            abs_path=p,
            lines=count_source_lines(p, "go"),
        )
        for p in go_paths
    ]
    go_index = build_go_text_index(go_paths)

    # 3. Build matches.
    matches = [build_match(py, go_index, go_files) for py in py_files]

    # 4. Render.
    md = render_markdown(matches)
    args.out.write_text(md, encoding="utf-8")
    print(f"Wrote {args.out} ({len(matches)} Python files mapped)")

    n_ok = sum(1 for m in matches if m.status == "✅")
    n_warn = sum(1 for m in matches if m.status.startswith("⚠️"))
    n_miss = sum(1 for m in matches if m.status == "❌")
    n_arch = sum(1 for m in matches if m.status == "🟢 (architecture)")
    n_skip = sum(1 for m in matches if m.status == "➖")
    print(f"  ✅ {n_ok}   ⚠️ {n_warn}   ❌ {n_miss}   🟢 {n_arch}   ➖ {n_skip}")
    return 0


# ---------------------------------------------------------------------------
# L2.4 — Konstanten-Diff const.py ↔ Go-Inline
#
# Extrahiert konstanten-ähnliche Werte aus aiohomematic/const.py und
# vergleicht sie mit Konstanten in internal/ (Go). Ausgabe: Tabelle
# „nur in Python" / „nur in Go" / „beidseitig".
# ---------------------------------------------------------------------------

_CONST_PY_RE = re.compile(r"^_?([A-Z][A-Z0-9_]{2,})\s*[:=]", re.MULTILINE)
_GO_CONST_RE = re.compile(r"\b([A-Z][A-Za-z0-9]{2,})\b")


def _load_py_constants(path: Path) -> set[str]:
    """Extrahiert CONSTANT_LIKE-Namen aus einer Python-Datei per Regex."""
    try:
        text = path.read_text(encoding="utf-8", errors="replace")
    except OSError:
        return set()
    return {m.group(1) for m in _CONST_PY_RE.finditer(text)}


def _load_go_constants(internal_root: Path) -> set[str]:
    """Extrahiert potenzielle Konstantennamen aus Go-Dateien unter internal/.

    Sucht nach `const`-Blöcken und exportierten Bezeichnern (Großbuchstaben)
    in .go-Dateien. Keine AST-Analyse — reine Heuristik via Regex.
    """
    names: set[str] = set()
    if not internal_root.exists():
        return names
    # Grob: alle Zeilen, die in einem const-Block stehen könnten.
    const_block_re = re.compile(
        r"(?:^|\s)const\s*\(([^)]*)\)|(?:^|\s)const\s+([A-Z]\w+)", re.MULTILINE
    )
    for go_file in internal_root.rglob("*.go"):
        if go_file.name.endswith("_test.go"):
            continue
        try:
            text = go_file.read_text(encoding="utf-8", errors="replace")
        except OSError:
            continue
        for m in const_block_re.finditer(text):
            block = m.group(1) or m.group(2) or ""
            for nm in _GO_CONST_RE.findall(block):
                if len(nm) >= 3:
                    names.add(nm)
    return names


def constants_diff(
    py_const_path: Path,
    go_internal_root: Path,
) -> dict[str, set[str]]:
    """Vergleicht Python-Konstanten aus const.py mit Go-Konstanten.

    Gibt dict mit drei Keys zurück:
      - "only_python": Konstanten nur in Python
      - "only_go": Konstanten nur in Go
      - "both": in beiden vorhanden
    """
    py_names = _load_py_constants(py_const_path)
    go_names = _load_go_constants(go_internal_root)

    only_py = py_names - go_names
    only_go = go_names - py_names
    both = py_names & go_names

    return {"only_python": only_py, "only_go": only_go, "both": both}


def print_constants_diff_table(
    py_const_path: Path,
    go_internal_root: Path,
) -> None:
    """Gibt die Konstanten-Diff-Tabelle auf stdout aus."""
    if not py_const_path.exists():
        print(f"WARNUNG: {py_const_path} nicht gefunden — diff übersprungen", file=sys.stderr)
        return

    diff = constants_diff(py_const_path, go_internal_root)

    print("\n## Konstanten-Diff: const.py ↔ Go-Inline\n")
    print(f"Quelle Python: {py_const_path}")
    print(f"Quelle Go:     {go_internal_root}\n")

    col_w = 40

    def _section(title: str, names: set[str]) -> None:
        sorted_names = sorted(names)
        print(f"### {title} ({len(sorted_names)} Einträge)\n")
        print(f"{'Name':<{col_w}}")
        print("-" * col_w)
        for n in sorted_names:
            print(f"{n:<{col_w}}")
        print()

    _section("Nur in Python (const.py)", diff["only_python"])
    _section("Nur in Go (internal/)", diff["only_go"])
    _section("Beidseitig vorhanden", diff["both"])

    print(
        f"Zusammenfassung: {len(diff['only_python'])} nur-Python | "
        f"{len(diff['only_go'])} nur-Go | "
        f"{len(diff['both'])} beidseitig"
    )


if __name__ == "__main__":
    raise SystemExit(main())
