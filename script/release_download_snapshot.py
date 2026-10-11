# SPDX-License-Identifier: MIT
# Copyright (C) 2026 SukramJ.
"""
release_download_snapshot.py — append one day's download counters to a
newline-delimited JSON log.

GitHub keeps only the running total per release asset, and the Home Assistant
analytics export shows only the current opt-in install count per add-on
version. Neither carries history, so a time series exists only if someone
samples both at a fixed cadence. The download-stats workflow does that daily
and commits the rows to the `stats` branch; this script turns the two raw
responses into rows.

Usage:
    python3 script/release_download_snapshot.py \
        --releases releases.json --addons addons.json \
        --addon-slug openccu-loom --addon-slug openccu-loom-remote \
        --out downloads.ndjson

`--releases` is the output of `gh api --paginate repos/<owner>/<repo>/releases`,
which is one JSON array per page concatenated without a separator; a single
array is accepted too. `--addons` is https://analytics.home-assistant.io/addons.json.

Row shapes (one line each):
    {"ts", "kind": "github_release", "tag", "published_at", "prerelease",
     "total", "assets": {asset name: download count}}
    {"ts", "kind": "ha_addon", "addon", "slug", "present", "total",
     "versions": {version: installs}, "auto_update", "protected"}

The HA export keys add-ons as `<repository hash>_<slug>`; the hash is derived
from the add-on repository URL and is not carried by this repo, so add-ons are
matched on the part after the underscore, which is the slug from
`packaging/ha-addon/<addon>/config.yaml`. An add-on with no entry in the
export is written with `present: false` and zero installs — analytics only
lists add-ons with at least one opted-in install, so absence is a count of
zero, not a missing sample.
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import sys
from pathlib import Path
from typing import Any


def parse_concatenated_json(text: str) -> list[Any]:
    """Decode a stream of JSON values laid end to end and return them in order.

    `gh api --paginate` writes each page as its own top-level value with no
    separator, so a plain json.loads fails after the first page. Values are
    read with the raw decoder until the text is exhausted.
    """
    decoder = json.JSONDecoder()
    values: list[Any] = []
    pos = 0
    length = len(text)
    while pos < length:
        while pos < length and text[pos].isspace():
            pos += 1
        if pos >= length:
            break
        value, pos = decoder.raw_decode(text, pos)
        values.append(value)
    return values


def load_releases(text: str) -> list[dict[str, Any]]:
    releases: list[dict[str, Any]] = []
    for value in parse_concatenated_json(text):
        if not isinstance(value, list):
            raise ValueError("releases: expected a JSON array per page")
        releases.extend(value)
    return releases


def release_rows(releases: list[dict[str, Any]], ts: str) -> list[dict[str, Any]]:
    rows = []
    for release in sorted(releases, key=lambda r: r["tag_name"]):
        assets = {a["name"]: int(a["download_count"]) for a in release.get("assets", [])}
        rows.append(
            {
                "ts": ts,
                "kind": "github_release",
                "tag": release["tag_name"],
                "published_at": release.get("published_at"),
                "prerelease": bool(release.get("prerelease", False)),
                "total": sum(assets.values()),
                "assets": dict(sorted(assets.items())),
            }
        )
    return rows


def addon_rows(addons: dict[str, Any], slugs: list[str], ts: str) -> list[dict[str, Any]]:
    rows = []
    for slug in slugs:
        matches = [(key, entry) for key, entry in addons.items() if key.split("_", 1)[-1] == slug]
        if len(matches) > 1:
            keys = ", ".join(sorted(k for k, _ in matches))
            raise ValueError(f"addon slug {slug!r} matches more than one export key: {keys}")
        if matches:
            key, entry = matches[0]
            rows.append(
                {
                    "ts": ts,
                    "kind": "ha_addon",
                    "addon": slug,
                    "slug": key,
                    "present": True,
                    "total": int(entry.get("total", 0)),
                    "versions": dict(sorted(entry.get("versions", {}).items())),
                    "auto_update": int(entry.get("auto_update", 0)),
                    "protected": int(entry.get("protected", 0)),
                }
            )
        else:
            rows.append(
                {
                    "ts": ts,
                    "kind": "ha_addon",
                    "addon": slug,
                    "slug": None,
                    "present": False,
                    "total": 0,
                    "versions": {},
                    "auto_update": 0,
                    "protected": 0,
                }
            )
    return rows


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n", 1)[0])
    parser.add_argument("--releases", required=True, type=Path, help="gh api --paginate output")
    parser.add_argument("--addons", type=Path, help="HA analytics addons.json (optional)")
    parser.add_argument("--addon-slug", action="append", default=[], help="add-on slug to record; repeatable")
    parser.add_argument("--out", required=True, type=Path, help="NDJSON file to append to")
    parser.add_argument("--now", help="sample timestamp (ISO 8601 UTC); defaults to the current time")
    args = parser.parse_args(argv)

    if args.addon_slug and args.addons is None:
        parser.error("--addon-slug needs --addons")

    ts = args.now or dt.datetime.now(dt.timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")

    rows = release_rows(load_releases(args.releases.read_text(encoding="utf-8")), ts)
    if args.addons is not None:
        addons = json.loads(args.addons.read_text(encoding="utf-8"))
        if not isinstance(addons, dict):
            raise SystemExit("addons: expected a JSON object keyed by add-on")
        rows.extend(addon_rows(addons, args.addon_slug, ts))

    args.out.parent.mkdir(parents=True, exist_ok=True)
    with args.out.open("a", encoding="utf-8") as fh:
        for row in rows:
            fh.write(json.dumps(row, sort_keys=True, ensure_ascii=False) + "\n")

    releases = sum(1 for r in rows if r["kind"] == "github_release")
    print(f"appended {len(rows)} rows ({releases} releases, {len(rows) - releases} add-ons) at {ts}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
