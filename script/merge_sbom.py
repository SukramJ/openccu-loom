#!/usr/bin/env python3
"""Merge the daemon's Go SBOM with the Config UI's npm SBOM.

Inputs (CycloneDX JSON):
  1. the Go module graph (cyclonedx-gomod) — its metadata (the daemon as
     the root component, tool list, timestamp) is kept as the merged
     document's metadata;
  2. the npm tree of assets/ui (`npm sbom --sbom-format cyclonedx`).

The merged document is the Go document with the npm components (and
dependencies, when present) appended, de-duplicated by bom-ref. It is
written gzipped to the path `internal/sbom` embeds, and plain to the
release artifact path. Stdlib only — no third-party imports — so it runs
wherever the release workflow's Python does.

Usage: merge_sbom.py <go.json> <npm.json> <out.json.gz> [<out.json>]
"""

from __future__ import annotations

import gzip
import json
import sys


def load(path: str) -> dict:
    with open(path, encoding="utf-8") as fh:
        doc = json.load(fh)
    if doc.get("bomFormat") != "CycloneDX":
        raise SystemExit(f"{path}: not a CycloneDX document (bomFormat={doc.get('bomFormat')!r})")
    return doc


def ref_of(component: dict) -> str:
    return component.get("bom-ref") or component.get("purl") or (
        component.get("name", "") + "@" + component.get("version", "")
    )


def main() -> None:
    if len(sys.argv) not in (4, 5):
        raise SystemExit(__doc__)
    go_doc, npm_doc = load(sys.argv[1]), load(sys.argv[2])

    merged = go_doc
    go_count = len(merged.get("components", []))
    seen = {ref_of(c) for c in merged.get("components", [])}
    for comp in npm_doc.get("components", []):
        if ref_of(comp) not in seen:
            merged.setdefault("components", []).append(comp)
            seen.add(ref_of(comp))

    dep_refs = {d.get("ref") for d in merged.get("dependencies", [])}
    for dep in npm_doc.get("dependencies", []):
        if dep.get("ref") not in dep_refs:
            merged.setdefault("dependencies", []).append(dep)
            dep_refs.add(dep.get("ref"))

    payload = json.dumps(merged, indent=1).encode()
    # mtime=0 keeps the gzip output reproducible across runs.
    with open(sys.argv[3], "wb") as fh:
        fh.write(gzip.compress(payload, mtime=0))
    if len(sys.argv) == 5:
        with open(sys.argv[4], "wb") as fh:
            fh.write(payload)
    total = len(merged.get("components", []))
    print(f"merged SBOM: {total} components ({go_count} go + {total - go_count} npm) -> {sys.argv[3]}")


if __name__ == "__main__":
    main()
