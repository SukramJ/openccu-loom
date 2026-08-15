import { SvelteMap } from "svelte/reactivity";
import { api } from "$lib/api/client";
import { subscribe } from "./events.svelte";
import type { EventEnvelope } from "$lib/api/types";

/**
 * Pending-message counters for the sidebar badge.
 *
 * The daemon already broadcasts `hub.service_message` / `hub.alarm_message`
 * whenever a central's list changes, each carrying that central's current
 * count — until now nothing consumed them, so an operator only learned
 * about a pending message by opening the message list. This store seeds the
 * counts from the hub snapshot and then keeps them live from the
 * broadcasts, so acknowledging a message anywhere (this tab, another tab,
 * the CCU WebUI) moves the badge without a reload.
 *
 * Counts are kept per central and summed. A broadcast only ever reports the
 * one central it came from, so a single shared total would let the last
 * central to speak overwrite every other. `GET /hub/data-points` returns
 * the same per-central shape, which is why it is the seed rather than the
 * flat message lists.
 *
 * Reference-counted like installModeStore: the sidebar holds one
 * subscription for the session and any view may add its own.
 */
function createMessagesStore() {
  // SvelteMap, not `$state(new Map())`: the rune's proxy passes a Map through
  // untouched, so an in-place `set()` invalidates nothing and a view reading
  // the totals below paints once at mount and never again.
  const serviceByCentral = new SvelteMap<string, number>();
  const alarmByCentral = new SvelteMap<string, number>();
  let seeded = $state(false);

  let unsub: (() => void) | null = null;
  let listeners = 0;
  // Centrals whose live broadcast already landed. A seed that resolves
  // after the first broadcast must not roll those back to the snapshot
  // value it read before the change.
  const live = new Set<string>();

  function sum(m: ReadonlyMap<string, number>): number {
    let total = 0;
    for (const n of m.values()) total += n;
    return total;
  }

  async function refresh(): Promise<void> {
    try {
      const snapshot = await api.getHubDataPoints();
      for (const entry of snapshot) {
        const central = entry.central ?? "";
        if (live.has(central)) continue;
        alarmByCentral.set(central, entry.alarm_messages?.value ?? 0);
        serviceByCentral.set(central, entry.service_messages?.value ?? 0);
      }
      seeded = true;
    } catch {
      // A failed seed must not break navigation: the badge stays at its
      // previous value until a broadcast or the next seed lands.
    }
  }

  function applyEvent(ev: EventEnvelope): void {
    const target =
      ev.type === "hub.service_message"
        ? serviceByCentral
        : ev.type === "hub.alarm_message"
          ? alarmByCentral
          : null;
    if (!target) return;
    const payload = ev.payload as { central?: string; count?: number } | null;
    const central = payload?.central ?? "";
    target.set(central, typeof payload?.count === "number" ? payload.count : 0);
    live.add(central);
  }

  function ensureStream(): void {
    listeners += 1;
    if (!unsub) unsub = subscribe(applyEvent);
    if (!seeded) void refresh();
  }

  function release(): void {
    listeners = Math.max(0, listeners - 1);
    if (listeners === 0 && unsub) {
      unsub();
      unsub = null;
    }
  }

  return {
    get serviceCount() {
      return sum(serviceByCentral);
    },
    get alarmCount() {
      return sum(alarmByCentral);
    },
    get total() {
      return sum(serviceByCentral) + sum(alarmByCentral);
    },
    get seeded() {
      return seeded;
    },
    refresh,
    ensureStream,
    release,
  };
}

export const messagesStore = createMessagesStore();
