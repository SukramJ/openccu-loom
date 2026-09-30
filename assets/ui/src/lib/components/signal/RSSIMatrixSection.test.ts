// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
//
// BidCos matrix section: invisible without matrix data (HmIP-only system,
// non-admin viewer), one table per central otherwise, and a central whose
// read failed shows the shared error state in place of its table.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor } from "@testing-library/svelte";

const { mockMatrix } = vi.hoisted(() => ({ mockMatrix: vi.fn() }));

vi.mock("$lib/api/client", () => ({
  api: {
    rssiMatrix: (...a: unknown[]) => mockMatrix(...a),
    receiverProposal: vi.fn().mockResolvedValue([]),
    assignRFInterface: vi.fn(),
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
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : String(err)),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import RSSIMatrixSection from "./RSSIMatrixSection.svelte";
import { ApiError } from "$lib/api/client";

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => cleanup());

describe("RSSIMatrixSection", () => {
  it("renders nothing when no central returns matrix data", async () => {
    mockMatrix.mockResolvedValue([{ central: "hmip", interface_id: "hmip-BidCos-RF", devices: [] }]);
    render(RSSIMatrixSection);
    await waitFor(() => expect(mockMatrix).toHaveBeenCalled());
    await Promise.resolve();
    expect(document.querySelector('[data-testid="rssi-matrix"]')).toBeNull();
  });

  it("stays hidden for a viewer the endpoint refuses", async () => {
    mockMatrix.mockRejectedValue(new (ApiError as unknown as new (s: number, b: unknown, m: string) => Error)(403, null, "forbidden"));
    render(RSSIMatrixSection);
    await waitFor(() => expect(mockMatrix).toHaveBeenCalled());
    await Promise.resolve();
    expect(document.querySelector('[data-testid="rssi-matrix"]')).toBeNull();
  });

  it("renders partner rows and a failed central's error inline", async () => {
    mockMatrix.mockResolvedValue([
      {
        central: "ccu-a",
        interface_id: "ccu-a-BidCos-RF",
        interfaces: [{ address: "IF-A", description: "Main LAN gateway" }],
        devices: [
          {
            address: "DEV1",
            name: "Blind",
            partners: [
              { address: "IF-A", rx_dbm: -65, tx_dbm: -70 },
              { address: "DEV2", rx_dbm: null, tx_dbm: -85 },
            ],
          },
        ],
      },
      { central: "ccu-b", interface_id: "ccu-b-BidCos-RF", error: "rssiInfo timed out", devices: [] },
    ]);
    render(RSSIMatrixSection);
    await waitFor(() => expect(document.querySelector('[data-testid="rssi-matrix"]')).toBeTruthy());
    const text = document.body.textContent ?? "";
    expect(text).toContain("Main LAN gateway");
    expect(text).toContain("signal.matrix.gateway");
    expect(text).toContain("-65 dBm");
    expect(text).toContain("-85 dBm");
    expect(text).toContain("rssiInfo timed out");
    expect(text).toContain("signal.proposal.open");
  });
});
