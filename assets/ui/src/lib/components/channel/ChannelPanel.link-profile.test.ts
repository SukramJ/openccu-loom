// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, fireEvent, screen } from "@testing-library/svelte";
import type { UISchema, UISchemaParameter } from "$lib/api/types";

const { mockUiSchema, mockPutLinkParamset, mockToastSuccess } = vi.hoisted(() => ({
  mockUiSchema: vi.fn(),
  mockPutLinkParamset: vi.fn(),
  mockToastSuccess: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  api: {
    uiSchema: (...args: unknown[]) => mockUiSchema(...args),
    listDataPoints: vi.fn().mockResolvedValue([]),
    openEditSession: vi.fn().mockResolvedValue({ key: "k", token: "tok", expires_at: "" }),
    heartbeatEditSession: vi.fn().mockResolvedValue(null),
    closeEditSession: vi.fn().mockResolvedValue(undefined),
    putParamset: vi.fn(),
    putLinkParamset: (...args: unknown[]) => mockPutLinkParamset(...args),
    setValue: vi.fn(),
    takeOverEditSession: vi.fn(),
    getDevice: vi.fn().mockResolvedValue({ address: "X", interface: "HmIP-RF" }),
    testLinkAtDevice: vi.fn(),
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, _body: unknown, message: string) {
      super(message);
      this.status = status;
    }
  },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : String(err)),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    success: (...args: unknown[]) => mockToastSuccess(...args),
    error: vi.fn(),
    warn: vi.fn(),
    push: vi.fn(),
  },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: vi.fn().mockResolvedValue(false) },
}));

vi.mock("$lib/stores/events.svelte", () => ({
  onResync: () => () => {},
  subscribe: () => () => {},
}));

vi.mock("$lib/components/ui/Select.svelte", async () => {
  const mod = await import("../../../routes/__testutils__/SelectStub.svelte");
  return { default: mod.default };
});

import ChannelPanel from "./ChannelPanel.svelte";
import { prefs } from "$lib/stores/preferences.svelte";

function param(name: string, value: number, extra: Partial<UISchemaParameter> = {}): UISchemaParameter {
  return {
    name,
    label: `L:${name}`,
    type: "INTEGER",
    min: 0,
    max: 200,
    operations: { read: true, write: true, event: false },
    flags: { visible: true, internal: false, service: false },
    observed: true,
    value,
    ...extra,
  };
}

// Receiver side of a key -> switch link. Profile 3 (the stored one)
// leaves the two ON_LEVELs to the operator and pins ON_TIME_MODE;
// SHORT_JT_ON is a jump target, hidden outside the expert view.
function linkSchema(): UISchema {
  return {
    channel: { address: "RECV:4", number: 4, type: "SWITCH_VIRTUAL_RECEIVER", device_address: "RECV" },
    parameters: [
      param("SHORT_ON_TIME_MODE", 0),
      param("SHORT_ON_LEVEL", 100),
      param("LONG_ON_LEVEL", 100),
      param("SHORT_JT_ON", 1, { hidden_by_default: true }),
    ],
    profile: {
      receiver_type: "SWITCH_VIRTUAL_RECEIVER",
      sender_type: "KEY_TRANSCEIVER",
      active_profile_id: 3,
      raw: {
        KEY_TRANSCEIVER: {
          profiles: [
            { id: 0, name: { en: "Expert" }, params: {} },
            {
              id: 1,
              name: { en: "Switch on" },
              params: {
                SHORT_ON_TIME_MODE: { constraint_type: "fixed", value: 1 },
                SHORT_ON_LEVEL: { constraint_type: "range", default: 50, min_value: 0, max_value: 200 },
              },
            },
            {
              id: 3,
              name: { en: "Switch on / off" },
              params: {
                SHORT_ON_TIME_MODE: { constraint_type: "fixed", value: 0 },
                SHORT_ON_LEVEL: { constraint_type: "range", default: 100, min_value: 0, max_value: 200 },
                LONG_ON_LEVEL: { constraint_type: "range", default: 100, min_value: 0, max_value: 200 },
                SHORT_JT_ON: { constraint_type: "list", values: [1, 3] },
              },
            },
          ],
        },
      },
    },
  };
}

function shown(name: string): boolean {
  return screen.queryByText(`L:${name}`) !== null;
}

async function mount(extra: Record<string, unknown> = {}) {
  const onDirtyChange = vi.fn();
  const r = render(ChannelPanel, {
    props: {
      address: "RECV",
      channel: 4,
      paramset: "LINK",
      peer: "SEND:1",
      locale: "en",
      onDirtyChange,
      ...extra,
    },
  });
  await waitFor(() => expect(shown("SHORT_ON_LEVEL")).toBe(true));
  return { ...r, onDirtyChange };
}

beforeEach(() => {
  mockUiSchema.mockReset();
  mockUiSchema.mockImplementation(() => Promise.resolve(linkSchema()));
  mockPutLinkParamset.mockReset();
  mockPutLinkParamset.mockResolvedValue(undefined);
  mockToastSuccess.mockReset();
  prefs.writePreview = false;
});

