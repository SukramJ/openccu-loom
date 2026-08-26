#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
# Copyright (C) 2026 SukramJ.
#
# routing_key_parity.py — automatic Go↔Python routing-key parity gate.
#
# The Go contract test (tests/contract/routing_key_contract_test.go)
# pins Go's routingkey.GenerateUniqueID / GenerateChannelUniqueID /
# HubSlug bit-for-bit against the golden fixtures under
# tests/contract/testdata/routing_key/. Those fixtures carry `expected`
# values that were originally copied from aiohomematic BY HAND, so the
# Go test alone cannot catch automatic drift between Go and the Python
# source of truth.
#
# This script closes that gap from the Python side: it imports the
# authoritative aiohomematic functions and replays every fixture case
# through them, asserting Python == fixtures. Combined with the Go test
# (Go == fixtures), the two halves form a real cross-repo guard:
#
#     Go == fixtures  (routing_key_contract_test.go)
#     Python == fixtures  (this script)
#     ⇒ Go == Python
#
# That construction only covers cases the fixtures carry, so a rule that
# exists in one implementation and not the other stays invisible while
# every replay passes. Deliberate divergences therefore get their own
# fixture (cuxd_scoping_golden.json) carrying BOTH answers, and both
# sides assert it: Go pins `expected`, this script pins
# `reference_expected`, and each also fails when the two stop differing.
#
# Source of truth (../aiohomematic/aiohomematic/model/support.py):
#   - generate_unique_id(*, config_provider, address, parameter, prefix)
#   - generate_channel_unique_id(*, config_provider, address)
#   Both read the central id via `config_provider.config.central_id`; we
#   pass a tiny SimpleNamespace stub that exposes exactly that path.
#
#   - the hub-slug rule is python-slugify's `slugify(name)` with default
#     settings (dash separator, Unicode transliteration, lowercased).
#     aiohomematic applies it to hub data-point names before building
#     the unique_id — e.g. aiohomematic/model/hub/inbox.py:60
#     `parameter=slugify(INBOX_SENSOR_NAME)`. Go's routingkey.HubSlug
#     mirrors that same `slugify(name)` (NOT generate_translation_key,
#     which additionally folds "." and "-" to "_"). We therefore compare
#     the hub_slug fixtures against `slugify(name)` directly.
#
# Exit code:
#   0  — every case matches (parity holds)
#   1  — at least one case mismatches (parity broken) OR aiohomematic
#        could not be imported (treated as a hard failure, mirroring
#        script/aiohomematic_snapshot.py's precedent of sys.exit(1) on a
#        missing reference stack). A CI lane without the aiohomematic
#        venv should either provision it (see below) or gate this step
#        behind venv availability.
#
# Usage:
#   python3 script/routing_key_parity.py
#   make routing-key-parity
#
# Run from the repository root (openccu-loom/). It uses whichever
# Python3 is on PATH; if `aiohomematic` (and `slugify`) are not
# importable, the script re-execs itself inside the sibling aiohomematic
# venv when one is found (same discovery order as
# script/aiohomematic_snapshot.py).
#
# CI: the cross-stack-parity workflow
# (.github/workflows/cross-stack-parity.yml) already provisions the
# aiohomematic + pydevccu + openccu-data Python stack on a nightly
# schedule; this script runs there as an extra step. It is intentionally
# NOT on the per-PR lane because the reference venv is not provisioned
# there.

from __future__ import annotations

import importlib.util
import json
import os
import sys
import types
from pathlib import Path


def _ensure_venv() -> None:
    """
    Re-exec inside the sibling aiohomematic venv when `aiohomematic` is
    not importable from the active interpreter. Mirrors the discovery
    order in script/aiohomematic_snapshot.py:

      1. AIOHOMEMATIC_VENV_PYTHON env var (explicit override).
      2. Sibling-repo conventions relative to this script:
         ../../aiohomematic/{venv,.venv}/bin/python3
         ../../../aiohomematic/{venv,.venv}/bin/python3
    """
    try:
        import aiohomematic  # noqa: F401
        import slugify  # noqa: F401

        return
    except ImportError:
        pass

    already_marker = "_AIOHOMEMATIC_VENV_REEXEC_DONE"
    if os.environ.get(already_marker) == "1":
        # We already re-exec'd once and aiohomematic still isn't
        # importable — fall through and let the import error below
        # produce a clear message + exit.
        return

    candidates: list[str] = []
    if env := os.environ.get("AIOHOMEMATIC_VENV_PYTHON", "").strip():
        candidates.append(env)
    here = Path(__file__).resolve().parent
    for offset in ("../..", "../../.."):
        for venv_name in ("venv", ".venv"):
            cand = os.path.normpath(
                str(here / offset / "aiohomematic" / venv_name / "bin" / "python3")
            )
            candidates.append(cand)

    for cand in candidates:
        if os.path.exists(cand):
            os.environ[already_marker] = "1"
            print(
                f"[routing-key-parity] re-execing in aiohomematic venv: {cand}",
                file=sys.stderr,
            )
            os.execv(cand, [cand, *sys.argv])
    # Fall through — let the caller see the ImportError below.


