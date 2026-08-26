# SPDX-License-Identifier: MIT
# Copyright (C) 2026 SukramJ.
"""
test_migration_inventory.py — cross-stack test-migration tracking tool.

Scans Python tests across three sibling repos (aiohomematic, aiohomematic-config,
homematicip_local), classifies each file into the L14/L15 parity-audit clusters,
attempts heuristic matching against openccu-loom Go tests, and emits a CSV to
stdout.  A per-cluster summary is printed to stderr so it does not pollute the
CSV pipe.

Usage:
    python3 script/test_migration_inventory.py | head -20
    python3 script/test_migration_inventory.py > inventory.csv

All paths are resolved relative to the openccu-loom repo root which is inferred
as the directory two levels above this script file.
"""

import csv
import os
import re
import sys
from pathlib import Path

# ---------------------------------------------------------------------------
# Cluster definitions
# Each key is the cluster label; the value is a regex applied to the *stem*
# of the Python test filename (i.e. the filename without directory and without
# the .py extension).  A file may match multiple clusters.
# ---------------------------------------------------------------------------
CLUSTER_PATTERNS: dict[str, str] = {
    "L14-connection-recovery": (
        r"test_central_state_machine"
        r"|test_command_retry"
        r"|test_circuit_breaker"
        r"|test_central_config_conn_state"
        r"|test_connection"
        r"|test_recovery"
        r"|test_reconnect"
    ),
    "L14-event-bus": (
        r"test_central_event_bus"
        r"|test_central_event_coordinator"
        r"|test_central_event_types"
        r"|test_event"
    ),
    "L14-json-rpc": (
        r"test_client_json_rpc"
        r"|test_json_rpc"
        r"|test_client_rpc_errors"
    ),
    "L15-model": (
        # test_device but NOT test_device_action or test_device_trigger
        r"test_data_point"
        r"|test_device(?!_action|_trigger)"
        r"|test_channel"
        r"|test_param"
        r"|test_calculated"
        r"|test_custom"
    ),
    "L15-coordinators": (
        r"test_central_(cache|client|configuration|device|hub|link)_coordinator"
        r"|test_central_background_scheduler"
        r"|test_configuration_coordinator"
    ),
    "L15-reliability": (
        r"test_command_throttle"
        r"|test_circuit_breaker"
        r"|test_retry"
        r"|test_throttle"
    ),
    "L15-schedule": (
        r"test_schedule"
        r"|test_week_profile"
    ),
    "L15-hub": (
        r"test_hub"
        r"|test_program"
        r"|test_sysvar"
        r"|test_install_mode"
        r"|test_alarm"
        r"|test_service_message"
    ),
}

# ---------------------------------------------------------------------------
# Repository configuration
# ---------------------------------------------------------------------------
# __file__ is  …/openccu-loom/script/test_migration_inventory.py
SCRIPT_DIR = Path(__file__).resolve().parent
REPO_ROOT = SCRIPT_DIR.parent  # openccu-loom repo root

PYTHON_REPOS: list[tuple[str, Path]] = [
    ("aio",        REPO_ROOT.parent / "aiohomematic" / "tests"),
    ("aio-config", REPO_ROOT.parent / "aiohomematic-config" / "tests"),
    ("hm_local",   REPO_ROOT.parent / "homematicip_local" / "tests"),
]

GO_SEARCH_ROOTS: list[Path] = [
    REPO_ROOT / "tests",
    REPO_ROOT / "internal",
]

# Minimum token-match score to include a candidate in the CSV.
MATCH_THRESHOLD = 0.30
# Minimum score to count as "matched" in the summary.
MATCH_SUMMARY_THRESHOLD = 0.50
# Number of top candidates to report per Python file.
TOP_N = 3


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def count_loc(path: Path) -> int:
    """Non-blank, non-pure-comment lines."""
    count = 0
    try:
        with path.open(encoding="utf-8", errors="replace") as fh:
            for line in fh:
                stripped = line.strip()
                if stripped and not stripped.startswith("#"):
                    count += 1
    except OSError:
        pass
    return count


