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
    AlarmOutputClass,
    DeviceSummary,
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
  // output set of one alarm area as class cards. Each card carries the
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
  // decides which controls apply (§7).
  type Caps = {
    policy: boolean;
    outdoor: boolean;
    duration: boolean;
    tone: boolean;
    optical: boolean;
    level: boolean;
  };
  const CAPS: Record<AlarmOutputClass, Caps> = {
    acoustic_siren: { policy: true, outdoor: true, duration: true, tone: true, optical: false, level: false },
    switched_siren: { policy: true, outdoor: true, duration: true, tone: false, optical: false, level: false },
    smoke_sounder: { policy: true, outdoor: false, duration: false, tone: false, optical: false, level: false },
    optical_siren: { policy: false, outdoor: true, duration: true, tone: false, optical: true, level: false },
    alarm_light: { policy: false, outdoor: true, duration: true, tone: false, optical: true, level: true },
    chirp: { policy: false, outdoor: false, duration: false, tone: true, optical: false, level: false },
    notification: { policy: true, outdoor: false, duration: false, tone: false, optical: false, level: false },
    sysvar_mirror: { policy: false, outdoor: false, duration: false, tone: false, optical: false, level: false },
  };

  // Devices surfaced by the add-output assist by default: sirens, switch
  // actuators (plug-in sirens / alarm lights), smoke sounders. Expert mode
  // widens the list to every modelled actuator (§12.2).
  const OUTPUT_RE =
    /asir|sir|swsd|ps[m]?\b|psm|switch|schalt|dimmer|dim|light|licht|lamp|relay|relais|bsm|fsm|dr\b|mp3/i;

  // --- area state --------------------------------------------------
  const areas = $derived(alarmPanelStore.areasConfig);
  let areaId = $state("");

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
  let addChannel = $state("");
  let addName = $state("");

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
  function outPolicy(o: AlarmOutput): "loud" | "silent" {
    return o.config?.policy === "silent" ? "silent" : "loud";
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
    if (!areaId) {
      outputs = [];
      return;
    }
    loading = true;
    loadError = null;
    try {
      outputs = await api.listAlarmAreaOutputs(areaId);
      dirty = false;
    } catch (err) {
      loadError = friendlyError(err, t);
    } finally {
      loading = false;
    }
  }

  async function save() {
    if (!areaId) return;
    saving = true;
    try {
      await api.putAlarmAreaOutputs(areaId, outputs);
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
    const base: Record<string, unknown> = { modes: ["full"], policy: "loud" };
    if (CAPS[cls].duration) base.duration_s = 180;
    if (cls === "alarm_light") base.level = 100;
    return base;
  }
  function openAdd() {
    addOpen = true;
    addClass = "acoustic_siren";
    addExpert = false;
    addDeviceSearch = "";
    addDevice = null;
    addChannel = "";
    addName = "";
  }
  function pickAddDevice(d: DeviceSummary) {
    addDevice = d;
    addChannel = `${d.address}:1`;
    addName = d.name ?? "";
  }
  const canAdd = $derived(!!addDevice && addChannel.trim() !== "");
  function confirmAdd() {
    if (!addDevice || !canAdd) return;
    const channel = addChannel.trim();
    const output: AlarmOutput = {
      id: `${addDevice.central ?? ""}|${channel}|${addClass}`,
      class: addClass,
      central: addDevice.central ?? "",
      channel_address: channel,
      name: addName.trim() || undefined,
      config: defaultConfig(addClass),
    };
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

  const areaOptions = $derived(areas.map((a) => ({ value: a.id, label: a.name })));

  $effect(() => {
    if (areas.length > 0 && !areas.some((a) => a.id === areaId)) {
      areaId = areas[0].id;
    }
  });

  let loadedFor = $state("");
  $effect(() => {
    if (areaId && areaId !== loadedFor) {
      loadedFor = areaId;
      void loadOutputs();
    }
  });

  onMount(() => {
    deviceStore.refresh();
    deviceStore.ensureStream();
  });
</script>

<svelte:window onkeydown={onKeydown} />

{#if areas.length === 0}
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
  <!-- Toolbar: area selector + add -->
  <div class="mb-4 flex flex-wrap items-center gap-3">
    <label class="flex items-center gap-2 text-sm text-[var(--ha-secondary-text-color)]">
      <span>{t("alarm.sensors.area")}</span>
      <div class="min-w-48">
        <Select options={areaOptions} bind:value={areaId} />
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

          <!-- Loud / silent policy -->
          {#if caps.policy}
            {@const policy = outPolicy(o)}
            <div class="flex items-center gap-2">
              <span class="text-xs text-[var(--ha-secondary-text-color)]">{t("alarm.outputs.policy")}</span>
              <div class="inline-flex overflow-hidden rounded-md border border-[var(--ha-divider-color)]">
                {#each ["loud", "silent"] as p (p)}
                  <button
                    type="button"
                    aria-pressed={policy === p}
                    class="px-3 py-1 text-xs transition {policy === p
                      ? 'bg-[color-mix(in_srgb,var(--ha-primary-color)_15%,transparent)] text-[var(--ha-primary-color)]'
                      : 'text-[var(--ha-secondary-text-color)] hover:bg-black/5 dark:hover:bg-white/5'}"
                    onclick={() => updateOutputConfig(o.id, { policy: p })}
                  >
                    {t(`alarm.outputs.policy.${p}`)}
                  </button>
                {/each}
              </div>
            </div>
          {/if}

          <!-- Numeric / text fields -->
          {#if caps.duration || caps.tone || caps.optical || caps.level}
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
                </label>
              {/if}
              {#if caps.level}
                <label class="flex flex-col gap-1 text-xs text-[var(--ha-secondary-text-color)]">
                  {t("alarm.outputs.level")}
                  <input
                    type="number"
                    min="0"
                    max="100"
                    class="h-9 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-2 text-sm text-[var(--ha-primary-text-color)]"
                    value={outNum(o, "level") ?? ""}
                    oninput={(e) => setNum(o, "level", e.currentTarget.value)}
                  />
                </label>
              {/if}
              {#if caps.tone}
                <label class="flex flex-col gap-1 text-xs text-[var(--ha-secondary-text-color)]">
                  {t("alarm.outputs.tone")}
                  <Input value={outStr(o, "tone")} oninput={(e) => updateOutputConfig(o.id, { tone: e.currentTarget.value || undefined })} />
                </label>
              {/if}
              {#if caps.optical}
                <label class="flex flex-col gap-1 text-xs text-[var(--ha-secondary-text-color)]">
                  {t("alarm.outputs.optical_pattern")}
                  <Input value={outStr(o, "optical_pattern")} oninput={(e) => updateOutputConfig(o.id, { optical_pattern: e.currentTarget.value || undefined })} />
                </label>
              {/if}
            </div>
          {/if}

          <!-- Switches -->
          <div class="flex flex-col gap-2">
            {#if caps.outdoor}
              <label class="flex items-center justify-between gap-2 text-sm text-[var(--ha-primary-text-color)]">
                <span>{t("alarm.outputs.outdoor")}</span>
                <Switch checked={outBool(o, "outdoor")} onCheckedChange={(v) => updateOutputConfig(o.id, { outdoor: v })} />
              </label>
            {/if}
            <label class="flex items-center justify-between gap-2 text-sm text-[var(--ha-primary-text-color)]">
              <span>{t("alarm.outputs.shared_with_ccu")}</span>
              <Switch checked={outBool(o, "shared_with_ccu")} onCheckedChange={(v) => updateOutputConfig(o.id, { shared_with_ccu: v })} />
            </label>
          </div>

          <!-- Test fire -->
          <div class="mt-1 flex flex-wrap items-center gap-3 border-t border-[var(--ha-divider-color)] pt-3">
            <label class="flex items-center gap-1.5 text-xs text-[var(--ha-secondary-text-color)]">
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
            onValueChange={(v) => (addClass = v as AlarmOutputClass)}
            options={CLASSES.map((c) => ({ value: c, label: t(`alarm.output_class.${c}`) }))}
          />
        </div>

        <div class="flex flex-col gap-1.5">
          <div class="flex items-center justify-between">
            <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.sensors.field.device")}</span>
            <label class="flex items-center gap-1.5 text-xs text-[var(--ha-secondary-text-color)]" title={t("alarm.outputs.expert.hint")}>
              <input type="checkbox" bind:checked={addExpert} />
              {t("alarm.outputs.expert")}
            </label>
          </div>
          <Input type="search" placeholder={t("common.search")} bind:value={addDeviceSearch} />
          <div class="mt-1 max-h-48 overflow-y-auto rounded-md border border-[var(--ha-divider-color)]">
            {#if addCandidates.length === 0}
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
                </button>
              {/each}
            {/if}
          </div>
        </div>

        {#if addDevice}
          <div class="flex flex-col gap-1.5">
            <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.sensors.field.channel")}</span>
            <Input bind:value={addChannel} />
          </div>
          <div class="flex flex-col gap-1.5">
            <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.sensors.field.name")}</span>
            <Input bind:value={addName} />
          </div>
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