_ensure_venv()

# ──────────────────────────────────────────────────────────────────────────────
# Path bootstrap: make aiohomematic importable when running directly from
# the openccu-loom repo without activating a venv.
# ──────────────────────────────────────────────────────────────────────────────

_GITHUB_ROOT = Path(__file__).resolve().parents[2]


def _bootstrap_sibling_checkout() -> None:
    """
    Make aiohomematic importable when the script runs straight from the
    openccu-loom repo with no environment prepared for it.

    This is a *fallback*, and the ordering matters. Whatever the active
    interpreter can already import wins: appending (never prepending) keeps a
    deliberately provisioned environment — CI's pinned reference stack, or one
    named via AIOHOMEMATIC_VENV_PYTHON — in charge of which aiohomematic
    version the fixtures are compared against. Prepending a sibling working
    copy silently pointed the gate at whatever uncommitted edits sat next to
    the repo, which is the one thing a parity gate must not do.
    """
    if importlib.util.find_spec("aiohomematic") is not None:
        return
    repo = _GITHUB_ROOT / "aiohomematic"
    if repo.is_dir() and str(repo) not in sys.path:
        sys.path.append(str(repo))


_bootstrap_sibling_checkout()

try:
    from aiohomematic.model.support import (
        generate_channel_unique_id,
        generate_unique_id,
    )
    from slugify import slugify
except ImportError as exc:
    print(
        f"ERROR: aiohomematic not available — cannot run routing-key parity: {exc}",
        file=sys.stderr,
    )
    print(
        "Provide the aiohomematic reference stack (pip install aiohomematic "
        "python-slugify) or point AIOHOMEMATIC_VENV_PYTHON at a venv that "
        "has it. CI provisions this via .github/workflows/cross-stack-parity.yml.",
        file=sys.stderr,
    )
    sys.exit(1)


_SCRIPT_DIR = Path(__file__).resolve().parent
_REPO_ROOT = _SCRIPT_DIR.parent
_FIXTURE_DIR = _REPO_ROOT / "tests" / "contract" / "testdata" / "routing_key"


def _config_provider(central_id: str) -> object:
    """
    Build the stub `config_provider` aiohomematic's generate_* helpers
    expect. They read only `config_provider.config.central_id`, so a
    nested SimpleNamespace exposing exactly that path is sufficient.
    """
    return types.SimpleNamespace(config=types.SimpleNamespace(central_id=central_id))


def _load_fixture(name: str, key: str = "cases") -> list[dict]:
    path = _FIXTURE_DIR / name
    with path.open(encoding="utf-8") as fh:
        data = json.load(fh)
    cases = data.get(key, [])
    if not cases:
        print(f"ERROR: fixture {name} carries no {key}", file=sys.stderr)
        sys.exit(1)
    return cases


def _check_unique_id() -> int:
    cases = _load_fixture("unique_id_golden.json")
    mismatches = 0
    for c in cases:
        got = generate_unique_id(
            config_provider=_config_provider(c["central_id"]),
            address=c["address"],
            parameter=c.get("parameter"),
            prefix=c.get("prefix"),
        )
        if got != c["expected"]:
            mismatches += 1
            print(
                "MISMATCH generate_unique_id"
                f"(central_id={c['central_id']!r}, address={c['address']!r}, "
                f"parameter={c.get('parameter')!r}, prefix={c.get('prefix')!r})\n"
                f"  python   = {got!r}\n"
                f"  fixture  = {c['expected']!r}",
                file=sys.stderr,
            )
    print(f"generate_unique_id: {len(cases) - mismatches}/{len(cases)} cases match")
    return mismatches


