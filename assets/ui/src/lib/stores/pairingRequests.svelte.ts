import { api, friendlyError } from "$lib/api/client";
import { t } from "$lib/i18n";
import { subscribe } from "./events.svelte";
import type { PairingView } from "$lib/api/types";
import type { EventEnvelope } from "$lib/api/types";

/**
 * Svelte 5 rune-based store for the pending client-token pairing
 * requests card (ADR 0076) that sits above the API-tokens list.
 * `refresh()` seeds from `GET /pairing-requests`; `ensureStream()` wires
 * the `pairing.requests_changed` WS broadcast so a request created,
 * approved or rejected from elsewhere (another admin tab) is reflected
 * here without a manual reload. Mirrors `addonUpdateStore` — one
 * singleton shared by the card.
 */
function createPairingRequestsStore() {
  let requests = $state<PairingView[]>([]);
  let loading = $state(false);
  let error = $state<string | null>(null);

  let unsub: (() => void) | null = null;

  async function refresh(): Promise<void> {
    loading = true;
    error = null;
    try {
      requests = await api.listPairingRequests();
    } catch (err) {
      error = friendlyError(err, t);
    } finally {
      loading = false;
    }
  }

  function ensureStream(): void {
    if (!unsub) unsub = subscribe(applyEvent);
  }

  function applyEvent(ev: EventEnvelope): void {
    if (ev.type !== "pairing.requests_changed") return;
    void refresh();
  }

  return {
    get requests() {
      return requests;
    },
    get loading() {
      return loading;
    },
    get error() {
      return error;
    },
    refresh,
    ensureStream,
    close() {
      unsub?.();
      unsub = null;
    },
  };
}

export const pairingRequestsStore = createPairingRequestsStore();
