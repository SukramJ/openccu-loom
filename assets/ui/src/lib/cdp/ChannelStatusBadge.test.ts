// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent } from "@testing-library/svelte";

// Mock the API — `listDataPoints` is called on mount.
vi.mock("$lib/api/client", () => ({
  api: { listDataPoints: vi.fn() },
  ApiError: class ApiError extends Error {
    constructor(
      public readonly status: number,
      public readonly body: unknown,
      message: string,
    ) {
      super(message);
    }
  },
  friendlyError: (err: unknown) =>
    err instanceof Error ? err.message : String(err),
}));

// Stub the events store — no live WebSocket in unit tests.
vi.mock("$lib/stores/events.svelte", () => ({
  onResync: () => () => {},
  subscribe: vi.fn(() => () => {}),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}::${JSON.stringify(vars)}` : key,
}));

// dpLabel just returns the parameter name in the stub; enumValueLabel keeps
// the real value_list lookup so the badge's ENUM path stays exercised.
vi.mock("$lib/sensor-actor/classify", () => ({
  dpLabel: (dp: { parameter: string }) => dp.parameter,
  enumValueLabel: (dp: {
    type?: string;
    value: unknown;
    value_list?: string[];
  }) => {
    if ((dp.type ?? "").toUpperCase() !== "ENUM") return undefined;
    if (typeof dp.value !== "number" || !dp.value_list) return undefined;
    return dp.value_list[dp.value];
  },
}));

vi.mock("$lib/quickcontrol/domain", () => ({
  STATUS_HEADLINE_KEYS: ["TEMPERATURE", "HUMIDITY", "STATE", "LEVEL"],
}));

import { api } from "$lib/api/client";
import ChannelStatusBadge from "./ChannelStatusBadge.svelte";
import type { DataPointSummary } from "$lib/api/types";

const listDataPointsMock = api.listDataPoints as ReturnType<typeof vi.fn>;

function makeDP(
  overrides: Partial<DataPointSummary> = {},
): DataPointSummary {
  return {
    parameter: "STATE",
    value: null,
    observed: false,
    source: "unobserved",
    ...overrides,
  } as DataPointSummary;
}

beforeEach(() => {
  vi.clearAllMocks();
  // Default: empty list — component mounts cleanly without DPs.
  listDataPointsMock.mockResolvedValue([]);
});

afterEach(() => {
  cleanup();
});

describe("ChannelStatusBadge — display name", () => {
  it("shows the name prop as the channel label", async () => {
    listDataPointsMock.mockResolvedValue([]);
    const { getByText } = render(ChannelStatusBadge, {
      props: { address: "ABC123", channel: 0, name: "Main channel" },
    });
    expect(getByText("Main channel")).toBeTruthy();
  });

  it("falls back to typeLabel when name is absent", () => {
    const { getByText } = render(ChannelStatusBadge, {
      props: { address: "ABC123", channel: 2, typeLabel: "MAINTENANCE" },
    });
    expect(getByText("MAINTENANCE")).toBeTruthy();
  });

  it("falls back to the localized channel caption when neither name nor typeLabel is set", () => {
    const { getByText } = render(ChannelStatusBadge, {
      props: { address: "ABC123", channel: 3 },
    });
    expect(getByText('device.channel_n::{"n":3}')).toBeTruthy();
  });
});

describe("ChannelStatusBadge — value formatting", () => {
  it("renders booleans through the catalogue instead of German literals", async () => {
    listDataPointsMock.mockResolvedValue([
      makeDP({ parameter: "STATE", type: "BOOL", value: true, observed: true }),
    ]);
    const { getByText } = render(ChannelStatusBadge, {
      props: { address: "ABC123", channel: 0 },
    });
    await vi.waitFor(() => expect(getByText("quick.on")).toBeTruthy());
  });

  it("renders a window contact's STATE as the open/closed catalogue entry", async () => {
    listDataPointsMock.mockResolvedValue([
      makeDP({ parameter: "STATE", type: "BOOL", value: false, observed: true }),
    ]);
    const { getByText } = render(ChannelStatusBadge, {
      props: { address: "ABC123", channel: 1, type: "SHUTTER_CONTACT" },
    });
    await vi.waitFor(() => expect(getByText("enum.CLOSED")).toBeTruthy());
  });

  // A read-only ENUM arrives as the raw value_list index; showing the digit
  // turns a smoke detector's alarm status into an unreadable "1".
  it("resolves an ENUM index to its value_list label", async () => {
    listDataPointsMock.mockResolvedValue([
      makeDP({
        parameter: "SMOKE_DETECTOR_ALARM_STATUS",
        type: "ENUM",
        value: 1,
        value_list: ["IDLE_OFF", "PRIMARY_ALARM", "INTRUSION_ALARM"],
        observed: true,
      }),
    ]);
    const { getByText } = render(ChannelStatusBadge, {
      props: { address: "ABC123", channel: 2 },
    });
    await vi.waitFor(() => expect(getByText("PRIMARY_ALARM")).toBeTruthy());
  });
});

describe("ChannelStatusBadge — collapse / expand toggle", () => {
  it("renders collapsed by default (no data-point list visible)", () => {
    const { container } = render(ChannelStatusBadge, {
      props: { address: "ABC123", channel: 0 },
    });
    const list = container.querySelector("ul");
    expect(list).toBeNull();
  });

  it("expands when the button is clicked", async () => {
    listDataPointsMock.mockResolvedValue([
      makeDP({ parameter: "STATE", value: true, observed: true }),
    ]);
    const { container } = render(ChannelStatusBadge, {
      props: { address: "ABC123", channel: 0 },
    });
    // Wait for onMount load to complete.
    await vi.waitFor(() =>
      expect(listDataPointsMock).toHaveBeenCalledWith("ABC123", 0),
    );
    const btn = container.querySelector("button") as HTMLButtonElement;
    await fireEvent.click(btn);
    // After expand the ul should be present.
    const list = container.querySelector("ul");
    expect(list).not.toBeNull();
  });

  it("collapses again on a second click", async () => {
    listDataPointsMock.mockResolvedValue([
      makeDP({ parameter: "STATE", value: false, observed: true }),
    ]);
    const { container } = render(ChannelStatusBadge, {
      props: { address: "ABC123", channel: 0 },
    });
    await vi.waitFor(() =>
      expect(listDataPointsMock).toHaveBeenCalledWith("ABC123", 0),
    );
    const btn = container.querySelector("button") as HTMLButtonElement;
    await fireEvent.click(btn); // expand
    await fireEvent.click(btn); // collapse
    const list = container.querySelector("ul");
    expect(list).toBeNull();
  });
});

describe("ChannelStatusBadge — empty expanded state", () => {
  it("shows the no-datapoints key when list is empty and expanded", async () => {
    listDataPointsMock.mockResolvedValue([]);
    const { container, getByText } = render(ChannelStatusBadge, {
      props: { address: "ABC123", channel: 0 },
    });
    await vi.waitFor(() =>
      expect(listDataPointsMock).toHaveBeenCalledWith("ABC123", 0),
    );
    const btn = container.querySelector("button") as HTMLButtonElement;
    await fireEvent.click(btn);
    expect(getByText("cdp.status.no_datapoints")).toBeTruthy();
  });
});

describe("ChannelStatusBadge — API call", () => {
  it("calls listDataPoints with the correct address and channel on mount", async () => {
    listDataPointsMock.mockResolvedValue([]);
    render(ChannelStatusBadge, {
      props: { address: "DEV456", channel: 4 },
    });
    await vi.waitFor(() =>
      expect(listDataPointsMock).toHaveBeenCalledWith("DEV456", 4),
    );
  });
});

describe("ChannelStatusBadge — opacity when not yet loaded", () => {
  it("has opacity-60 class while data is still being fetched", () => {
    // Never resolve so the component stays in loading state.
    listDataPointsMock.mockReturnValue(new Promise(() => {}));
    const { container } = render(ChannelStatusBadge, {
      props: { address: "ABC123", channel: 1 },
    });
    const wrapper = container.querySelector(".opacity-60");
    expect(wrapper).not.toBeNull();
  });
});
