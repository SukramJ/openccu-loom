// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.
//
// @vitest-environment happy-dom
//
// The status words feed the tooltip and the aria-label. Built once at
// init they froze at the locale that was active when the view mounted,
// so a language switch left half the strip in the old language.
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, screen, waitFor } from "@testing-library/svelte";
import { tick } from "svelte";
import { prefs } from "$lib/stores/preferences.svelte";

vi.mock("$lib/api/client", () => ({
  api: {
    health: () => Promise.resolve({ status: "ok", components: [] }),
  },
}));

import ConnectivityLights from "./ConnectivityLights.svelte";

const original = prefs.locale;
afterEach(() => {
  prefs.locale = original;
  cleanup();
});

describe("ConnectivityLights — locale switch", () => {
  it("re-renders the status words when the UI language changes", async () => {
    prefs.locale = "de";
    render(ConnectivityLights);

    await waitFor(() => expect(screen.getByLabelText("CCU: deaktiviert")).toBeTruthy());

    prefs.locale = "en";
    await tick();

    expect(screen.getByLabelText("CCU: disabled")).toBeTruthy();
    expect(screen.queryByLabelText("CCU: deaktiviert")).toBeNull();
  });
});
