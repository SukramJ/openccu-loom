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

const { subscribers } = vi.hoisted(() => ({
  subscribers: [] as ((env: { type: string; payload: unknown }) => void)[],
}));

vi.mock("$lib/stores/events.svelte", () => ({
  onResync: () => () => {},
  subscribe: (fn: (env: { type: string; payload: unknown }) => void) => {
    subscribers.push(fn);
    return () => {};
  },
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

// Live events patch the grid only for this device's own :0 channel — never
// for a sibling device whose address merely starts with this one.
describe("MaintenanceStatusGrid — live event filter", () => {
  function push(channel_address: string, value: unknown) {
    for (const fn of subscribers) {
      fn({ type: "data_point", payload: { channel_address, parameter: "CONFIG_PENDING", value } });
    }
  }

  it("ignores a sibling device's :0 and applies its own", async () => {
    subscribers.length = 0;
    mockListDataPoints.mockResolvedValue([{ parameter: "CONFIG_PENDING", value: false }]);
    const { container } = render(MaintenanceStatusGrid, {
      props: { address: "ABC1", pushesConfigPending: true },
    });
    await waitFor(() => expect(container.textContent).toContain("common.no"));

    push("ABC12:0", true);
    await Promise.resolve();
    expect(container.textContent).toContain("common.no");
    expect(container.textContent).not.toContain("common.yes");

    push("ABC1:0", true);
    await waitFor(() => expect(container.textContent).toContain("common.yes"));
  });
});

// A set CONFIG_PENDING means different things per interface: on HmIP (which
// pushes it reliably) a flag that stays set is a transfer not getting
// through, on BidCos it is the normal wake-up queue of a battery device.
describe("MaintenanceStatusGrid — CONFIG_PENDING explanation", () => {
  function hint(): string | null {
    return document.querySelector('[data-testid="config-pending-hint"]')?.textContent?.trim() ?? null;
  }

  it("points at repair on an interface that pushes CONFIG_PENDING", async () => {
    mockListDataPoints.mockResolvedValue([{ parameter: "CONFIG_PENDING", value: true }]);
    render(MaintenanceStatusGrid, { props: { address: "HMIP1", pushesConfigPending: true } });
    await waitFor(() => expect(hint()).toBe("device.config_pending.hint_reliable"));
  });

  it("explains the wake-up queue elsewhere", async () => {
    mockListDataPoints.mockResolvedValue([{ parameter: "CONFIG_PENDING", value: true }]);
    render(MaintenanceStatusGrid, { props: { address: "BIDCOS1", pushesConfigPending: false } });
    await waitFor(() => expect(hint()).toBe("device.config_pending.hint_queued"));
  });

  it("says nothing while CONFIG_PENDING is clear", async () => {
    mockListDataPoints.mockResolvedValue([{ parameter: "CONFIG_PENDING", value: false }]);
    const { container } = render(MaintenanceStatusGrid, {
      props: { address: "HMIP1", pushesConfigPending: true },
    });
    await waitFor(() => expect(container.textContent).toContain("device.maintenance.config_pending"));
    expect(hint()).toBeNull();
  });
});
