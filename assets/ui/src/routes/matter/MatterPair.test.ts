// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
//
// The pairing card is the one Matter surface an operator uses under
// time pressure: a commissioning window is open, a countdown is
// running, and the code has to reach another device before it closes.
// The tests below cover the two ways that goes wrong silently — a card
// that does not say whether this is the first controller or an
// additional one, and a copy button that reports success on a browser
// that refused the clipboard.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, screen, waitFor } from "@testing-library/svelte";

const {
  mockStatus,
  mockSetupPayload,
  mockToastSuccess,
  mockToastError,
  mockWriteText,
} = vi.hoisted(() => ({
  mockStatus: vi.fn(),
  mockSetupPayload: vi.fn(),
  mockToastSuccess: vi.fn(),
  mockToastError: vi.fn(),
  mockWriteText: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  api: {
    matterStatus: () => mockStatus(),
    matterSetupPayload: () => mockSetupPayload(),
    matterFabrics: () => Promise.resolve({ fabrics: [] }),
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
  setUnauthorizedHandler: vi.fn(),
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: mockToastSuccess, error: mockToastError },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}:${JSON.stringify(vars)}` : key,
}));

import { matterStore } from "$lib/stores/matter.svelte";
import MatterPair from "./MatterPair.svelte";

// The Matter store is a module singleton, and the card reads it: a test
// that leaves the card in the "open" phase makes the next test's hydrate
// return early, and a status fetched once is never fetched again. Both
// are reset here rather than re-importing the module graph — a second
// copy of the Svelte runtime orphans every effect.
async function renderPair() {
  matterStore.resetCommissioning();
  await matterStore.loadStatus();
  return render(MatterPair);
}

const WINDOW_OPEN = {
  enabled: true,
  listening: true,
  endpoint_count: 4,
  fabric_count: 1,
  enabled_count: 4,
  advertising: true,
  commissioning_window_open: true,
  commissioning_window_duration_seconds: 300,
};

const SETUP_PAYLOAD = {
  qr_code: "MT:Y.K90SO527JA0648G00",
  manual_code: "34970112332",
  discriminator: 3840,
  passcode: 20202021,
};

beforeEach(() => {
  vi.clearAllMocks();
  mockSetupPayload.mockResolvedValue(SETUP_PAYLOAD);
  mockWriteText.mockResolvedValue(undefined);
  Object.defineProperty(globalThis.navigator, "clipboard", {
    value: { writeText: mockWriteText },
    configurable: true,
  });
});

afterEach(cleanup);

describe("MatterPair — first pairing vs. adding a controller", () => {
  // An operator opening a window on a bridge that three controllers
  // already hold is doing something different from first-time pairing,
  // and the daemon knows which it is. Saying so is the difference
  // between "did my setup not stick?" and "I am adding a fourth".
  it("frames the action as first-time pairing while no controller holds the bridge", async () => {
    mockStatus.mockResolvedValue({ ...WINDOW_OPEN, fabric_count: 0, commissioning_window_open: false });

    await renderPair();

    expect(await screen.findByText("matter.pair.window_open")).toBeTruthy();
    expect(screen.queryByText(/matter\.pair\.already_paired/)).toBeNull();
  });

  it("says how many controllers already hold the bridge and offers to add one", async () => {
    mockStatus.mockResolvedValue({ ...WINDOW_OPEN, fabric_count: 3, commissioning_window_open: false });

    await renderPair();

    expect(await screen.findByText('matter.pair.already_paired:{"count":3}')).toBeTruthy();
    expect(screen.getByText("matter.pair.add_controller")).toBeTruthy();
  });
});

describe("MatterPair — copying the codes", () => {
  it("copies the manual code, which is otherwise transcribed by hand", async () => {
    mockStatus.mockResolvedValue(WINDOW_OPEN);

    await renderPair();

    (await screen.findByLabelText("matter.pair.copy_manual_code")).click();

    await waitFor(() => expect(mockWriteText).toHaveBeenCalledWith(SETUP_PAYLOAD.manual_code));
    // The toast lands a microtask after the write resolves, so it is
    // polled too: asserting it straight after the write tests the
    // scheduler, not the component.
    await waitFor(() => expect(mockToastSuccess).toHaveBeenCalledWith("matter.pair.copied"));
  });

  it("copies the QR payload, which is the fallback when the code cannot be scanned", async () => {
    mockStatus.mockResolvedValue(WINDOW_OPEN);

    await renderPair();

    (await screen.findByLabelText("matter.pair.copy_qr_payload")).click();

    await waitFor(() => expect(mockWriteText).toHaveBeenCalledWith(SETUP_PAYLOAD.qr_code));
  });

  // The clipboard API is unavailable on a page served over plain HTTP,
  // which is exactly how the Config UI is reached behind Home
  // Assistant's ingress and on a bare LAN address. A copy button that
  // reports success there sends the operator to another device with an
  // empty clipboard and a window that closes in five minutes.
  it("reports a refused clipboard instead of claiming the code was copied", async () => {
    mockStatus.mockResolvedValue(WINDOW_OPEN);
    mockWriteText.mockRejectedValue(new Error("NotAllowedError"));

    await renderPair();

    (await screen.findByLabelText("matter.pair.copy_manual_code")).click();

    await waitFor(() => expect(mockToastError).toHaveBeenCalledWith("matter.pair.copy_failed"));
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });
});
