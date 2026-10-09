// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, screen, act, waitFor } from "@testing-library/svelte";
import { createRawSnippet } from "svelte";
import type { ChannelSummary, DeviceDetail } from "$lib/api/types";

const { mockToastSuccess, mockToastError } = vi.hoisted(() => ({
  mockToastSuccess: vi.fn(),
  mockToastError: vi.fn(),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) => (vars ? `${key} ${JSON.stringify(vars)}` : key),
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    success: (...a: unknown[]) => mockToastSuccess(...a),
    error: (...a: unknown[]) => mockToastError(...a),
    warn: vi.fn(),
    push: vi.fn(),
  },
}));

vi.mock("$lib/components/channel/ChannelPanel.svelte", async () => {
  const mod = await import("../../../routes/__testutils__/ChannelPanelStub.svelte");
  return { default: mod.default };
});

import DeviceParameters from "./DeviceParameters.svelte";
import { stubSides, resetStubSides } from "../../../routes/__testutils__/channel-panel-stub";
import { prefs } from "$lib/stores/preferences.svelte";

function ch(no: number, name = `Ch${no}`, type = "SWITCH_VIRTUAL_RECEIVER"): ChannelSummary {
  return { address: `DEV:${no}`, number: no, name, type, paramset_key: "MASTER", data_points_count: 1 };
}

const DETAIL = {
  address: "DEV",
  name: "Licht",
  model: "HmIP-BSM",
  master_pushes_config_pending: true,
  channels: [],
} as unknown as DeviceDetail;

const settings = createRawSnippet((c: () => ChannelSummary) => ({
  render: () => `<p data-testid="settings-${c().number}">settings</p>`,
}));

function mount(extra: Record<string, unknown> = {}) {
  return render(DeviceParameters, {
    props: {
      detail: DETAIL,
      locale: "en",
      deviceChannel: null,
      channelZero: ch(0, "", "MAINTENANCE"),
      channels: [ch(4), ch(1), ch(2)],
      linkCounts: new Map(),
      settings,
      ...extra,
    },
  });
}

async function dirty(key: string, n = 1) {
  await act(() => stubSides[key].markDirty!(n));
}

beforeEach(() => {
  resetStubSides();
  mockToastSuccess.mockReset();
  mockToastError.mockReset();
  prefs.expertMode = false;
});
afterEach(() => cleanup());

describe("DeviceParameters", () => {
  it("loads the device parameters and the first channel, nothing else", () => {
    mount();
    expect(screen.getByTestId("panel-ch0")).toBeTruthy();
    expect(screen.getByTestId("panel-ch1")).toBeTruthy();
    expect(screen.queryByTestId("panel-ch2")).toBeNull();
    expect(screen.queryByTestId("panel-ch4")).toBeNull();
  });

  it("loads a channel when it is opened and keeps it after closing", async () => {
    mount();
    const block = document.querySelector('section[data-channel="2"]') as HTMLElement;
    const toggle = block.querySelector("button[aria-expanded]") as HTMLElement;
    await fireEvent.click(toggle);
    expect(screen.getByTestId("panel-ch2")).toBeTruthy();
    expect(screen.getByTestId("settings-2")).toBeTruthy();
    await fireEvent.click(toggle);
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    // Still mounted, so its unsaved edits survive.
    expect(screen.getByTestId("panel-ch2")).toBeTruthy();
  });

  it("opens a routed channel instead of the first", () => {
    mount({ focusChannel: 4 });
    expect(screen.getByTestId("panel-ch4")).toBeTruthy();
    expect(screen.queryByTestId("panel-ch1")).toBeNull();
  });

  it("writes every channel with edits through one save bar", async () => {
    mount();
    expect(screen.queryByText("links.editor.apply")).toBeNull();
    await dirty("ch0", 1);
    await dirty("ch1", 2);
    expect(screen.getByText(/links\.editor\.pending \{"count":3\}/)).toBeTruthy();
    await fireEvent.click(screen.getByText("links.editor.apply"));
    await waitFor(() => expect(mockToastSuccess).toHaveBeenCalledWith("device.params.saved"));
    expect(stubSides.ch0.saves).toBe(1);
    expect(stubSides.ch1.saves).toBe(1);
  });

  it("names the channels that failed and still writes the rest", async () => {
    mount();
    stubSides.ch0.saveResult = false;
    await dirty("ch0");
    await dirty("ch1");
    await fireEvent.click(screen.getByText("links.editor.apply"));
    await waitFor(() => expect(mockToastError).toHaveBeenCalled());
    expect(stubSides.ch1.saves).toBe(1);
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });

  it("discards every channel on Abbrechen", async () => {
    mount();
    await dirty("ch1");
    await fireEvent.click(screen.getByText("common.cancel"));
    expect(stubSides.ch0.discards).toBe(1);
    expect(stubSides.ch1.discards).toBe(1);
  });

  it("offers the one expert switch for the whole page", async () => {
    mount();
    const box = screen.getByLabelText("channel.expert_label") as HTMLInputElement;
    await fireEvent.click(box);
    expect(prefs.expertMode).toBe(true);
    prefs.expertMode = false;
  });
});
