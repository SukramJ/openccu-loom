import { api, ApiError, friendlyError } from "$lib/api/client";
import { t } from "$lib/i18n";
import { toastStore } from "./toast.svelte";
import { subscribe } from "./events.svelte";
import { authStore } from "./auth.svelte";
import type {
  AlarmArea,
  AlarmAreaStatus,
  AlarmArmAccepted,
  AlarmArmRequest,
  AlarmCountdownPayload,
  AlarmHealthChangedPayload,
  AlarmJournalAppendedPayload,
  AlarmJournalEntry,
  AlarmModeReadiness,
  AlarmReadinessChangedPayload,
  AlarmStateChangedPayload,
  AlarmTriggeredPayload,
  AlarmWalkTestProgressPayload,
  EventEnvelope,
} from "$lib/api/types";

// Client-side countdown cache. The engine ticks `alarm.countdown` once
// per second, but a dropped frame or a slow socket must not freeze the
// ring — so we decay `remaining_s` locally every second and let each WS
// tick re-seat the authoritative value (see the ticker below).
type Countdown = {
  kind: "exit_delay" | "entry_delay";
  remaining_s: number;
  total_s: number;
};

// Live walk-test progress counter, fed by `alarm.walktest_progress`.
type WalkTestProgress = { seen: number; total: number };

// Alarm-subsystem health, mirrored from `alarm.health_changed` and the
// per-area status snapshot. `note` is a stable English machine string.
type AlarmHealth = { healthy: boolean; note: string };

// Newest-first journal ring buffer size (docs/alarm-concept.md §12.5 —
// the panel keeps a short live tail; the Journal view re-fetches for
// the full, filterable history).
const JOURNAL_MAX = 200;

/**
 * Svelte 5 rune-based store for the alarm panel. One singleton shared by
 * every alarm view: the Overview reads `areas`/`countdowns`/`readiness`,
 * the Journal reads `journal`, the Walk-test view reads `walktest`, and
 * the health chip reads `health`. `ensureStream()` wires the WS pump and
 * the 1 s decay ticker; `close()` tears both down. Structure mirrors
 * devices.svelte.ts.
 */
