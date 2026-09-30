// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
//
// MASTER multi-apply: nothing is pre-ticked, the write stays locked until a
// dry run over exactly the current selection, the write goes only to the
// targets the dry run cleared, and every outcome — a refusal with its
// reason, a read-back divergence — is on screen.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, fireEvent } from "@testing-library/svelte";

const { mockTargets, mockApply, mockToast } = vi.hoisted(() => ({
  mockTargets: vi.fn(),
  mockApply: vi.fn(),
  mockToast: { success: vi.fn(), warn: vi.fn(), error: vi.fn() },
}));

vi.mock("$lib/api/client", () => ({
  api: {
    getParamsetApplyTargets: (...a: unknown[]) => mockTargets(...a),
    applyParamsetToChannels: (...a: unknown[]) => mockApply(...a),
  },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : String(err)),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key} ${JSON.stringify(vars)}` : key,
}));

vi.mock("$lib/stores/toast.svelte", () => ({ toastStore: mockToast }));

import ApplyToChannelsDialog from "./ApplyToChannelsDialog.svelte";

const targets = [
  { address: "AAA:1", name: "Kitchen 1", device_name: "Kitchen", device_model: "HM-LC-Sw1-FM" },
  { address: "BBB:1", name: "Hall 1", device_name: "Hall", device_model: "HM-LC-Sw1-FM" },
];

function button(prefix: string): HTMLButtonElement {
  const b = Array.from(document.querySelectorAll("button")).find((el) =>
    el.textContent?.trim().startsWith(prefix),
  );
  if (!b) throw new Error(`no button ${prefix}`);
  return b as HTMLButtonElement;
}

function checkboxes(): HTMLInputElement[] {
  return Array.from(document.querySelectorAll('input[type="checkbox"]'));
}

async function renderOpen() {
  render(ApplyToChannelsDialog, {
    props: {
      open: true,
      channelAddress: "SRC:1",
      values: { ON_TIME: 5 },
      editToken: "tok",
      onClose: vi.fn(),
    },
  });
  await waitFor(() => expect(checkboxes().length).toBe(2));
}

beforeEach(() => {
  vi.clearAllMocks();
  mockTargets.mockResolvedValue(targets);
});

afterEach(() => cleanup());

describe("ApplyToChannelsDialog", () => {
  it("lists the targets with none pre-ticked and the write locked", async () => {
    await renderOpen();
    expect(mockTargets).toHaveBeenCalledWith("SRC:1");
    expect(checkboxes().every((c) => !c.checked)).toBe(true);
    expect(button("channel.apply.check").disabled).toBe(true);
    expect(button("channel.apply.apply").disabled).toBe(true);
    expect(document.body.textContent).toContain("Kitchen · HM-LC-Sw1-FM");
  });

  it("renders dry-run outcomes including the refusal reason", async () => {
    mockApply.mockResolvedValueOnce([
      { address: "AAA:1", status: "would_apply" },
      { address: "BBB:1", status: "refused", reason: "description differs" },
    ]);
    await renderOpen();
    await fireEvent.click(checkboxes()[0]);
    await fireEvent.click(checkboxes()[1]);
    await fireEvent.click(button("channel.apply.check"));

    await waitFor(() => expect(document.body.textContent).toContain("description differs"));
    expect(mockApply).toHaveBeenCalledWith("SRC:1", { ON_TIME: 5 }, ["AAA:1", "BBB:1"], true, "tok");
    expect(document.body.textContent).toContain("channel.apply.status.refused");
    expect(document.body.textContent).toContain("channel.apply.status.would_apply");
    expect(button("channel.apply.apply").disabled).toBe(false);
  });

  it("writes only the cleared targets and shows read-back divergences", async () => {
    mockApply
      .mockResolvedValueOnce([
        { address: "AAA:1", status: "would_apply" },
        { address: "BBB:1", status: "refused", reason: "description differs" },
      ])
      .mockResolvedValueOnce([
        {
          address: "AAA:1",
          status: "applied",
          result: {
            written: ["ON_TIME"],
            readback_divergences: [{ parameter: "ON_TIME", sent: 5, stored: 3 }],
          },
        },
      ]);
    await renderOpen();
    await fireEvent.click(checkboxes()[0]);
    await fireEvent.click(checkboxes()[1]);
    await fireEvent.click(button("channel.apply.check"));
    await waitFor(() => expect(button("channel.apply.apply").disabled).toBe(false));
    await fireEvent.click(button("channel.apply.apply"));

    await waitFor(() => expect(mockApply).toHaveBeenCalledTimes(2));
    expect(mockApply.mock.calls[1]).toEqual(["SRC:1", { ON_TIME: 5 }, ["AAA:1"], false, "tok"]);
    await waitFor(() =>
      expect(document.body.textContent).toContain(
        'channel.apply.divergence {"parameter":"ON_TIME","sent":"5","stored":"3"}',
      ),
    );
    // The dry run's refusal stays next to the write outcome.
    expect(document.body.textContent).toContain("description differs");
    expect(document.body.textContent).toContain("channel.apply.status.applied");
    expect(mockToast.warn).toHaveBeenCalled();
  });

  it("re-locks the write when the selection changes after the dry run", async () => {
    mockApply.mockResolvedValueOnce([{ address: "AAA:1", status: "would_apply" }]);
    await renderOpen();
    await fireEvent.click(checkboxes()[0]);
    await fireEvent.click(button("channel.apply.check"));
    await waitFor(() => expect(button("channel.apply.apply").disabled).toBe(false));
    await fireEvent.click(checkboxes()[1]);
    await waitFor(() => expect(button("channel.apply.apply").disabled).toBe(true));
  });
});
