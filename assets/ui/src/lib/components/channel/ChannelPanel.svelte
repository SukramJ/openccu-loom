<script module lang="ts">
  // Several ChannelPanels are mounted at once — device MASTER next to
  // channel MASTER, or the two halves of the link editor. The undo /
  // redo shortcuts are window-level, so without an owner one Ctrl+Z
  // would step every mounted change stack and silently revert edits the
  // operator never looked at. The panel the operator last interacted
  // with (pointer or focus) owns the shortcut.
  let focusedPanel: HTMLElement | null = null;
</script>

<script lang="ts">
  import { untrack } from "svelte";
  import type { ParamsetWriteResult, UISchema } from "$lib/api/types";
  import ApplyToChannelsDialog from "./ApplyToChannelsDialog.svelte";
  import { api, ApiError, friendlyError } from "$lib/api/client";
  import ParameterGrid from "./ParameterGrid.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import OverflowMenu from "$lib/components/ui/OverflowMenu.svelte";
  import ProfileSelector from "./ProfileSelector.svelte";
  import LinkKeypressTable from "./LinkKeypressTable.svelte";
  import {
    EXPERT_PROFILE_ID,
    activeVariant,
    profilePatch,
    profileVariants,
    type ProfileVariant,
  } from "$lib/links/link-profiles";
  import SubsetGroupSelector from "./SubsetGroupSelector.svelte";
  import SecureTransmission from "./SecureTransmission.svelte";
  import WritePreviewDialog from "./WritePreviewDialog.svelte";
  import { prefs, setExpertMode } from "$lib/stores/preferences.svelte";
  import {
    buildPreview,
    readBackDiff,
    readBackFromReport,
    type PreviewEntry,
    type ReadBackEntry,
  } from "$lib/channel/write-preview";
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
  import { onResync, subscribe } from "$lib/stores/events.svelte";
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
     * changes individually via `PUT .../data-points/{param}/value`.
     * "MASTER" renders channel configuration and writes the whole set
     * as a batch via `PUT .../paramsets/MASTER`.
     * "LINK" renders the per-peer direct-link configuration and
     * writes as a batch via `PUT .../link-ps/{peer}`. The
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
    /**
     * The page around the panel owns saving. The panel drops its own
     * Reset / Save buttons and sticky save bar, reports its unsaved-edit
     * count through `onDirtyChange`, and is saved through the exported
     * `save()` / `discard()`. The link page uses this so one
     * "Übernehmen" writes the sender and the receiver side together.
     */
    hosted?: boolean;
    onDirtyChange?: (count: number) => void;
    /**
     * Which end of a direct link this LINK panel edits. The device test
     * triggers the receiver as if the sender fired, so it is offered on
     * the receiver side only.
     */
    linkRole?: "sender" | "receiver";
  };

  let {
    address,
    channel,
    paramset,
    peer,
    locale,
    pushesConfigPending = false,
    onLoaded,
    hosted = false,
    onDirtyChange,
    linkRole = "receiver",
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

  // The one expert switch (prefs.expertMode, shared with Settings): the
  // backend stops filtering out untranslated MASTER parameters, and the
  // raw CCU names show under every label.
  const expertMode = $derived(prefs.expertMode);

  function setExpert(v: boolean) {
    setExpertMode(v);
  }

  // Monotonic generation counter guarding the async load path. Every
  // call captures its own generation; a response that lands after a
  // newer call has already started is discarded instead of applied,
  // so a slow response for channel 1 can never overwrite the
  // schema/values/serverValues the UI has since moved on to for
  // channel 2. Mirrors the `cancelled` guard the brightness effect
  // below already uses for its own async fetch.
  let loadGeneration = 0;

  async function load(
    addr: string,
    ch: number,
    ps: "VALUES" | "MASTER" | "LINK",
    loc: string,
    pr: string | undefined,
    exp: boolean,
  ) {
    const generation = ++loadGeneration;
    loading = true;
    loadError = null;
    try {
      const next = await api.uiSchema(addr, ch, ps, loc, pr, exp);
      if (generation !== loadGeneration) return;
      const seed: ParamValues = {};
      for (const p of next.parameters) {
        if (p.observed) seed[p.name] = p.value;
      }
      schema = next;
      serverValues = seed;
      values = { ...seed };
      stack = emptyStack();
      lockedParams = new Set();
      profileId = detectedProfileId(next, ps, loc);
      onLoaded?.({ count: next.parameters.length, error: false });
    } catch (err) {
      if (generation !== loadGeneration) return;
      loadError = friendlyError(err, t);
      onLoaded?.({ count: 0, error: true });
    } finally {
      if (generation === loadGeneration) loading = false;
    }
  }

  // Reload whenever the caller switches channels, paramsets, or
  // devices. $effect tracks every reactive read it performs, so
  // changes to any of the props re-invoke load() automatically. The
  // locale and expert flag are read here too — a language switch or
  // an expert-mode toggle changes what the ui-schema returns (labels,
  // field set), so both trigger a refetch.
  //
  // A locale/expert-mode change alone (address/channel/paramset/peer
  // unchanged) must not silently reload over an unsaved working copy —
  // that discards the edits and the undo stack with no confirm and no
  // toast. The CONFIG_PENDING-settle and resync reload paths below
  // both already guard on `dirtyNames`; this one is read via untrack()
  // so its value gates this decision without becoming a dependency —
  // a save clearing `dirtyNames` must not by itself re-trigger a
  // reload that's already happening through save()'s own call to load().
  let loadedSubject: string | null = null;
  $effect(() => {
    const addr = address;
    const ch = channel;
    const ps = paramset;
    const loc = locale;
    const pr = peer;
    const exp = expertMode;
    const subject = `${addr}:${ch}:${ps}:${pr ?? ""}`;
    if (subject === loadedSubject && untrack(() => dirtyNames.length > 0)) {
      return;
    }
    loadedSubject = subject;
    void load(addr, ch, ps, loc, pr, exp);
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

  // A daemon boot snapshot signals a resync instead of replaying the
  // model into the event stream, so this panel refetches rather than
  // waiting for pushes that will not come. Skipped over an unsaved
  // working copy for the same reason the reload above is.
  $effect(() => {
    return onResync(() => {
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
    // A conflict/loss banner belongs to the channel that produced it.
    // Clear both synchronously on every channel/paramset/peer switch so
    // a still-in-flight open-session request for the *previous* key can
    // never leave its stale banner showing on the new one while the new
    // key's own request is still pending.
    lockedByOther = null;
    lockLost = false;
    let cancelled = false;
    let timer: ReturnType<typeof setInterval> | null = null;
    (async () => {
      try {
        const sess = await api.openEditSession(key);
        if (cancelled) {
          // The panel moved on (channel/paramset switch, unmount) while the
          // open was in flight. The server created the lock anyway and holds
          // it for the full session TTL, blocking the very operator who left —
          // the cleanup below has already run and cannot see this session, so
          // release it here.
          void api.closeEditSession(sess).catch(() => {
            // ignore — server prunes by TTL
          });
          return;
        }
        lockSession = sess;
        lockedByOther = null;
        lockLost = false;
      } catch (err) {
        if (cancelled) return;
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
    // The chip reports what the device did with the last write; editing the
    // row supersedes that, so it goes rather than sitting next to a value it
    // no longer describes.
    if (readBack.length > 0) {
      readBack = readBack.filter((entry) => entry.name !== name);
    }
  }

  // Root element of this panel — the identity the shortcut owner is
  // tracked by.
  let panelRoot = $state<HTMLElement | null>(null);

  $effect(() => {
    const root = panelRoot;
    if (!root) return;
    function claim() {
      focusedPanel = root;
    }
    root.addEventListener("pointerdown", claim);
    root.addEventListener("focusin", claim);
    return () => {
      root.removeEventListener("pointerdown", claim);
      root.removeEventListener("focusin", claim);
      // Never leave a torn-down panel as the shortcut owner.
      if (focusedPanel === root) focusedPanel = null;
    };
  });

  function onUndo() {
    const result = undo(stack, values, lockedParams);
    values = result.values;
    stack = result.state;
    lockedParams = result.lockedParams;
    if (result.profile !== undefined) profileId = result.profile;
    banner = null;
  }

  function onRedo() {
    const result = redo(stack, values, lockedParams);
    values = result.values;
    stack = result.state;
    lockedParams = result.lockedParams;
    if (result.profile !== undefined) profileId = result.profile;
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
      // Only the panel the operator last worked in reacts.
      if (!panelRoot || focusedPanel !== panelRoot) return;
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

  // --- Link profile (LINK only) ----------------------------------
  // The selected easymode profile decides which LINK parameters the
  // panel shows, as the CCU WebUI's profile dropdown decides which
  // easymode form it renders: a profile shows only the parameters it
  // leaves to the operator, "Experte" (profile 0) shows the whole
  // paramset including jump targets and conditions. A link the archive
  // has no profiles for is edited in the expert view.
  let profileId = $state<number | null>(null);

  function detectedProfileId(
    s: UISchema,
    ps: "VALUES" | "MASTER" | "LINK",
    loc: string,
  ): number | null {
    if (ps !== "LINK" || !s.profile) return null;
    return activeVariant(profileVariants(s.profile, loc), s.profile.active_profile_id)?.id ?? null;
  }

  const profileOptions = $derived(
    paramset === "LINK" && schema?.profile ? profileVariants(schema.profile, locale) : [],
  );
  const selectedProfile = $derived(
    profileOptions.find((v) => v.id === profileId) ?? null,
  );
  const linkExpert = $derived(
    paramset === "LINK" &&
      (selectedProfile === null || selectedProfile.id === EXPERT_PROFILE_ID),
  );
  const profileEditable = $derived(
    paramset === "LINK" && selectedProfile && !linkExpert
      ? new Set(profilePatch(selectedProfile).editable)
      : null,
  );

  // Choosing a profile stages its values at once as one undoable edit;
  // the device sees them only on save. Switching to Experte stages
  // nothing and releases the fields the previous profile held fixed.
  function selectProfile(variant: ProfileVariant) {
    const before = profileId;
    if (variant.id === EXPERT_PROFILE_ID) {
      stack = pushEntry(
        stack,
        entryFromPatch({}, values, "profile.select", { before: [...lockedParams], after: [] }, {
          before,
          after: variant.id,
        }),
      );
      lockedParams = new Set();
      profileId = variant.id;
      return;
    }
    const { patch, fixed } = profilePatch(variant);
    stack = pushEntry(
      stack,
      entryFromPatch(patch, values, "profile.apply", { before: [...lockedParams], after: fixed }, {
        before,
        after: variant.id,
      }),
    );
    values = { ...values, ...patch };
    lockedParams = new Set(fixed);
    profileId = variant.id;
  }

  const visibleParams = $derived.by(() => {
    if (!schema) return [];
    const all = visibleParameters(schema.parameters, schema.visibility, values);
    if (paramset === "LINK") {
      if (linkExpert) return all;
      return all.filter((p) => !p.hidden_by_default && (profileEditable?.has(p.name) ?? false));
    }
    return all.filter((p) => showAdvanced || !p.hidden_by_default);
  });

  // Raw CCU parameter names belong to the expert view: the global expert
  // mode, or a link edited under its "Experte" profile.
  const showRawNames = $derived(expertMode || linkExpert);

  // How many parameters the profile view leaves out — shown so the
  // operator knows "Experte" holds more than the form does.
  const hiddenByProfile = $derived(
    schema && paramset === "LINK" && !linkExpert
      ? visibleParameters(schema.parameters, schema.visibility, values).length -
          visibleParams.length
      : 0,
  );

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
  // Parameters the device reported differently from what was written, kept
  // until the row is edited again. See lib/channel/write-preview.ts.
  let readBack = $state<ReadBackEntry[]>([]);
  const readBackMap = $derived(
    new Map(readBack.map((entry) => [entry.name, entry.got])),
  );
  const hasErrors = $derived(Object.keys(crossErrors).length > 0);

  // Write preview + read-back state. `previewOpen` gates the dialog;
  // `readBack` holds the parameters the device reported differently from what
  // was sent, so their rows can carry a chip until the next edit.
  let previewOpen = $state(false);
  let previewEntries = $state<PreviewEntry[]>([]);

  // The request line the preview shows. It names the endpoint the write
  // actually goes to, which is the part an operator cannot infer from the
  // form: a LINK paramset is addressed per peer, not by the key alone.
  // The path segments mirror api.putLinkParamset / api.putParamset exactly.
  // A preview that names an endpoint the client never calls is worse than no
  // preview: it is a wrong answer to the one question the dialog exists to
  // answer, and nothing else in the app would contradict it.
  const previewRequest = $derived(
    paramset === "LINK" && peer
      ? `PUT /api/v1/devices/${channelAddress}/link-ps/${peer}`
      : `PUT /api/v1/devices/${channelAddress}/paramsets/${paramset}`,
  );

  /**
   * The Save button's entry point. A MASTER or LINK write is device
   * configuration the operator cannot inspect from the device itself, so it
   * goes through the preview when the preference is on. VALUES writes never
   * do: those are immediate control actions whose effect is the point, and a
   * dialog in front of a light switch is friction with nothing behind it.
   */
  function requestSave(): Promise<boolean> {
    if (!schema || hasErrors) return Promise.resolve(false);
    if (dirtyNames.length === 0) return Promise.resolve(true);
    // Refuse a lost lock here rather than after the preview: previewing a
    // write that cannot happen asks the operator to review and approve a
    // change the daemon will refuse, and the refusal then reads as a failure
    // of their approval. performSave keeps the same guard as the backstop —
    // the lock can lapse while the dialog is open.
    if (lockedByOther || lockLost) {
      toastStore.error(t("channel.lock_lost"), t("channel.lock_lost_detail"));
      return Promise.resolve(false);
    }
    if (prefs.writePreview && paramset !== "VALUES") {
      previewEntries = buildPreview(schema, values, serverValues, dirtyNames);
      previewOpen = true;
      // Settled by the dialog: confirm runs the write, cancel reports
      // "not saved" so a hosting page stops instead of writing the other side.
      return new Promise((resolve) => {
        previewResolve = resolve;
      });
    }
    return performSave();
  }

  let previewResolve: ((saved: boolean) => void) | null = null;

  function cancelPreview() {
    previewOpen = false;
    previewResolve?.(false);
    previewResolve = null;
  }

  // MASTER multi-apply. Offered only while the panel holds unsaved MASTER
  // edits and an edit token: the apply endpoint is gated on the SOURCE
  // channel's lock, and without dirty values there is nothing to apply.
  // The dialog carries the dirty values only — the same set a Save sends.
  let applyOpen = $state(false);
  const canApplyToOthers = $derived(
    paramset === "MASTER" &&
      dirtyNames.length > 0 &&
      !!lockSession?.token &&
      !lockedByOther &&
      !lockLost,
  );
  const applyValues = $derived.by(() => {
    const out: Record<string, unknown> = {};
    for (const name of dirtyNames) out[name] = values[name];
    return out;
  });

  function confirmPreview() {
    previewOpen = false;
    const resolve = previewResolve;
    previewResolve = null;
    void performSave().then((saved) => resolve?.(saved));
  }

  /**
   * Write the unsaved edits. Resolves true when the device took them
   * (or there was nothing to write), false when the write was refused
   * or failed — the failure itself is already toasted here.
   */
  async function performSave(): Promise<boolean> {
    if (!schema || hasErrors) return false;
    if (dirtyNames.length === 0) return true;
    // Refuse to write once our edit lock was taken over or dropped
    // mid-life: PUTting now would silently clobber whoever holds the
    // lock. A lock we never acquired (sessions unwired → both null)
    // still saves optimistically. The server's 409/423 is the backstop.
    if (lockedByOther || lockLost) {
      toastStore.error(t("channel.lock_lost"), t("channel.lock_lost_detail"));
      return false;
    }
    saving = true;
    banner = null;
    // Snapshot what goes out before the write, so the read-back compares
    // against the sent values rather than against the working copy, which the
    // reload is about to overwrite.
    const sent: Record<string, unknown> = {};
    for (const name of dirtyNames) sent[name] = values[name];
    readBack = [];
    // The daemon's post-write read-back report (MASTER / LINK only). When
    // it carries divergences it is the primary source; the client-side
    // comparison below is the fallback.
    let report: ParamsetWriteResult | undefined;
    try {
      if (paramset === "MASTER") {
        // MASTER writes must go through putParamset: the CCU applies
        // configuration changes atomically and rejects individual
        // per-parameter writes for many MASTER fields. The daemon
        // enforces the edit lock, so we present the held token.
        const batch: Record<string, unknown> = {};
        for (const name of dirtyNames) batch[name] = values[name];
        report = await api.putParamset(channelAddress, "MASTER", batch, lockSession?.token);
      } else if (paramset === "LINK") {
        if (!peer) throw new Error("LINK save requires a peer address");
        const batch: Record<string, unknown> = {};
        for (const name of dirtyNames) batch[name] = values[name];
        report = await api.putLinkParamset(channelAddress, peer, batch, lockSession?.token);
      } else {
        for (const name of dirtyNames) {
          await api.setValue(address, channel, name, values[name]);
        }
      }
      // Reload so the server-confirmed state replaces our optimistic
      // working copy (the callback server will also stream the event
      // through, but a refresh is simpler for the initial scope).
      await load(address, channel, paramset, locale, peer, expertMode);
      // What the device kept is not always what was sent: rfd clamps an
      // out-of-range value to MAX and answers ok, while hmipserver stores the
      // rejected value (both measured by the homematic-manager project
      // against CCU firmware 3.89.8, its docs/config-pending.md — an external
      // measurement). A success toast on top of either is a lie the operator
      // has no way to catch, so the reloaded values are compared against what
      // went out. The daemon reads the paramset back itself straight after
      // the write; its report wins whenever it carries one. A report whose
      // own read failed says nothing about divergences, so the reload
      // comparison stands in for it and the failure is surfaced.
      const serverReport =
        report && Array.isArray(report.readback_divergences) && !report.readback_error
          ? report
          : null;
      readBack = serverReport
        ? readBackFromReport(schema, serverReport.readback_divergences)
        : readBackDiff(schema, sent, serverValues);
      if (report?.readback_error) {
        toastStore.warn(t("channel.readback.error_title"), report.readback_error);
      }
      if (readBack.length > 0) {
        toastStore.warn(
          t("channel.readback.title"),
          t("channel.readback.body", { count: readBack.length }),
        );
      }
      banner = null;
      // A hosting page reports the outcome once for all its panels.
      if (!hosted) {
        // A LINK paramset write goes to a battery device only on its next
        // wakeup; surface that hint in place of the plain success toast.
        const wakeupShown =
          paramset === "LINK" ? await notifyWakeupPending([address]) : false;
        if (!wakeupShown) toastStore.success(t("channel.saved_short"));
      }
      return true;
    } catch (err) {
      // 423 Locked: our edit lock lapsed mid-save (heartbeat missed or
      // taken over). Drop the dead session and flag the loss — lockLost
      // blocks further saves and shows its banner; retrying blind would
      // clobber whoever holds the lock now.
      if (err instanceof ApiError && err.status === 423) {
        lockSession = null;
        lockLost = true;
        toastStore.error(t("channel.save_failed"), t("channel.lock_lost"));
      } else {
        toastStore.error(t("channel.save_failed"), friendlyError(err, t));
      }
      return false;
    } finally {
      saving = false;
    }
  }

  function reset() {
    values = { ...serverValues };
    stack = emptyStack();
    lockedParams = new Set();
    if (schema) profileId = detectedProfileId(schema, paramset, locale);
    banner = null;
  }

  // Entry points for a hosting page (see the `hosted` prop). save()
  // takes the same path as the panel's own Save button, write preview
  // included, and tells the page whether the device took the edits.
  export function save(): Promise<boolean> {
    return requestSave();
  }

  export function discard(): void {
    reset();
  }

  // The count is this effect's only dependency. The host's callback runs
  // untracked: a host that reads its own state while storing the count
  // (the parameter page keeps one count per channel) would otherwise make
  // that state a dependency of this effect, and every store would wake it
  // again — effect_update_depth_exceeded after one keystroke.
  $effect(() => {
    const count = dirtyNames.length;
    untrack(() => onDirtyChange?.(count));
  });

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
    try {
      // ACTION parameters are write-only — the value is irrelevant
      // for most channel types; true mirrors the CCU WebUI.
      await api.setValue(address, channel, name, true);
      toastStore.success(t("channel.action_triggered", { name }));
    } catch (err) {
      toastStore.error(t("channel.action_failed", { name }), friendlyError(err, t));
    } finally {
      saving = false;
    }
  }

  // Parameters the last-applied profile fixed. Locked fields are
  // rendered as disabled so the user does not accidentally desync
  // the form from the preset. Cleared on reset / reload / save; for a
  // link, switching to "Experte" releases them.
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
    // undo entry — together with the locked-field set transition —
    // so the user can roll back an accidental profile apply in one
    // step, including which fields go back to being editable.
    const entry = entryFromPatch(patch, values, "profile.apply", {
      before: [...lockedParams],
      after: meta.fixed,
    });
    stack = pushEntry(stack, entry);
    values = { ...values, ...patch };
    lockedParams = new Set(meta.fixed);
    banner = t("channel.profile_staged");
  }

  // Test the direct link at the device (V03): trigger the receiver
  // (channelAddress) as if the sender (peer) fired. It physically actuates
  // the device, so it is confirmed first.
  let testingLink = $state(false);
  async function testLinkAtDevice(longPress: boolean) {
    if (paramset !== "LINK" || !peer) return;
    const ok = await confirmStore.ask({
      title: t("links.test.confirm_title"),
      body: t("links.test.confirm_body"),
      confirmLabel: longPress ? t("profile.test.long") : t("profile.test.short"),
    });
    if (!ok) return;
    testingLink = true;
    try {
      await api.testLinkAtDevice(channelAddress, peer, longPress);
      toastStore.success(t("links.test.ok"));
    } catch (err) {
      if (err instanceof ApiError && err.status === 501) {
        toastStore.error(t("links.test.unsupported"));
      } else {
        toastStore.error(t("links.test.error"), friendlyError(err, t));
      }
    } finally {
      testingLink = false;
    }
  }
</script>

<!-- display:contents wrapper: no layout of its own, it only gives the
     panel a DOM identity so the undo / redo shortcut can tell which of
     the mounted panels the operator is working in. -->
<div class="contents" bind:this={panelRoot}>
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
          } else {
            // A network error / 403 (viewer role) / 503 (sessions
            // unwired) leaves the banner and button exactly as they
            // were — without a toast the click reads as "did
            // nothing", so surface it like every other action result.
            toastStore.error(t("channel.take_over_failed"), friendlyError(err, t));
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
    <header
      class="mb-4 flex flex-wrap items-center gap-3 {hosted ? 'justify-end' : 'justify-between'}"
    >
      <!-- A hosting page names the channel in its own header. -->
      {#if !hosted}
        <div>
          <h2 class="text-lg font-semibold">
            {schema.channel.label || schema.channel.type}
          </h2>
          <p class="text-xs text-[var(--ha-secondary-text-color)]">
            {schema.channel.address} · {t("channel.kanal", { n: schema.channel.number })}
          </p>
        </div>
      {/if}
      <div class="flex flex-wrap items-center gap-2">
        {#if banner}
          <span class="text-xs text-[var(--ha-secondary-text-color)]">{banner}</span>
        {/if}
        <!-- Undo / redo stay in view; export and import are rare and
             live in the overflow menu. -->
        <Button
          type="button"
          variant="ghost"
          size="icon"
          onclick={onUndo}
          disabled={!undoEnabled || saving}
          title={t("channel.undo.tooltip")}
          aria-label={t("channel.undo.tooltip")}
        >
          <Icon name="mdi:undo" size={18} />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          onclick={onRedo}
          disabled={!redoEnabled || saving}
          title={t("channel.redo.tooltip")}
          aria-label={t("channel.redo.tooltip")}
        >
          <Icon name="mdi:redo" size={18} />
        </Button>
        {#if hosted && canApplyToOthers}
          <Button
            type="button"
            variant="outline"
            size="sm"
            onclick={() => (applyOpen = true)}
            disabled={saving || hasErrors}
            title={t("channel.apply.tooltip")}
          >
            {t("channel.apply.open")}
          </Button>
        {/if}
        <OverflowMenu
          ariaLabel={t("channel.more_actions")}
          items={[
            { label: t("channel.export"), onSelect: exportSnapshot, disabled: saving },
            { label: t("channel.import"), onSelect: pickImport, disabled: saving },
          ]}
        />
        {#if !hosted}
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
            onclick={() => void requestSave()}
            disabled={dirtyNames.length === 0 || saving || hasErrors}
          >
            {saving ? t("common.saving") : t("channel.save_n", { count: dirtyNames.length })}
          </Button>
        {/if}
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

    {#if paramset === "LINK" && (profileOptions.length > 0 || (peer && linkRole === "receiver"))}
      <div class="mb-4 space-y-3">
        {#if profileOptions.length > 0}
          <ProfileSelector
            variants={profileOptions}
            selectedId={profileId ?? EXPERT_PROFILE_ID}
            detectedId={schema.profile?.active_profile_id ?? EXPERT_PROFILE_ID}
            onSelect={selectProfile}
          />
        {/if}
        {#if peer && linkRole === "receiver"}
          <div class="flex flex-wrap items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={testingLink}
              onclick={() => void testLinkAtDevice(false)}
            >
              {t("profile.test.short")}
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={testingLink}
              onclick={() => void testLinkAtDevice(true)}
            >
              {t("profile.test.long")}
            </Button>
          </div>
        {/if}
      </div>
    {/if}

    {#if hasAdvanced && paramset !== "LINK"}
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
      <!-- A hosting page offers the one expert switch for all its panels. -->
      {#if !hosted}
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
    {/if}

    {#if paramset === "LINK"}
      {#if visibleParams.length > 0}
        <LinkKeypressTable
          parameters={visibleParams}
          {values}
          dirty={dirtySet}
          errors={crossErrors}
          readBack={readBackMap}
          {locale}
          locked={lockedParams}
          {brightnessSource}
          {onParamChange}
          onAction={runAction}
          showRawName={showRawNames}
        />
      {:else}
        <p class="text-sm text-[var(--ha-secondary-text-color)]">
          {t("profile.no_settings")}
        </p>
      {/if}
      {#if hiddenByProfile > 0}
        <p class="mt-3 text-xs text-[var(--ha-secondary-text-color)]">
          {t("profile.hidden_count", { count: hiddenByProfile })}
        </p>
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
              readBack={readBackMap}
              {locale}
              locked={lockedParams}
              brightnessSource={brightnessSource}
              {onParamChange}
              onAction={runAction}
              onDetermine={determineHandler}
              showRawName={showRawNames}
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
            readBack={readBackMap}
            {locale}
            locked={lockedParams}
            brightnessSource={brightnessSource}
            {onParamChange}
            onAction={runAction}
            onDetermine={determineHandler}
            showRawName={showRawNames}
          />
        </section>
      {/if}
    {:else}
      <ParameterGrid
        parameters={visibleParams}
        {values}
        dirty={dirtySet}
        errors={crossErrors}
        readBack={readBackMap}
        {locale}
        locked={lockedParams}
        brightnessSource={brightnessSource}
        {onParamChange}
        onAction={runAction}
        onDetermine={determineHandler}
        showRawName={showRawNames}
      />
    {/if}

    {#if dirtyNames.length > 0 && !hosted}
      <!-- Sticky save bar: mirrors the header's Reset/Save so they stay
           reachable on long channel-config pages without scrolling up.
           Negative margins bleed to the Card's p-4 edges. -->
      <div class="sticky bottom-0 z-10 -mx-4 -mb-4 mt-4 flex flex-wrap items-center justify-end gap-2 border-t border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-4 py-3">
        <span class="mr-auto text-xs text-[var(--ha-secondary-text-color)]">
          {t("channel.unsaved")}
        </span>
        {#if canApplyToOthers}
          <Button
            type="button"
            variant="outline"
            size="sm"
            onclick={() => (applyOpen = true)}
            disabled={saving || hasErrors}
            title={t("channel.apply.tooltip")}
          >
            {t("channel.apply.open")}
          </Button>
        {/if}
        <Button type="button" variant="outline" size="sm" onclick={reset} disabled={saving}>
          {t("common.reset")}
        </Button>
        <Button type="button" size="sm" onclick={() => void requestSave()} disabled={saving || hasErrors}>
          {saving ? t("common.saving") : t("channel.save_n", { count: dirtyNames.length })}
        </Button>
      </div>
    {/if}
  </Card>
{/if}

<WritePreviewDialog
  open={previewOpen}
  entries={previewEntries}
  request={previewRequest}
  onCancel={cancelPreview}
  onConfirm={confirmPreview}
/>

{#if canApplyToOthers || applyOpen}
  <ApplyToChannelsDialog
    open={applyOpen}
    {channelAddress}
    values={applyValues}
    editToken={lockSession?.token}
    onClose={() => (applyOpen = false)}
  />
{/if}
</div>