function createAlarmPanelStore() {
  // Live per-area status (state machine + incident + countdown snapshot).
  // LiveAreaStatus widens the REST DTO: the live triggered broadcast
  // carries the cause and sensor name, which the snapshot endpoint
  // reconstructs from the journal instead — keep them when we have them.
  type LiveIncident = NonNullable<AlarmAreaStatus["incident"]> & {
    cause?: string;
    sensor_name?: string;
  };
  type LiveAreaStatus = Omit<AlarmAreaStatus, "incident"> & { incident?: LiveIncident };
  let areas = $state<LiveAreaStatus[]>([]);
  // Config-level area list (identity + ordering) — drives "no areas yet"
  // empty state + the wizard entry point.
  let areasConfig = $state<AlarmArea[]>([]);
  // Per-area, per-mode arm readiness, keyed areaId → mode → verdict.
  let readiness = $state<Record<string, Record<string, AlarmModeReadiness>>>(
    {},
  );
  // Running exit/entry countdowns, keyed by areaId; decayed locally.
  let countdowns = $state<Record<string, Countdown>>({});
  // Newest-first live journal tail (capped at JOURNAL_MAX).
  let journal = $state<AlarmJournalEntry[]>([]);
  // Live walk-test progress per area.
  let walktest = $state<Record<string, WalkTestProgress>>({});
  // Alarm-subsystem health. Defaults healthy until told otherwise.
  let health = $state<AlarmHealth>({ healthy: true, note: "" });

  let loading = $state(false);
  let error = $state<string | null>(null);
  let lastLoaded = $state<Date | null>(null);

  let unsub: (() => void) | null = null;
  let ticker: ReturnType<typeof setInterval> | null = null;

  function areaIndex(id: string): number {
    return areas.findIndex((a) => a.id === id);
  }

  async function refresh() {
    loading = true;
    error = null;
    try {
      const [state, config, entries] = await Promise.all([
        api.getAlarmState(),
        api.listAlarmAreas(),
        api.listAlarmJournal({ limit: JOURNAL_MAX }),
      ]);
      areas = state.areas;
      areasConfig = config;
      journal = entries.slice(0, JOURNAL_MAX);
      // Seed the derived maps from the authoritative status snapshot so
      // the first paint is correct before any WS frame arrives.
      const nextReadiness: Record<string, Record<string, AlarmModeReadiness>> =
        {};
      const nextCountdowns: Record<string, Countdown> = {};
      for (const a of state.areas) {
        if (a.readiness) nextReadiness[a.id] = a.readiness;
        if (
          a.countdown &&
          a.countdown.kind &&
          typeof a.countdown.remaining_s === "number" &&
          typeof a.countdown.total_s === "number"
        ) {
          nextCountdowns[a.id] = {
            kind: a.countdown.kind,
            remaining_s: a.countdown.remaining_s,
            total_s: a.countdown.total_s,
          };
        }
      }
      readiness = nextReadiness;
      countdowns = nextCountdowns;
      lastLoaded = new Date();
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        await authStore.probe();
        error = t("api.error.unauthorized");
      } else {
        error = friendlyError(err, t);
      }
    } finally {
      loading = false;
    }
  }

  function ensureStream() {
    if (!unsub) unsub = subscribe(applyEvent);
    if (!ticker) {
      // 1 s local decay so a running ring keeps moving between WS ticks.
      ticker = setInterval(tick, 1000);
    }
  }

  function tick() {
    let changed = false;
    for (const id of Object.keys(countdowns)) {
      const c = countdowns[id];
      const next = c.remaining_s - 1;
      if (next <= 0) {
        delete countdowns[id];
        changed = true;
      } else if (next !== c.remaining_s) {
        countdowns[id] = { ...c, remaining_s: next };
        changed = true;
      }
    }
    // Reassign only when nothing else already mutated a proxied member,
    // so the getter consumers re-run their $derived blocks.
    if (changed) countdowns = { ...countdowns };
  }

  function applyEvent(ev: EventEnvelope) {
    switch (ev.type) {
      case "alarm.state_changed": {
        const p = ev.payload as AlarmStateChangedPayload;
        const i = areaIndex(p.area_id);
        if (i >= 0) {
          areas[i] = { ...areas[i], state: p.new_state, mode: p.mode };
        }
        // Exit/entry countdown only lives while arming/pending; drop it
        // on any other transition so a stale ring cannot linger.
        if (p.new_state !== "arming" && p.new_state !== "pending") {
          if (countdowns[p.area_id]) {
            delete countdowns[p.area_id];
            countdowns = { ...countdowns };
          }
        }
        break;
      }
      case "alarm.countdown": {
        const p = ev.payload as AlarmCountdownPayload;
        countdowns = {
          ...countdowns,
          [p.area_id]: {
            kind: p.kind,
            remaining_s: p.remaining_s,
            total_s: p.total_s,
          },
        };
        break;
      }
      case "alarm.readiness_changed": {
        const p = ev.payload as AlarmReadinessChangedPayload;
        readiness = { ...readiness, [p.area_id]: p.readiness };
        const i = areaIndex(p.area_id);
        if (i >= 0) areas[i] = { ...areas[i], readiness: p.readiness };
        break;
      }
      case "alarm.triggered": {
        const p = ev.payload as AlarmTriggeredPayload;
        const i = areaIndex(p.area_id);
        if (i >= 0) {
          areas[i] = {
            ...areas[i],
            state: "triggered",
            mode: p.mode ?? areas[i].mode,
            incident: {
              id: String(p.incident_id),
              silenced: false,
              cause: p.cause,
              sensor_name: p.sensor_name,
            },
          };
        }
        break;
      }
      case "alarm.journal_appended": {
        const p = ev.payload as AlarmJournalAppendedPayload;
        if (p.event === "silenced" && p.area_id) {
          const i = areaIndex(p.area_id);
          const inc = i >= 0 ? areas[i].incident : undefined;
          if (i >= 0 && inc) {
            areas[i] = {
              ...areas[i],
              incident: { ...inc, silenced: true },
            };
          }
        }
        // The broadcast carries only the entry head; synthesize a
        // display row (the Journal view re-fetches for authoritative
        // detail). `when` is approximated with the receive time.
        const entry: AlarmJournalEntry = {
          id: p.entry_id,
          when: new Date().toISOString(),
          area_id: p.area_id ?? "",
          class: p.class,
          event: p.event,
          actor: p.actor,
          incident_id: p.incident_id,
        };
        journal = [entry, ...journal].slice(0, JOURNAL_MAX);
        break;
      }
      case "alarm.walktest_progress": {
        const p = ev.payload as AlarmWalkTestProgressPayload;
        walktest = {
          ...walktest,
          [p.area_id]: { seen: p.seen, total: p.total },
        };
        break;
      }
      case "alarm.health_changed": {
        const p = ev.payload as AlarmHealthChangedPayload;
        health = { healthy: p.healthy, note: p.note };
        break;
      }
      default:
        // Not an alarm frame — the shared pump multicasts every topic.
        break;
    }
  }

  // Control verbs. Each swallows failure into a toast and returns a
  // success flag — the UI must never be blocked by an alarm action, and
  // silence/disarm in particular are single-tap safety paths (S3/S6).
  async function arm(
    id: string,
    req: AlarmArmRequest,
  ): Promise<AlarmArmAccepted | null> {
    try {
      return await api.armAlarmArea(id, req);
    } catch (err) {
      toastStore.error(t("alarm.toast.arm_failed"), friendlyError(err, t));
      return null;
    }
  }

  async function disarm(id: string): Promise<boolean> {
    try {
      await api.disarmAlarmArea(id);
      return true;
    } catch (err) {
      toastStore.error(t("alarm.toast.disarm_failed"), friendlyError(err, t));
      return false;
    }
  }

  async function silence(id: string): Promise<boolean> {
    try {
      await api.silenceAlarmArea(id);
      return true;
    } catch (err) {
      toastStore.error(t("alarm.toast.silence_failed"), friendlyError(err, t));
      return false;
    }
  }

  async function silenceAll(): Promise<boolean> {
    try {
      await api.silenceAllAlarmAreas();
      return true;
    } catch (err) {
      toastStore.error(t("alarm.toast.silence_failed"), friendlyError(err, t));
      return false;
    }
  }

  async function acknowledge(id: string): Promise<boolean> {
    try {
      await api.acknowledgeAlarmArea(id);
      return true;
    } catch (err) {
      toastStore.error(t("alarm.toast.ack_failed"), friendlyError(err, t));
      return false;
    }
  }

  return {
    get areas() {
      return areas;
    },
    get areasConfig() {
      return areasConfig;
    },
    get readiness() {
      return readiness;
    },
    get countdowns() {
      return countdowns;
    },
    get journal() {
      return journal;
    },
    get walktest() {
      return walktest;
    },
    get health() {
      return health;
    },
    get loading() {
      return loading;
    },
    get error() {
      return error;
    },
    get lastLoaded() {
      return lastLoaded;
    },
    refresh,
    ensureStream,
    arm,
    disarm,
    silence,
    silenceAll,
    acknowledge,
    close() {
      unsub?.();
      unsub = null;
      if (ticker) {
        clearInterval(ticker);
        ticker = null;
      }
    },
  };
}

export const alarmPanelStore = createAlarmPanelStore();
