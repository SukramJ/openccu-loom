// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.
//
// @vitest-environment happy-dom
//
// The readout age stamp is operator-facing text, so it has to follow the
// UI language like every other string — it used to be a German literal
// that an English operator saw verbatim.
import { describe, it, expect, afterEach } from "vitest";
import { prefs } from "$lib/stores/preferences.svelte";
import { formatValueAge } from "./age";

const original = prefs.locale;
afterEach(() => {
  prefs.locale = original;
});

describe("formatValueAge", () => {
  it("is empty for an unknown age", () => {
    expect(formatValueAge(undefined)).toBe("");
    expect(formatValueAge(null)).toBe("");
    expect(formatValueAge(Number.NaN)).toBe("");
  });

  it("renders every unit in English", () => {
    prefs.locale = "en";
    expect(formatValueAge(3)).toBe("3 s ago");
    expect(formatValueAge(180)).toBe("3 min ago");
    expect(formatValueAge(7200)).toBe("2 h ago");
    expect(formatValueAge(172800)).toBe("2 d ago");
  });

  it("renders every unit in German", () => {
    prefs.locale = "de";
    expect(formatValueAge(3)).toBe("vor 3 s");
    expect(formatValueAge(180)).toBe("vor 3 min");
    expect(formatValueAge(7200)).toBe("vor 2 h");
    expect(formatValueAge(172800)).toBe("vor 2 d");
  });
});
