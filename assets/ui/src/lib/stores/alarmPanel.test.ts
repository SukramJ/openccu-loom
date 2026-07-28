// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// The alarm panel store is a module-level singleton (mirrors devices.svelte.ts
// / devices.test.ts): mock its collaborators, capture the WS handler that
// ensureStream() registers, then drive applyEvent by invoking it directly —
// exactly how the real events pump would.

let capturedHandler: ((ev: { type: string; payload?: unknown }) => void) | null =
  null;

vi.mock("$lib/stores/events.svelte", () => ({
  subscribe: vi.fn((h: (ev: { type: string; payload?: unknown }) => void) => {
    capturedHandler = h;
    return vi.fn();
  }),
}));

vi.mock("$lib/stores/auth.svelte", () => ({
  authStore: { probe: vi.fn() },
}));

const mockToastError = vi.fn();
vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    success: vi.fn(),
    error: (...args: unknown[]) => mockToastError(...args),
  },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

vi.mock("$lib/api/client", () => ({
  api: {
    getAlarmState: vi.fn(),
    listAlarmZones: vi.fn(),
    listAlarmJournal: vi.fn(),
    armAlarmZone: vi.fn(),
    disarmAlarmZone: vi.fn(),
    silenceAlarmZone: vi.fn(),
    silenceAllAlarmZones: vi.fn(),
    acknowledgeAlarmZone: vi.fn(),
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
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : "error"),
}));

import { api } from "$lib/api/client";
import { alarmPanelStore } from "./alarmPanel.svelte";
import type { AlarmZoneStatus } from "$lib/api/types";

function zone(overrides: Partial<AlarmZoneStatus> = {}): AlarmZoneStatus {
  return {
    id: "zone-1",
    name: "Ground floor",
    state: "disarmed",
    walktest_active: false,
    ...overrides,
  };
}

const getAlarmStateMock = api.getAlarmState as ReturnType<typeof vi.fn>;
const listAlarmZonesMock = api.listAlarmZones as ReturnType<typeof vi.fn>;
const listAlarmJournalMock = api.listAlarmJournal as ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.clearAllMocks();
  capturedHandler = null;
  getAlarmStateMock.mockResolvedValue({ zones: [zone()] });
  listAlarmZonesMock.mockResolvedValue([{ id: "zone-1", name: "Ground floor" }]);
  listAlarmJournalMock.mockResolvedValue([]);
});

afterEach(() => {
  alarmPanelStore.close();
});

async function seedAndSubscribe() {
  await alarmPanelStore.refresh();
  alarmPanelStore.ensureStream();
  expect(capturedHandler).not.toBeNull();
  return capturedHandler!;
}

describe("alarmPanelStore.refresh", () => {
  it("seeds zones/readiness/countdowns from GET /alarm/state", async () => {
    getAlarmStateMock.mockResolvedValueOnce({
      zones: [
        zone({
          readiness: { perimeter: { ready: true } },
          countdown: { kind: "exit_delay", remaining_s: 12, total_s: 30 },
        }),
      ],
    });
    await alarmPanelStore.refresh();
    expect(alarmPanelStore.zones).toHaveLength(1);
    expect(alarmPanelStore.readiness["zone-1"].perimeter.ready).toBe(true);
    expect(alarmPanelStore.countdowns["zone-1"]).toEqual({
      kind: "exit_delay",
      remaining_s: 12,
      total_s: 30,
    });
  });
});

describe("alarmPanelStore.applyEvent — alarm.state_changed", () => {
  it("updates the matching zone's state + mode and clears a stale countdown", async () => {
    getAlarmStateMock.mockResolvedValueOnce({
      zones: [
        zone({
          state: "arming",
          mode: "full",
          countdown: { kind: "exit_delay", remaining_s: 5, total_s: 30 },
        }),
      ],
    });
    const handler = await seedAndSubscribe();
    expect(alarmPanelStore.countdowns["zone-1"]).toBeDefined();

    handler({
      type: "alarm.state_changed",
      payload: {
        zone_id: "zone-1",
        zone_name: "Ground floor",
        old_state: "arming",
        new_state: "armed",
        mode: "full",
      },
    });

    expect(alarmPanelStore.zones[0].state).toBe("armed");
    expect(alarmPanelStore.zones[0].mode).toBe("full");
    // armed is neither "arming" nor "pending" → the running countdown is dropped.
    expect(alarmPanelStore.countdowns["zone-1"]).toBeUndefined();
  });
});

