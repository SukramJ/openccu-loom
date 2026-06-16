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
  subscribe: vi.fn(() => () => {}),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

// dpLabel just returns the parameter name in the stub.
vi.mock("$lib/sensor-actor/classify", () => ({
  dpLabel: (dp: { parameter: string }) => dp.parameter,
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

  it("falls back to 'Kanal N' when neither name nor typeLabel is set", () => {
    const { getByText } = render(ChannelStatusBadge, {
      props: { address: "ABC123", channel: 3 },
    });
    expect(getByText("Kanal 3")).toBeTruthy();
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
