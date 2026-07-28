import { api, friendlyError } from "$lib/api/client";
import { t } from "$lib/i18n";
import type { AlarmOutput, AlarmOutputCandidate, AlarmSensor } from "$lib/api/types";

// §4 mode order — shared with every other alarm surface so the delay
// table and the mode chips read identically everywhere.
const MODE_ORDER = ["perimeter", "full", "night", "vacation", "custom"] as const;
export type AlarmWizardMode = (typeof MODE_ORDER)[number];
export { MODE_ORDER as ALARM_WIZARD_MODE_ORDER };

export type AlarmWizardDelayField = "exit" | "entry" | "trigger";
export type AlarmWizardDelays = Record<AlarmWizardMode, Record<AlarmWizardDelayField, number>>;

// Defaults mirror internal/alarm/engine/config.go's ModeConfig field
// names (exit_delay_s / entry_delay_s / trigger_time_s) — 180s is also
// that package's DefaultTriggerSeconds.
const DEFAULT_DELAYS = { exit: 30, entry: 15, trigger: 180 } as const;

// The engine hard-caps one mode's trigger_time_s at MaxTriggerSeconds
// (internal/alarm/engine/config.go) — the delays step clamps to match
// so a wizard-created zone never ships with a value the engine would
// silently clamp on its own.
export const ALARM_WIZARD_MAX_TRIGGER_SECONDS = 600;

const TOTAL_STEPS = 6;

function defaultDelays(): AlarmWizardDelays {
  const out = {} as AlarmWizardDelays;
  for (const m of MODE_ORDER) out[m] = { ...DEFAULT_DELAYS };
  return out;
}

/**
 * Svelte 5 rune-based store for the alarm setup wizard (docs/alarm-
 * concept.md §12.3, pattern: alarmPanel.svelte.ts). A module singleton
 * holds every step's collected data, so navigating away from the
 * wizard route (e.g. to double-check an existing zone) and back
 * preserves progress instead of resetting on unmount. AlarmWizard.svelte
 * is a thin view over this store; `finish()`'s API orchestration and
 * the toast/redirect side effects stay in the component.
 */
function createAlarmWizardStore() {
  let step = $state(1);
  let zoneName = $state("");
  let delays = $state<AlarmWizardDelays>(defaultDelays());
  let selectedSensors = $state<AlarmSensor[]>([]);
  let selectedOutputs = $state<AlarmOutput[]>([]);
  // Set once finish() successfully creates the zone, so a retry after a
  // partial failure (e.g. the sensors PUT fails) updates the existing
  // zone instead of creating a duplicate.
  let createdZoneId = $state<string | null>(null);

  // Output-candidate cache, fetched once on first entry into the
  // outputs step and reused for the rest of the wizard session.
  let outputCandidates = $state<AlarmOutputCandidate[]>([]);
  let candidatesLoading = $state(false);
  let candidatesError = $state<string | null>(null);
  let candidatesLoaded = $state(false);

  function setStep(n: number) {
    step = Math.min(Math.max(n, 1), TOTAL_STEPS);
  }
  function back() {
    if (step > 1) step -= 1;
  }
  function next() {
    if (step < TOTAL_STEPS) step += 1;
  }
  // Skip clears whatever the current step would have collected back to
  // its safe default before advancing — every step is skippable (§12.3)
  // and "skip" means "keep the sensible default", not "leave it
  // half-filled".
  function skip() {
    if (step === 1) zoneName = "";
    if (step === 2) selectedSensors = [];
    if (step === 3) selectedOutputs = [];
    if (step === 4) delays = defaultDelays();
    next();
  }

  function setZoneName(name: string) {
    zoneName = name;
  }

  function setDelay(mode: AlarmWizardMode, field: AlarmWizardDelayField, raw: string) {
    let n = Math.max(0, Math.round(Number(raw)));
    if (Number.isNaN(n)) return;
    if (field === "trigger") n = Math.min(n, ALARM_WIZARD_MAX_TRIGGER_SECONDS);
    delays = { ...delays, [mode]: { ...delays[mode], [field]: n } };
  }
  function resetDelays() {
    delays = defaultDelays();
  }

  function hasSensor(id: string): boolean {
    return selectedSensors.some((s) => s.id === id);
  }
  function addSensor(sensor: AlarmSensor) {
    if (hasSensor(sensor.id)) return;
    selectedSensors = [...selectedSensors, sensor];
  }
  function removeSensor(id: string) {
    selectedSensors = selectedSensors.filter((s) => s.id !== id);
  }

  function hasOutput(id: string): boolean {
    return selectedOutputs.some((o) => o.id === id);
  }
  function addOutput(output: AlarmOutput) {
    if (hasOutput(output.id)) return;
    selectedOutputs = [...selectedOutputs, output];
  }
  function removeOutput(id: string) {
    selectedOutputs = selectedOutputs.filter((o) => o.id !== id);
  }

  function setCreatedZoneId(id: string | null) {
    createdZoneId = id;
  }

  async function loadOutputCandidates() {
    // Cached for the wizard session — a prior success is not refetched.
    // A prior FAILURE also blocks the auto path: the step-3 $effect
    // re-runs whenever the loading flag it (transitively) read flips
    // back to false, so without the error guard a failed fetch would
    // loop forever. Recovery is explicit via retryOutputCandidates().
    if (candidatesLoaded || candidatesLoading || candidatesError) return;
    candidatesLoading = true;
    try {
      outputCandidates = await api.listAlarmOutputCandidates();
      candidatesLoaded = true;
    } catch (err) {
      candidatesError = friendlyError(err, t);
    } finally {
      candidatesLoading = false;
    }
  }

  function retryOutputCandidates() {
    candidatesError = null;
    void loadOutputCandidates();
  }

  function reset() {
    step = 1;
    zoneName = "";
    delays = defaultDelays();
    selectedSensors = [];
    selectedOutputs = [];
    createdZoneId = null;
    // The output-candidate cache is a read-only capability list, not
    // per-run wizard data — leave it in place for the next run.
  }

  return {
    get step() {
      return step;
    },
    get zoneName() {
      return zoneName;
    },
    get delays() {
      return delays;
    },
    get selectedSensors() {
      return selectedSensors;
    },
    get selectedOutputs() {
      return selectedOutputs;
    },
    get createdZoneId() {
      return createdZoneId;
    },
    get outputCandidates() {
      return outputCandidates;
    },
    get candidatesLoading() {
      return candidatesLoading;
    },
    get candidatesError() {
      return candidatesError;
    },
    setStep,
    back,
    next,
    skip,
    setZoneName,
    setDelay,
    resetDelays,
    hasSensor,
    addSensor,
    removeSensor,
    hasOutput,
    addOutput,
    removeOutput,
    setCreatedZoneId,
    loadOutputCandidates,
    retryOutputCandidates,
    reset,
  };
}

export const alarmWizardStore = createAlarmWizardStore();
