// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, fireEvent } from "@testing-library/svelte";
import type { SecuritySnapshot } from "$lib/api/types";

const mockGetSecuritySnapshot = vi.fn();
vi.mock("$lib/api/client", () => ({
  api: { getSecuritySnapshot: (...args: unknown[]) => mockGetSecuritySnapshot(...args) },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : "error"),
}));

vi.mock("$lib/stores/preferences.svelte", () => ({
  prefs: { locale: "en" },
}));

// The view refreshes off the daemon's security.* broadcasts, so the shared
// pump is replaced by a hand-driven one: `emit` plays the frame a real
// daemon would send.
let emit: ((ev: { type: string }) => void) | null = null;
vi.mock("$lib/stores/events.svelte", () => ({
  subscribe: (handler: (ev: { type: string }) => void) => {
    emit = handler;
    return () => {
      emit = null;
    };
  },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}:${JSON.stringify(vars)}` : key,
}));

import SecurityOverview from "./SecurityOverview.svelte";

function snapshot(overrides: Partial<SecuritySnapshot> = {}): SecuritySnapshot {
  return {
    severity: "ok",
    classes: [],
    engine_healthy: true,
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => cleanup());

describe("SecurityOverview — folded severity + classes", () => {
  it("renders the severity badge and one tile per hazard class", async () => {
    mockGetSecuritySnapshot.mockResolvedValue(
      snapshot({
        severity: "warning",
        classes: [
          {
            class: "smoke",
            active: true,
            known: 3,
            sources: [{ ref: "r1", name: "Kitchen smoke", at: "2026-07-01T00:00:00Z" }],
          },
          { class: "water", active: false, known: 1, sources: [] },
        ],
      }),
    );
    const { findByText, getByText } = render(SecurityOverview);

    await findByText("security.severity.warning");
    expect(getByText("security.class.smoke")).toBeTruthy();
    expect(getByText("security.class.water")).toBeTruthy();
    expect(getByText("Kitchen smoke")).toBeTruthy();
    expect(getByText("security.overview.class_inactive")).toBeTruthy();
  });

  it("shows the last alarm and last fault reports as plain rendered text", async () => {
    mockGetSecuritySnapshot.mockResolvedValue(
      snapshot({
        last_alarm: {
          class: "intrusion",
          severity: "alarm",
          verb: "triggered",
          subject: "Front door opened",
          message: "Front door opened at 10:00 while armed.",
          i18n_key: "x",
          at: "2026-07-01T10:00:00Z",
        },
        last_fault: {
          class: "smoke",
          severity: "warning",
          verb: "raised",
          subject: "Smoke detector unreachable",
          message: "Kitchen smoke detector has been unreachable since yesterday.",
          i18n_key: "y",
          at: "2026-07-01T09:00:00Z",
        },
      }),
    );
    const { findByText, getByText } = render(SecurityOverview);

    await findByText("Front door opened");
    expect(getByText("Front door opened at 10:00 while armed.")).toBeTruthy();
    expect(getByText("Smoke detector unreachable")).toBeTruthy();
    expect(getByText("Kitchen smoke detector has been unreachable since yesterday.")).toBeTruthy();
  });
});

describe("SecurityOverview — zones absence is a feature, not an error", () => {
  it("shows a plain explanation instead of an error/empty-list treatment when there are no zones", async () => {
    mockGetSecuritySnapshot.mockResolvedValue(
      snapshot({
        classes: [{ class: "smoke", active: false, known: 1, sources: [] }],
      }),
    );
    const { findByText, getByText } = render(SecurityOverview);

    await findByText("security.overview.zones_empty");
    expect(getByText("security.overview.zones_empty.description")).toBeTruthy();
  });
});

describe("SecurityOverview — fully empty installation", () => {
  it("renders the top-level empty state when nothing is classified at all", async () => {
    mockGetSecuritySnapshot.mockResolvedValue(snapshot());
    const { findByText } = render(SecurityOverview);

    await findByText("security.overview.empty");
  });
});

describe("SecurityOverview — load failure", () => {
  it("surfaces the error via ErrorState and retries on demand", async () => {
    mockGetSecuritySnapshot.mockRejectedValueOnce(new Error("rega down"));
    const { findByText, getByRole } = render(SecurityOverview);

    await findByText("common.error rega down");

    mockGetSecuritySnapshot.mockResolvedValueOnce(snapshot());
    await fireEvent.click(getByRole("button", { name: "common.reload" }));

    await waitFor(() => expect(mockGetSecuritySnapshot).toHaveBeenCalledTimes(2));
    await findByText("security.overview.empty");
  });
});

describe("SecurityOverview — live refresh off the daemon's push", () => {
  it("re-reads the snapshot when a security broadcast arrives", async () => {
    mockGetSecuritySnapshot.mockResolvedValue(
      snapshot({ classes: [{ class: "smoke", active: false, known: 1, sources: [] }] }),
    );
    const { findByText } = render(SecurityOverview);
    await findByText("security.severity.ok");

    // What a running smoke alarm produces: the class goes active and the
    // fold escalates. Without the push binding the badge stays "ok" until
    // someone reloads the page by hand.
    mockGetSecuritySnapshot.mockResolvedValue(
      snapshot({
        severity: "alarm",
        classes: [
          {
            class: "smoke",
            active: true,
            known: 1,
            sources: [{ ref: "r1", name: "Kitchen smoke", at: "2026-08-01T00:00:00Z" }],
          },
        ],
      }),
    );
    emit?.({ type: "security.class_changed" });

    await findByText("security.severity.alarm");
  });

  it("collapses a burst of broadcasts into one refetch", async () => {
    mockGetSecuritySnapshot.mockResolvedValue(snapshot());
    render(SecurityOverview);
    await waitFor(() => expect(mockGetSecuritySnapshot).toHaveBeenCalledTimes(1));

    // One physical hazard moves class, fold and notification at once.
    emit?.({ type: "security.class_changed" });
    emit?.({ type: "security.state_changed" });
    emit?.({ type: "security.notification" });

    await waitFor(() => expect(mockGetSecuritySnapshot).toHaveBeenCalledTimes(2));
    expect(mockGetSecuritySnapshot).toHaveBeenCalledTimes(2);
  });

  it("ignores broadcasts from other domains", async () => {
    mockGetSecuritySnapshot.mockResolvedValue(snapshot());
    render(SecurityOverview);
    await waitFor(() => expect(mockGetSecuritySnapshot).toHaveBeenCalledTimes(1));

    emit?.({ type: "datapoint.value_changed" });
    await new Promise((resolve) => setTimeout(resolve, 400));
    expect(mockGetSecuritySnapshot).toHaveBeenCalledTimes(1);
  });
});
