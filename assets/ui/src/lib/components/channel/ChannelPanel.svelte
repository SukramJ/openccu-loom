<script lang="ts">
  import type { UISchema } from "$lib/api/types";
  import { api, ApiError, friendlyError } from "$lib/api/client";
  import ParameterGrid from "./ParameterGrid.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import ProfileSelector from "./ProfileSelector.svelte";
  import SubsetGroupSelector from "./SubsetGroupSelector.svelte";
  import SecureTransmission from "./SecureTransmission.svelte";
  import {
    validateCrossRules,
    visibleParameters,
    type ParamValues,
  } from "$lib/channel/validate";
  import {
    canRedo,
    canUndo,
    emptyStack,
    entryFromPatch,
    pushEntry,
    redo,
    undo,
    type ChangeStackState,
  } from "$lib/channel/change-stack";
  import {
    coerceNumber,
    isBrightnessDataPoint,
    pickBrightnessReading,
  } from "$lib/channel/brightness-helper";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { notifyWakeupPending } from "$lib/links/wakeup-hint";
  import { subscribe } from "$lib/stores/events.svelte";
  import { maintenanceStore } from "$lib/stores/maintenance.svelte";
  import type { DataPointChangedEvent } from "$lib/api/types";
  import { dirty } from "$lib/stores/dirty.svelte";
  import SessionTimeoutWarning from "$lib/components/ui/SessionTimeoutWarning.svelte";
  import type { EditSessionResponse } from "$lib/api/types";
  import { t } from "$lib/i18n";

  type Props = {
    address: string;
    channel: number;
    /**
     * "VALUES" renders the runtime state (STATE, LEVEL, …) and writes
     * changes individually via `PUT .../data_points/{param}/value`.
     * "MASTER" renders channel configuration and writes the whole set
     * as a batch via `PUT .../paramsets/MASTER`.
     * "LINK" renders the per-peer direct-link configuration and
     * writes as a batch via `PUT .../link-paramsets/{peer}`. The
     * `peer` prop must be set for LINK; ignored otherwise.
     */
    paramset: "VALUES" | "MASTER" | "LINK";
    /** Peer channel address; required when paramset == LINK. */
    peer?: string;
    locale: string;
    /**
     * True when the device's interface delivers reliable CONFIG_PENDING
     * events on MASTER writes (HmIP-RF, HmIP-Wired). The SPA then
     * registers a `maintenanceStore.onSettled` listener and reloads
     * MASTER once CONFIG_PENDING goes true→false. On the other
     * interfaces (BidCos-*, VirtualDevices, CUxD) CONFIG_PENDING is
     * silent or unreliable, so the in-line `await load(...)` after
     * each save is the only refresh — that path already runs
     * unconditionally.
     */
    pushesConfigPending?: boolean;
    /**
     * Fired after every (re)load with the number of rendered parameters
     * and whether the load failed. Lets a parent decide whether to show
     * the panel at all — e.g. a LINK sender side that carries no
     * paramset for the given peer reports `{ count: 0 }` and can be
     * hidden. Optional; omit when the panel is always shown.
     */
    onLoaded?: (info: { count: number; error: boolean }) => void;
  };

  let {
    address,
    channel,
    paramset,
    peer,
    locale,
    pushesConfigPending = false,
    onLoaded,
  }: Props = $props();

  const channelAddress = $derived(`${address}:${channel}`);

  let schema = $state<UISchema | null>(null);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let saving = $state(false);
  let banner = $state<string | null>(null);

  // Working copy of the values; initialised from the schema, mutated
  // on input, diffed against the server state to decide which
  // parameters to PUT.
  let serverValues = $state<ParamValues>({});
  let values = $state<ParamValues>({});

  // Undo/redo stack. Every edit (single field change, profile apply,
  // …) is recorded as one ChangeEntry so the user can step backward
  // one action at a time. Cleared on load and after save.
  let stack = $state<ChangeStackState>(emptyStack());

  // Expert toggle (MASTER only): when enabled, the backend stops
  // filtering out untranslated parameters. Persisted in localStorage
  // so the user does not have to re-enable it on every nav.
  let expertMode = $state<boolean>(
    typeof localStorage !== "undefined" &&
      localStorage.getItem("openccu-loom.expert_mode") === "1",
  );

  function setExpert(v: boolean) {
    expertMode = v;
    try {
      if (v) localStorage.setItem("openccu-loom.expert_mode", "1");
      else localStorage.removeItem("openccu-loom.expert_mode");
    } catch {
      // storage may be disabled; the in-memory state still works.
    }
  }

  async function load(
    addr: string,
    ch: number,
    ps: "VALUES" | "MASTER" | "LINK",
    loc: string,
    pr: string | undefined,
    exp: boolean,
  ) {
    loading = true;
    loadError = null;
    try {
      const next = await api.uiSchema(addr, ch, ps, loc, pr, exp);
      const seed: ParamValues = {};
      for (const p of next.parameters) {
        if (p.observed) seed[p.name] = p.value;
      }
      schema = next;
      serverValues = seed;
      values = { ...seed };
      stack = emptyStack();
      lockedParams = new Set();
      onLoaded?.({ count: next.parameters.length, error: false });
    } catch (err) {
      loadError = friendlyError(err, t);
      onLoaded?.({ count: 0, error: true });
    } finally {
      loading = false;
    }
  }

  // Reload whenever the caller switches channels, paramsets, or
  // devices. $effect tracks every reactive read it performs, so
  // changes to any of the props re-invoke load() automatically. The
  // expert flag is read here too — toggling it triggers a refetch.
  $effect(() => {
    void load(address, channel, paramset, locale, peer, expertMode);
  });

  // Live-Updates: subscribe to data_point events for our channel
  // and patch the server snapshot in place. Pending edits stay
  // untouched (the user's working copy wins), but unmodified
  // parameters reflect the new value the moment the CCU pushes it.
  // VALUES paramsets only — MASTER/LINK never change at runtime.
  $effect(() => {
    if (paramset !== "VALUES") return;
    const wantedChannel = `${address}:${channel}`;
    return subscribe((ev) => {
      if (ev.type !== "data_point") return;
      const p = ev.payload as DataPointChangedEvent;
      if (p.channel_address !== wantedChannel) return;
      // Decide whether the operator holds an unsaved edit for this
      // parameter BEFORE patching the server snapshot: once
      // serverValues carries the incoming value the field would no
      // longer register as dirty, so we'd lose the ability to tell an
      // edit apart from a settled value. The working copy only follows
      // the CCU push when the field is not dirty, so a pending edit is
      // never clobbered.
      const userTouched = dirtySet.has(p.parameter);
      // The server snapshot always tracks the CCU's latest value.
      serverValues = { ...serverValues, [p.parameter]: p.value };
      if (!userTouched) {
        values = { ...values, [p.parameter]: p.value };
      }
    });
  });

  // Motion-detector brightness helper (LINK only). When the peer (the
  // link's sender channel) exposes a brightness / illuminance reading,
  // we surface it so the receiver's SHORT_/LONG_ COND_VALUE_LO/_HI
  // threshold fields can be filled with one click. Mirrors the CCU
  // WebUI's config/ic_md.cgi, which drops the sender's current
  // BRIGHTNESS into SHORT_COND_VALUE_LO/_HI. We read the peer channel's
  // data points once, then follow its live pushes so the value stays
  // current. Null hides the helper (no reading yet, or not a LINK).
  let senderBrightness = $state<{
    parameter: string;
    value: number;
    unit: string | null;
  } | null>(null);
  const brightnessSource = $derived(
    senderBrightness
      ? { value: senderBrightness.value, unit: senderBrightness.unit }
      : null,
  );
  $effect(() => {
    if (paramset !== "LINK" || !peer) {
      senderBrightness = null;
      return;
    }
    const senderAddress = peer;
    const [senderDev, senderChStr] = senderAddress.split(":");
    const senderCh = Number(senderChStr ?? 0);
    let cancelled = false;
    (async () => {
      try {
        const dps = await api.listDataPoints(senderDev, senderCh);
        if (!cancelled) senderBrightness = pickBrightnessReading(dps);
      } catch {
        // Sender data points are optional context; a fetch failure just
        // means the helper stays hidden. The link editor works without it.
        if (!cancelled) senderBrightness = null;
      }
    })();
    // Follow live brightness pushes from the sender channel so the
    // one-click value reflects the current reading, not a boot snapshot.
    const unsub = subscribe((ev) => {
      if (ev.type !== "data_point") return;
      const p = ev.payload as DataPointChangedEvent;
      if (p.channel_address !== senderAddress) return;
      if (!isBrightnessDataPoint(p.parameter)) return;
      // Ignore a second brightness DP once we have locked onto one, so a
      // channel with both BRIGHTNESS and ILLUMINATION does not flip.
      if (senderBrightness && senderBrightness.parameter !== p.parameter) return;
      const n = coerceNumber(p.value);
      if (n === null) return;
      senderBrightness = {
        parameter: p.parameter,
        value: n,
        unit: senderBrightness?.unit ?? null,
      };
    });
    return () => {
      cancelled = true;
      unsub();
    };
  });

  // MASTER reload after CONFIG_PENDING / UPDATE_PENDING resolves on
  // the device. Only registered for interfaces that actually emit
  // reliable CONFIG_PENDING events (HmIP-*); BidCos-* fall back to
  // the save-path reload because their CONFIG_PENDING is unreliable
  // (mirrors aiohomematic's BidCos polling pass). Skipped while the
  // user has unsaved local edits so we don't clobber their working
  // copy.
  $effect(() => {
    if (paramset !== "MASTER" || !pushesConfigPending) return;
    return maintenanceStore.onSettled(address, () => {
      if (dirtyNames.length > 0) return;
      void load(address, channel, paramset, locale, peer, expertMode);
    });
  });

  // Track dirty state globally so the App-level beforeunload guard
  // can warn before the user navigates away with unsaved edits.
  $effect(() => {
    const id = `channel:${channelAddress}:${paramset}${peer ? `:${peer}` : ""}`;
    dirty.set(id, dirtyNames.length > 0);
    return () => dirty.clear(id);
  });

  // Server-side edit-lock. Acquired once per panel mount; refreshed
  // every 90 s (TTL is 5 min server-side); released on unmount. When
  // another session already holds the key the open call returns 423
  // and we surface the conflict in the banner.
  let lockSession = $state<EditSessionResponse | null>(null);
  let lockedByOther = $state<string | null>(null);
  // True once a lock we HELD was lost mid-life (heartbeat failure /
  // take-over). Distinct from "never acquired" (e.g. 503 when sessions
  // aren't wired), which stays optimistic; a lost lock blocks saves
  // because another operator may now own the paramset.
  let lockLost = $state(false);
  let lockKey = $state("");
  $effect(() => {
    const key = `channel:${channelAddress}:${paramset}${peer ? `:${peer}` : ""}`;
    lockKey = key;
    let cancelled = false;
    let timer: ReturnType<typeof setInterval> | null = null;
    (async () => {
      try {
        const sess = await api.openEditSession(key);
        if (!cancelled) {
          lockSession = sess;
          lockedByOther = null;
          lockLost = false;
        }
      } catch (err) {
        if (err instanceof ApiError && err.status === 423) {
          lockedByOther = err.message;
        }
        // Other errors (e.g. 503 when sessions aren't wired) — fall
        // through silently, the panel keeps working optimistically.
      }
    })();
    timer = setInterval(async () => {
      if (!lockSession) return;
      try {
        const next = await api.heartbeatEditSession(lockSession);
        lockSession = next;
      } catch {
        // Lock expired or revoked; clear and flag so save() blocks
        // instead of clobbering whoever took the lock over.
        lockSession = null;
        lockLost = true;
      }
    }, 90_000);
    return () => {
      cancelled = true;
      if (timer) clearInterval(timer);
      const ls = lockSession;
      lockSession = null;
      if (ls) {
        void api.closeEditSession(ls).catch(() => {
          // ignore — server prunes by TTL
        });
      }
    };
  });

  function onParamChange(name: string, next: unknown) {
    const entry = entryFromPatch({ [name]: next }, values);
    stack = pushEntry(stack, entry);
    values = { ...values, [name]: next };
    banner = null;
  }

  function onUndo() {
    const result = undo(stack, values);
    values = result.values;
    stack = result.state;
    banner = null;
  }

  function onRedo() {
    const result = redo(stack, values);
    values = result.values;
    stack = result.state;
    banner = null;
  }

  const undoEnabled = $derived(canUndo(stack));
  const redoEnabled = $derived(canRedo(stack));

  // Keyboard shortcuts: Ctrl/Cmd+Z to undo, Ctrl/Cmd+Y or
  // Ctrl/Cmd+Shift+Z to redo. Skip when the user is typing in an
  // input / textarea so native editor undo keeps working there.
  $effect(() => {
    function onKeydown(e: KeyboardEvent) {
      if (!(e.ctrlKey || e.metaKey)) return;
      const target = e.target as HTMLElement | null;
      const tag = target?.tagName?.toLowerCase();
      if (tag === "input" || tag === "textarea" || target?.isContentEditable) return;
      const key = e.key.toLowerCase();
      if (key === "z" && !e.shiftKey) {
        e.preventDefault();
        onUndo();
      } else if ((key === "z" && e.shiftKey) || key === "y") {
        e.preventDefault();
        onRedo();
      }
    }
    window.addEventListener("keydown", onKeydown);
    return () => window.removeEventListener("keydown", onKeydown);
  });

  const crossErrors = $derived(
    schema
      ? validateCrossRules(schema.cross_validations ?? [], values)
      : {},
  );

  // Advanced toggle: LINK paramsets mark JT_/CT_/ACTION_TYPE as
  // `hidden_by_default`. Casual users don't need to see them at all;
  // power users click the toggle to reveal them. We only render the
  // checkbox when at least one parameter carries the flag so VALUES
  // and MASTER paramsets keep their existing look.
  let showAdvanced = $state(false);
  const hasAdvanced = $derived(
    !!schema && schema.parameters.some((p) => p.hidden_by_default),
  );

  const visibleParams = $derived(
    schema
      ? visibleParameters(schema.parameters, schema.visibility, values).filter(
          (p) => showAdvanced || !p.hidden_by_default,
        )
      : [],
  );

  // LINK paramsets carry keypress-specific groups (common/short/long)
  // which we render as tabs instead of three stacked sections. Falls
  // back to stacked rendering when the schema has no keypress tags
  // (VALUES, MASTER, pre-classifier LINK).
  const keypressGroups = $derived(
    (schema?.groups ?? []).filter((g) => g.id.startsWith("keypress.")),
  );
  const useKeypressTabs = $derived(
    paramset === "LINK" && keypressGroups.length > 1,
  );

  type KeypressTab = "common" | "short" | "long";
  let activeKeypressTab = $state<KeypressTab>("common");
  // When the schema (or visible-params filter) removes the current
  // tab, fall back to the first available group.
  $effect(() => {
    if (!useKeypressTabs) return;
    const availableIds = new Set(keypressGroups.map((g) => g.id));
    const wantedId = `keypress.${activeKeypressTab}`;
    if (!availableIds.has(wantedId)) {
      const first = keypressGroups[0]?.id;
      if (first === "keypress.short") activeKeypressTab = "short";
      else if (first === "keypress.long") activeKeypressTab = "long";
      else activeKeypressTab = "common";
    }
  });

  function keypressTabLabel(id: string): string {
    switch (id) {
      case "keypress.common":
        return t("channel.tab.common");
      case "keypress.short":
        return t("channel.tab.short");
      case "keypress.long":
        return t("channel.tab.long");
      default:
        return id;
    }
  }

  // groupLabel localises a parameter group's heading. The curated
  // pattern-based groups carry a stable id (temperature, timing, …) and
  // an English fallback title from the backend; we prefer a
  // `config.paramgroup.<id>` i18n row when present. Metadata-derived
  // groups (easymode archive) arrive already localised, so the fallback
  // to the backend label keeps them intact.
  function groupLabel(group: { id: string; label: string }): string {
    const key = "config.paramgroup." + group.id;
    const translated = t(key);
    return translated === key ? group.label : translated;
  }

  const parameterIndex = $derived(
    new Map(visibleParams.map((p) => [p.name, p])),
  );

  const dirtyNames = $derived(
    Object.keys(values).filter((k) => {
      const a = values[k];
      const b = serverValues[k];
      if (a === b) return false;
      // Normalise numeric equality (input returns numbers already,
      // but server echoes may be numbers vs strings for MIN/MAX).
      if (typeof a === "number" && typeof b === "number") return a !== b;
      return JSON.stringify(a) !== JSON.stringify(b);
    }),
  );

  const dirtySet = $derived(new Set(dirtyNames));
  const hasErrors = $derived(Object.keys(crossErrors).length > 0);

  async function save() {
    if (!schema || dirtyNames.length === 0 || hasErrors) return;
    // Refuse to write once our edit lock was taken over or dropped
    // mid-life: PUTting now would silently clobber whoever holds the
    // lock. A lock we never acquired (sessions unwired → both null)
    // still saves optimistically. The server's 409/423 is the backstop.
    if (lockedByOther || lockLost) {
      toastStore.error(t("channel.lock_lost"), t("channel.lock_lost_detail"));
      return;
    }
    saving = true;
    banner = null;
    try {
      if (paramset === "MASTER") {
        // MASTER writes must go through putParamset: the CCU applies
        // configuration changes atomically and rejects individual
        // per-parameter writes for many MASTER fields. The daemon
        // enforces the edit lock, so we present the held token.
        const batch: Record<string, unknown> = {};
        for (const name of dirtyNames) batch[name] = values[name];
        await api.putParamset(channelAddress, "MASTER", batch, lockSession?.token);
      } else if (paramset === "LINK") {
        if (!peer) throw new Error("LINK save requires a peer address");
        const batch: Record<string, unknown> = {};
        for (const name of dirtyNames) batch[name] = values[name];
        await api.putLinkParamset(channelAddress, peer, batch, lockSession?.token);
      } else {
        for (const name of dirtyNames) {
          await api.setValue(address, channel, name, values[name]);
        }
      }
      // Reload so the server-confirmed state replaces our optimistic
      // working copy (the callback server will also stream the event
      // through, but a refresh is simpler for the initial scope).
      await load(address, channel, paramset, locale, peer, expertMode);
      // A LINK paramset write goes to a battery device only on its next
      // wakeup; surface that hint in place of the plain success toast.
      const wakeupShown =
        paramset === "LINK" ? await notifyWakeupPending([address]) : false;
      if (!wakeupShown) toastStore.success(t("channel.saved_short"));
      banner = null;
    } catch (err) {
      // 423 Locked: our edit lock lapsed (heartbeat missed, taken over,
      // or never acquired). Clear it so the "locked by other" recovery
      // banner shows, and prompt the user to re-open the session.
      if (err instanceof ApiError && err.status === 423) {
        lockSession = null;
        toastStore.error(t("channel.save_failed"), t("channel.lock_lost"));
      } else {
        toastStore.error(t("channel.save_failed"), friendlyError(err, t));
      }
    } finally {
      saving = false;
    }
  }

  function reset() {
    values = { ...serverValues };
    stack = emptyStack();
    lockedParams = new Set();
    banner = null;
  }

  // Determine one parameter's live value from the device and stage it
  // into the working copy through onParamChange, so dirty tracking + undo
  // apply exactly as for a manual edit. Errors surface as a toast; the
  // ParameterField owns the button spinner (it awaits this promise). Only
  // wired for MASTER — the CCU's determineParameter auto-selects the
  // paramset, which is unambiguous for MASTER but not for per-peer LINK.
  async function determineParam(name: string) {
    try {
      const res = await api.determineParameter(address, channel, paramset, name);
      if (res.value === null || res.value === undefined) {
        toastStore.error(
          t("parameter.determine.failed"),
          t("parameter.determine.unsupported"),
        );
        return;
      }
      onParamChange(name, res.value);
      toastStore.success(t("parameter.determine.done", { name }));
    } catch (err) {
      toastStore.error(t("parameter.determine.failed"), friendlyError(err, t));
    }
  }

  // MASTER-only: the "Determine" button reads the current configuration
  // value from the device. Passed as undefined for VALUES/LINK so the
  // button never renders there.
  const determineHandler = $derived(
    paramset === "MASTER" ? determineParam : undefined,
  );

  async function runAction(name: string) {
    saving = true;
    banner = null;
    try {
      // ACTION parameters are write-only — the value is irrelevant
      // for most channel types; true mirrors the CCU WebUI.
      await api.setValue(address, channel, name, true);
      banner = t("channel.action_triggered", { name });
    } catch (err) {
      banner = err instanceof Error ? err.message : String(err);
    } finally {
      saving = false;
    }
  }

  // Parameters the last-applied profile fixed. Locked fields are
  // rendered as disabled so the user does not accidentally desync
  // the form from the preset. Clear on reset / reload / save; user
  // can unlock explicitly with the "Profil aufheben"-button below
  // the profile selector.
  let lockedParams = $state<Set<string>>(new Set());

  // --- Export / Import ------------------------------------------
  // Snapshots the currently-displayed paramset (server values + any
  // pending edits) as a JSON file the user can keep, share or
  // re-import later. The snapshot carries the channel/paramset id
  // so the import can refuse cross-channel paste accidents.
  function exportSnapshot() {
    if (!schema) return;
    const snap = {
      openccu_loom_export: 1,
      exported_at: new Date().toISOString(),
      channel: schema.channel,
      paramset,
      peer: peer ?? null,
      values,
    };
    const blob = new Blob([JSON.stringify(snap, null, 2)], {
      type: "application/json",
    });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${channelAddress}-${paramset}-${new Date()
      .toISOString()
      .replace(/[:.]/g, "-")}.json`;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
    toastStore.success(t("channel.snapshot_downloaded"));
  }

  async function onImportFile(file: File) {
    try {
      const text = await file.text();
      const parsed = JSON.parse(text) as {
        openccu_loom_export?: number;
        channel?: { address?: string };
        paramset?: string;
        values?: Record<string, unknown>;
      };
      if (!parsed || parsed.openccu_loom_export !== 1 || !parsed.values) {
        throw new Error(t("channel.import_invalid_file"));
      }
      if (parsed.channel?.address && parsed.channel.address !== channelAddress) {
        const ok = await confirmStore.ask({
          title: t("channel.import"),
          body: t("channel.import_cross_channel_confirm", {
            snapshot: parsed.channel.address,
            current: channelAddress,
          }),
          confirmLabel: t("channel.import"),
        });
        if (!ok) {
          return;
        }
      }
      if (parsed.paramset && parsed.paramset !== paramset) {
        toastStore.warn(
          t("channel.import_paramset_mismatch", {
            snapshot: parsed.paramset,
            current: paramset,
          }),
        );
      }
      // Treat the import as one undo entry. Locked fields are
      // cleared because an import is the user's own choice, not a
      // profile constraint.
      const entry = entryFromPatch(parsed.values, values, "import");
      stack = pushEntry(stack, entry);
      values = { ...values, ...parsed.values };
      lockedParams = new Set();
      toastStore.success(t("channel.import_staged"));
    } catch (err) {
      toastStore.error(
        t("channel.import_failed"),
        err instanceof Error ? err.message : String(err),
      );
    }
  }

  function pickImport() {
    const input = document.createElement("input");
    input.type = "file";
    input.accept = "application/json,.json";
    input.onchange = () => {
      const f = input.files?.[0];
      if (f) onImportFile(f);
    };
    input.click();
  }

  function applyProfilePatch(
    patch: Record<string, unknown>,
    meta: { fixed: string[]; editable: string[] },
  ) {
    // Merge preset values into the working copy. Parameters the
    // preset doesn't touch stay as they were. Recorded as a single
    // undo entry so the user can roll back an accidental profile
    // apply in one step.
    const entry = entryFromPatch(patch, values, "profile.apply");
    stack = pushEntry(stack, entry);
    values = { ...values, ...patch };
    lockedParams = new Set(meta.fixed);
    banner = t("channel.profile_staged");
  }

  function unlockProfile() {
    lockedParams = new Set();
    banner = null;
  }
</script>

<SessionTimeoutWarning dirty={dirtyNames.length > 0} />

{#if lockedByOther}
  <div class="mb-3 flex flex-wrap items-center gap-2 rounded border border-amber-300 bg-amber-50 p-2 text-xs text-amber-900 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-100">
    <span class="flex-1">{t("channel.session_lock_other")}</span>
    <button
      type="button"
      class="rounded border border-amber-400 px-2 py-0.5 text-xs hover:bg-amber-100 dark:border-amber-600 dark:hover:bg-[color-mix(in_srgb,var(--color-amber-900)_40%,transparent)]"
      onclick={async () => {
        // Recovery flow: force the foreign lock to release, then
        // acquire it ourselves. Mirrors aiohomematic-config's
        // "Bearbeitung übernehmen" button.
        try {
          await api.takeOverEditSession(lockKey);
          const sess = await api.openEditSession(lockKey);
          lockSession = sess;
          lockedByOther = null;
          lockLost = false;
        } catch (err) {
          if (err instanceof ApiError && err.status === 423) {
            lockedByOther = err.message;
          }
        }
      }}
    >
      {t("channel.take_over")}
    </button>
  </div>
{/if}

{#if lockLost}
  <div class="mb-3 flex flex-wrap items-center gap-2 rounded border border-amber-300 bg-amber-50 p-2 text-xs text-amber-900 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-100">
    <span class="flex-1">{t("channel.lock_lost_detail")}</span>
  </div>
{/if}

{#if loading}
  <p class="p-6 text-sm text-[var(--ha-secondary-text-color)]">{t("channel.loading_schema")}</p>
{:else if loadError}
  <Card class="p-4">
    <p class="text-sm text-red-600 dark:text-red-400">
      {t("channel.schema_failed")}: {loadError}
    </p>
  </Card>
{:else if schema}
  <Card class="p-4">
    <header class="mb-4 flex flex-wrap items-center justify-between gap-3">
      <div>
        <h2 class="text-lg font-semibold">
          {schema.channel.label || schema.channel.type}
        </h2>
        <p class="text-xs text-[var(--ha-secondary-text-color)]">
          {schema.channel.address} · {t("channel.kanal", { n: schema.channel.number })}
        </p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        {#if banner}
          <span class="text-xs text-[var(--ha-secondary-text-color)]">{banner}</span>
        {/if}
        <Button
          type="button"
          variant="outline"
          size="sm"
          onclick={onUndo}
          disabled={!undoEnabled || saving}
          title={t("channel.undo.tooltip")}
        >
          ↶
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onclick={onRedo}
          disabled={!redoEnabled || saving}
          title={t("channel.redo.tooltip")}
        >
          ↷
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onclick={exportSnapshot}
          disabled={saving}
          title={t("channel.export.tooltip")}
        >
          {t("channel.export")}
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onclick={pickImport}
          disabled={saving}
          title={t("channel.import.tooltip")}
        >
          {t("channel.import")}
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onclick={reset}
          disabled={dirtyNames.length === 0 || saving}
        >
          {t("common.reset")}
        </Button>
        <Button
          type="button"
          size="sm"
          onclick={save}
          disabled={dirtyNames.length === 0 || saving || hasErrors}
        >
          {saving ? t("common.saving") : t("channel.save_n", { count: dirtyNames.length })}
        </Button>
      </div>
    </header>

    {#if hasErrors}
      <div
        class="mb-4 rounded border border-red-300 bg-red-50 p-3 text-sm text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200"
      >
        {t("channel.cross_validation_error")}
      </div>
    {/if}

    {#if schema.subset_groups && schema.subset_groups.length > 0}
      <div class="mb-4 space-y-2">
        {#each schema.subset_groups as group (group.id)}
          <SubsetGroupSelector
            {group}
            onApply={applyProfilePatch}
          />
        {/each}
      </div>
    {/if}

    {#if schema.profile}
      <div class="mb-4">
        <ProfileSelector
          profile={schema.profile}
          locale={locale}
          currentValues={values}
          onApply={applyProfilePatch}
        />
        {#if lockedParams.size > 0}
          <p class="mt-2 flex items-center gap-2 text-xs text-[var(--ha-secondary-text-color)]">
            <span>{t("channel.lock_count", { count: lockedParams.size })}</span>
            <button
              type="button"
              class="text-brand-700 underline hover:text-brand-800"
              onclick={unlockProfile}
            >
              {t("channel.unlock_label")}
            </button>
          </p>
        {/if}
      </div>
    {/if}

    {#if hasAdvanced}
      <label class="mb-4 flex items-center gap-2 text-xs text-slate-600 dark:text-slate-400">
        <input
          type="checkbox"
          bind:checked={showAdvanced}
          class="h-4 w-4 rounded border-[var(--ha-divider-color)]"
        />
        {t("channel.advanced_label")}
      </label>
    {/if}

    {#if paramset === "MASTER"}
      <!-- Secured-transmission (AES_ACTIVE) toggle. Rendered from the raw
           MASTER paramset independent of the visibility store, since the
           parameter carries the `internal` ui-flag and is filtered out of
           the schema. Writes through the same edit-locked MASTER path. -->
      <SecureTransmission
        {channelAddress}
        editToken={lockSession?.token}
        disabled={!!lockedByOther || lockLost}
      />
      <label class="mb-4 flex items-center gap-2 text-xs text-slate-600 dark:text-slate-400">
        <input
          type="checkbox"
          checked={expertMode}
          onchange={(e) => setExpert((e.target as HTMLInputElement).checked)}
          class="h-4 w-4 rounded border-[var(--ha-divider-color)]"
        />
        {t("channel.expert_label")}
      </label>
    {/if}

    {#if useKeypressTabs}
      <!-- LINK paramsets: tab between common / short / long keypress
           groups. Matches homematicip-local-frontend's link-config
           view where the three sections are UX-sibling tabs rather
           than stacked. -->
      <nav class="mb-4 flex gap-1 border-b border-slate-200 dark:border-slate-800">
        {#each keypressGroups as group (group.id)}
          {@const tabKey = group.id.split(".")[1] as KeypressTab}
          {@const active = activeKeypressTab === tabKey}
          <button
            type="button"
            class="border-b-2 px-3 py-2 text-sm transition {active
              ? 'border-brand-500 text-brand-700'
              : 'border-transparent text-[var(--ha-secondary-text-color)] hover:text-brand-700'}"
            onclick={() => (activeKeypressTab = tabKey)}
          >
            {keypressTabLabel(group.id)}
            <Badge variant="muted">{group.parameters.length}</Badge>
          </button>
        {/each}
      </nav>
      {@const activeGroup = keypressGroups.find(
        (g) => g.id === `keypress.${activeKeypressTab}`,
      )}
      {#if activeGroup}
        {@const activeItems = activeGroup.parameters
          .map((name) => parameterIndex.get(name))
          .filter((p): p is NonNullable<typeof p> => p != null)}
        {#if activeItems.length > 0}
          <section class="mb-6">
            <ParameterGrid
              parameters={activeItems}
              {values}
              dirty={dirtySet}
              errors={crossErrors}
              {locale}
              locked={lockedParams}
              brightnessSource={brightnessSource}
              {onParamChange}
              onAction={runAction}
              onDetermine={determineHandler}
            />
          </section>
        {:else}
          <p class="mb-6 text-sm text-[var(--ha-secondary-text-color)]">
            {t("channel.no_params_in_group")}
          </p>
        {/if}
      {/if}
      {@const keypressNames = new Set(
        keypressGroups.flatMap((g) => g.parameters),
      )}
      {@const leftover = visibleParams.filter(
        (p) => !keypressNames.has(p.name),
      )}
      {#if leftover.length > 0}
        <section>
          <h3 class="mb-3 flex items-center gap-2 border-b border-slate-200 pb-1 text-sm font-semibold text-slate-700 dark:border-slate-700 dark:text-slate-200">
            {t("channel.other")}
            <Badge variant="muted">{leftover.length}</Badge>
          </h3>
          <ParameterGrid
            parameters={leftover}
            {values}
            dirty={dirtySet}
            errors={crossErrors}
            {locale}
            locked={lockedParams}
            brightnessSource={brightnessSource}
            {onParamChange}
            onAction={runAction}
            onDetermine={determineHandler}
          />
        </section>
      {/if}
    {:else if schema.groups && schema.groups.length > 0}
      <!-- Grouped rendering: only parameters inside a known group are
           shown under their group; everything else falls into a
           generic "Weitere" section. -->
      {#each schema.groups as group (group.id)}
        {@const groupItems = group.parameters
          .map((name) => parameterIndex.get(name))
          .filter((p): p is NonNullable<typeof p> => p != null)}
        {#if groupItems.length > 0}
          <section class="mb-6">
            <h3 class="mb-3 flex items-center gap-2 border-b border-slate-200 pb-1 text-sm font-semibold text-slate-700 dark:border-slate-700 dark:text-slate-200">
              {groupLabel(group)}
              <Badge variant="muted">{groupItems.length}</Badge>
            </h3>
            <ParameterGrid
              parameters={groupItems}
              {values}
              dirty={dirtySet}
              errors={crossErrors}
              {locale}
              locked={lockedParams}
              brightnessSource={brightnessSource}
              {onParamChange}
              onAction={runAction}
              onDetermine={determineHandler}
            />
          </section>
        {/if}
      {/each}
      {@const groupedNames = new Set(
        (schema.groups ?? []).flatMap((g) => g.parameters),
      )}
      {@const remaining = visibleParams.filter(
        (p) => !groupedNames.has(p.name),
      )}
      {#if remaining.length > 0}
        <section>
          <h3 class="mb-3 flex items-center gap-2 border-b border-slate-200 pb-1 text-sm font-semibold text-slate-700 dark:border-slate-700 dark:text-slate-200">
            {t("channel.other")}
            <Badge variant="muted">{remaining.length}</Badge>
          </h3>
          <ParameterGrid
            parameters={remaining}
            {values}
            dirty={dirtySet}
            errors={crossErrors}
            {locale}
            locked={lockedParams}
            brightnessSource={brightnessSource}
            {onParamChange}
            onAction={runAction}
            onDetermine={determineHandler}
          />
        </section>
      {/if}
    {:else}
      <ParameterGrid
        parameters={visibleParams}
        {values}
        dirty={dirtySet}
        errors={crossErrors}
        {locale}
        locked={lockedParams}
        brightnessSource={brightnessSource}
        {onParamChange}
        onAction={runAction}
        onDetermine={determineHandler}
      />
    {/if}

    {#if dirtyNames.length > 0}
      <!-- Sticky save bar: mirrors the header's Reset/Save so they stay
           reachable on long channel-config pages without scrolling up.
           Negative margins bleed to the Card's p-4 edges. -->
      <div class="sticky bottom-0 z-10 -mx-4 -mb-4 mt-4 flex flex-wrap items-center justify-end gap-2 border-t border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-4 py-3">
        <span class="mr-auto text-xs text-[var(--ha-secondary-text-color)]">
          {t("channel.unsaved")}
        </span>
        <Button type="button" variant="outline" size="sm" onclick={reset} disabled={saving}>
          {t("common.reset")}
        </Button>
        <Button type="button" size="sm" onclick={save} disabled={saving || hasErrors}>
          {saving ? t("common.saving") : t("channel.save_n", { count: dirtyNames.length })}
        </Button>
      </div>
    {/if}
  </Card>
{/if}
