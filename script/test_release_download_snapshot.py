# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 SukramJ.
"""Unit tests for release_download_snapshot.py (stdlib unittest, no fixtures on disk).

Run: python3 script/test_release_download_snapshot.py
"""

from __future__ import annotations

import json
import sys
import tempfile
import unittest
from pathlib import Path

# script/ is not a package (its entries are standalone tools), so the module
# under test is imported from this file's own directory.
sys.path.insert(0, str(Path(__file__).resolve().parent))

import release_download_snapshot as snap  # noqa: E402

TS = "2026-10-11T03:41:00Z"


def _release(tag: str, assets: dict[str, int], prerelease: bool = False) -> dict:
    return {
        "tag_name": tag,
        "published_at": "2026-10-09T10:00:00Z",
        "prerelease": prerelease,
        "assets": [{"name": n, "download_count": c} for n, c in assets.items()],
    }


class ParseConcatenatedJSON(unittest.TestCase):
    def test_two_pages_without_separator_are_both_read(self) -> None:
        text = json.dumps([_release("v0.1.0", {"a": 1})]) + json.dumps([_release("v0.2.0", {"a": 2})])
        self.assertEqual([r["tag_name"] for r in snap.load_releases(text)], ["v0.1.0", "v0.2.0"])

    def test_single_array_is_accepted(self) -> None:
        text = json.dumps([_release("v0.1.0", {"a": 1})])
        self.assertEqual(len(snap.load_releases(text)), 1)

    def test_non_array_page_is_rejected(self) -> None:
        with self.assertRaises(ValueError):
            snap.load_releases(json.dumps({"message": "Not Found"}))


class ReleaseRows(unittest.TestCase):
    def test_total_is_the_sum_over_assets_and_assets_are_sorted(self) -> None:
        rows = snap.release_rows([_release("v0.9.0", {"z.tar.gz": 3, "a.tar.gz": 5})], TS)
        self.assertEqual(rows[0]["total"], 8)
        self.assertEqual(list(rows[0]["assets"]), ["a.tar.gz", "z.tar.gz"])
        self.assertEqual(rows[0]["ts"], TS)
        self.assertEqual(rows[0]["kind"], "github_release")

    def test_releases_are_sorted_by_tag(self) -> None:
        rows = snap.release_rows([_release("v0.2.0", {}), _release("v0.1.0", {})], TS)
        self.assertEqual([r["tag"] for r in rows], ["v0.1.0", "v0.2.0"])


class AddonRows(unittest.TestCase):
    EXPORT = {
        "143edcd5_openccu-loom": {"total": 2, "versions": {"0.89.0": 1, "0.88.0": 1}, "protected": 2, "auto_update": 1},
        "01790305_meshloom": {"total": 2, "versions": {"4.17.0": 2}, "protected": 2, "auto_update": 1},
    }

    def test_matches_on_the_slug_after_the_repository_hash(self) -> None:
        rows = snap.addon_rows(self.EXPORT, ["openccu-loom"], TS)
        self.assertEqual(rows[0]["slug"], "143edcd5_openccu-loom")
        self.assertTrue(rows[0]["present"])
        self.assertEqual(rows[0]["total"], 2)
        self.assertEqual(list(rows[0]["versions"]), ["0.88.0", "0.89.0"])

    def test_a_substring_match_is_not_a_match(self) -> None:
        # "meshloom" contains "loom" but is a different add-on.
        rows = snap.addon_rows(self.EXPORT, ["loom"], TS)
        self.assertFalse(rows[0]["present"])

    def test_absent_addon_is_a_zero_sample_not_a_gap(self) -> None:
        rows = snap.addon_rows(self.EXPORT, ["openccu-loom-remote"], TS)
        self.assertEqual(rows[0], {
            "ts": TS, "kind": "ha_addon", "addon": "openccu-loom-remote", "slug": None,
            "present": False, "total": 0, "versions": {}, "auto_update": 0, "protected": 0,
        })

    def test_ambiguous_slug_is_an_error(self) -> None:
        export = dict(self.EXPORT, **{"deadbeef_openccu-loom": {"total": 1, "versions": {}}})
        with self.assertRaises(ValueError):
            snap.addon_rows(export, ["openccu-loom"], TS)


class MainAppends(unittest.TestCase):
    def test_second_run_appends_instead_of_overwriting(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            d = Path(tmp)
            (d / "releases.json").write_text(json.dumps([_release("v0.1.0", {"a": 1})]))
            (d / "addons.json").write_text(json.dumps(AddonRows.EXPORT))
            out = d / "downloads.ndjson"
            argv = ["--releases", str(d / "releases.json"), "--addons", str(d / "addons.json"),
                    "--addon-slug", "openccu-loom", "--out", str(out), "--now", TS]
            self.assertEqual(snap.main(argv), 0)
            self.assertEqual(snap.main(argv + []), 0)
            lines = out.read_text().splitlines()
            self.assertEqual(len(lines), 4)
            kinds = [json.loads(l)["kind"] for l in lines]
            self.assertEqual(kinds, ["github_release", "ha_addon", "github_release", "ha_addon"])

    def test_default_timestamp_is_utc_with_z_suffix(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            d = Path(tmp)
            (d / "releases.json").write_text(json.dumps([_release("v0.1.0", {"a": 1})]))
            out = d / "o.ndjson"
            self.assertEqual(snap.main(["--releases", str(d / "releases.json"), "--out", str(out)]), 0)
            ts = json.loads(out.read_text())["ts"]
            self.assertRegex(ts, r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$")

    def test_addon_slug_without_addons_file_is_a_usage_error(self) -> None:
        with tempfile.TemporaryDirectory() as tmp, self.assertRaises(SystemExit):
            snap.main(["--releases", f"{tmp}/r.json", "--addon-slug", "x", "--out", f"{tmp}/o"])


if __name__ == "__main__":
    unittest.main()