describe("alarmPanelStore.applyEvent — alarm.countdown", () => {
  it("seats the countdown for the zone", async () => {
    const handler = await seedAndSubscribe();
    handler({
      type: "alarm.countdown",
      payload: { zone_id: "zone-1", kind: "entry_delay", remaining_s: 8, total_s: 15 },
    });
    expect(alarmPanelStore.countdowns["zone-1"]).toEqual({
      kind: "entry_delay",
      remaining_s: 8,
      total_s: 15,
    });
  });
});

describe("alarmPanelStore.applyEvent — alarm.readiness_changed", () => {
  it("replaces the per-mode readiness map for the zone", async () => {
    const handler = await seedAndSubscribe();
    handler({
      type: "alarm.readiness_changed",
      payload: {
        zone_id: "zone-1",
        readiness: { full: { ready: false, blockers: ["sensor-9"] } },
      },
    });
    expect(alarmPanelStore.readiness["zone-1"].full.ready).toBe(false);
    expect(alarmPanelStore.readiness["zone-1"].full.blockers).toEqual(["sensor-9"]);
    expect(alarmPanelStore.zones[0].readiness?.full.ready).toBe(false);
  });
});

describe("alarmPanelStore.applyEvent — alarm.triggered", () => {
  it("flips the zone to triggered and opens an unsilenced incident", async () => {
    const handler = await seedAndSubscribe();
    handler({
      type: "alarm.triggered",
      payload: {
        zone_id: "zone-1",
        zone_name: "Ground floor",
        incident_id: 42,
        cause: "sensor",
        mode: "full",
      },
    });
    expect(alarmPanelStore.zones[0].state).toBe("triggered");
    expect(alarmPanelStore.zones[0].mode).toBe("full");
    expect(alarmPanelStore.zones[0].incident).toMatchObject({ id: "42", silenced: false });
    // The live broadcast's cause survives into the incident so the
    // triggered surface can name the sensor without a refresh.
    expect(alarmPanelStore.zones[0].incident?.cause).toBe("sensor");
  });
});

describe("alarmPanelStore.applyEvent — alarm.journal_appended", () => {
  it("prepends a synthesized entry to the live journal buffer", async () => {
    const handler = await seedAndSubscribe();
    expect(alarmPanelStore.journal).toHaveLength(0);
    handler({
      type: "alarm.journal_appended",
      payload: {
        entry_id: 7,
        zone_id: "zone-1",
        class: "arm",
        event: "armed",
        actor: "admin",
      },
    });
    expect(alarmPanelStore.journal).toHaveLength(1);
    expect(alarmPanelStore.journal[0]).toMatchObject({
      id: 7,
      zone_id: "zone-1",
      class: "arm",
      event: "armed",
      actor: "admin",
    });
  });
});

describe("alarmPanelStore.applyEvent — alarm.walktest_progress", () => {
  it("records the seen/total counter for the zone", async () => {
    const handler = await seedAndSubscribe();
    handler({
      type: "alarm.walktest_progress",
      payload: { zone_id: "zone-1", sensor_id: "s1", seen: 2, total: 5 },
    });
    expect(alarmPanelStore.walktest["zone-1"]).toEqual({ seen: 2, total: 5 });
  });
});

describe("alarmPanelStore.applyEvent — alarm.health_changed", () => {
  it("replaces the health snapshot", async () => {
    const handler = await seedAndSubscribe();
    expect(alarmPanelStore.health.healthy).toBe(true);
    handler({
      type: "alarm.health_changed",
      payload: { healthy: false, note: "siren stop-verification failed" },
    });
    expect(alarmPanelStore.health).toEqual({
      healthy: false,
      note: "siren stop-verification failed",
    });
  });
});

describe("alarmPanelStore verbs — failure surfaces a toast, never throws", () => {
  it("silence() swallows a rejected API call into a toast and returns false", async () => {
    (api.silenceAlarmZone as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new Error("central unreachable"),
    );
    const ok = await alarmPanelStore.silence("zone-1");
    expect(ok).toBe(false);
    expect(mockToastError).toHaveBeenCalledOnce();
  });
});
