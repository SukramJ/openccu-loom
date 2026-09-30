// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
//
// Configuration repair: opening runs the dry run and shows per channel what
// a rewrite would correct; only the confirm step writes, and only the
// channels the dry run found something to correct on.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, fireEvent } from "@testing-library/svelte";

const { mockRepair, mockToast } = vi.hoisted(() => ({
  mockRepair: vi.fn(),
  mockToast: { success: vi.fn(), warn: vi.fn(), error: vi.fn() },
}));

vi.mock("$lib/api/client", () => ({
  api: { repairDeviceConfig: (...a: unknown[]) => mockRepair(...a) },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : String(err)),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key} ${JSON.stringify(vars)}` : key,
}));

vi.mock("$lib/stores/toast.svelte", () => ({ toastStore: mockToast }));

import ConfigRepairDialog from "./ConfigRepairDialog.svelte";

const dryRunOutcomes = [
  { channel: "DEV:0", status: "clean" },
  {
    channel: "DEV:1",
    status: "would_repair",
    corrections: [
      { parameter: "ON_TIME", stored: 999, corrected: 111, reason: "above maximum" },
    ],
  },
  { channel: "DEV:2", status: "foreign_parameters", foreign: ["LEGACY_X"] },
];

function confirmButton(): HTMLButtonElement {
  const b = Array.from(document.querySelectorAll("button")).find((el) =>
    el.textContent?.trim().startsWith("device.repair_config.confirm"),
  );
  if (!b) throw new Error("no confirm button");
  return b as HTMLButtonElement;
}

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => cleanup());

describe("ConfigRepairDialog", () => {
  it("runs the dry run on open and renders the corrections table", async () => {
    mockRepair.mockResolvedValueOnce(dryRunOutcomes);
    render(ConfigRepairDialog, {
      props: { open: true, address: "DEV", name: "Lamp", onClose: vi.fn() },
    });
    await waitFor(() =>
      expect(document.querySelectorAll('[data-testid="repair-outcome"]').length).toBe(3),
    );
    expect(mockRepair).toHaveBeenCalledWith("DEV", true);

    const table = document.querySelector('[data-testid="repair-corrections"]');
    expect(table).toBeTruthy();
    const cells = Array.from(table!.querySelectorAll("tbody td")).map((td) => td.textContent?.trim());
    expect(cells).toEqual(["ON_TIME", "999", "111", "above maximum"]);

    expect(document.body.textContent).toContain('device.repair_config.foreign {"names":"LEGACY_X"}');
    expect(document.body.textContent).toContain("device.repair_config.status.would_repair");
    expect(confirmButton().disabled).toBe(false);
  });

  it("writes only the actionable channels on confirm and shows the final outcomes", async () => {
    mockRepair.mockResolvedValueOnce(dryRunOutcomes).mockResolvedValueOnce([
      { channel: "DEV:1", status: "repaired", result: { written: ["ON_TIME"], readback_divergences: [] } },
      { channel: "DEV:2", status: "failed", error: "CCU refused" },
    ]);
    render(ConfigRepairDialog, {
      props: { open: true, address: "DEV", name: "Lamp", onClose: vi.fn() },
    });
    await waitFor(() => expect(confirmButton().disabled).toBe(false));
    await fireEvent.click(confirmButton());

    await waitFor(() => expect(mockRepair).toHaveBeenCalledTimes(2));
    expect(mockRepair.mock.calls[1]).toEqual(["DEV", false, ["DEV:1", "DEV:2"]]);
    await waitFor(() => expect(document.body.textContent).toContain("CCU refused"));
    expect(document.body.textContent).toContain("device.repair_config.status.repaired");
    // The clean channel stays listed so every channel is accounted for.
    expect(document.querySelectorAll('[data-testid="repair-outcome"]').length).toBe(3);
    expect(mockToast.warn).toHaveBeenCalled();
  });

  it("offers no write when every channel is clean", async () => {
    mockRepair.mockResolvedValueOnce([{ channel: "DEV:1", status: "clean" }]);
    render(ConfigRepairDialog, {
      props: { open: true, address: "DEV", name: "Lamp", onClose: vi.fn() },
    });
    await waitFor(() => expect(document.querySelector('[data-testid="repair-nothing"]')).toBeTruthy());
    expect(confirmButton().disabled).toBe(true);
  });
});