def classify_clusters(stem: str) -> list[str]:
    """Return list of cluster keys that match the file stem."""
    matches = []
    for cluster, pattern in CLUSTER_PATTERNS.items():
        if re.search(pattern, stem):
            matches.append(cluster)
    if not matches:
        matches = ["other"]
    return matches


def stem_to_tokens(stem: str) -> list[str]:
    """
    Derive a token list from a Python test file stem.
    Drops the leading 'test_' prefix, then splits on '_'.
    Single-character tokens are dropped (too noisy).
    """
    s = stem
    if s.startswith("test_"):
        s = s[len("test_"):]
    tokens = [t for t in s.split("_") if len(t) > 1]
    return tokens


def go_stem(path: Path) -> str:
    """Return the Go test file stem without the _test.go suffix."""
    name = path.name  # e.g. "circuit_breaker_test.go"
    if name.endswith("_test.go"):
        return name[: -len("_test.go")]
    return name


def score_match(py_tokens: list[str], go_path: Path) -> float:
    """
    Score how well a Go test file matches the Python token list.
    score = matched_tokens / total_tokens, where a token is 'matched'
    if it appears as a substring of the Go file stem.
    """
    if not py_tokens:
        return 0.0
    g_stem = go_stem(go_path)
    matched = sum(1 for t in py_tokens if t in g_stem)
    return round(matched / len(py_tokens), 2)


def find_go_candidates(
    py_tokens: list[str],
    go_files: list[Path],
) -> list[tuple[Path, float]]:
    """
    Return up to TOP_N Go files with score >= MATCH_THRESHOLD,
    sorted descending by score.
    """
    scored = []
    for gf in go_files:
        s = score_match(py_tokens, gf)
        if s >= MATCH_THRESHOLD:
            scored.append((gf, s))
    scored.sort(key=lambda x: x[1], reverse=True)
    return scored[:TOP_N]


