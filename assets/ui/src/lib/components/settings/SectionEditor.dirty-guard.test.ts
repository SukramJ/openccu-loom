// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
//
// SectionEditor tracked its own `isDirty` derived value but never told
// the global `dirty` store about it — so App's route guard and
// beforeunload handler both saw `dirty.any() === false` no matter how
// many unsaved config fields the operator had edited, and navigating
// away (or switching a settings tab) discarded the edit with no
// confirm and no toast.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor } from "@testing-library/svelte";

const mockGetConfigSection = vi.fn();
const mockPutConfigSection = vi.fn();

vi.mock("$lib/api/client", () => ({
  api: {
    getConfigSection: (...args: unknown[]) => mockGetConfigSection(...args),
    putConfigSection: (...args: unknown[]) => mockPutConfigSection(...args),
    getRestartPending: vi.fn().mockResolvedValue({ pending: false, fields: [] }),
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, _body: unknown, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { error: vi.fn(), success: vi.fn() },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: vi.fn().mockResolvedValue(false) },
}));

vi.mock("$lib/stores/preferences.svelte", () => ({
  prefs: { expertMode: false },
  applyTheme: vi.fn(),
  setLocale: vi.fn(),
  setTheme: vi.fn(),
  setNavCollapsed: vi.fn(),
  setExpertMode: vi.fn(),
  setDeviceView: vi.fn(),
  bindSystemTheme: vi.fn(() => () => {}),
}));

vi.mock("$lib/stores/restartPending.svelte", () => ({
  restartPending: { pending: false, fields: [] },
  restartCaps: { supervised: false, loaded: false },
  refreshRestartPending: vi.fn().mockResolvedValue(undefined),
  loadRestartCaps: vi.fn().mockResolvedValue(undefined),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

// Deliberately NOT mocking $lib/stores/dirty.svelte — this test needs the
// real global registry to prove SectionEditor actually participates in it.
import { dirty } from "$lib/stores/dirty.svelte";
import type { ConfigSchemaField } from "$lib/api/client";
import SectionEditor from "./SectionEditor.svelte";

const PUBLIC_URL_FIELD: ConfigSchemaField = {
  path: "north.rest.public_url",
  class: "basic",
  go_type: "string",
};

function renderEditor() {
  return render(SectionEditor, {
    props: {
      section: "north.rest",
      schemaFields: [PUBLIC_URL_FIELD],
      sources: {} as Record<string, "bootstrap" | "db" | "env" | "default">,
      allSections: ["north.rest"],
    },
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  mockGetConfigSection.mockResolvedValue({ public_url: "https://old.example.com" });
  mockPutConfigSection.mockResolvedValue({
    section: "north.rest",
    version: 1,
    updated_at: "",
    restart_required: false,
  });
});

afterEach(() => {
  cleanup();
  dirty.discardAll();
});

describe("SectionEditor — global dirty registration", () => {
  it("reports dirty to the global store once a field is edited, and clears on unmount", async () => {
    const { container, unmount } = renderEditor();
    await waitFor(() => {
      expect(container.querySelectorAll('input[type="text"]').length).toBeGreaterThan(0);
    });
    expect(dirty.any()).toBe(false);

    const input = container.querySelector('input[type="text"]') as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "https://new.example.com" } });

    await waitFor(() => expect(dirty.any()).toBe(true));

    unmount();
    expect(dirty.any()).toBe(false);
  });

  it("clears the global dirty flag again once the field is saved", async () => {
    const { container } = renderEditor();
    await waitFor(() => {
      expect(container.querySelectorAll('input[type="text"]').length).toBeGreaterThan(0);
    });

    const input = container.querySelector('input[type="text"]') as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "https://new.example.com" } });
    await waitFor(() => expect(dirty.any()).toBe(true));

    const saveBtn = (
      Array.from(container.querySelectorAll("button")) as HTMLButtonElement[]
    ).find((b) => b.textContent?.trim().includes("common.save"))!;
    await fireEvent.click(saveBtn);

    await waitFor(() => expect(mockPutConfigSection).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(dirty.any()).toBe(false));
  });

  it("rolling the global store back (discardAll) reverts the working copy", async () => {
    const { container } = renderEditor();
    await waitFor(() => {
      expect(container.querySelectorAll('input[type="text"]').length).toBeGreaterThan(0);
    });

    const input = container.querySelector('input[type="text"]') as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "https://new.example.com" } });
    await waitFor(() => expect(dirty.any()).toBe(true));
    expect(input.value).toBe("https://new.example.com");

    // This is what App's route guard / beforeunload confirm calls once
    // the operator confirms they want to leave and lose the edits.
    dirty.discardAll();
    await waitFor(() => expect(input.value).toBe("https://old.example.com"));
    expect(dirty.any()).toBe(false);
  });
});
