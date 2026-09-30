// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
//
// Best-receiver proposal: only `switch` rows are applicable and they start
// ticked; applying sends one interface assignment per ticked row (best
// interface, roaming off) and reports success or failure per row.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, fireEvent } from "@testing-library/svelte";

const { mockProposal, mockAssign, mockToast } = vi.hoisted(() => ({
  mockProposal: vi.fn(),
  mockAssign: vi.fn(),
  mockToast: { success: vi.fn(), warn: vi.fn(), error: vi.fn() },
}));

vi.mock("$lib/api/client", () => ({
  api: {
    receiverProposal: (...a: unknown[]) => mockProposal(...a),
    assignRFInterface: (...a: unknown[]) => mockAssign(...a),
  },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : String(err)),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key} ${JSON.stringify(vars)}` : key,
}));

vi.mock("$lib/stores/toast.svelte", () => ({ toastStore: mockToast }));

import ReceiverProposalDialog from "./ReceiverProposalDialog.svelte";

const proposals = [
  { address: "SW1", name: "Switch one", central: "ccu", current_interface: "IF-A", best_interface: "IF-B", current_rx_dbm: -90, best_rx_dbm: -60, verdict: "switch" },
  { address: "SW2", name: "Switch two", central: "ccu", current_interface: "IF-A", best_interface: "IF-C", current_rx_dbm: -88, best_rx_dbm: -70, verdict: "switch" },
  { address: "KP1", name: "Keeper", central: "ccu", current_interface: "IF-A", best_interface: "IF-A", current_rx_dbm: -50, best_rx_dbm: -50, verdict: "keep" },
  { address: "MG1", name: "Marginal", central: "ccu", current_interface: "IF-A", best_interface: "IF-B", current_rx_dbm: -70, best_rx_dbm: -67, verdict: "marginal" },
  { address: "RO1", name: "Roamer", central: "ccu", verdict: "roaming", roaming: true },
];

function ticks(): HTMLInputElement[] {
  return Array.from(document.querySelectorAll('[data-testid="proposal-tick"]'));
}

function applyButton(): HTMLButtonElement {
  const b = Array.from(document.querySelectorAll("button")).find((el) =>
    el.textContent?.trim().startsWith("signal.proposal.apply"),
  );
  if (!b) throw new Error("no apply button");
  return b as HTMLButtonElement;
}

beforeEach(() => {
  vi.clearAllMocks();
  mockProposal.mockResolvedValue(proposals);
});

afterEach(() => cleanup());

async function renderOpen() {
  render(ReceiverProposalDialog, { props: { open: true, onClose: vi.fn() } });
  await waitFor(() => expect(mockProposal).toHaveBeenCalledWith(6));
  await waitFor(() => expect(ticks().length).toBeGreaterThan(0));
}

describe("ReceiverProposalDialog", () => {
  it("offers a checkbox only on switch rows, pre-ticked", async () => {
    await renderOpen();
    // DataTable renders each row once; only the two switch rows carry a tick.
    const boxes = ticks();
    const labels = boxes.map((b) => b.getAttribute("aria-label"));
    expect(new Set(labels)).toEqual(
      new Set([
        'signal.proposal.select {"name":"Switch one"}',
        'signal.proposal.select {"name":"Switch two"}',
      ]),
    );
    expect(boxes.every((b) => b.checked)).toBe(true);
    expect(document.body.textContent).toContain("signal.proposal.verdict.keep");
    expect(document.body.textContent).toContain("signal.proposal.verdict.roaming");
  });

  it("assigns the best interface per ticked row and reports each row", async () => {
    mockAssign.mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error("422 not BidCos"));
    await renderOpen();
    // Untick nothing: both switch rows go out.
    await fireEvent.click(applyButton());

    await waitFor(() => expect(mockAssign).toHaveBeenCalledTimes(2));
    const calls = mockAssign.mock.calls.map((c) => c.join("|")).sort();
    expect(calls).toEqual(["SW1|IF-B|false", "SW2|IF-C|false"]);
    await waitFor(() => {
      const results = Array.from(document.querySelectorAll('[data-testid="proposal-result"]')).map(
        (el) => el.textContent?.trim(),
      );
      expect(results).toContain("signal.proposal.assigned");
      expect(results).toContain("422 not BidCos");
    });
    expect(mockToast.warn).toHaveBeenCalled();
  });

  it("skips a row the operator unticked", async () => {
    mockAssign.mockResolvedValue(undefined);
    await renderOpen();
    const sw2 = ticks().find((b) => b.getAttribute("aria-label")?.includes("Switch two"))!;
    await fireEvent.click(sw2);
    await fireEvent.click(applyButton());
    await waitFor(() => expect(mockAssign).toHaveBeenCalledTimes(1));
    expect(mockAssign).toHaveBeenCalledWith("SW1", "IF-B", false);
    expect(mockToast.success).toHaveBeenCalled();
  });
});