def format_candidates(candidates: list[tuple[Path, float]]) -> tuple[str, float]:
    """
    Format candidates as 'path:score|path:score|…'.
    Returns the formatted string and the top score (0.0 if empty).
    """
    if not candidates:
        return "—", 0.0  # em-dash
    parts = []
    for gf, s in candidates:
        # Make the path relative to the repo root for readability.
        try:
            rel = gf.relative_to(REPO_ROOT)
        except ValueError:
            rel = gf
        parts.append(f"{rel}:{s:.2f}")
    top_score = candidates[0][1]
    return "|".join(parts), top_score


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main() -> None:
    # ------------------------------------------------------------------
    # Collect all Go test files once.
    # ------------------------------------------------------------------
    go_files: list[Path] = []
    for root in GO_SEARCH_ROOTS:
        if not root.exists():
            print(f"WARNING: Go search root not found: {root}", file=sys.stderr)
            continue
        for p in root.rglob("*_test.go"):
            go_files.append(p)
    go_files.sort()

    # ------------------------------------------------------------------
    # CSV writer to stdout.
    # ------------------------------------------------------------------
    writer = csv.writer(sys.stdout, lineterminator="\n")
    writer.writerow(["cluster", "python_repo", "python_file", "python_loc",
                     "go_candidates", "top_score", "notes"])

    # ------------------------------------------------------------------
    # Per-cluster accumulators for the summary.
    # key → {files, loc, matched_count, unmatched_count}
    # A file that maps to multiple clusters is counted in each.
    # ------------------------------------------------------------------
    summary: dict[str, dict] = {}
    for cluster in list(CLUSTER_PATTERNS.keys()) + ["other"]:
        summary[cluster] = {"files": 0, "loc": 0, "matched": 0, "unmatched": 0}

    total_files = 0
    total_loc = 0

    # ------------------------------------------------------------------
    # Scan each Python repo.
    # ------------------------------------------------------------------
    for repo_tag, test_root in PYTHON_REPOS:
        if not test_root.exists():
            print(
                f"WARNING: Python test root not found, skipping: {test_root}",
                file=sys.stderr,
            )
            continue

        py_files = sorted(test_root.rglob("test_*.py"))

        for py_path in py_files:
            stem = py_path.stem  # e.g. "test_circuit_breaker"
            loc = count_loc(py_path)
            clusters = classify_clusters(stem)
            py_tokens = stem_to_tokens(stem)
            candidates = find_go_candidates(py_tokens, go_files)
            go_col, top_score = format_candidates(candidates)

            # Relative path from the test root for the CSV.
            try:
                rel_py = py_path.relative_to(test_root.parent)
            except ValueError:
                rel_py = py_path

            is_matched = top_score >= MATCH_SUMMARY_THRESHOLD

            # Write one CSV row per cluster the file belongs to.
            for cluster in clusters:
                writer.writerow([
                    cluster,
                    repo_tag,
                    str(rel_py),
                    loc,
                    go_col,
                    f"{top_score:.2f}",
                    "",
                ])
                s = summary[cluster]
                s["files"] += 1
                s["loc"] += loc
                if is_matched:
                    s["matched"] += 1
                else:
                    s["unmatched"] += 1

            total_files += 1
            total_loc += loc

    # ------------------------------------------------------------------
    # Summary to stderr.
    # ------------------------------------------------------------------
    sep = "-" * 72
    print(sep, file=sys.stderr)
    print("TEST MIGRATION INVENTORY — CLUSTER SUMMARY", file=sys.stderr)
    print(sep, file=sys.stderr)
    print(
        f"{'Cluster':<32} {'Files':>6} {'LoC':>7} {'Matched(≥0.5)':>14} {'Unmatched':>10}",
        file=sys.stderr,
    )
    print(sep, file=sys.stderr)

    residual_loc = 0
    for cluster, data in summary.items():
        if data["files"] == 0:
            continue
        print(
            f"{cluster:<32} {data['files']:>6} {data['loc']:>7}"
            f" {data['matched']:>14} {data['unmatched']:>10}",
            file=sys.stderr,
        )
        residual_loc += data["loc"] - (
            # Approximate: assume matched files carry proportional LoC.
            # We track unmatched count but not unmatched LoC directly here.
            # Use a per-file average.
            (data["matched"] / data["files"] * data["loc"]) if data["files"] else 0
        )

    print(sep, file=sys.stderr)
    print(f"Total Python files scanned : {total_files}", file=sys.stderr)
    print(f"Total Python LoC           : {total_loc}", file=sys.stderr)
    print(sep, file=sys.stderr)
    print(
        "Note: a file matching multiple clusters is counted once per cluster row.",
        file=sys.stderr,
    )
    print(
        "      'Matched' = at least one Go counterpart with token-score >= 0.50.",
        file=sys.stderr,
    )
    print(sep, file=sys.stderr)

    # ------------------------------------------------------------------
    # Recompute residual accurately (unmatched LoC across all clusters).
    # Because a file can appear in >1 cluster we need to avoid double-counting;
    # collect unmatched unique Python paths instead.
    # ------------------------------------------------------------------
    # We need a second pass for accurate residual LoC.
    # Re-scan briefly (no CSV output this time) to collect unmatched file LoC.
    unmatched_loc_total = 0
    unmatched_files_total = 0
    for _repo_tag, test_root in PYTHON_REPOS:
        if not test_root.exists():
            continue
        for py_path in sorted(test_root.rglob("test_*.py")):
            stem = py_path.stem
            loc = count_loc(py_path)
            py_tokens = stem_to_tokens(stem)
            candidates = find_go_candidates(py_tokens, go_files)
            _, top_score = format_candidates(candidates)
            if top_score < MATCH_SUMMARY_THRESHOLD:
                unmatched_loc_total += loc
                unmatched_files_total += 1

    print(
        f"Unmatched files (score < 0.50): {unmatched_files_total} files,"
        f" {unmatched_loc_total} LoC  ← residual migration work",
        file=sys.stderr,
    )
    print(sep, file=sys.stderr)


if __name__ == "__main__":
    main()
