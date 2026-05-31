// Per-device maintenance store. Tracks the values of the well-known
// `:0`-channel data points (UNREACH, LOW_BAT, DUTY_CYCLE,
// CONFIG_PENDING, RSSI_DEVICE) so the DeviceList can render compact
// status badges without N+1 API calls.
//
// The store does NOT bulk-fetch on mount. Instead, it lazily mirrors
// every `data_point` envelope arriving on the WS bus that targets a
// `:0` channel — the daemon publishes each MAINT-DP as it changes,
// which means after a short warm-up the store has fresh values for
// every reachable device, and it stays live afterwards.
//
// Devices we have not yet seen carry the value `undefined` in the
// returned record; the UI treats that as "unknown" and shows neither
// a positive nor a negative pill.

import { subscribe } from "./events.svelte";
import type { DataPointChangedEvent } from "$lib/api/types";

const TRACKED = new Set([
  "UNREACH",
  "LOW_BAT",
  "DUTY_CYCLE",
  "DUTY_CYCLE_LEVEL",
  "CARRIER_SENSE_LEVEL",
  "CONFIG_PENDING",
  "UPDATE_PENDING",
  "RSSI_DEVICE",
]);

type DeviceMaintenance = Record<string, unknown>;

const state = $state<{ byDevice: Record<string, DeviceMaintenance> }>({
  byDevice: {},
});

// Per-device "settled" callback list — fires when CONFIG_PENDING (and
// UPDATE_PENDING) transitions from a truthy state to a falsy one. This
// is the moment a queued MASTER write actually landed on the device,
// so any panel that displays MASTER (or post-write VALUES) wants to
// re-fetch its data.
const settledHandlers = new Map<string, Set<() => void>>();

let bound = false;

function ensureBound() {
  if (bound) return;
  bound = true;
  subscribe((env) => {
    if (env.type !== "data_point") return;
    // The discriminated union narrows to the data_point branch but
    // the cast keeps payload typing tight under noUncheckedIndexedAccess.
    const p = env.payload as DataPointChangedEvent;
    if (!p.channel_address?.endsWith(":0")) return;
    if (!TRACKED.has(p.parameter)) return;
    const addr = p.channel_address.slice(0, -2);
    const prev = state.byDevice[addr] ?? {};
    const previousValue = prev[p.parameter];
    const next = { ...state.byDevice };
    next[addr] = { ...prev, [p.parameter]: p.value };
    state.byDevice = next;
    // Detect a true→false transition on CONFIG_PENDING / UPDATE_PENDING
    // and fan-out to subscribers. Only fire the first time we observe
    // the transition (prev was truthy AND next is falsy).
    if (
      (p.parameter === "CONFIG_PENDING" || p.parameter === "UPDATE_PENDING") &&
      Boolean(previousValue) === true &&
      Boolean(p.value) === false
    ) {
      const handlers = settledHandlers.get(addr);
      if (handlers) {
        for (const fn of handlers) fn();
      }
    }
  });
}

export const maintenanceStore = {
  /**
   * Returns the maintenance map for `address` (well-known DP names →
   * latest observed values). Returns `null` when nothing has been
   * observed yet; the UI then renders no badge instead of a stale or
   * misleading one.
   */
  get(address: string): DeviceMaintenance | null {
    ensureBound();
    return state.byDevice[address] ?? null;
  },
  /**
   * Reactive accessor — Svelte 5 components read this in a $derived
   * to opt into change-tracking on the underlying rune object.
   */
  bind(): void {
    ensureBound();
  },
  /**
   * Direct read of the rune state for components that want to derive
   * over the full set in one go.
   */
  all(): Record<string, DeviceMaintenance> {
    ensureBound();
    return state.byDevice;
  },
  /**
   * True iff CONFIG_PENDING or UPDATE_PENDING is currently set on the
   * device. The header badge reads this; SensorChannelList /
   * ChannelPanel render a "scheduled" notice based on the same value.
   */
  isPending(address: string): boolean {
    ensureBound();
    const m = state.byDevice[address];
    if (!m) return false;
    return Boolean(m.CONFIG_PENDING) || Boolean(m.UPDATE_PENDING);
  },
  /**
   * Register a callback that fires when CONFIG_PENDING / UPDATE_PENDING
   * for `address` transitions from true to false — the moment a
   * queued MASTER write actually flushes to the device. Returns an
   * idempotent unsubscribe closure.
   */
  onSettled(address: string, fn: () => void): () => void {
    ensureBound();
    let set = settledHandlers.get(address);
    if (!set) {
      set = new Set();
      settledHandlers.set(address, set);
    }
    set.add(fn);
    return () => {
      const s = settledHandlers.get(address);
      if (!s) return;
      s.delete(fn);
      if (s.size === 0) settledHandlers.delete(address);
    };
  },
};
