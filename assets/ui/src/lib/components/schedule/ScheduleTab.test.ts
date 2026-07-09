// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, screen, waitFor } from "@testing-library/svelte";
import type { ClimateSchedule } from "$lib/api/types";

const mockGetDeviceSchedule = vi.fn();

vi.mock("$lib/api/client", () => ({
  api: {
    getDeviceSchedule: (...args: unknown[]) => mockGetDeviceSchedule(...args),
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, _body: unknown, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

// The two editors are exercised by their own component tests
// (SimpleScheduleEditor.test.ts) / e2e coverage; ScheduleTab only needs to
// prove it dispatches to the right one, so both are stubbed to a marker.
vi.mock("./SimpleScheduleEditor.svelte", () => ({
  default: () => {
    document.body.setAttribute("data-rendered", "simple");
    return { $set: vi.fn(), $destroy: vi.fn() };
  },
}));
vi.mock("./ClimateScheduleEditor.svelte", () => ({
  default: () => {
    document.body.setAttribute("data-rendered", "climate");
    return { $set: vi.fn(), $destroy: vi.fn() };
  },
}));

import { ApiError } from "$lib/api/client";
import ScheduleTab from "./ScheduleTab.svelte";

beforeEach(() => {
  vi.clearAllMocks();
  document.body.removeAttribute("data-rendered");
});

afterEach(() => {
  cleanup();
});

describe("ScheduleTab — dispatch", () => {
  it("shows the unsupported message when the device has no schedule channel (404)", async () => {
    mockGetDeviceSchedule.mockRejectedValue(new ApiError(404, {}, "not found"));
    render(ScheduleTab, { props: { address: "0002E4A17B93C1" } });

    await waitFor(() => {
      expect(screen.getByText("schedule.unsupported")).toBeInTheDocument();
    });
  });

  it("shows a load error for a non-404 failure", async () => {
    mockGetDeviceSchedule.mockRejectedValue(new Error("network down"));
    render(ScheduleTab, { props: { address: "0002E4A17B93C1" } });

    await waitFor(() => {
      expect(screen.getByText(/network down/)).toBeInTheDocument();
    });
  });

  it("renders the simple editor for a 'simple' schedule", async () => {
    const schedule: ClimateSchedule = {
      channel: { address: "0002E4A17B93C1:4", number: 4, device_address: "0002E4A17B93C1" },
      kind: "simple",
      simple_entries: [],
    };
    mockGetDeviceSchedule.mockResolvedValue(schedule);
    render(ScheduleTab, { props: { address: "0002E4A17B93C1" } });

    await waitFor(() => {
      expect(document.body.getAttribute("data-rendered")).toBe("simple");
    });
    expect(mockGetDeviceSchedule).toHaveBeenCalledWith("0002E4A17B93C1");
  });

  it("renders the climate editor for a 'climate' schedule", async () => {
    const schedule: ClimateSchedule = {
      channel: { address: "0002E4A17B93C1:1", number: 1, device_address: "0002E4A17B93C1" },
      kind: "climate",
    };
    mockGetDeviceSchedule.mockResolvedValue(schedule);
    render(ScheduleTab, { props: { address: "0002E4A17B93C1" } });

    await waitFor(() => {
      expect(document.body.getAttribute("data-rendered")).toBe("climate");
    });
  });
});
