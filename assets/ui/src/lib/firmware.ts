// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

/**
 * Compares two dotted-numeric firmware version strings and returns true if
 * `available` is strictly newer than `current`.
 *
 * Rules:
 *  - Both arguments must be non-empty strings after trimming; otherwise false.
 *  - A leading "v" or "V" prefix is stripped before parsing.
 *  - Each segment is split on "."; non-numeric segments parse as 0.
 *  - Segments are zero-padded to the longer of the two version arrays.
 *  - Comparison is left-to-right; the first differing segment decides.
 *  - Pre-release / build-metadata suffixes (e.g. "-rc1", "+build") are
 *    intentionally NOT parsed — only the leading numeric part of each segment
 *    is considered. A version "2.0.10-rc1" therefore compares as "2.0.10".
 *    This is acceptable for Homematic firmware strings which are plain dotted
 *    integers in practice.
 */
export function isUpdateAvailable(
  current: string | null | undefined,
  available: string | null | undefined,
): boolean {
  const cur = (current ?? "").trim().replace(/^[vV]/, "");
  const avl = (available ?? "").trim().replace(/^[vV]/, "");

  if (!cur || !avl) return false;

  const curParts = cur.split(".").map((s) => parseInt(s, 10) || 0);
  const avlParts = avl.split(".").map((s) => parseInt(s, 10) || 0);

  const len = Math.max(curParts.length, avlParts.length);
  for (let i = 0; i < len; i++) {
    const c = i < curParts.length ? curParts[i] : 0;
    const a = i < avlParts.length ? avlParts[i] : 0;
    if (a > c) return true;
    if (a < c) return false;
  }
  return false; // all segments equal
}
