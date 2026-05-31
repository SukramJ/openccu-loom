#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
# Copyright (C) 2026 OpenCCU-Loom authors.
"""Pull a model snapshot from a running openccu-loom daemon via the REST surface.

Closes asks.md C4 (Drei-Wege-Diff) at the daemon-vs-py-client wiring
level. The full three-way diff (aiohomematic <-> loom-go <-> loom-py-client)
extension to `script/model_snapshot_diff.py` requires the full
`py-openccu-loom-client` (transport + caching + auth flows) to exist;
this script is the minimal pre-cursor: it shows what the py-client
snapshot WILL look like, and validates the wire payload against the
generated `openccu_loom_types` Pydantic models when available.

Usage:

    python3 script/openccu_loom_client_snapshot.py \\
        --base-url http://127.0.0.1:8080 \\
        --token "$OPENCCU_LOOM_TOKEN" \\
        --out tests/integration/testdata/model_snapshot_py_client.json

When `openccu_loom_types` is importable (`pip install openccu-loom-types`
or local editable from ../openccu-loom-types-py), the script also runs
the snapshot through the Pydantic models to surface any wire-contract
drift before the snapshot lands on disk.

The output JSON shape currently mirrors `GET /snapshot` directly. When
`py-openccu-loom-client` ships, the script should be re-pointed at the
client's higher-level dumper so the three-way diff input matches
aiohomematic / loom-go on the exact field-by-field schema documented in
`docs/parity/model_snapshot_schema.md`.
"""

from __future__ import annotations

import argparse
import json
import sys
import urllib.error
import urllib.request
from pathlib import Path


def fetch_snapshot(base_url: str, token: str | None, timeout: int = 30) -> dict:
    """GET /api/v1/snapshot and return the parsed JSON envelope."""
    url = base_url.rstrip("/") + "/api/v1/snapshot"
    req = urllib.request.Request(url, headers={"Accept": "application/json"})
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:  # noqa: S310
            if resp.status != 200:
                raise SystemExit(f"snapshot returned HTTP {resp.status}")
            return json.loads(resp.read())
    except urllib.error.HTTPError as e:
        body = e.read().decode("utf-8", errors="replace")
        raise SystemExit(f"snapshot HTTP {e.code}: {body}") from e


def try_pydantic_validate(envelope: dict) -> str | None:
    """Validate envelope against openccu_loom_types if installed.

    Returns None on success / not-installed, or an error string on
    validation failure. Treats missing import as a soft skip — the
    sidecar package is optional for the snapshot dump.
    """
    try:
        from openccu_loom_types import rest  # type: ignore
    except ImportError:
        return None
    schema_name = getattr(rest, "SnapshotEnvelope", None)
    if schema_name is None:
        return None
    try:
        schema_name.model_validate(envelope)  # pydantic v2
    except Exception as exc:  # pragma: no cover - validation surface
        return f"pydantic validation failed: {exc}"
    return None


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--base-url", default="http://127.0.0.1:8080",
                   help="Daemon REST base URL (default: http://127.0.0.1:8080).")
    p.add_argument("--token", default=None,
                   help="Bearer token (or set OPENCCU_LOOM_TOKEN env var).")
    p.add_argument("--out", required=True, type=Path,
                   help="Output JSON path.")
    p.add_argument("--timeout", type=int, default=30)
    args = p.parse_args()

    token = args.token
    if not token:
        import os
        token = os.environ.get("OPENCCU_LOOM_TOKEN")

    envelope = fetch_snapshot(args.base_url, token, args.timeout)

    err = try_pydantic_validate(envelope)
    if err:
        print(f"warning: {err}", file=sys.stderr)

    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps(envelope, indent=2, sort_keys=True))
    print(f"wrote snapshot ({len(envelope.get('devices', []))} devices) to {args.out}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
