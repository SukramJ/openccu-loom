// @vitest-environment happy-dom
//
// A radio module that reports both DUTY_CYCLE (the blocked flag) and
// DUTY_CYCLE_LEVEL (the load percentage) renders two adjacent cells. They
// must not share a caption: one says "blocked / OK", the other a percentage,
// and an operator cannot tell them apart when both read "Duty cycle".
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor } from "@testing-library/svelte";

const mockListDataPoints = vi.fn();

vi.mock("$lib/api/client", () => ({
  api: {
    listDataPoints: (...args: unknown[]) => mockListDataPoints(...args),
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
}));

vi.mock("$lib/stores/events.svelte", () => ({
  onResync: () => () => {},
  subscribe: () => () => {},
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import MaintenanceStatusGrid from "./MaintenanceStatusGrid.svelte";

function labelTexts(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll("span"))
    .map((el) => el.textContent?.trim() ?? "")
    // Label cells render as "<label>:"; the value cell next to them carries
    // no colon, so the suffix separates captions from values.
    .filter((s) => s.startsWith("device.maintenance.") && s.endsWith(":"));
}

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => cleanup());

describe("MaintenanceStatusGrid — duty-cycle rows", () => {
  it("labels the blocked flag and the load percentage differently", async () => {
    mockListDataPoints.mockResolvedValue([
      { parameter: "DUTY_CYCLE", value: false },
      { parameter: "DUTY_CYCLE_LEVEL", value: 42 },
    ]);

    const { container } = render(MaintenanceStatusGrid, {
      props: { address: "VCU1150287" },
    });

    await waitFor(() => {
      expect(labelTexts(container).length).toBe(2);
    });

    const labels = labelTexts(container);
    expect(labels).toContain("device.maintenance.duty_cycle:");
    expect(labels).toContain("device.maintenance.duty_cycle_level:");
  });
});
