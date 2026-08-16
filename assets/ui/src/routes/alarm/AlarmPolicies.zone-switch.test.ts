// @vitest-environment happy-dom
//
// The policy document is fetched per zone but saved against whatever zone the
// selector points at when Save is pressed, and the PUT replaces the target
// zone's name and whole config. Switching zones must therefore drop the
// previous zone's working copy immediately and discard a response that
// arrives after the operator has already moved on — otherwise zone A's code
// policy, schedules and re-arm rules overwrite zone B's.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor, screen, within } from "@testing-library/svelte";
import type { AlarmZone } from "$lib/api/types";

let mockZonesConfig: AlarmZone[] = [];
vi.mock("$lib/stores/alarmPanel.svelte", () => ({
  alarmPanelStore: {
    get zonesConfig() {
      return mockZonesConfig;
    },
    get loading() {
      return false;
    },
    get error() {
      return null;
    },
    refresh: vi.fn().mockResolvedValue(undefined),
  },
}));

const mockGetAlarmZone = vi.fn();
const mockPutAlarmZone = vi.fn();
vi.mock("$lib/api/client", () => ({
  api: {
    getAlarmZone: (...args: unknown[]) => mockGetAlarmZone(...args),
    putAlarmZone: (...args: unknown[]) => mockPutAlarmZone(...args),
  },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : "error"),
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));

const mockConfirmAsk = vi.fn();
vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: (...args: unknown[]) => mockConfirmAsk(...args) },
}));

vi.mock("$lib/i18n", () => ({ t: (key: string) => key }));

// The real Select wraps bits-ui's floating-portal listbox, which happy-dom
// cannot drive (see SelectStub.svelte).
vi.mock("$lib/components/ui/Select.svelte", async () => {
  const mod = await import("../__testutils__/SelectStub.svelte");
  return { default: mod.default };
});

import AlarmPolicies from "./AlarmPolicies.svelte";

function zone(id: string, name: string, config: Record<string, unknown> = {}) {
  return { id, name, position: 1, config };
}

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

/** The zone selector is the first Select on the page. */
async function pickZone(label: string) {
  const zoneSelect = screen.getAllByRole("listbox")[0];
  await fireEvent.click(within(zoneSelect).getByText(label));
}

/** A schedule row is the cheapest edit that flips the view to dirty. */
async function makeDirty() {
  await fireEvent.click(screen.getByRole("button", { name: /alarm.policies.schedules.add/ }));
  await waitFor(() => expect(screen.getByText("common.modified")).toBeInTheDocument());
}

beforeEach(() => {
  vi.clearAllMocks();
  mockZonesConfig = [
    { id: "zone-a", name: "Ground floor" },
    { id: "zone-b", name: "Upper floor" },
  ] as AlarmZone[];
  mockPutAlarmZone.mockResolvedValue(undefined);
  mockConfirmAsk.mockResolvedValue(true);
});

afterEach(() => cleanup());

describe("AlarmPolicies — zone switch", () => {
  it("discards a document that arrives after the operator moved to another zone", async () => {
    const zoneA = deferred<ReturnType<typeof zone>>();
    const zoneB = deferred<ReturnType<typeof zone>>();
    mockGetAlarmZone.mockImplementation((id: string) =>
      id === "zone-a" ? zoneA.promise : zoneB.promise,
    );

    render(AlarmPolicies);
    await waitFor(() => expect(mockGetAlarmZone).toHaveBeenCalledWith("zone-a"));

    await pickZone("Upper floor");
    await waitFor(() => expect(mockGetAlarmZone).toHaveBeenCalledWith("zone-b"));

    zoneB.resolve(zone("zone-b", "Upper floor", { auto_rearm_s: 60 }));
    await waitFor(() =>
      expect(screen.getByDisplayValue("60")).toBeInTheDocument(),
    );

    // The zone the operator left answers late — it must not land.
    zoneA.resolve(zone("zone-a", "Ground floor", { auto_rearm_s: 900 }));
    await new Promise((r) => setTimeout(r, 0));

    expect(screen.getByDisplayValue("60")).toBeInTheDocument();
    expect(screen.queryByDisplayValue("900")).not.toBeInTheDocument();
  });

  it("drops the previous zone's edited document instead of offering it for save", async () => {
    const zoneB = deferred<ReturnType<typeof zone>>();
    mockGetAlarmZone.mockImplementation((id: string) =>
      id === "zone-a"
        ? Promise.resolve(zone("zone-a", "Ground floor", { auto_rearm_s: 900 }))
        : zoneB.promise,
    );

    render(AlarmPolicies);
    await waitFor(() => expect(screen.getByDisplayValue("900")).toBeInTheDocument());
    await makeDirty();

    await pickZone("Upper floor");
    await waitFor(() => expect(mockGetAlarmZone).toHaveBeenCalledWith("zone-b"));

    // While zone B is still loading, neither its predecessor's document nor
    // the Save bar that would write it to zone B may remain on screen.
    expect(screen.queryByDisplayValue("900")).not.toBeInTheDocument();
    expect(screen.queryByText("common.modified")).not.toBeInTheDocument();

    zoneB.resolve(zone("zone-b", "Upper floor", { auto_rearm_s: 60 }));
    await waitFor(() => expect(screen.getByDisplayValue("60")).toBeInTheDocument());
    expect(screen.queryByText("common.modified")).not.toBeInTheDocument();
    expect(mockPutAlarmZone).not.toHaveBeenCalled();
  });

  it("keeps the unsaved document when the operator declines the discard prompt", async () => {
    mockGetAlarmZone.mockImplementation((id: string) =>
      Promise.resolve(
        id === "zone-a"
          ? zone("zone-a", "Ground floor", { auto_rearm_s: 900 })
          : zone("zone-b", "Upper floor", { auto_rearm_s: 60 }),
      ),
    );
    mockConfirmAsk.mockResolvedValue(false);

    render(AlarmPolicies);
    await waitFor(() => expect(screen.getByDisplayValue("900")).toBeInTheDocument());
    await makeDirty();

    await pickZone("Upper floor");
    await waitFor(() => expect(mockConfirmAsk).toHaveBeenCalledOnce());

    expect(mockGetAlarmZone).not.toHaveBeenCalledWith("zone-b");
    expect(screen.getByDisplayValue("900")).toBeInTheDocument();
    expect(screen.getByText("common.modified")).toBeInTheDocument();
  });
});
