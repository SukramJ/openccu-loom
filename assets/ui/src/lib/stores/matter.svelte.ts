// Runed store for the Matter bridge feature. Manages status,
// exposures, fabrics, commissioning window state and the dirty
// set for unsaved exposure changes.

import { api, ApiError } from "$lib/api/client";
import { subscribe } from "./events.svelte";
import { centralStore } from "./centrals.svelte";
import { dirty } from "./dirty.svelte";
import { toastStore } from "./toast.svelte";
import { t } from "$lib/i18n";
import type {
  MatterCommissioningWindow,
  MatterExposure,
  MatterExposureUpdate,
  MatterFabric,
  MatterStatus,
} from "$lib/api/matter-types";
import type { EventEnvelope } from "$lib/api/types";

// Dirty key for the global unsaved-changes guard.
const DIRTY_KEY = "matter:exposures";

export type WindowPhase = "idle" | "open" | "success";

export type CommissioningState = {
  phase: WindowPhase;
  window: MatterCommissioningWindow | null;
  /** seconds remaining; null when window is closed */
  remaining: number | null;
  /** fabric label received via WS */
  addedFabricLabel: string | null;
};

function createMatterStore() {
  let status = $state<MatterStatus | null>(null);
  let statusError = $state<string | null>(null);
  let statusLoading = $state(false);
  // True when a 503/null status is caused by no CCU having finished its
  // readiness-gated bring-up yet, rather than the bridge being disabled
  // by config. Lets the view show "waiting for CCU" instead of the
  // terminal "disabled" state.
  let waitingForCcu = $state(false);

  let exposures = $state<MatterExposure[]>([]);
  let exposuresLoading = $state(false);
  let exposuresError = $state<string | null>(null);

  let fabrics = $state<MatterFabric[]>([]);
  let fabricsLoading = $state(false);
  let fabricsError = $state<string | null>(null);

  // Dirty set: map of composite key → pending update.
  // Key = `${central_name}|${device_address}|${channel_no}|${dp_kind}|${dp_key}`
  let pendingUpdates = $state<Map<string, MatterExposureUpdate>>(new Map());

  let commissioning = $state<CommissioningState>({
    phase: "idle",
    window: null,
    remaining: null,
    addedFabricLabel: null,
  });

  let unsub: (() => void) | null = null;
  let countdownTimer: ReturnType<typeof setInterval> | null = null;
  let exposuresReloadTimer: ReturnType<typeof setTimeout> | null = null;

  // The daemon emits one matter.exposable_changed frame per affected
  // allowlist row, so saving a select-all change arrives here as a burst
  // of hundreds. Collapse the burst into a single refetch of the list.
  function scheduleExposuresReload(): void {
    if (exposuresReloadTimer) clearTimeout(exposuresReloadTimer);
    exposuresReloadTimer = setTimeout(() => {
      exposuresReloadTimer = null;
      void loadExposures();
    }, 300);
  }

  function exposureKey(e: {
    central_name: string;
    device_address: string;
    channel_no: number;
    dp_kind: string;
    dp_key: string;
  }): string {
    return `${e.central_name}|${e.device_address}|${e.channel_no}|${e.dp_kind}|${e.dp_key}`;
  }

  function applyWsEvent(ev: EventEnvelope) {
    switch (ev.type) {
      case "matter.fabric_added": {
        const p = ev.payload as { label?: string };
        commissioning = {
          ...commissioning,
          phase: "success",
          addedFabricLabel: p.label ?? null,
        };
        stopCountdown();
        void loadFabrics();
        void loadStatus();
        break;
      }
      case "matter.fabric_removed": {
        void loadFabrics();
        void loadStatus();
        break;
      }
      case "matter.commissioning_window_opened": {
        void loadStatus();
        break;
      }
      case "matter.endpoint_assembled": {
        void loadStatus();
        break;
      }
      case "matter.exposable_changed": {
        scheduleExposuresReload();
        break;
      }
      case "matter.commissioning_progress": {
        // Localize the operator-close notice off the `stage` token rather
        // than the server-supplied English `message` (mirrors
        // MatterCommissioningClose in
        // internal/north/rest/handlers/matter_exposures.go, which fires
        // {stage: "closed"} after POST /matter/commissioning/window/close).
        // Only reacts while our own window is open — a stray "closed" for
        // a window this tab never saw open should not disturb the success
        // step.
        const p = ev.payload as { stage?: string };
        if (p.stage === "closed" && commissioning.phase === "open") {
          stopCountdown();
          commissioning = {
            phase: "idle",
            window: null,
            remaining: null,
            addedFabricLabel: null,
          };
          toastStore.info(t("matter.commissioning.closed"));
        }
        break;
      }
    }
  }

  function ensureStream() {
    if (unsub) return;
    unsub = subscribe(applyWsEvent);
  }

  function stopCountdown() {
    if (countdownTimer !== null) {
      clearInterval(countdownTimer);
      countdownTimer = null;
    }
  }

  function startCountdown(durationSeconds: number) {
    stopCountdown();
    let remaining = durationSeconds;
    commissioning = { ...commissioning, remaining };
    countdownTimer = setInterval(() => {
      remaining -= 1;
      commissioning = { ...commissioning, remaining };
      if (remaining <= 0) {
        stopCountdown();
        // Auto-close: backend already closed it; update phase if not succeeded.
        if (commissioning.phase === "open") {
          commissioning = {
            phase: "idle",
            window: null,
            remaining: null,
            addedFabricLabel: null,
          };
        }
      }
    }, 1000);
  }

  async function loadStatus() {
    statusLoading = true;
    statusError = null;
    try {
      status = await api.matterStatus();
      waitingForCcu = false;
    } catch (err) {
      if (err instanceof ApiError && err.status === 503) {
        // A 503 has two causes: the bridge is disabled by config, or no
        // CCU has finished its readiness-gated bring-up yet. Distinguish
        // them off the fleet so the view can show "waiting for CCU"
        // instead of the terminal "disabled" state.
        status = null;
        statusError = null;
        waitingForCcu = !centralStore.anyReady;
      } else {
        statusError = err instanceof Error ? err.message : String(err);
        waitingForCcu = false;
      }
    } finally {
      statusLoading = false;
    }
  }

  async function loadExposures() {
    exposuresLoading = true;
    exposuresError = null;
    try {
      const resp = await api.matterExposable();
      exposures = resp.items;
    } catch (err) {
      exposuresError = err instanceof Error ? err.message : String(err);
    } finally {
      exposuresLoading = false;
    }
  }

  async function loadFabrics() {
    fabricsLoading = true;
    fabricsError = null;
    try {
      const resp = await api.matterFabrics();
      fabrics = resp.fabrics;
    } catch (err) {
      fabricsError = err instanceof Error ? err.message : String(err);
    } finally {
      fabricsLoading = false;
    }
  }

  function markDirty(exposure: MatterExposure, patch: Partial<MatterExposureUpdate>) {
    const key = exposureKey(exposure);
    const existing = pendingUpdates.get(key);
    const base: MatterExposureUpdate = existing ?? {
      central_name: exposure.central_name,
      device_address: exposure.device_address,
      channel_no: exposure.channel_no,
      dp_kind: exposure.dp_kind,
      dp_key: exposure.dp_key,
      enabled: exposure.enabled,
      friendly_name: exposure.friendly_name,
    };
    const updated: MatterExposureUpdate = { ...base, ...patch };
    const next = new Map(pendingUpdates);
    next.set(key, updated);
    pendingUpdates = next;
    // The pending map is module state and outlives the exposure list, so
    // the leave-confirm needs a way to actually drop it.
    dirty.set(DIRTY_KEY, true, discardDirty);
  }

  function discardDirty() {
    pendingUpdates = new Map();
    dirty.clear(DIRTY_KEY);
  }

  async function saveBulk(): Promise<number> {
    const items = Array.from(pendingUpdates.values());
    if (items.length === 0) return 0;
    const resp = await api.bulkMatterExposure({ items });
    pendingUpdates = new Map();
    dirty.clear(DIRTY_KEY);
    await loadExposures();
    return resp.applied;
  }

  async function openWindow(durationSeconds: number) {
    const win = await api.openCommissioningWindow(durationSeconds);
    commissioning = {
      phase: "open",
      window: win,
      remaining: durationSeconds,
      addedFabricLabel: null,
    };
    startCountdown(durationSeconds);
  }

  // hydrateCommissioning re-syncs the local pairing state from the
  // daemon. Called on Pair-tab mount so a refresh / tab switch /
  // out-of-band `POST /matter/commissioning/window` (e.g. via curl)
  // recovers a fresh QR + manual code instead of the SPA showing the
  // empty "open window" form. The daemon's setup-payload endpoint is
  // the single source of truth for the active discriminator/passcode/
  // QR; the status endpoint reports whether a window is open.
  async function hydrateCommissioning() {
    if (commissioning.phase !== "idle") return; // user-driven flow wins.
    let st = status;
    if (st === null) {
      try {
        st = await api.matterStatus();
        status = st;
      } catch {
        return; // bridge disabled — keep UI in idle.
      }
    }
    if (!st || !st.commissioning_window_open) return;
    let payload;
    try {
      payload = await api.matterSetupPayload();
    } catch {
      return;
    }
    const duration = st.commissioning_window_duration_seconds || 0;
    commissioning = {
      phase: "open",
      window: {
        discriminator: payload.discriminator,
        passcode: payload.passcode,
        duration_seconds: duration,
        qr_code: payload.qr_code,
        manual_code: payload.manual_code,
      },
      // Without a server-side "remaining" value we display the full
      // duration as upper bound. Better than no countdown at all; the
      // daemon will fire matter.commissioning_window_opened on real
      // transitions which the WS handler can refine later.
      remaining: duration > 0 ? duration : null,
      addedFabricLabel: null,
    };
    if (duration > 0) startCountdown(duration);
  }

  async function closeWindow() {
    await api.closeCommissioningWindow();
    stopCountdown();
    commissioning = {
      phase: "idle",
      window: null,
      remaining: null,
      addedFabricLabel: null,
    };
  }

  function resetCommissioning() {
    stopCountdown();
    commissioning = {
      phase: "idle",
      window: null,
      remaining: null,
      addedFabricLabel: null,
    };
  }

  function close() {
    stopCountdown();
    if (exposuresReloadTimer) {
      clearTimeout(exposuresReloadTimer);
      exposuresReloadTimer = null;
    }
    unsub?.();
    unsub = null;
  }

  return {
    get status() { return status; },
    get statusError() { return statusError; },
    get statusLoading() { return statusLoading; },
    get waitingForCcu() { return waitingForCcu; },
    get exposures() { return exposures; },
    get exposuresLoading() { return exposuresLoading; },
    get exposuresError() { return exposuresError; },
    get fabrics() { return fabrics; },
    get fabricsLoading() { return fabricsLoading; },
    get fabricsError() { return fabricsError; },
    get pendingUpdates() { return pendingUpdates; },
    get commissioning() { return commissioning; },
    get hasDirty() { return pendingUpdates.size > 0; },
    exposureKey,
    loadStatus,
    loadExposures,
    loadFabrics,
    markDirty,
    discardDirty,
    saveBulk,
    openWindow,
    closeWindow,
    resetCommissioning,
    hydrateCommissioning,
    ensureStream,
    close,
  };
}

export const matterStore = createMatterStore();
