import type {
  CentralPairingRequest,
  CentralPairingStarted,
  CentralPairingStatus,
} from "$lib/api/client";

/**
 * Client pairing with an openccu-lite box, as a state machine the
 * onboarding form renders. A started pairing shows its code until the
 * box's administrator approves, rejects or lets it expire; the machine
 * long-polls the daemon meanwhile. The token an approval yields never
 * reaches the browser: what the form hands on is the pairing id, which
 * the central create or update names.
 */

export type PairingPhase =
  | "idle"
  | "starting"
  | "pending"
  | "approved"
  | "rejected"
  | "expired"
  | "error"
  | "cancelled";

/** The daemon calls the machine makes; injected so tests drive it. */
export type PairingApi = {
  start(req: CentralPairingRequest): Promise<CentralPairingStarted>;
  status(id: string, waitSeconds: number): Promise<CentralPairingStatus>;
  cancel(id: string): Promise<void>;
};

export type PairingOptions = {
  /** Long-poll window per status request, in seconds. */
  waitSeconds?: number;
  /**
   * Pause after a pending answer that came back early, so a daemon that
   * answers at once cannot turn the loop into a busy one.
   */
  minIntervalMs?: number;
  sleep?: (ms: number) => Promise<void>;
  now?: () => number;
};

function message(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

export function createPairing(api: PairingApi, opts: PairingOptions = {}) {
  const waitSeconds = opts.waitSeconds ?? 20;
  const minIntervalMs = opts.minIntervalMs ?? 1000;
  const sleep = opts.sleep ?? ((ms: number) => new Promise<void>((r) => setTimeout(r, ms)));
  const now = opts.now ?? (() => Date.now());

  let phase = $state<PairingPhase>("idle");
  let pairingId = $state("");
  let code = $state("");
  let fingerprint = $state("");
  let scopes = $state<string[]>([]);
  let error = $state("");
  // Every start, cancel and reset opens a new generation; a loop or an
  // answer from an older one is dropped, so a cancelled pairing can never
  // flip the form to approved.
  let generation = 0;

  async function poll(gen: number) {
    while (gen === generation && phase === "pending") {
      const began = now();
      let st: CentralPairingStatus;
      try {
        st = await api.status(pairingId, waitSeconds);
      } catch (err) {
        if (gen !== generation) return;
        phase = "error";
        error = message(err);
        return;
      }
      if (gen !== generation) return;
      if (st.state === "pending") {
        const elapsed = now() - began;
        if (elapsed < minIntervalMs) await sleep(minIntervalMs - elapsed);
        continue;
      }
      if (st.state === "approved") scopes = st.scopes ?? [];
      if (st.state === "error") error = st.error ?? "";
      phase = st.state;
    }
  }

  async function start(req: CentralPairingRequest) {
    const gen = ++generation;
    phase = "starting";
    pairingId = "";
    code = "";
    fingerprint = "";
    scopes = [];
    error = "";
    try {
      const started = await api.start(req);
      if (gen !== generation) {
        // Cancelled while the box was still being asked: withdraw it.
        void api.cancel(started.pairing_id).catch(() => {});
        return;
      }
      pairingId = started.pairing_id;
      code = started.code;
      fingerprint = started.fingerprint ?? "";
      phase = "pending";
    } catch (err) {
      if (gen !== generation) return;
      phase = "error";
      error = message(err);
      return;
    }
    await poll(gen);
  }

  async function cancel() {
    const id = pairingId;
    const wasOpen = phase === "pending";
    generation++;
    phase = "cancelled";
    if (wasOpen && id) {
      try {
        await api.cancel(id);
      } catch {
        // The box lets an abandoned request expire on its own.
      }
    }
  }

  function reset() {
    generation++;
    phase = "idle";
    pairingId = "";
    code = "";
    fingerprint = "";
    scopes = [];
    error = "";
  }

  return {
    get phase() {
      return phase;
    },
    get pairingId() {
      return pairingId;
    },
    get code() {
      return code;
    },
    get fingerprint() {
      return fingerprint;
    },
    get scopes() {
      return scopes;
    },
    get error() {
      return error;
    },
    /** The pairing id a central may name — only once the box approved. */
    get approvedId() {
      return phase === "approved" ? pairingId : "";
    },
    start,
    cancel,
    reset,
  };
}

export type Pairing = ReturnType<typeof createPairing>;
