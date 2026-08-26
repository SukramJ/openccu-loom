// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
//
// The two maintenance actions sit next to each other and one of them is
// irreversible: the reset unpairs every controller, and each one has to
// commission the bridge again. The tests below pin the gate between the
// tap and the request, because a confirm dialog that is asked but not
// awaited looks identical in a screenshot.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, screen } from "@testing-library/svelte";
import { tick } from "svelte";

const {
  mockForceSync,
  mockFactoryReset,
  mockConfirmAsk,
  mockToastSuccess,
  mockToastError,
} = vi.hoisted(() => ({
  mockForceSync: vi.fn(),
  mockFactoryReset: vi.fn(),
  mockConfirmAsk: vi.fn(),
  mockToastSuccess: vi.fn(),
  mockToastError: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  api: {
    matterFabrics: () => Promise.resolve({ fabrics: [] }),
    matterStatus: () => Promise.resolve({}),
    matterForceSync: () => mockForceSync(),
    matterFactoryReset: () => mockFactoryReset(),
  },
  // The real ApiError carries the status and the problem body; the
  // component only reads .message, but the type comes from the real
  // module, so the fake keeps the same constructor.
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

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: mockConfirmAsk },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}:${JSON.stringify(vars)}` : key,
}));

import { ApiError } from "$lib/api/client";
import MatterFabrics from "./MatterFabrics.svelte";

beforeEach(() => {
  vi.clearAllMocks();
  mockForceSync.mockResolvedValue(undefined);
  mockFactoryReset.mockResolvedValue(undefined);
});

afterEach(cleanup);

async function click(label: string) {
  render(MatterFabrics);
  (await screen.findByText(label)).click();
  await tick();
  await tick();
  await tick();
}

describe("MatterFabrics — maintenance actions", () => {
  it("re-syncs the topology without asking, because nothing is lost by it", async () => {
    await click("matter.maint.force_sync");

    expect(mockForceSync).toHaveBeenCalledTimes(1);
    expect(mockConfirmAsk).not.toHaveBeenCalled();
    expect(mockToastSuccess).toHaveBeenCalledWith(
      "matter.maint.force_sync_done",
    );
  });

  it("surfaces a failed re-sync instead of reporting success", async () => {
    mockForceSync.mockRejectedValue(new ApiError(500, null, "assembler refused"));

    await click("matter.maint.force_sync");

    expect(mockToastError).toHaveBeenCalledWith("assembler refused");
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });

  it("does not remove any pairing when the confirm dialog is declined", async () => {
    mockConfirmAsk.mockResolvedValue(false);

    await click("matter.maint.reset");

    expect(mockConfirmAsk).toHaveBeenCalledTimes(1);
    expect(mockFactoryReset).not.toHaveBeenCalled();
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });

  it("asks destructively and removes every pairing once confirmed", async () => {
    mockConfirmAsk.mockResolvedValue(true);

    await click("matter.maint.reset");

    expect(mockConfirmAsk.mock.calls[0][0]).toMatchObject({
      destructive: true,
    });
    expect(mockFactoryReset).toHaveBeenCalledTimes(1);
    expect(mockToastSuccess).toHaveBeenCalledWith("matter.maint.reset_done");
  });

  it("surfaces a partial reset, which leaves the bridge paired to something", async () => {
    mockConfirmAsk.mockResolvedValue(true);
    mockFactoryReset.mockRejectedValue(
      new ApiError(502, null, "revoke fabric 2: timeout"),
    );

    await click("matter.maint.reset");

    expect(mockToastError).toHaveBeenCalledWith("revoke fabric 2: timeout");
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });
});
