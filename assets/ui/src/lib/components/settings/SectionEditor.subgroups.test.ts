// @vitest-environment happy-dom
//
// subgroupLabel() falls back to humanize() when the catalogue has no
// `config.subgroup.*` row. The fallback returns the same English string in
// both locales and does not know the acronym, so a missing row shows up as an
// untranslated, mis-capitalised heading ("Ssdp") next to localized siblings.
// These cases run against the REAL catalogue — a mocked t() that echoes its
// argument would pass with no rows at all.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor } from "@testing-library/svelte";
import type { ConfigSchemaField } from "$lib/api/client";
import { prefs } from "$lib/stores/preferences.svelte";

const mockGetConfigSection = vi.fn();

vi.mock("$lib/api/client", () => ({
  api: {
    getConfigSection: (...args: unknown[]) => mockGetConfigSection(...args),
    putConfigSection: vi.fn(),
  },
  ApiError: class ApiError extends Error {
    constructor(
      public readonly status: number,
      public readonly body: unknown,
      message: string,
    ) {
      super(message);
    }
  },
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { error: vi.fn(), success: vi.fn() },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: vi.fn().mockResolvedValue(false) },
}));

vi.mock("$lib/stores/restartPending.svelte", () => ({
  restartPending: { pending: false, fields: [] },
  restartCaps: { supervised: false, loaded: false },
  refreshRestartPending: vi.fn().mockResolvedValue(undefined),
  loadRestartCaps: vi.fn().mockResolvedValue(undefined),
}));

import SectionEditor from "./SectionEditor.svelte";

function field(path: string, cls: ConfigSchemaField["class"]): ConfigSchemaField {
  return { path, class: cls, go_type: "bool" };
}

function headings(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll("h3")).map((h) => h.textContent?.trim() ?? "");
}

async function renderSection(section: string, fields: ConfigSchemaField[]) {
  const { container } = render(SectionEditor, {
    props: { section, schemaFields: fields, sources: {}, allSections: [section] },
  });
  await waitFor(() => expect(headings(container).length).toBeGreaterThan(0));
  return container;
}

const originalLocale = prefs.locale;
const originalExpert = prefs.expertMode;

beforeEach(() => {
  vi.clearAllMocks();
  mockGetConfigSection.mockResolvedValue({});
  // values_cache is an expert-class bucket; without expert mode it never renders.
  prefs.expertMode = true;
});

afterEach(() => {
  cleanup();
  prefs.locale = originalLocale;
  prefs.expertMode = originalExpert;
});

describe("SectionEditor — subgroup headings", () => {
  it("labels the SSDP bucket from the catalogue in both locales", async () => {
    for (const locale of ["en", "de"] as const) {
      prefs.locale = locale;
      const container = await renderSection("north.discovery", [
        field("north.discovery.enabled", "basic"),
        field("north.discovery.ssdp.enabled", "basic"),
      ]);
      expect(headings(container)).toContain("SSDP");
      expect(headings(container)).not.toContain("Ssdp");
      cleanup();
    }
  });

  it("labels the VALUES-cache bucket from the catalogue in both locales", async () => {
    const expected = { en: "VALUES Cache", de: "VALUES-Cache" };
    for (const locale of ["en", "de"] as const) {
      prefs.locale = locale;
      const container = await renderSection("persistence", [
        field("persistence.values_cache.enabled", "expert"),
        field("persistence.history.enabled", "expert"),
      ]);
      expect(headings(container)).toContain(expected[locale]);
      expect(headings(container)).not.toContain("Values Cache");
      cleanup();
    }
  });
});
