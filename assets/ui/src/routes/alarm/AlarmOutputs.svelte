<script lang="ts">
  import { onMount } from "svelte";
  import { api, friendlyError } from "$lib/api/client";
  import { alarmPanelStore } from "$lib/stores/alarmPanel.svelte";
  import { deviceStore } from "$lib/stores/devices.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { t } from "$lib/i18n";
  import { makeTextMatcher } from "$lib/utils";
  import type {
    AlarmOutput,
    AlarmOutputCandidate,
    AlarmOutputClass,
    DeviceSummary,
    SysvarEntry,
  } from "$lib/api/types";
  import type { IconName } from "$lib/icons";
  import Icon from "$lib/components/ui/Icon.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import Switch from "$lib/components/ui/Switch.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";

  // Output picker (docs/alarm-concept.md §7 / §12.2). Manages the enrolled
  // output set of one alarm zone as class cards. Each card carries the
  // class Badge, per-mode assignment chips, loud/silent policy, outdoor +
  // shared-with-CCU switches, and the duration/tone/pattern/level fields
  // its class supports. Smoke-sounder and switched-siren classes render
  // their safety caveats prominently. A per-output test-fire sits behind a
  // confirm dialog — a real device activation, so confirmation IS wanted
  // here (the opposite of silence/disarm, which act on first tap).

  const MODES = ["perimeter", "full", "night", "vacation", "custom"] as const;
  type Mode = (typeof MODES)[number];

  const CLASSES: AlarmOutputClass[] = [
    "acoustic_siren",
    "switched_siren",
    "smoke_sounder",
    "optical_siren",
    "alarm_light",
    "chirp",
    "notification",
    "sysvar_mirror",
  ];

  const CLASS_ICON: Record<AlarmOutputClass, IconName> = {
    acoustic_siren: "mdi:bell-alert",
    switched_siren: "mdi:power",
    smoke_sounder: "mdi:smoke-detector-variant",
    optical_siren: "mdi:zap",
    alarm_light: "mdi:lightbulb",
    chirp: "mdi:volume",
    notification: "mdi:bell",
    sysvar_mirror: "mdi:server",
  };

  // Per-class field capabilities — the class, not the backing device,
  // decides which controls apply (§7). Loud/silent is a property of the
  // mode/hazard/panic output policies (Policies tab), not of a single
  // output, so no per-output toggle exists.
  type Caps = {
    outdoor: boolean;
    duration: boolean;
    tone: boolean;
    optical: boolean;
    level: boolean;
    // chirpTones exposes the three chirp tone labels the driver reads:
    // arm squawk, disarm squawk, and tick (countdown ticks, entry
    // warning, and the door chime all use the tick tone).
    chirpTones: boolean;
  };
  const CAPS: Record<AlarmOutputClass, Caps> = {
    // The acoustic activation carries the optical selection in the same
    // atomic device write, so the acoustic card exposes both pickers.
    acoustic_siren: { outdoor: true, duration: true, tone: true, optical: true, level: false, chirpTones: false },
    switched_siren: { outdoor: true, duration: true, tone: false, optical: false, level: false, chirpTones: false },
    smoke_sounder: { outdoor: false, duration: false, tone: false, optical: false, level: false, chirpTones: false },
    optical_siren: { outdoor: true, duration: true, tone: false, optical: true, level: false, chirpTones: false },
    alarm_light: { outdoor: true, duration: true, tone: false, optical: true, level: true, chirpTones: false },
    chirp: { outdoor: false, duration: false, tone: false, optical: false, level: false, chirpTones: true },
    notification: { outdoor: false, duration: false, tone: false, optical: false, level: false, chirpTones: false },
    sysvar_mirror: { outdoor: false, duration: false, tone: false, optical: false, level: false, chirpTones: false },
  };

  // Device-backed classes enroll a real channel that must resolve to an
  // output driver; notification and sysvar_mirror carry the channel
  // address as identity only, so no candidate filtering applies.
  const DEVICE_BACKED = new Set<AlarmOutputClass>([
    "acoustic_siren",
    "optical_siren",
    "switched_siren",
    "smoke_sounder",
    "alarm_light",
    "chirp",
  ]);

  // Fallback device heuristic for expert mode and the non-device-backed
  // classes: sirens, switch actuators (plug-in sirens / alarm lights),
  // smoke sounders. The default add flow uses the backend candidate
  // list instead (capability ground truth from the live model, §12.2).
  const OUTPUT_RE =
    /asir|sir|swsd|ps[m]?\b|psm|switch|schalt|dimmer|dim|light|licht|lamp|relay|relais|bsm|fsm|dr\b|mp3/i;

  // --- zone state --------------------------------------------------
  const zones = $derived(alarmPanelStore.zonesConfig);
  let zoneId = $state("");

  let outputs = $state<AlarmOutput[]>([]);
  let loading = $state(false);
  let loadError = $state<string | null>(null);
  let saving = $state(false);
  let dirty = $state(false);

  // Per-output optical-only test flag + in-flight guard.
  let testOptical = $state<Record<string, boolean>>({});
  let testBusy = $state<Record<string, boolean>>({});

  // --- add flow ----------------------------------------------------
  let addOpen = $state(false);
  let addClass = $state<AlarmOutputClass>("acoustic_siren");
  let addExpert = $state(false);
  let addDeviceSearch = $state("");
  let addDevice = $state<DeviceSummary | null>(null);
  let addCandidate = $state<AlarmOutputCandidate | null>(null);
  let addChannel = $state("");
  let addName = $state("");

  // --- sysvar-mirror add assist -------------------------------------
  // The mirror either manages its own value-list variable (created on
  // the CCU automatically) or writes an operator-owned ALARM (bool)
  // variable picked from the central's existing sysvars.
  let addSysvarCentral = $state("");
  let addSysvarExisting = $state(false);
  let addSysvarName = $state("");
  let sysvars = $state<SysvarEntry[]>([]);
  let sysvarsLoaded = $state(false);
  async function loadSysvars() {
    try {
      sysvars = await api.listSysvars();
      sysvarsLoaded = true;
    } catch (e) {
      toastStore.error(t("alarm.outputs.sysvar.load_failed"), friendlyError(e, t));
    }
  }
  $effect(() => {
    if (addOpen && addClass === "sysvar_mirror" && !sysvarsLoaded) void loadSysvars();
  });
  // Centrals known to this daemon, derived from the loaded device and
  // sysvar inventories (viewer-safe; no operator-gated centrals CRUD).
  const centralOptions = $derived(
    [...new Set([
      ...deviceStore.items.map((d) => d.central ?? ""),
      ...sysvars.map((v) => v.central ?? ""),
    ])]
      .filter((c) => c !== "")
      .sort()
      .map((c) => ({ value: c, label: c })),
  );
  $effect(() => {
    if (addOpen && addClass === "sysvar_mirror" && !addSysvarCentral && centralOptions.length === 1) {
      addSysvarCentral = centralOptions[0].value;
    }
  });
  const alarmSysvarOptions = $derived(
    sysvars
      .filter((v) => (v.central ?? "") === addSysvarCentral && v.value_type === "ALARM")
      .map((v) => ({ value: v.name, label: v.name }))
      .sort((a, b) => a.label.localeCompare(b.label)),
  );

  // Enrollment candidates from the live domain model (all device-backed
  // classes at once). Also feeds the per-output tone/pattern pickers
  // with the device's real ENUM label lists.
  let candidates = $state<AlarmOutputCandidate[]>([]);
  // deviceByAddr resolves a candidate's model LABEL from the live device
  // inventory — the candidate DTO only carries the raw wire `model`, not
  // the localised label the device list already has cached.
  const deviceByAddr = $derived.by(() => {
    const m = new Map<string, DeviceSummary>();
    for (const d of deviceStore.items) m.set(d.address, d);
    return m;
  });
  function candidateModelLabel(c: AlarmOutputCandidate): string {
    const d = deviceByAddr.get(c.device_address);
    return d?.model_label || d?.model || c.model;
  }
  const candidateByChannel = $derived(
    new Map(candidates.map((c) => [`${c.central}|${c.channel_address}`, c])),
  );
  // Address-only fallback index for enrollments whose stored central
  // differs from the candidate's (older rows may carry an empty
  // central). Ambiguous addresses (same channel on two centrals) are
  // dropped rather than guessed.
  const candidateByAddress = $derived.by(() => {
    const map = new Map<string, AlarmOutputCandidate | null>();
    for (const c of candidates) {
      map.set(c.channel_address, map.has(c.channel_address) ? null : c);
    }
    return map;
  });
  function extrasFor(o: AlarmOutput): AlarmOutputCandidate | undefined {
    return (
      candidateByChannel.get(`${o.central}|${o.channel_address}`) ??
      candidateByAddress.get(o.channel_address) ??
      undefined
    );
  }
  // ENUM picker options: raw wire value + localised label (same-order
  // parallel array from the candidates endpoint), with the
  // device-default empty choice first.
  function enumOptions(values: string[] | undefined, labels: string[] | undefined) {
    return [
      { value: "", label: t("alarm.outputs.device_default") },
      ...(values ?? []).map((v, i) => ({ value: v, label: labels?.[i] || v })),
    ];
  }
  // outputRepair flags an enrolled device-backed output whose channel
  // cannot back its class (typical: a pre-0.43 enrollment defaulting to
  // channel :1) — the server rejects saving such a row with 422. When
  // exactly one channel of the same device can back the class, it is
  // offered as a one-click repair target. Unknown devices (central
  // down, model not loaded) are never flagged.
  function outputRepair(o: AlarmOutput): { flagged: boolean; target?: AlarmOutputCandidate } {
    if (!DEVICE_BACKED.has(o.class as AlarmOutputClass) || candidates.length === 0) {
      return { flagged: false };
    }
    const devAddr = o.channel_address.split(":")[0] ?? "";
    const dev = candidates.filter((c) => c.device_address === devAddr);
    if (dev.length === 0) return { flagged: false };
    const ok = dev.some(
      (c) => c.channel_address === o.channel_address && ((c.classes ?? []) as string[]).includes(o.class),
    );
    if (ok) return { flagged: false };
    const eligible = dev.filter((c) => ((c.classes ?? []) as string[]).includes(o.class));
    return { flagged: true, target: eligible.length === 1 ? eligible[0] : undefined };
  }
  function repairOutput(o: AlarmOutput, target: AlarmOutputCandidate) {
    outputs = outputs.map((x) =>
      x.id === o.id ? { ...x, central: target.central, channel_address: target.channel_address } : x,
    );
    markDirty();
  }
  async function loadCandidates() {
    try {
      candidates = await api.listAlarmOutputCandidates();
    } catch (e) {
      // Candidates are an assist — the expert flow stays usable, but
      // the failure must surface (operating concept: no silent aborts).
      toastStore.error(t("alarm.outputs.candidates.load_failed"), friendlyError(e, t));
    }
  }

  // --- config accessors -------------------------------------------
  function outModes(o: AlarmOutput): string[] {
    const m = o.config?.modes;
    return Array.isArray(m) ? m.filter((x): x is string => typeof x === "string") : [];
  }
  function outBool(o: AlarmOutput, key: string): boolean {
    return o.config?.[key] === true;
  }
  function outNum(o: AlarmOutput, key: string): number | undefined {
    const v = o.config?.[key];
    return typeof v === "number" ? v : undefined;
  }
  function outStr(o: AlarmOutput, key: string): string {
    const v = o.config?.[key];
    return typeof v === "string" ? v : "";
  }
  // Legacy-aware readers: early builds wrote `tone` / `chirp_chime_tone`,
  // which the engine never read (it reads `acoustic_tone` and
  // `chirp_*_tone`). Reading falls back to the legacy key; saving writes
  // the engine key and clears the legacy one.
  function outAcousticTone(o: AlarmOutput): string {
    return outStr(o, "acoustic_tone") || outStr(o, "tone");
  }
  // SOUNDFILE_NNN ↔ numeric soundfile_index round-trip (device wire
  // labels are 1-based; unset falls back to the device default).
  function soundfileLabel(idx: number | undefined): string {
    return idx && idx > 0 ? `SOUNDFILE_${String(idx).padStart(3, "0")}` : "";
  }
  function soundfileIndex(label: string): number | undefined {
    const m = /^SOUNDFILE_(\d+)$/.exec(label);
    return m ? parseInt(m[1], 10) : undefined;
  }
  function outChirpTickTone(o: AlarmOutput): string {
    return outStr(o, "chirp_tick_tone") || outStr(o, "chirp_chime_tone");
  }

  function markDirty() {
    dirty = true;
  }

  function updateOutputConfig(id: string, patch: Record<string, unknown>) {
    outputs = outputs.map((o) => {
      if (o.id !== id) return o;
      const cfg: Record<string, unknown> = { ...(o.config ?? {}) };
      for (const [k, v] of Object.entries(patch)) {
        if (v === undefined) delete cfg[k];
        else cfg[k] = v;
      }
      return { ...o, config: cfg };
    });
    markDirty();
  }
  function toggleMode(o: AlarmOutput, mode: Mode) {
    const set = new Set(outModes(o));
    if (set.has(mode)) set.delete(mode);
    else set.add(mode);
    updateOutputConfig(o.id, { modes: MODES.filter((m) => set.has(m)) });
  }
  function setNum(o: AlarmOutput, key: string, raw: string) {
    const trimmed = raw.trim();
    const n = trimmed === "" ? undefined : Number(trimmed);
    updateOutputConfig(o.id, {
      [key]: n === undefined || Number.isNaN(n) ? undefined : n,
    });
  }
  function removeOutput(id: string) {
    outputs = outputs.filter((o) => o.id !== id);
    markDirty();
  }

  // --- data loading ------------------------------------------------
  async function loadOutputs() {
    if (!zoneId) {
      outputs = [];
      return;
    }
    loading = true;
    loadError = null;
    try {
      outputs = await api.listAlarmZoneOutputs(zoneId);
      dirty = false;
    } catch (err) {
      loadError = friendlyError(err, t);
    } finally {
      loading = false;
    }
  }

  async function save() {
    if (!zoneId) return;
    saving = true;
    try {
      await api.putAlarmZoneOutputs(zoneId, outputs);
      toastStore.success(t("alarm.toast.saved"));
      dirty = false;
      await alarmPanelStore.refresh();
      await loadOutputs();
    } catch (err) {
      toastStore.error(t("alarm.toast.save_failed"), friendlyError(err, t));
    } finally {
      saving = false;
    }
  }

  // --- test fire ---------------------------------------------------
  async function testFire(o: AlarmOutput) {
    // A test fire activates the real siren/light on the CCU, so it is a
    // genuinely destructive real-world action and is gated by a confirm
    // dialog. This is deliberately UNLIKE silence/disarm, which must act
    // on the first tap with no confirmation (safety invariants S3/S6).
    const ok = await confirmStore.ask({
      title: t("alarm.outputs.test.confirm.title"),
      body: t("alarm.outputs.test.confirm.body"),
      confirmLabel: t("alarm.outputs.test"),
      destructive: true,
    });
    if (!ok) return;
    testBusy = { ...testBusy, [o.id]: true };
    try {
      await api.testAlarmOutput(o.id, { optical_only: testOptical[o.id] === true });
      toastStore.success(t("alarm.toast.test_fired"));
    } catch (err) {
      toastStore.error(t("alarm.toast.test_failed"), friendlyError(err, t));
    } finally {
      testBusy = { ...testBusy, [o.id]: false };
    }
  }

  // --- add flow ----------------------------------------------------
  const addDeviceMatch = $derived(makeTextMatcher(addDeviceSearch));
  // Candidate mode: device-backed class without expert override — the
  // list comes from the backend capability ground truth, one row per
  // eligible channel.
  const addUseCandidates = $derived(DEVICE_BACKED.has(addClass) && !addExpert);
  const addClassCandidates = $derived(
    candidates
      .filter((c) => ((c.classes ?? []) as string[]).includes(addClass))
      .filter(
        (c) =>
          !addDeviceSearch ||
          addDeviceMatch(c.device_name ?? "") ||
          addDeviceMatch(c.channel_name ?? "") ||
          addDeviceMatch(c.device_address) ||
          addDeviceMatch(c.channel_address) ||
          addDeviceMatch(c.model),
      )
      .slice(0, 60),
  );
  const addCandidates = $derived(
    deviceStore.items
      .filter((d) => {
        if (!addExpert) {
          const hay = `${d.model} ${d.model_label ?? ""} ${d.name ?? ""}`;
          if (!OUTPUT_RE.test(hay)) return false;
        }
        if (addDeviceSearch) {
          return (
            addDeviceMatch(d.name ?? "") ||
            addDeviceMatch(d.address) ||
            addDeviceMatch(d.model) ||
            addDeviceMatch(d.model_label ?? "")
          );
        }
        return true;
      })
      .slice(0, 60),
  );

  function defaultConfig(cls: AlarmOutputClass): Record<string, unknown> {
    const base: Record<string, unknown> = { modes: ["full"] };
    if (CAPS[cls].duration) base.duration_s = 180;
    // Dimmer levels are 0–1 on the wire; default to full brightness.
    if (cls === "alarm_light") base.level = 1;
    return base;
  }
  function openAdd() {
    addOpen = true;
    addClass = "acoustic_siren";
    addExpert = false;
    addDeviceSearch = "";
    addDevice = null;
    addCandidate = null;
    addChannel = "";
    addName = "";
  }
  // The class and the expert toggle both switch the candidate pool, so
  // a prior pick may no longer be valid — always reset the selection.
  function resetAddSelection() {
    addDevice = null;
    addCandidate = null;
    addChannel = "";
    addName = "";
    addSysvarCentral = "";
    addSysvarExisting = false;
    addSysvarName = "";
  }
  function pickAddDevice(d: DeviceSummary) {
    addDevice = d;
    addChannel = `${d.address}:1`;
    addName = d.name ?? "";
  }
  function pickAddCandidate(c: AlarmOutputCandidate) {
    addCandidate = c;
    addName = c.channel_name || c.device_name || "";
  }
  const canAdd = $derived(
    addClass === "sysvar_mirror"
      ? addSysvarCentral !== "" && addSysvarName.trim() !== ""
      : addClass === "notification"
        ? true
        : addUseCandidates
          ? !!addCandidate
          : !!addDevice && addChannel.trim() !== "",
  );
  function buildAddOutput(): AlarmOutput | null {
    if (addClass === "sysvar_mirror") {
      const svName = addSysvarName.trim();
      if (!addSysvarCentral || !svName) return null;
      const config: Record<string, unknown> = { ...defaultConfig(addClass), sysvar_name: svName };
      if (addSysvarExisting) config.sysvar_existing = true;
      return {
        id: `${addSysvarCentral}|sysvar:${svName}|${addClass}`,
        class: addClass,
        central: addSysvarCentral,
        channel_address: "",
        name: addName.trim() || svName,
        config,
      };
    }
    if (addClass === "notification") {
      return {
        id: `notification|${crypto.randomUUID()}`,
        class: addClass,
        central: "",
        channel_address: "",
        name: addName.trim() || undefined,
        config: defaultConfig(addClass),
      };
    }
    const central = addUseCandidates ? (addCandidate?.central ?? "") : (addDevice?.central ?? "");
    const channel = addUseCandidates ? (addCandidate?.channel_address ?? "") : addChannel.trim();
    if (channel === "") return null;
    return {
      id: `${central}|${channel}|${addClass}`,
      class: addClass,
      central,
      channel_address: channel,
      name: addName.trim() || undefined,
      config: defaultConfig(addClass),
    };
  }
  function confirmAdd() {
    if (!canAdd) return;
    const output = buildAddOutput();
    if (!output) return;
    if (outputs.some((o) => o.id === output.id)) {
      addOpen = false;
      return;
    }
    outputs = [...outputs, output];
    markDirty();
    addOpen = false;
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === "Escape" && addOpen) addOpen = false;
  }

  const zoneOptions = $derived(zones.map((a) => ({ value: a.id, label: a.name })));

  $effect(() => {
    if (zones.length > 0 && !zones.some((a) => a.id === zoneId)) {
      zoneId = zones[0].id;
    }
  });

  let loadedFor = $state("");
  $effect(() => {
    if (zoneId && zoneId !== loadedFor) {
      loadedFor = zoneId;
      void loadOutputs();
    }
  });

  onMount(() => {
    deviceStore.refresh();
    deviceStore.ensureStream();
    void loadCandidates();
  });
