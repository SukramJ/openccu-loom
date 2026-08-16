// @vitest-environment happy-dom
//
// GET /config/sections/{section} is admin-gated while the schema behind the
// field list is not. A viewer or operator therefore has to get the section as
// a read-only field list — a red load error would make every Settings tab
// look broken for everyone but the admin. The write actions are dropped in
// that state: they would be rejected, and Save would push default values over
// values the identity cannot even see.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor } from "@testing-library/svelte";
import type { ConfigSchemaField } from "$lib/api/client";

const mockGetConfigSection = vi.fn();

vi.mock("$lib/api/client", () => ({
  api: {
    getConfigSection: (...args: unknown[]) => mockGetConfigSection(...args),
    putConfigSection: vi.fn(),
    deleteConfigSection: vi.fn(),
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

vi.mock("$lib/i18n", () => ({
  t: (key: string, params?: Record<string, string>) =>
    params ? `${key}:${JSON.stringify(params)}` : key,
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
import { ApiError } from "$lib/api/client";

const FIELDS: ConfigSchemaField[] = [
  { path: "north.mqtt.enabled", class: "basic", go_type: "bool" },
  { path: "north.mqtt.broker_url", class: "basic", go_type: "string" },
];

function renderSection() {
  return render(SectionEditor, {
    props: {
      section: "north.mqtt",
      schemaFields: FIELDS,
      sources: {},
      allSections: ["north.mqtt"],
    },
  });
}

function buttonLabels(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll("button")).map(
    (b) => b.textContent?.trim() ?? "",
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  mockGetConfigSection.mockResolvedValue({});
});

afterEach(() => cleanup());

describe("SectionEditor — admin-gated section values", () => {
  it("renders the fields read-only with a note when the read is forbidden", async () => {
    mockGetConfigSection.mockRejectedValue(
      new ApiError(403, null, "forbidden"),
    );

    const { container } = renderSection();

    await waitFor(() =>
      expect(container.textContent).toContain("settings.values_admin_only"),
    );
    // The field list is what makes the view useful without the values.
    expect(container.textContent).toContain("Broker URL");
    // No red load error, and no write actions that would be rejected anyway.
    expect(container.textContent).not.toContain("common.error");
    expect(buttonLabels(container)).not.toContain("common.save");
    expect(buttonLabels(container)).not.toContain("settings.reset");
  });

  it("keeps the defaults note and the actions for an unset section (404)", async () => {
    mockGetConfigSection.mockRejectedValue(
      new ApiError(404, null, "not found"),
    );

    const { container } = renderSection();

    await waitFor(() =>
      expect(container.textContent).toContain("settings.section_unset"),
    );
    expect(container.textContent).not.toContain("settings.values_admin_only");
    expect(buttonLabels(container)).toContain("common.save");
  });

  it("still shows a load error for a failure that is not 403/404", async () => {
    mockGetConfigSection.mockRejectedValue(
      new ApiError(500, null, "store down"),
    );

    const { container } = renderSection();

    await waitFor(() => expect(container.textContent).toContain("store down"));
    expect(container.textContent).toContain("common.error");
    expect(container.textContent).not.toContain("settings.values_admin_only");
  });
});