def _check_channel_unique_id() -> int:
    cases = _load_fixture("channel_unique_id_golden.json")
    mismatches = 0
    for c in cases:
        got = generate_channel_unique_id(
            config_provider=_config_provider(c["central_id"]),
            address=c["address"],
        )
        if got != c["expected"]:
            mismatches += 1
            print(
                "MISMATCH generate_channel_unique_id"
                f"(central_id={c['central_id']!r}, address={c['address']!r})\n"
                f"  python   = {got!r}\n"
                f"  fixture  = {c['expected']!r}",
                file=sys.stderr,
            )
    print(
        f"generate_channel_unique_id: {len(cases) - mismatches}/{len(cases)} cases match"
    )
    return mismatches


def _check_hub_slug() -> int:
    cases = _load_fixture("hub_slug_golden.json")
    mismatches = 0
    for c in cases:
        got = slugify(c["name"])
        if got != c["slug"]:
            mismatches += 1
            print(
                f"MISMATCH slugify(name={c['name']!r})\n"
                f"  python   = {got!r}\n"
                f"  fixture  = {c['slug']!r}",
                file=sys.stderr,
            )
    print(f"hub_slug (slugify): {len(cases) - mismatches}/{len(cases)} cases match")
    return mismatches


def _check_declared_divergences() -> int:
    """
    Replay the cases the Go side deliberately answers differently.

    A divergence that lives only in the Go code is invisible here: the
    shared fixtures carry no case for it, so every replay passes while
    the two implementations disagree, and a client that rebuilds the key
    from the reference emits an id the daemon never publishes. So the
    divergence gets its own fixture and is asserted from both ends —
    Python must still produce `reference_expected`, and that value must
    still differ from the `expected` the Go contract test pins. If either
    side moves (the Go rule is reverted, or the reference adopts it),
    one of the two assertions fails and the fixture, the by_design.md
    entry and the published client contract get updated together.
    """
    mismatches = 0
    cases = _load_fixture("cuxd_scoping_golden.json", key="unique_id_cases")
    for c in cases:
        got = generate_unique_id(
            config_provider=_config_provider(c["central_id"]),
            address=c["address"],
            parameter=c.get("parameter"),
            prefix=c.get("prefix"),
        )
        reference_expected = c.get("reference_expected")
        if reference_expected is None:
            mismatches += 1
            print(
                f"MISSING reference_expected for declared divergence case {c['address']!r}",
                file=sys.stderr,
            )
            continue
        if got != reference_expected:
            mismatches += 1
            print(
                "MISMATCH generate_unique_id (declared divergence)"
                f"(central_id={c['central_id']!r}, address={c['address']!r}, "
                f"parameter={c.get('parameter')!r}, prefix={c.get('prefix')!r})\n"
                f"  python              = {got!r}\n"
                f"  reference_expected  = {reference_expected!r}",
                file=sys.stderr,
            )
        if reference_expected == c["expected"]:
            mismatches += 1
            print(
                f"STALE DIVERGENCE {c['address']!r}: the reference now produces the same key as the Go "
                "side. Retire the fixture entry, the by_design.md rationale and the scoped-class list "
                "in docs/external-clients/ together.",
                file=sys.stderr,
            )

    channel_cases = _load_fixture(
        "cuxd_scoping_golden.json", key="channel_unique_id_cases"
    )
    for c in channel_cases:
        got = generate_channel_unique_id(
            config_provider=_config_provider(c["central_id"]),
            address=c["address"],
        )
        if got != c["expected"]:
            mismatches += 1
            print(
                "MISMATCH generate_channel_unique_id (declared divergence fixture)"
                f"(central_id={c['central_id']!r}, address={c['address']!r})\n"
                f"  python   = {got!r}\n"
                f"  fixture  = {c['expected']!r}",
                file=sys.stderr,
            )

    total = len(cases) + len(channel_cases)
    print(f"declared divergences: {total - mismatches}/{total} cases match")
    return mismatches


def main() -> int:
    total_mismatches = 0
    total_mismatches += _check_unique_id()
    total_mismatches += _check_channel_unique_id()
    total_mismatches += _check_hub_slug()
    total_mismatches += _check_declared_divergences()

    if total_mismatches:
        print(
            f"\nROUTING-KEY PARITY BROKEN: {total_mismatches} case(s) diverge "
            "between aiohomematic and the Go-pinned golden fixtures.",
            file=sys.stderr,
        )
        return 1
    print(
        "\nrouting-key parity: OK (Python == fixtures; Go == fixtures ⇒ Go == Python, "
        "except the declared divergences, which match on both sides)"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
