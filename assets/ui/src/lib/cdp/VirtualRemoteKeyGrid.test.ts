// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, screen, fireEvent, waitFor } from "@testing-library/svelte";
import type { ChannelSummary, DeviceDetail } from "$lib/api/types";

const { mockSetValue, mockToastError } = vi.hoisted(() => ({
  mockSetValue: vi.fn(),
  mockToastError: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  api: {
    setValue: (...args: unknown[]) => mockSetValue(...args),
  },
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    error: (...args: unknown[]) => mockToastError(...args),
  },
}));

// Capture every registered handler so a test can simulate a
// `device.trigger` broadcast from the WS pump, mirroring the
// `eventHandlers` capture pattern used by ChannelPanel.brightness.test.ts.
const eventHandlers: Array<(ev: unknown) => void> = [];
vi.mock("$lib/stores/events.svelte", () => ({
  onResync: () => () => {},
  subscribe: (handler: (ev: unknown) => void) => {
    eventHandlers.push(handler);
    return () => {
      const idx = eventHandlers.indexOf(handler);
      if (idx >= 0) eventHandlers.splice(idx, 1);
    };
  },
}));

// `t()` mock follows the repo-wide convention (e.g.
// ChannelPanel.determine.test.ts): interpolated calls render the key plus
// its vars so distinct channel numbers produce distinguishable text/labels.
vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}::${JSON.stringify(vars)}` : key,
}));

import VirtualRemoteKeyGrid from "./VirtualRemoteKeyGrid.svelte";

const ADDRESS = "0001RCV50";

function emit(ev: unknown) {
  for (const handler of [...eventHandlers]) handler(ev);
}

function ch(number: number, name?: string): ChannelSummary {
  return {
    address: `${ADDRESS}:${number}`,
    number,
    name,
    paramset_key: "VALUES",
    data_points_count: 2,
  };
}

function baseDetail(): DeviceDetail {
  return {
    address: ADDRESS,
    interface: "BidCos-RF",
    interface_id: "BidCos-RF",
    model: "HM-RCV-50",
    name: "Central remote",
    available: true,
    channels_count: 4,
    updatable: false,
    update_available: false,
    master_pushes_config_pending: false,
    has_sub_devices: false,
    firmware: {},
    availability: {},
    // Channel 0 is the maintenance pseudo-channel — must never render a cell.
    channels: [ch(0), ch(1, "Key 1"), ch(2), ch(3, "Key 3")],
  };
}

function shortButtonFor(n: number) {
  return screen.getByRole("button", {
    name: `remote.press_short_aria::${JSON.stringify({ n })}`,
  });
}

function longButtonFor(n: number) {
  return screen.getByRole("button", {
    name: `remote.press_long_aria::${JSON.stringify({ n })}`,
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  eventHandlers.length = 0;
  mockSetValue.mockResolvedValue(undefined);
});

afterEach(() => cleanup());

describe("VirtualRemoteKeyGrid — rendering", () => {
  it("renders one cell per KEY channel, excluding channel 0", () => {
    render(VirtualRemoteKeyGrid, { props: { detail: baseDetail() } });

    expect(screen.getByText("Key 1")).toBeInTheDocument();
    expect(screen.getByText("Key 3")).toBeInTheDocument();
    // Channel 2 has no CCU name — falls back to the "Key {n}" label.
    expect(screen.getByText("remote.key_n::" + JSON.stringify({ n: 2 }))).toBeInTheDocument();

    // 3 keys x (short + long) = 6 press buttons; channel 0 contributes none.
    expect(screen.getAllByRole("button")).toHaveLength(6);
  });

  it("registers exactly one WS subscription on mount", () => {
    render(VirtualRemoteKeyGrid, { props: { detail: baseDetail() } });
    expect(eventHandlers).toHaveLength(1);
  });
});

describe("VirtualRemoteKeyGrid — press actions", () => {
  it("sends a PRESS_SHORT write for the clicked channel", async () => {
    render(VirtualRemoteKeyGrid, { props: { detail: baseDetail() } });
    await fireEvent.click(shortButtonFor(1));

    await waitFor(() => {
      expect(mockSetValue).toHaveBeenCalledWith(ADDRESS, 1, "PRESS_SHORT", true);
    });
  });

  it("sends a PRESS_LONG write for the clicked channel", async () => {
    render(VirtualRemoteKeyGrid, { props: { detail: baseDetail() } });
    await fireEvent.click(longButtonFor(3));

    await waitFor(() => {
      expect(mockSetValue).toHaveBeenCalledWith(ADDRESS, 3, "PRESS_LONG", true);
    });
  });

  it("shows an error toast when the write fails, without throwing", async () => {
    mockSetValue.mockRejectedValueOnce(new Error("ccu unreachable"));
    render(VirtualRemoteKeyGrid, { props: { detail: baseDetail() } });

    await fireEvent.click(shortButtonFor(1));

    await waitFor(() => {
      expect(mockToastError).toHaveBeenCalledWith("remote.press_failed", "ccu unreachable");
    });
  });
});

describe("VirtualRemoteKeyGrid — device.trigger echo flash", () => {
  it("flashes the matching cell on a device.trigger event for this device+channel", async () => {
    render(VirtualRemoteKeyGrid, { props: { detail: baseDetail() } });
    const cell = shortButtonFor(2).parentElement?.parentElement as HTMLElement;
    expect(cell.className).not.toContain("border-brand-400");

    emit({ type: "device.trigger", payload: { device_address: ADDRESS, channel: 2 } });

    await waitFor(() => {
      expect(cell.className).toContain("border-brand-400");
    });
  });

  it("ignores a device.trigger event for a different device address", async () => {
    render(VirtualRemoteKeyGrid, { props: { detail: baseDetail() } });
    const cell = shortButtonFor(1).parentElement?.parentElement as HTMLElement;

    emit({ type: "device.trigger", payload: { device_address: "OTHERDEV", channel: 1 } });
    await Promise.resolve();

    expect(cell.className).not.toContain("border-brand-400");
  });

  it("ignores non-device.trigger events without throwing", () => {
    render(VirtualRemoteKeyGrid, { props: { detail: baseDetail() } });
    expect(() =>
      emit({ type: "data_point.value_changed", payload: {} }),
    ).not.toThrow();
  });
});
