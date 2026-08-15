// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// @vitest-environment happy-dom
//
// The channel dropdown is the only place the operator sees which device an
// assignment belongs to, so a channel list arriving for a device they have
// already moved off must never replace the one being loaded — otherwise a
// system variable can be assigned to a channel of the wrong device.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor } from "@testing-library/svelte";

const mockListChannels = vi.fn();
vi.mock("$lib/api/client", () => ({
  api: { listChannels: (...a: unknown[]) => mockListChannels(...a) },
}));

vi.mock("$lib/stores/devices.svelte", () => ({
  deviceStore: {
    items: [
      {
        address: "ABC0000001",
        central: "ccu1",
        interface_id: "ccu1-HmIP-RF",
        name: "Wohnzimmer",
        model: "HmIP-eTRV",
        model_label: "",
      },
      {
        address: "DEF0000002",
        central: "ccu1",
        interface_id: "ccu1-HmIP-RF",
        name: "Küche",
        model: "HmIP-STHD",
        model_label: "",
      },
    ],
    refresh: vi.fn(),
  },
}));
vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));
vi.mock("$lib/i18n", () => ({ t: (k: string) => k }));

import SysvarChannelPicker from "./SysvarChannelPicker.svelte";

// GET /devices/{addr}/channels answers with a bare JSON array — mocking a
// wrapper here would keep the picker green against a client that reads a
// property the response does not have.
function channelList(address: string, number: number) {
  return [{ address, number, name: "Kanal", type_label: "" }];
}

beforeEach(() => vi.clearAllMocks());
afterEach(() => cleanup());

describe("SysvarChannelPicker — device pick", () => {
  it("loads the channels of the picked device", async () => {
    mockListChannels.mockResolvedValue(channelList("ABC0000001:1", 1));
    const { getByText, container } = render(SysvarChannelPicker, {
      props: { value: "", onChange: vi.fn() },
    });

    await fireEvent.click(getByText("Wohnzimmer"));

    await waitFor(() =>
      expect(mockListChannels).toHaveBeenCalledWith("ABC0000001"),
    );
    // The dropdown replaces the loading line once the list has arrived.
    await waitFor(() =>
      expect(container.textContent).not.toContain("common.loading"),
    );
  });

  it("labels the assigned channel from the loaded list", async () => {
    // Asserting only that the loading line disappears passes against an
    // empty list too — and an empty list is a picker no assignment can
    // ever be made from. The dropdown resolves its label from the loaded
    // channels, so the label is the proof that they arrived.
    mockListChannels.mockResolvedValue(channelList("ABC0000001:1", 1));
    const { container } = render(SysvarChannelPicker, {
      props: { value: "ABC0000001:1", onChange: vi.fn() },
    });

    await waitFor(() =>
      expect(mockListChannels).toHaveBeenCalledWith("ABC0000001"),
    );
    await waitFor(() =>
      expect(
        container.querySelector("[data-select-trigger]")?.textContent,
      ).toContain("#1 Kanal"),
    );
  });

  it("keeps loading when a response for the previous device arrives late", async () => {
    const first: { resolve?: (v: unknown) => void } = {};
    const second: { resolve?: (v: unknown) => void } = {};
    mockListChannels.mockImplementation((addr: string) =>
      addr === "ABC0000001"
        ? new Promise((resolve) => (first.resolve = resolve))
        : new Promise((resolve) => (second.resolve = resolve)),
    );

    const { getByText, container } = render(SysvarChannelPicker, {
      props: { value: "", onChange: vi.fn() },
    });

    await fireEvent.click(getByText("Wohnzimmer"));
    await waitFor(() =>
      expect(mockListChannels).toHaveBeenCalledWith("ABC0000001"),
    );
    await fireEvent.click(getByText("Küche"));
    await waitFor(() =>
      expect(mockListChannels).toHaveBeenCalledWith("DEF0000002"),
    );

    // The abandoned first request answers while the second is still in
    // flight. Publishing it would show the previous device's channels as
    // if they were the picked device's.
    first.resolve?.(channelList("ABC0000001:9", 9));
    await new Promise((r) => setTimeout(r, 0));
    expect(container.textContent).toContain("common.loading");

    second.resolve?.(channelList("DEF0000002:1", 1));
    await waitFor(() =>
      expect(container.textContent).not.toContain("common.loading"),
    );
  });
});
