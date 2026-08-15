// @vitest-environment happy-dom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, fireEvent } from "@testing-library/svelte";

vi.mock("$lib/api/client", () => ({
  api: { setValue: vi.fn() },
  friendlyError: (err: unknown) => String(err),
}));

import NumericActionFeature from "./NumericActionFeature.svelte";
import { prefs } from "$lib/stores/preferences.svelte";
import type { DataPointSummary } from "$lib/api/types";

const dp: DataPointSummary = {
  unique_id: "ABC123:1.ON_TIME",
  parameter: "ON_TIME",
  observed: false,
  operations: { read: false, write: true, event: false },
};

const originalLocale = prefs.locale;

afterEach(() => {
  prefs.locale = originalLocale;
  cleanup();
});

/**
 * The submit button carries both a visible caption and an aria-label. A
 * literal caption next to a translated aria-label reads as an untranslated
 * control to a German operator and makes the accessible name disagree with
 * the visible text (WCAG 2.5.3), so the two are pinned to the same string.
 */
describe("NumericActionFeature — submit button caption", () => {
  it("renders the send caption in the active locale", async () => {
    prefs.locale = "de";
    const { getByRole } = render(NumericActionFeature, {
      props: { address: "ABC123", channel: 1, dp, label: "Einschaltdauer" },
    });

    await fireEvent.click(getByRole("button", { name: "Einschaltdauer" }));

    const send = getByRole("button", { name: "Senden" });
    expect(send.textContent?.trim()).toBe("Senden");
  });

  it("keeps caption and accessible name identical in English", async () => {
    prefs.locale = "en";
    const { getByRole } = render(NumericActionFeature, {
      props: { address: "ABC123", channel: 1, dp, label: "On time" },
    });

    await fireEvent.click(getByRole("button", { name: "On time" }));

    const send = getByRole("button", { name: "Send" });
    expect(send.textContent?.trim()).toBe(send.getAttribute("aria-label"));
  });
});