afterEach(() => {
  cleanup();
  prefs.writePreview = true;
});

describe("ChannelPanel LINK — profile view", () => {
  it("shows only the parameters the stored profile leaves open", async () => {
    await mount();
    expect(shown("LONG_ON_LEVEL")).toBe(true);
    expect(shown("SHORT_ON_TIME_MODE")).toBe(false);
    expect(shown("SHORT_JT_ON")).toBe(false);
    expect(screen.getByText("profile.hidden_count")).toBeTruthy();
  });

  it("shows every parameter, jump targets included, under Experte", async () => {
    await mount();
    await fireEvent.click(screen.getByRole("option", { name: "Expert" }));
    await waitFor(() => expect(shown("SHORT_JT_ON")).toBe(true));
    expect(shown("SHORT_ON_TIME_MODE")).toBe(true);
    expect(screen.queryByText("profile.hidden_count")).toBeNull();
  });

  it("stages a chosen profile as unsaved edits and undo moves the picker back", async () => {
    const { onDirtyChange } = await mount();
    await fireEvent.click(screen.getByRole("option", { name: "Switch on" }));
    // ON_TIME_MODE 0 -> 1 and SHORT_ON_LEVEL 100 -> 50.
    await waitFor(() => expect(onDirtyChange).toHaveBeenLastCalledWith(2));
    expect(screen.getByRole("option", { name: "Switch on" }).getAttribute("aria-selected")).toBe("true");
    expect(mockPutLinkParamset).not.toHaveBeenCalled();

    await fireEvent.click(screen.getByTitle("channel.undo.tooltip"));
    await waitFor(() => expect(onDirtyChange).toHaveBeenLastCalledWith(0));
    expect(screen.getByRole("option", { name: "Switch on / off" }).getAttribute("aria-selected")).toBe("true");
  });

  it("shows the raw CCU names only under Experte", async () => {
    await mount();
    expect(screen.queryByText("SHORT_ON_LEVEL", { exact: true })).toBeNull();
    await fireEvent.click(screen.getByRole("option", { name: "Expert" }));
    await waitFor(() => expect(screen.getByText("SHORT_ON_LEVEL", { exact: true })).toBeTruthy());
  });

  it("puts the short and long twin of a setting in one row", async () => {
    const { container } = await mount();
    const row = container.querySelector('[data-row="ON_LEVEL"]');
    expect(row?.textContent).toContain("L:SHORT_ON_LEVEL");
    expect(row?.textContent).toContain("L:LONG_ON_LEVEL");
  });
});

describe("ChannelPanel LINK — device test", () => {
  it("offers the test on the receiver side", async () => {
    await mount();
    expect(screen.getByText("profile.test.short")).toBeTruthy();
  });

  it("does not offer it on the sender side", async () => {
    await mount({ linkRole: "sender" });
    expect(screen.queryByText("profile.test.short")).toBeNull();
  });
});

describe("ChannelPanel — hosted", () => {
  it("drops its own save controls", async () => {
    await mount({ hosted: true });
    await fireEvent.click(screen.getByRole("option", { name: "Switch on" }));
    expect(screen.queryByText("channel.save_n")).toBeNull();
    expect(screen.queryByText("channel.unsaved")).toBeNull();
  });

  it("writes through save() and leaves the success toast to the page", async () => {
    const { component } = await mount({ hosted: true });
    await fireEvent.click(screen.getByRole("option", { name: "Switch on" }));
    const ok = await (component as unknown as { save: () => Promise<boolean> }).save();
    expect(ok).toBe(true);
    expect(mockPutLinkParamset).toHaveBeenCalledWith(
      "RECV:4",
      "SEND:1",
      { SHORT_ON_TIME_MODE: 1, SHORT_ON_LEVEL: 50 },
      "tok",
    );
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });

  it("reports a failed write as not saved", async () => {
    mockPutLinkParamset.mockRejectedValueOnce(new Error("boom"));
    const { component } = await mount({ hosted: true });
    await fireEvent.click(screen.getByRole("option", { name: "Switch on" }));
    const ok = await (component as unknown as { save: () => Promise<boolean> }).save();
    expect(ok).toBe(false);
  });

  it("reports a cancelled write preview as not saved", async () => {
    prefs.writePreview = true;
    const { component } = await mount({ hosted: true });
    await fireEvent.click(screen.getByRole("option", { name: "Switch on" }));
    const pending = (component as unknown as { save: () => Promise<boolean> }).save();
    const cancel = await waitFor(() => screen.getByText("common.cancel"));
    await fireEvent.click(cancel);
    expect(await pending).toBe(false);
    expect(mockPutLinkParamset).not.toHaveBeenCalled();
  });

  it("reports nothing to write as saved", async () => {
    const { component } = await mount({ hosted: true });
    const ok = await (component as unknown as { save: () => Promise<boolean> }).save();
    expect(ok).toBe(true);
    expect(mockPutLinkParamset).not.toHaveBeenCalled();
  });
});