</script>

<svelte:window onkeydown={onKeydown} />

{#if alarmPanelStore.loading && zones.length === 0}
  <LoadingState />
{:else if alarmPanelStore.error && zones.length === 0}
  <ErrorState message={alarmPanelStore.error} onRetry={() => void alarmPanelStore.refresh()} />
{:else if zones.length === 0}
  <EmptyState
    icon="mdi:shield-home"
    message={t("alarm.overview.empty")}
    description={t("alarm.overview.empty.description")}
  >
    {#snippet action()}
      <a href="#/alarm/wizard">
        <Button variant="outline" size="sm">{t("alarm.wizard.launch")}</Button>
      </a>
    {/snippet}
  </EmptyState>
{:else}
  <!-- Toolbar: zone selector + add -->
  <div class="mb-4 flex flex-wrap items-center gap-3">
    <label class="flex items-center gap-2 text-sm text-[var(--ha-secondary-text-color)]">
      <span>{t("alarm.sensors.zone")}</span>
      <div class="min-w-48">
        <Select options={zoneOptions} bind:value={zoneId} />
      </div>
    </label>
    <Button size="sm" class="ml-auto" onclick={openAdd}>
      <Icon name="mdi:plus" size={16} />
      {t("alarm.outputs.add")}
    </Button>
  </div>

  {#if dirty}
    <div
      class="mb-3 flex flex-wrap items-center gap-2 rounded-md border border-[color-mix(in_srgb,var(--ha-warning-color)_40%,transparent)] bg-[color-mix(in_srgb,var(--ha-warning-color)_12%,transparent)] p-2 text-sm"
    >
      <span class="font-medium text-[var(--ha-warning-color)]">{t("common.modified")}</span>
      <div class="ml-auto flex gap-2">
        <Button variant="outline" size="sm" onclick={() => void loadOutputs()} disabled={saving}>
          {t("common.reset")}
        </Button>
        <Button size="sm" onclick={() => void save()} disabled={saving}>
          {saving ? t("common.saving") : t("common.save")}
        </Button>
      </div>
    </div>
  {/if}

  {#if loadError}
    <ErrorState message={loadError} onRetry={() => void loadOutputs()} />
  {:else if loading && outputs.length === 0}
    <LoadingState message={t("common.loading")} />
  {:else if outputs.length === 0}
    <EmptyState
      icon="mdi:volume"
      message={t("alarm.outputs.empty")}
      description={t("alarm.outputs.empty.description")}
    >
      {#snippet action()}
        <Button variant="outline" size="sm" onclick={openAdd}>
          <Icon name="mdi:plus" size={16} />
          {t("alarm.outputs.add")}
        </Button>
      {/snippet}
    </EmptyState>
  {:else}
    <div class="grid grid-cols-1 gap-3 lg:grid-cols-2">
      {#each outputs as o (o.id)}
        {@const caps = CAPS[o.class]}
        {@const ex = extrasFor(o)}
        {@const repair = outputRepair(o)}
        <Card class="flex flex-col gap-3 p-4">
          <!-- Header -->
          <div class="flex items-start gap-2">
            <Icon name={CLASS_ICON[o.class]} size={22} class="mt-0.5 text-[var(--ha-primary-color)]" />
            <div class="min-w-0 flex-1">
              <p class="truncate font-medium text-[var(--ha-primary-text-color)]" title={o.name || o.channel_address}>
                {o.name || o.channel_address}
              </p>
              <p class="truncate font-mono text-xs text-[var(--ha-secondary-text-color)]">{o.channel_address}</p>
            </div>
            <div class="flex shrink-0 items-center gap-1">
              <Badge variant="default">{t(`alarm.output_class.${o.class}`)}</Badge>
              <button
                type="button"
                class="text-[var(--ha-secondary-text-color)] hover:text-[var(--ha-error-color)]"
                title={t("common.remove")}
                aria-label={t("common.remove")}
                onclick={() => removeOutput(o.id)}
              >
                <Icon name="mdi:trash-can" size={16} />
              </button>
            </div>
          </div>

          <!-- Class caveats -->
          {#if o.class === "smoke_sounder"}
            <div
              class="flex gap-2 rounded-md border border-[color-mix(in_srgb,var(--ha-warning-color)_40%,transparent)] bg-[color-mix(in_srgb,var(--ha-warning-color)_10%,transparent)] p-2 text-xs text-[var(--ha-primary-text-color)]"
            >
              <Icon name="mdi:alert" size={16} class="mt-0.5 shrink-0 text-[var(--ha-warning-color)]" />
              <span>{t("alarm.outputs.smoke_caveat")}</span>
            </div>
          {:else if o.class === "switched_siren"}
            <div
              class="flex gap-2 rounded-md border border-[color-mix(in_srgb,var(--ha-warning-color)_40%,transparent)] bg-[color-mix(in_srgb,var(--ha-warning-color)_10%,transparent)] p-2 text-xs text-[var(--ha-primary-text-color)]"
            >
              <Icon name="mdi:alert" size={16} class="mt-0.5 shrink-0 text-[var(--ha-warning-color)]" />
              <span>{t("alarm.outputs.switched_caveat")}</span>
            </div>
          {/if}

          {#if repair.flagged}
            <div
              class="flex gap-2 rounded-md border border-[color-mix(in_srgb,var(--ha-error-color)_45%,transparent)] bg-[color-mix(in_srgb,var(--ha-error-color)_10%,transparent)] p-2 text-xs text-[var(--ha-primary-text-color)]"
            >
              <Icon name="mdi:alert-circle" size={16} class="mt-0.5 shrink-0 text-[var(--ha-error-color)]" aria-label="" />
              <div class="flex min-w-0 flex-col items-start gap-1.5">
                <span>{t("alarm.outputs.channel_mismatch")}</span>
                {#if repair.target}
                  <Button size="sm" variant="outline" onclick={() => repair.target && repairOutput(o, repair.target)}>
                    {t("alarm.outputs.channel_mismatch.repair")} → {repair.target.channel_address}
                  </Button>
                {/if}
              </div>
            </div>
          {/if}

          <!-- Per-mode assignment -->
          <div class="flex flex-wrap items-center gap-1.5">
            <span class="text-xs text-[var(--ha-secondary-text-color)]">{t("alarm.sensors.modes")}</span>
            {#each MODES as mode (mode)}
              {@const on = outModes(o).includes(mode)}
              <button
                type="button"
                aria-pressed={on}
                class="rounded-full border px-2 py-0.5 text-xs transition {on
                  ? 'border-[var(--ha-primary-color)] bg-[color-mix(in_srgb,var(--ha-primary-color)_15%,transparent)] text-[var(--ha-primary-color)]'
                  : 'border-[var(--ha-divider-color)] text-[var(--ha-secondary-text-color)] hover:border-[var(--ha-secondary-text-color)]'}"
                onclick={() => toggleMode(o, mode)}
              >
                {t(`alarm.mode.${mode}`)}
              </button>
            {/each}
          </div>

          {#if o.class === "sysvar_mirror"}
            <div class="flex flex-col gap-3">
              <div class="flex flex-col gap-1">
                <span class="text-xs text-[var(--ha-secondary-text-color)]">{t("alarm.outputs.sysvar.name")}</span>
                <p class="font-mono text-sm text-[var(--ha-primary-text-color)]">
                  {outStr(o, "sysvar_name") || "—"}
                  {#if outBool(o, "sysvar_existing")}
                    <Badge variant="muted">{t("alarm.outputs.sysvar.existing.badge")}</Badge>
                  {/if}
                </p>
              </div>
              {#if !outBool(o, "sysvar_existing")}
                <div class="flex flex-col gap-1">
                  <label class="flex items-center justify-between gap-2 text-sm text-[var(--ha-primary-text-color)]">
                    <span>{t("alarm.outputs.sysvar.allow_disarm")}</span>
                    <Switch
                      checked={outBool(o, "sysvar_allow_disarm")}
                      onCheckedChange={(v) => updateOutputConfig(o.id, { sysvar_allow_disarm: v || undefined })}
                    />
                  </label>
                  <p class="text-xs text-[var(--ha-secondary-text-color)]">{t("alarm.outputs.sysvar.allow_disarm.hint")}</p>
                </div>
              {/if}
            </div>
          {:else if o.class === "notification"}
            <div class="flex flex-col gap-3">
              <div class="flex flex-col gap-1">
                <label class="flex items-center justify-between gap-2 text-sm text-[var(--ha-primary-text-color)]">
                  <span>{t("alarm.outputs.notify.mqtt")}</span>
                  <Switch
                    checked={o.config?.notify_mqtt !== false}
                    onCheckedChange={(v) => updateOutputConfig(o.id, { notify_mqtt: v ? undefined : false })}
                  />
                </label>
                <p class="text-xs text-[var(--ha-secondary-text-color)]">{t("alarm.outputs.notify.mqtt.hint")}</p>
              </div>
              <div class="flex flex-col gap-1">
                <label class="flex items-center justify-between gap-2 text-sm text-[var(--ha-primary-text-color)]">
                  <span>{t("alarm.outputs.notify.webhook")}</span>
                  <Switch
                    checked={o.config?.notify_webhook !== false}
                    onCheckedChange={(v) => updateOutputConfig(o.id, { notify_webhook: v ? undefined : false })}
                  />
                </label>
                <p class="text-xs text-[var(--ha-secondary-text-color)]">{t("alarm.outputs.notify.webhook.hint")}</p>
              </div>
            </div>
          {/if}

          <!-- Numeric / text fields -->
          {#if caps.duration || caps.tone || caps.optical || caps.level || caps.chirpTones}
            <div class="grid grid-cols-2 gap-3">
              {#if caps.duration}
                <label class="flex flex-col gap-1 text-xs text-[var(--ha-secondary-text-color)]">
                  {t("alarm.outputs.duration")}
                  <input
                    type="number"
                    min="0"
                    class="h-9 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-2 text-sm text-[var(--ha-primary-text-color)]"
                    value={outNum(o, "duration_s") ?? ""}
                    oninput={(e) => setNum(o, "duration_s", e.currentTarget.value)}
                  />
                  <span>{t("alarm.outputs.duration.hint")}</span>
                </label>
              {/if}
              {#if caps.level}
                <label class="flex flex-col gap-1 text-xs text-[var(--ha-secondary-text-color)]">
                  {t("alarm.outputs.level")}
                  <input
                    type="number"
                    min="0"
                    max="1"
                    step="0.05"
                    class="h-9 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-2 text-sm text-[var(--ha-primary-text-color)]"
                    value={outNum(o, "level") ?? ""}
                    oninput={(e) => setNum(o, "level", e.currentTarget.value)}
                  />
                  <span>{t("alarm.outputs.level.hint")}</span>
                </label>
              {/if}
              {#if caps.tone}
                <label class="flex flex-col gap-1 text-xs text-[var(--ha-secondary-text-color)]">
                  {t("alarm.outputs.tone")}
                  {#if ex?.available_tones?.length}
                    <Select
                      value={outAcousticTone(o)}
                      onValueChange={(v) => updateOutputConfig(o.id, { acoustic_tone: v || undefined, tone: undefined })}
                      options={enumOptions(ex.available_tones, ex.available_tone_labels)}
                    />
                  {:else}
                    <Input
                      value={outAcousticTone(o)}
                      oninput={(e) =>
                        updateOutputConfig(o.id, { acoustic_tone: e.currentTarget.value || undefined, tone: undefined })}
                    />
                  {/if}
                  <span>{t("alarm.outputs.tone.hint")}</span>
                </label>
              {/if}
              {#if caps.chirpTones}
                {#if ex?.available_soundfiles?.length}
                  <!-- MP3-player chirp (e.g. HmIP-MP3P): one soundfile pick
                       instead of siren tone labels. -->
                  <label class="flex flex-col gap-1 text-xs text-[var(--ha-secondary-text-color)]">
                    {t("alarm.outputs.soundfile")}
                    <Select
                      value={soundfileLabel(outNum(o, "soundfile_index"))}
                      onValueChange={(v) =>
                        updateOutputConfig(o.id, { soundfile_index: soundfileIndex(v) })}
                      options={enumOptions(ex.available_soundfiles, ex.available_soundfile_labels)}
                    />
                    <span>{t("alarm.outputs.soundfile.hint")}</span>
                  </label>
                {:else}
                  <label class="flex flex-col gap-1 text-xs text-[var(--ha-secondary-text-color)]">
                    {t("alarm.outputs.chirp_arm_tone")}
                    {#if ex?.available_tones?.length}
                      <Select
                        value={outStr(o, "chirp_arm_tone")}
                        onValueChange={(v) => updateOutputConfig(o.id, { chirp_arm_tone: v || undefined })}
                        options={enumOptions(ex.available_tones, ex.available_tone_labels)}
                      />
                    {:else}
                      <Input
                        value={outStr(o, "chirp_arm_tone")}
                        oninput={(e) =>
                          updateOutputConfig(o.id, { chirp_arm_tone: e.currentTarget.value || undefined })}
                      />
                    {/if}
                    <span>{t("alarm.outputs.chirp_arm_tone.hint")}</span>
                  </label>
                  <label class="flex flex-col gap-1 text-xs text-[var(--ha-secondary-text-color)]">
                    {t("alarm.outputs.chirp_disarm_tone")}
                    {#if ex?.available_tones?.length}
                      <Select
                        value={outStr(o, "chirp_disarm_tone")}
                        onValueChange={(v) => updateOutputConfig(o.id, { chirp_disarm_tone: v || undefined })}
                        options={enumOptions(ex.available_tones, ex.available_tone_labels)}
                      />
                    {:else}
                      <Input
                        value={outStr(o, "chirp_disarm_tone")}
                        oninput={(e) =>
                          updateOutputConfig(o.id, { chirp_disarm_tone: e.currentTarget.value || undefined })}
                      />
                    {/if}
                    <span>{t("alarm.outputs.chirp_disarm_tone.hint")}</span>
                  </label>
                  <label class="flex flex-col gap-1 text-xs text-[var(--ha-secondary-text-color)]">
                    {t("alarm.outputs.chirp_tick_tone")}
                    {#if ex?.available_tones?.length}
                      <Select
                        value={outChirpTickTone(o)}
                        onValueChange={(v) =>
                          updateOutputConfig(o.id, { chirp_tick_tone: v || undefined, chirp_chime_tone: undefined })}
                        options={enumOptions(ex.available_tones, ex.available_tone_labels)}
                      />
                    {:else}
                      <Input
                        value={outChirpTickTone(o)}
                        oninput={(e) =>
                          updateOutputConfig(o.id, {
                            chirp_tick_tone: e.currentTarget.value || undefined,
                            chirp_chime_tone: undefined,
                          })}
                      />
                    {/if}
                    <span>{t("alarm.outputs.chirp_tick_tone.hint")}</span>
                  </label>
                {/if}
              {/if}
              {#if caps.optical}
                <label class="flex flex-col gap-1 text-xs text-[var(--ha-secondary-text-color)]">
                  {t("alarm.outputs.optical_pattern")}
                  {#if ex?.available_lights?.length}
                    <Select
                      value={outStr(o, "optical_pattern")}
                      onValueChange={(v) => updateOutputConfig(o.id, { optical_pattern: v || undefined })}
                      options={enumOptions(ex.available_lights, ex.available_light_labels)}
                    />
                  {:else}
                    <Input value={outStr(o, "optical_pattern")} oninput={(e) => updateOutputConfig(o.id, { optical_pattern: e.currentTarget.value || undefined })} />
                  {/if}
                  <span>{t("alarm.outputs.optical_pattern.hint")}</span>
                </label>
              {/if}
            </div>
          {/if}

          <!-- Switches -->
          <div class="flex flex-col gap-3">
            {#if caps.outdoor}
              <div class="flex flex-col gap-1">
                <label class="flex items-center justify-between gap-2 text-sm text-[var(--ha-primary-text-color)]">
                  <span>{t("alarm.outputs.outdoor")}</span>
                  <Switch checked={outBool(o, "outdoor")} onCheckedChange={(v) => updateOutputConfig(o.id, { outdoor: v })} />
                </label>
                <p class="text-xs text-[var(--ha-secondary-text-color)]">{t("alarm.outputs.outdoor.hint")}</p>
              </div>
            {/if}
            <div class="flex flex-col gap-1">
              <label class="flex items-center justify-between gap-2 text-sm text-[var(--ha-primary-text-color)]">
                <span>{t("alarm.outputs.shared_with_ccu")}</span>
                <Switch checked={outBool(o, "shared_with_ccu")} onCheckedChange={(v) => updateOutputConfig(o.id, { shared_with_ccu: v })} />
              </label>
              <p class="text-xs text-[var(--ha-secondary-text-color)]">{t("alarm.outputs.shared_with_ccu.hint")}</p>
            </div>
          </div>

          <!-- Test fire -->
          <div class="mt-1 flex flex-wrap items-center gap-3 border-t border-[var(--ha-divider-color)] pt-3">
            <label
              class="flex items-center gap-1.5 text-xs text-[var(--ha-secondary-text-color)]"
              title={t("alarm.outputs.test_optical_only.hint")}
            >
              <input
                type="checkbox"
                checked={testOptical[o.id] === true}
                onchange={(e) => (testOptical = { ...testOptical, [o.id]: e.currentTarget.checked })}
              />
              {t("alarm.outputs.test_optical_only")}
            </label>
            <Button
              variant="outline"
              size="sm"
              class="ml-auto"
              disabled={testBusy[o.id] === true}
              onclick={() => void testFire(o)}
            >
              <Icon name="mdi:play" size={16} />
              {t("alarm.outputs.test")}
            </Button>
          </div>
        </Card>
      {/each}
    </div>
  {/if}

  <!-- Add-output flow -->
  {#if addOpen}
    <div
      class="fixed inset-0 z-40 bg-black/40"
      role="presentation"
      onclick={() => (addOpen = false)}
    ></div>
    <div
      class="fixed right-0 top-0 z-50 flex h-full w-full max-w-md flex-col overflow-y-auto border-l border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] shadow-xl"
      role="dialog"
      aria-modal="true"
      aria-label={t("alarm.outputs.add")}
    >
      <header class="flex items-center gap-2 border-b border-[var(--ha-divider-color)] p-4">
        <Icon name="mdi:plus" size={20} class="text-[var(--ha-primary-color)]" />
        <h2 class="flex-1 font-semibold text-[var(--ha-primary-text-color)]">{t("alarm.outputs.add")}</h2>
        <button
          type="button"
          class="text-[var(--ha-secondary-text-color)] hover:text-[var(--ha-primary-text-color)]"
          aria-label={t("common.close")}
          onclick={() => (addOpen = false)}
        >
          <Icon name="mdi:close" size={20} />
        </button>
      </header>

      <div class="flex flex-col gap-4 p-4">
        <div class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.outputs.field.class")}</span>
          <Select
            value={addClass}
            onValueChange={(v) => {
              addClass = v as AlarmOutputClass;
              resetAddSelection();
            }}
            options={CLASSES.map((c) => ({ value: c, label: t(`alarm.output_class.${c}`) }))}
          />
          <span class="text-xs text-[var(--ha-secondary-text-color)]">{t(`alarm.output_class.${addClass}.hint`)}</span>
        </div>

        {#if addClass === "sysvar_mirror"}
          <!-- Sysvar mirror: no device — central + variable target. -->
          <div class="flex flex-col gap-1.5">
            <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.outputs.sysvar.central")}</span>
            <Select
              value={addSysvarCentral}
              onValueChange={(v) => {
                addSysvarCentral = v;
                addSysvarName = "";
              }}
              options={centralOptions}
            />
            <span class="text-xs text-[var(--ha-secondary-text-color)]">{t("alarm.outputs.sysvar.central.hint")}</span>
          </div>
          <label class="flex items-center justify-between gap-2 text-sm text-[var(--ha-primary-text-color)]">
            <span>{t("alarm.outputs.sysvar.existing")}</span>
            <Switch
              checked={addSysvarExisting}
              onCheckedChange={(v) => {
                addSysvarExisting = v;
                addSysvarName = "";
              }}
            />
          </label>
          <p class="text-xs text-[var(--ha-secondary-text-color)]">{t("alarm.outputs.sysvar.existing.hint")}</p>
          {#if addSysvarExisting}
            <div class="flex flex-col gap-1.5">
              <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.outputs.sysvar.pick")}</span>
              {#if alarmSysvarOptions.length === 0}
                <p class="text-xs text-[var(--ha-secondary-text-color)]">{t("alarm.outputs.sysvar.none")}</p>
              {:else}
                <Select
                  value={addSysvarName}
                  onValueChange={(v) => (addSysvarName = v)}
                  options={alarmSysvarOptions}
                />
              {/if}
            </div>
          {:else}
            <label class="flex flex-col gap-1.5">
              <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.outputs.sysvar.name")}</span>
              <Input bind:value={addSysvarName} />
              <span class="text-xs text-[var(--ha-secondary-text-color)]">{t("alarm.outputs.sysvar.name.hint")}</span>
            </label>
          {/if}
          <label class="flex flex-col gap-1.5">
            <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.sensors.field.name")}</span>
            <Input bind:value={addName} />
          </label>
        {:else if addClass === "notification"}
          <!-- Notification: no device — event fan-out to the enrolled planes. -->
          <p class="text-xs text-[var(--ha-secondary-text-color)]">{t("alarm.outputs.notification.note")}</p>
          <label class="flex flex-col gap-1.5">
            <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.sensors.field.name")}</span>
            <Input bind:value={addName} />
          </label>
        {:else}
        <div class="flex flex-col gap-1.5">
          <div class="flex items-center justify-between">
            <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.sensors.field.device")}</span>
            <label class="flex items-center gap-1.5 text-xs text-[var(--ha-secondary-text-color)]" title={t("alarm.outputs.expert.hint")}>
              <input type="checkbox" bind:checked={addExpert} onchange={resetAddSelection} />
              {t("alarm.outputs.expert")}
            </label>
          </div>
          <Input type="search" placeholder={t("common.search")} bind:value={addDeviceSearch} />
          <div class="mt-1 max-h-48 overflow-y-auto rounded-md border border-[var(--ha-divider-color)]">
            {#if addUseCandidates}
              {#if addClassCandidates.length === 0}
                <p class="p-3 text-center text-xs text-[var(--ha-secondary-text-color)]">{t("alarm.outputs.candidates.empty")}</p>
              {:else}
                {#each addClassCandidates as c (`${c.central}|${c.channel_address}`)}
                  <button
                    type="button"
                    class="flex w-full flex-col items-start gap-0.5 border-b border-[var(--ha-divider-color)] px-3 py-2 text-left transition last:border-0 hover:bg-[var(--ha-secondary-background-color)] {addCandidate?.channel_address ===
                      c.channel_address && addCandidate?.central === c.central
                      ? 'bg-[color-mix(in_srgb,var(--ha-primary-color)_12%,transparent)]'
                      : ''}"
                    onclick={() => pickAddCandidate(c)}
                  >
                    <span class="truncate text-sm text-[var(--ha-primary-text-color)]">
                      {c.device_name || c.device_address}{c.channel_name ? ` · ${c.channel_name}` : ""}
                    </span>
                    <span class="truncate font-mono text-xs text-[var(--ha-secondary-text-color)]">{candidateModelLabel(c)} · {c.channel_address}</span>
                    {#if (c.rooms ?? []).length > 0 || (c.functions ?? []).length > 0}
                      <span class="truncate text-xs text-[var(--ha-secondary-text-color)]">
                        {[...(c.rooms ?? []), ...(c.functions ?? [])].join(" · ")}
                      </span>
                    {/if}
                  </button>
                {/each}
              {/if}
            {:else if addCandidates.length === 0}
              <p class="p-3 text-center text-xs text-[var(--ha-secondary-text-color)]">{t("alarm.sensors.add.no_devices")}</p>
            {:else}
              {#each addCandidates as d (d.address)}
                <button
                  type="button"
                  class="flex w-full flex-col items-start gap-0.5 border-b border-[var(--ha-divider-color)] px-3 py-2 text-left transition last:border-0 hover:bg-[var(--ha-secondary-background-color)] {addDevice?.address ===
                  d.address
                    ? 'bg-[color-mix(in_srgb,var(--ha-primary-color)_12%,transparent)]'
                    : ''}"
                  onclick={() => pickAddDevice(d)}
                >
                  <span class="truncate text-sm text-[var(--ha-primary-text-color)]">{d.name || d.address}</span>
                  <span class="truncate font-mono text-xs text-[var(--ha-secondary-text-color)]">{d.model_label || d.model} · {d.address}</span>
                  {#if (d.rooms ?? []).length > 0}
                    <span class="truncate text-xs text-[var(--ha-secondary-text-color)]">
                      {(d.rooms ?? []).join(", ")}
                    </span>
                  {/if}
                </button>
              {/each}
            {/if}
          </div>
        </div>

        {#if addUseCandidates && addCandidate}
          <div class="flex flex-col gap-1.5">
            <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.sensors.field.name")}</span>
            <Input bind:value={addName} />
          </div>
        {:else if !addUseCandidates && addDevice}
          <div class="flex flex-col gap-1.5">
            <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.sensors.field.channel")}</span>
            <Input bind:value={addChannel} />
            <span class="text-xs text-[var(--ha-secondary-text-color)]">{t("alarm.outputs.channel.hint")}</span>
          </div>
          <div class="flex flex-col gap-1.5">
            <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.sensors.field.name")}</span>
            <Input bind:value={addName} />
          </div>
        {/if}
        {/if}
      </div>

      <footer class="mt-auto flex gap-2 border-t border-[var(--ha-divider-color)] p-4">
        <Button variant="outline" size="sm" onclick={() => (addOpen = false)}>{t("common.cancel")}</Button>
        <Button size="sm" class="ml-auto" disabled={!canAdd} onclick={confirmAdd}>
          <Icon name="mdi:plus" size={16} />
          {t("common.add")}
        </Button>
      </footer>
    </div>
  {/if}
{/if}
