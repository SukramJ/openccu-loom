<script lang="ts">
  import { onMount } from "svelte";
  import { api, friendlyError } from "$lib/api/client";
  import { alarmPanelStore } from "$lib/stores/alarmPanel.svelte";
  import { deviceStore } from "$lib/stores/devices.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";
  import { makeTextMatcher } from "$lib/utils";
  import type {
    AlarmSensor,
    AlarmSensorType,
    DeviceSummary,
    RoomEntry,
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

  // Sensor picker (docs/alarm-concept.md §12.2). Manages the ENROLLED
  // sensor set of one alarm area: an area selector, a left filter rail,
  // a card grid (or dense matrix table) with per-card mode-matrix chips,
  // a detail slide-over for the full flag set, an add-sensor flow with
  // device-picker assist + type presets (§6.1), and a bulk bar. Edits
  // are local + dirty-tracked; Save PUTs the whole set and reloads.

  // §4 mode order — used everywhere so the matrix reads identically on
  // every surface.
  const MODES = ["perimeter", "full", "night", "vacation", "custom"] as const;
  type Mode = (typeof MODES)[number];

  const SENSOR_TYPES: AlarmSensorType[] = [
    "door",
    "window",
    "motion",
    "tamper",
    "hazard",
    "panic",
  ];

  // Boolean behaviour flags (§6.2). entry_delay_s / hold_time / group are
  // rendered separately as their own value fields in the drawer. `chime`
  // is the door-chime-while-disarmed flag (§15 row 23,
  // internal/alarm/engine/config.go SensorConfig.Chime) — plays a chirp
  // when the sensor activates while its area is disarmed.
  const BOOL_FLAGS = [
    "use_exit_delay",
    "use_entry_delay",
    "always_on",
    "allow_open_after_arming",
    "arm_after_closing",
    "bypass_auto",
    "trigger_when_unavailable",
    "chime",
  ] as const;

  const TYPE_ICON: Record<AlarmSensorType, IconName> = {
    door: "mdi:door",
    window: "mdi:door-open",
    motion: "mdi:run-fast",
    tamper: "mdi:shield",
    hazard: "mdi:alert-triangle",
    panic: "mdi:bell-alert",
  };

  // Which devices are surfaced by the add-sensor assist by default: model
  // or name looks security-relevant. The "show all" toggle widens it.
  const SECURITY_RE =
    /swdo|sci|smo|smi|spi|sec|rc[ -]?\d|krca|wrc|wgc|motion|pir|presence|prescence|bewegung|sabot|tamper|contact|kontakt|fenster|window|door|t[üu]r|smoke|rauch|swsd|water|wasser|leak|co2|gas/i;

  // --- area state --------------------------------------------------
  const areas = $derived(alarmPanelStore.areasConfig);
  let areaId = $state("");

  let sensors = $state<AlarmSensor[]>([]);
  let loading = $state(false);
  let loadError = $state<string | null>(null);
  let saving = $state(false);
  let dirty = $state(false);

  let rooms = $state<RoomEntry[]>([]);

  // --- filter rail -------------------------------------------------
  let roomFilter = $state("");
  let typeFilter = $state<"all" | AlarmSensorType>("all");
  let assignedFilter = $state<"all" | "assigned" | "unassigned">("all");
  let search = $state("");

  // --- selection / view / drawer / add ----------------------------
  let selected = $state<Set<string>>(new Set());
  let view = $state<"cards" | "matrix">("cards");
  let drawerId = $state<string | null>(null);
  const drawerSensor = $derived(
    drawerId ? (sensors.find((s) => s.id === drawerId) ?? null) : null,
  );

  let addOpen = $state(false);
  let addShowAll = $state(false);
  let addDeviceSearch = $state("");
  let addDevice = $state<DeviceSummary | null>(null);
  let addChannel = $state("");
  let addParameter = $state("");
  let addType = $state<AlarmSensorType>("door");
  let addName = $state("");

  // --- config accessors (config is an engine-owned free document) --
  function cfgModes(s: AlarmSensor): string[] {
    const m = s.config?.modes;
    return Array.isArray(m) ? m.filter((x): x is string => typeof x === "string") : [];
  }
  function cfgBool(s: AlarmSensor, key: string): boolean {
    return s.config?.[key] === true;
  }
  function cfgNum(s: AlarmSensor, key: string): number | undefined {
    const v = s.config?.[key];
    return typeof v === "number" ? v : undefined;
  }
  function cfgStr(s: AlarmSensor, key: string): string {
    const v = s.config?.[key];
    return typeof v === "string" ? v : "";
  }

  function markDirty() {
    dirty = true;
  }

  function setSensorName(id: string, name: string) {
    sensors = sensors.map((s) =>
      s.id === id ? { ...s, name: name.trim() || undefined } : s,
    );
    markDirty();
  }

  function updateSensorConfig(id: string, patch: Record<string, unknown>) {
    sensors = sensors.map((s) => {
      if (s.id !== id) return s;
      const cfg: Record<string, unknown> = { ...(s.config ?? {}) };
      for (const [k, v] of Object.entries(patch)) {
        if (v === undefined) delete cfg[k];
        else cfg[k] = v;
      }
      return { ...s, config: cfg };
    });
    markDirty();
  }

  function toggleMode(s: AlarmSensor, mode: Mode) {
    const set = new Set(cfgModes(s));
    if (set.has(mode)) set.delete(mode);
    else set.add(mode);
    updateSensorConfig(s.id, { modes: MODES.filter((m) => set.has(m)) });
  }

  function toggleFlag(s: AlarmSensor, key: string) {
    updateSensorConfig(s.id, { [key]: !cfgBool(s, key) });
  }

  function setNum(s: AlarmSensor, key: string, raw: string) {
    const trimmed = raw.trim();
    const n = trimmed === "" ? undefined : Number(trimmed);
    updateSensorConfig(s.id, {
      [key]: n === undefined || Number.isNaN(n) ? undefined : n,
    });
  }

  // --- data loading ------------------------------------------------
  async function loadSensors() {
    if (!areaId) {
      sensors = [];
      return;
    }
    loading = true;
    loadError = null;
    try {
      sensors = await api.listAlarmAreaSensors(areaId);
      dirty = false;
      selected = new Set();
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
      await api.putAlarmAreaSensors(areaId, sensors);
      toastStore.success(t("alarm.toast.saved"));
      dirty = false;
      // Sensor membership feeds per-mode readiness, so refresh the shared
      // panel store, then re-pull the authoritative set.
      await alarmPanelStore.refresh();
      await loadSensors();
    } catch (err) {
      toastStore.error(t("alarm.toast.save_failed"), friendlyError(err, t));
    } finally {
      saving = false;
    }
  }

  // --- device / room helpers --------------------------------------
  function deviceAddrOf(channelAddress: string): string {
    const i = channelAddress.indexOf(":");
    return i >= 0 ? channelAddress.slice(0, i) : channelAddress;
  }
  const deviceByAddr = $derived.by(() => {
    const m = new Map<string, DeviceSummary>();
    for (const d of deviceStore.items) m.set(d.address, d);
    return m;
  });
  function deviceOf(s: AlarmSensor): DeviceSummary | undefined {
    return deviceByAddr.get(deviceAddrOf(s.channel_address));
  }
  function roomsOf(s: AlarmSensor): string[] {
    return deviceOf(s)?.rooms ?? [];
  }
  function displayName(s: AlarmSensor): string {
    return (s.name ?? "").trim() || deviceOf(s)?.name || s.channel_address;
  }
  // Live signal available without a per-DP subscription: the backing
  // device's reachability (kept fresh by the device store's WS stream).
  function isUnreach(s: AlarmSensor): boolean {
    const d = deviceOf(s);
    return d ? !d.available : false;
  }

  // --- filtering ---------------------------------------------------
  const nameMatch = $derived(makeTextMatcher(search));
  const filtered = $derived(
    sensors.filter((s) => {
      if (typeFilter !== "all" && s.type !== typeFilter) return false;
      const modeCount = cfgModes(s).length;
      if (assignedFilter === "assigned" && modeCount === 0) return false;
      if (assignedFilter === "unassigned" && modeCount > 0) return false;
      if (roomFilter && !roomsOf(s).includes(roomFilter)) return false;
      if (search) {
        const hit =
          nameMatch(displayName(s)) ||
          nameMatch(s.channel_address) ||
          nameMatch(s.parameter) ||
          nameMatch(s.central);
        if (!hit) return false;
      }
      return true;
    }),
  );

  // --- selection / bulk -------------------------------------------
  function toggleSelect(id: string, checked: boolean) {
    const next = new Set(selected);
    if (checked) next.add(id);
    else next.delete(id);
    selected = next;
  }
  function selectAllFiltered() {
    selected = new Set(filtered.map((s) => s.id));
  }
  function clearSelection() {
    selected = new Set();
  }
  function bulkAssign(mode: Mode) {
    sensors = sensors.map((s) => {
      if (!selected.has(s.id)) return s;
      const set = new Set(cfgModes(s));
      set.add(mode);
      return {
        ...s,
        config: { ...(s.config ?? {}), modes: MODES.filter((m) => set.has(m)) },
      };
    });
    markDirty();
  }
  function bulkRemove() {
    // Local edit only — reversible via the Discard action until Save, so
    // no confirm dialog is warranted here (unlike a live area delete).
    sensors = sensors.filter((s) => !selected.has(s.id));
    selected = new Set();
    markDirty();
  }
  function removeSensor(id: string) {
    sensors = sensors.filter((s) => s.id !== id);
    if (drawerId === id) drawerId = null;
    markDirty();
  }

  // --- type presets (§6.1) ----------------------------------------
  function presetModes(type: AlarmSensorType): Mode[] {
    switch (type) {
      case "door":
      case "window":
        return ["perimeter", "full", "night", "vacation"];
      case "motion":
        return ["full", "vacation"];
      case "tamper":
        return ["perimeter", "full", "night", "vacation", "custom"];
      case "hazard":
      case "panic":
        return [];
    }
  }
  function presetFlags(type: AlarmSensorType): Record<string, boolean> {
    switch (type) {
      case "door":
        return { use_entry_delay: true, arm_after_closing: true };
      case "motion":
        return { use_exit_delay: true, use_entry_delay: true };
      case "hazard":
      case "panic":
        return { always_on: true };
      default:
        return {};
    }
  }
  function guessType(d: DeviceSummary): AlarmSensorType {
    const s = `${d.model} ${d.model_label ?? ""} ${d.name ?? ""}`.toLowerCase();
    if (/sabot|tamper/.test(s)) return "tamper";
    if (/smoke|rauch|swsd|water|wasser|leak|co2|gas/.test(s)) return "hazard";
    if (/motion|pir|presence|prescence|bewegung|smi|spi/.test(s)) return "motion";
    if (/window|rotary|handle|swdo|fenster/.test(s)) return "window";
    if (/rc[ -]?\d|krca|wrc|remote|panic|taster/.test(s)) return "panic";
    return "door";
  }
  function guessParameter(type: AlarmSensorType): string {
    switch (type) {
      case "motion":
        return "MOTION";
      case "tamper":
        return "SABOTAGE";
      case "hazard":
        return "SMOKE_DETECTOR_ALARM_STATUS";
      case "panic":
        return "PRESS_SHORT";
      default:
        return "STATE";
    }
  }

  // --- add flow ----------------------------------------------------
  const addDeviceMatch = $derived(makeTextMatcher(addDeviceSearch));
  const addCandidates = $derived(
    deviceStore.items
      .filter((d) => {
        if (!addShowAll) {
          const hay = `${d.model} ${d.model_label ?? ""} ${d.name ?? ""}`;
          if (!SECURITY_RE.test(hay)) return false;
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

  function openAdd() {
    addOpen = true;
    addShowAll = false;
    addDeviceSearch = "";
    addDevice = null;
    addChannel = "";
    addParameter = "";
    addType = "door";
    addName = "";
  }
  function pickAddDevice(d: DeviceSummary) {
    addDevice = d;
    const type = guessType(d);
    addType = type;
    addChannel = `${d.address}:1`;
    addParameter = guessParameter(type);
    addName = d.name ?? "";
  }
  const canAdd = $derived(
    !!addDevice && addChannel.trim() !== "" && addParameter.trim() !== "",
  );
  function confirmAdd() {
    if (!addDevice || !canAdd) return;
    const channel = addChannel.trim();
    const parameter = addParameter.trim();
    const sensor: AlarmSensor = {
      // Deterministic id from the wire coordinates keeps re-adds idempotent
      // across a PUT round-trip; the engine may re-key on persist.
      id: `${addDevice.central ?? ""}|${channel}|${parameter}`,
      central: addDevice.central ?? "",
      interface_id: addDevice.interface_id,
      channel_address: channel,
      parameter,
      type: addType,
      name: addName.trim() || undefined,
      config: {
        modes: presetModes(addType),
        ...presetFlags(addType),
      },
    };
    if (sensors.some((s) => s.id === sensor.id)) {
      // Already enrolled — just focus it instead of duplicating.
      drawerId = sensor.id;
    } else {
      sensors = [...sensors, sensor];
      markDirty();
    }
    addOpen = false;
  }

  // --- keyboard ----------------------------------------------------
  function onKeydown(e: KeyboardEvent) {
    if (e.key !== "Escape") return;
    if (addOpen) addOpen = false;
    else if (drawerId) drawerId = null;
  }

  const areaOptions = $derived(
    areas.map((a) => ({ value: a.id, label: a.name })),
  );

  $effect(() => {
    // Default to (and re-pin on) the first area; reloads its sensor set
    // whenever the selected area changes.
    if (areas.length > 0 && !areas.some((a) => a.id === areaId)) {
      areaId = areas[0].id;
    }
  });

  // Reload sensors whenever the pinned area id changes.
  let loadedFor = $state("");
  $effect(() => {
    if (areaId && areaId !== loadedFor) {
      loadedFor = areaId;
      void loadSensors();
    }
  });

  onMount(() => {
    deviceStore.refresh();
    deviceStore.ensureStream();
    api
      .listRooms()
      .then((r) => (rooms = r))
      .catch(() => {
        // Rooms are a convenience filter; a failure just hides that facet.
        rooms = [];
      });
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
  <!-- Toolbar: area selector + add + view toggle -->
  <div class="mb-4 flex flex-wrap items-center gap-3">
    <label class="flex items-center gap-2 text-sm text-[var(--ha-secondary-text-color)]">
      <span>{t("alarm.sensors.area")}</span>
      <div class="min-w-48">
        <Select options={areaOptions} bind:value={areaId} />
      </div>
    </label>

    <div class="ml-auto flex items-center gap-2">
      <div
        class="inline-flex overflow-hidden rounded-md border border-[var(--ha-divider-color)]"
        role="group"
        aria-label={t("alarm.sensors.view.cards")}
      >
        <button
          type="button"
          class="px-2.5 py-2 transition {view === 'cards'
            ? 'bg-[color-mix(in_srgb,var(--ha-primary-color)_15%,transparent)] text-[var(--ha-primary-color)]'
            : 'text-[var(--ha-secondary-text-color)] hover:bg-black/5 dark:hover:bg-white/5'}"
          aria-pressed={view === "cards"}
          title={t("alarm.sensors.view.cards")}
          onclick={() => (view = "cards")}
        >
          <Icon name="mdi:dots-grid" size={18} />
        </button>
        <button
          type="button"
          class="px-2.5 py-2 transition {view === 'matrix'
            ? 'bg-[color-mix(in_srgb,var(--ha-primary-color)_15%,transparent)] text-[var(--ha-primary-color)]'
            : 'text-[var(--ha-secondary-text-color)] hover:bg-black/5 dark:hover:bg-white/5'}"
          aria-pressed={view === "matrix"}
          title={t("alarm.sensors.view.matrix")}
          onclick={() => (view = "matrix")}
        >
          <Icon name="mdi:format-list-bulleted" size={18} />
        </button>
      </div>
      {#if filtered.length > 0}
        <Button variant="ghost" size="sm" onclick={selectAllFiltered}>
          {t("alarm.sensors.select_all")}
        </Button>
      {/if}
      <Button size="sm" onclick={openAdd}>
        <Icon name="mdi:plus" size={16} />
        {t("alarm.sensors.add")}
      </Button>
    </div>
  </div>

  <div class="flex flex-col gap-4 lg:flex-row">
    <!-- Filter rail -->
    <aside class="lg:w-60 lg:shrink-0">
      <Card class="flex flex-col gap-4 p-4">
        <div class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("common.search")}</span>
          <Input type="search" placeholder={t("alarm.sensors.search")} bind:value={search} />
        </div>

        <div class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.sensors.filter.room")}</span>
          <Select
            bind:value={roomFilter}
            options={[
              { value: "", label: t("alarm.sensors.filter.all") },
              ...rooms.map((r) => ({ value: r.name, label: r.name })),
            ]}
          />
        </div>

        <div class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.sensors.filter.type")}</span>
          <Select
            value={typeFilter}
            onValueChange={(v) => (typeFilter = v as typeof typeFilter)}
            options={[
              { value: "all", label: t("alarm.sensors.filter.all") },
              ...SENSOR_TYPES.map((tp) => ({
                value: tp,
                label: t(`alarm.sensor_type.${tp}`),
              })),
            ]}
          />
        </div>

        <div class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.sensors.filter.status")}</span>
          <Select
            value={assignedFilter}
            onValueChange={(v) => (assignedFilter = v as typeof assignedFilter)}
            options={[
              { value: "all", label: t("alarm.sensors.filter.all") },
              { value: "assigned", label: t("alarm.sensors.filter.assigned") },
              { value: "unassigned", label: t("alarm.sensors.filter.unassigned") },
            ]}
          />
        </div>
      </Card>
    </aside>

    <!-- Main column -->
    <div class="min-w-0 flex-1">
      {#if dirty}
        <div
          class="mb-3 flex flex-wrap items-center gap-2 rounded-md border border-[color-mix(in_srgb,var(--ha-warning-color)_40%,transparent)] bg-[color-mix(in_srgb,var(--ha-warning-color)_12%,transparent)] p-2 text-sm"
        >
          <span class="font-medium text-[var(--ha-warning-color)]">{t("common.modified")}</span>
          <div class="ml-auto flex gap-2">
            <Button variant="outline" size="sm" onclick={() => void loadSensors()} disabled={saving}>
              {t("common.reset")}
            </Button>
            <Button size="sm" onclick={() => void save()} disabled={saving}>
              {saving ? t("common.saving") : t("common.save")}
            </Button>
          </div>
        </div>
      {/if}

      {#if selected.size > 0}
        <div
          class="mb-3 flex flex-wrap items-center gap-2 rounded-md border border-[color-mix(in_srgb,var(--ha-primary-color)_40%,transparent)] bg-[color-mix(in_srgb,var(--ha-primary-color)_12%,transparent)] p-2 text-sm"
        >
          <span class="font-medium text-[var(--ha-primary-color)]">
            {t("alarm.sensors.selected", { count: selected.size })}
          </span>
          <span class="text-[var(--ha-secondary-text-color)]">·</span>
          <span class="text-xs text-[var(--ha-secondary-text-color)]">{t("alarm.sensors.bulk.assign")}</span>
          {#each MODES as mode (mode)}
            <button
              type="button"
              class="rounded-full border border-[var(--ha-divider-color)] px-2 py-0.5 text-xs text-[var(--ha-primary-text-color)] transition hover:border-[var(--ha-primary-color)] hover:text-[var(--ha-primary-color)]"
              onclick={() => bulkAssign(mode)}
            >
              {t(`alarm.mode.${mode}`)}
            </button>
          {/each}
          <div class="ml-auto flex gap-2">
            <Button variant="outline" size="sm" onclick={clearSelection}>{t("common.close")}</Button>
            <Button variant="outline-destructive" size="sm" onclick={bulkRemove}>
              {t("alarm.sensors.bulk.remove")}
            </Button>
          </div>
        </div>
      {/if}

      {#if loadError}
        <ErrorState message={loadError} onRetry={() => void loadSensors()} />
      {:else if loading && sensors.length === 0}
        <LoadingState message={t("common.loading")} />
      {:else if sensors.length === 0}
        <EmptyState
          icon="mdi:door"
          message={t("alarm.sensors.empty")}
          description={t("alarm.sensors.empty.description")}
        >
          {#snippet action()}
            <Button variant="outline" size="sm" onclick={openAdd}>
              <Icon name="mdi:plus" size={16} />
              {t("alarm.sensors.add")}
            </Button>
          {/snippet}
        </EmptyState>
      {:else if filtered.length === 0}
        <EmptyState icon="mdi:filter" message={t("common.no_matches")} />
      {:else if view === "matrix"}
        <!-- Dense audit table: rows = sensors, cols = modes + entry delay -->
        <Card class="overflow-x-auto">
          <table class="w-full border-collapse text-sm">
            <thead class="sticky top-0 z-10 bg-[var(--ha-card-background-color)]">
              <tr class="border-b border-[var(--ha-divider-color)] text-left">
                <th class="p-2 font-medium">{t("alarm.matrix.sensor")}</th>
                {#each MODES as mode (mode)}
                  <th class="p-2 text-center font-medium">{t(`alarm.mode.${mode}`)}</th>
                {/each}
                <th class="p-2 text-center font-medium">{t("alarm.flag.entry_delay_override")}</th>
                <th class="p-2"></th>
              </tr>
            </thead>
            <tbody>
              {#each filtered as s (s.id)}
                <tr class="border-b border-[var(--ha-divider-color)] last:border-0">
                  <td class="p-2">
                    <div class="flex items-center gap-2">
                      <input
                        type="checkbox"
                        class="h-4 w-4 shrink-0 cursor-pointer accent-[var(--ha-primary-color)]"
                        checked={selected.has(s.id)}
                        onchange={(e) => toggleSelect(s.id, e.currentTarget.checked)}
                        aria-label={displayName(s)}
                      />
                      <Icon name={TYPE_ICON[s.type]} size={16} class="text-[var(--ha-secondary-text-color)]" />
                      <button
                        type="button"
                        class="truncate text-left font-medium text-[var(--ha-primary-color)] hover:underline"
                        onclick={() => (drawerId = s.id)}
                      >
                        {displayName(s)}
                      </button>
                    </div>
                  </td>
                  {#each MODES as mode (mode)}
                    <td class="p-2 text-center">
                      <input
                        type="checkbox"
                        class="h-4 w-4 cursor-pointer accent-[var(--ha-primary-color)]"
                        checked={cfgModes(s).includes(mode)}
                        onchange={() => toggleMode(s, mode)}
                        aria-label={`${displayName(s)} · ${t(`alarm.mode.${mode}`)}`}
                      />
                    </td>
                  {/each}
                  <td class="p-2 text-center">
                    <input
                      type="number"
                      min="0"
                      class="h-8 w-16 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-1 text-center text-sm text-[var(--ha-primary-text-color)]"
                      value={cfgNum(s, "entry_delay_s") ?? ""}
                      oninput={(e) => setNum(s, "entry_delay_s", e.currentTarget.value)}
                      aria-label={`${displayName(s)} · ${t("alarm.flag.entry_delay_override")}`}
                    />
                  </td>
                  <td class="p-2 text-right">
                    <button
                      type="button"
                      class="text-[var(--ha-secondary-text-color)] hover:text-[var(--ha-error-color)]"
                      title={t("common.remove")}
                      aria-label={t("common.remove")}
                      onclick={() => removeSensor(s.id)}
                    >
                      <Icon name="mdi:trash-can" size={16} />
                    </button>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </Card>
      {:else}
        <!-- Card grid -->
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
          {#each filtered as s (s.id)}
            {@const modes = cfgModes(s)}
            {@const flags = BOOL_FLAGS.filter((f) => cfgBool(s, f))}
            {@const entryOverride = cfgNum(s, "entry_delay_s")}
            <Card class="flex flex-col gap-2 p-3">
              <div class="flex items-start gap-2">
                <input
                  type="checkbox"
                  class="mt-1 h-4 w-4 shrink-0 cursor-pointer accent-[var(--ha-primary-color)]"
                  checked={selected.has(s.id)}
                  onchange={(e) => toggleSelect(s.id, e.currentTarget.checked)}
                  aria-label={displayName(s)}
                />
                <Icon name={TYPE_ICON[s.type]} size={20} class="mt-0.5 text-[var(--ha-primary-color)]" />
                <div class="min-w-0 flex-1">
                  <button
                    type="button"
                    class="block w-full truncate text-left font-medium text-[var(--ha-primary-text-color)] hover:text-[var(--ha-primary-color)]"
                    onclick={() => (drawerId = s.id)}
                    title={displayName(s)}
                  >
                    {displayName(s)}
                  </button>
                  <p class="truncate font-mono text-xs text-[var(--ha-secondary-text-color)]">
                    {s.channel_address}·{s.parameter}
                  </p>
                </div>
                <div class="flex shrink-0 flex-col items-end gap-1">
                  <Badge variant={modes.length === 0 ? "warning" : "default"}>
                    {t(`alarm.sensor_type.${s.type}`)}
                  </Badge>
                  {#if isUnreach(s)}
                    <Badge variant="danger">{t("alarm.sensors.state.unreach")}</Badge>
                  {/if}
                </div>
              </div>

              <!-- Mode-matrix toggle chips (§4 order) -->
              <div class="flex flex-wrap items-center gap-1.5">
                <span class="text-xs text-[var(--ha-secondary-text-color)]">{t("alarm.sensors.modes")}</span>
                {#each MODES as mode (mode)}
                  {@const on = modes.includes(mode)}
                  <button
                    type="button"
                    aria-pressed={on}
                    class="rounded-full border px-2 py-0.5 text-xs transition {on
                      ? 'border-[var(--ha-primary-color)] bg-[color-mix(in_srgb,var(--ha-primary-color)_15%,transparent)] text-[var(--ha-primary-color)]'
                      : 'border-[var(--ha-divider-color)] text-[var(--ha-secondary-text-color)] hover:border-[var(--ha-secondary-text-color)]'}"
                    onclick={() => toggleMode(s, mode)}
                  >
                    {t(`alarm.mode.${mode}`)}
                  </button>
                {/each}
              </div>

              <!-- Flag summary -->
              <div class="flex flex-wrap items-center gap-1 text-xs text-[var(--ha-secondary-text-color)]">
                <span>{t("alarm.flags.title")}:</span>
                {#if flags.length === 0 && entryOverride === undefined}
                  <span>{t("common.none")}</span>
                {:else}
                  {#if entryOverride !== undefined}
                    <Badge variant="muted">{t("alarm.flag.entry_delay_override")}: {entryOverride}s</Badge>
                  {/if}
                  {#each flags as f (f)}
                    <Badge variant="muted">{t(`alarm.flag.${f}`)}</Badge>
                  {/each}
                {/if}
              </div>
            </Card>
          {/each}
        </div>
      {/if}
    </div>
  </div>

  <!-- Detail slide-over -->
  {#if drawerSensor}
    {@const s = drawerSensor}
    <div
      class="fixed inset-0 z-40 bg-black/40"
      role="presentation"
      onclick={() => (drawerId = null)}
    ></div>
    <div
      class="fixed right-0 top-0 z-50 flex h-full w-full max-w-md flex-col overflow-y-auto border-l border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] shadow-xl"
      role="dialog"
      aria-modal="true"
      aria-label={t("alarm.sensors.detail.title")}
    >
      <header class="flex items-center gap-2 border-b border-[var(--ha-divider-color)] p-4">
        <Icon name={TYPE_ICON[s.type]} size={20} class="text-[var(--ha-primary-color)]" />
        <div class="min-w-0 flex-1">
          <h2 class="truncate font-semibold text-[var(--ha-primary-text-color)]">{displayName(s)}</h2>
          <p class="truncate font-mono text-xs text-[var(--ha-secondary-text-color)]">{s.channel_address}·{s.parameter}</p>
        </div>
        <button
          type="button"
          class="text-[var(--ha-secondary-text-color)] hover:text-[var(--ha-primary-text-color)]"
          aria-label={t("common.close")}
          onclick={() => (drawerId = null)}
        >
          <Icon name="mdi:close" size={20} />
        </button>
      </header>

      <div class="flex flex-col gap-5 p-4">
        <div class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.sensors.field.name")}</span>
          <Input
            value={s.name ?? ""}
            oninput={(e) => setSensorName(s.id, e.currentTarget.value)}
          />
        </div>

        <div class="flex flex-col gap-2">
          <span class="text-sm font-medium text-[var(--ha-primary-text-color)]">{t("alarm.sensors.modes")}</span>
          <div class="flex flex-wrap gap-1.5">
            {#each MODES as mode (mode)}
              {@const on = cfgModes(s).includes(mode)}
              <button
                type="button"
                aria-pressed={on}
                class="rounded-full border px-2.5 py-1 text-xs transition {on
                  ? 'border-[var(--ha-primary-color)] bg-[color-mix(in_srgb,var(--ha-primary-color)_15%,transparent)] text-[var(--ha-primary-color)]'
                  : 'border-[var(--ha-divider-color)] text-[var(--ha-secondary-text-color)] hover:border-[var(--ha-secondary-text-color)]'}"
                onclick={() => toggleMode(s, mode)}
              >
                {t(`alarm.mode.${mode}`)}
              </button>
            {/each}
          </div>
        </div>

        <div class="flex flex-col gap-2">
          <span class="text-sm font-medium text-[var(--ha-primary-text-color)]">{t("alarm.flags.title")}</span>
          {#each BOOL_FLAGS as f (f)}
            <label class="flex items-center justify-between gap-2 text-sm text-[var(--ha-primary-text-color)]">
              <span>{t(`alarm.flag.${f}`)}</span>
              <Switch checked={cfgBool(s, f)} onCheckedChange={() => toggleFlag(s, f)} />
            </label>
          {/each}
        </div>

        <div class="flex flex-col gap-3">
          <label class="flex items-center justify-between gap-2 text-sm text-[var(--ha-primary-text-color)]">
            <span>{t("alarm.flag.entry_delay_override")}</span>
            <input
              type="number"
              min="0"
              class="h-9 w-24 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-2 text-right text-sm text-[var(--ha-primary-text-color)]"
              value={cfgNum(s, "entry_delay_s") ?? ""}
              oninput={(e) => setNum(s, "entry_delay_s", e.currentTarget.value)}
            />
          </label>
          <label class="flex items-center justify-between gap-2 text-sm text-[var(--ha-primary-text-color)]">
            <span>{t("alarm.flag.hold_time")}</span>
            <input
              type="number"
              min="0"
              class="h-9 w-24 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-2 text-right text-sm text-[var(--ha-primary-text-color)]"
              value={cfgNum(s, "hold_time") ?? ""}
              oninput={(e) => setNum(s, "hold_time", e.currentTarget.value)}
            />
          </label>
          <div class="flex flex-col gap-1.5">
            <span class="text-sm text-[var(--ha-primary-text-color)]">{t("alarm.flag.group")}</span>
            <Input
              value={cfgStr(s, "group")}
              oninput={(e) =>
                updateSensorConfig(s.id, { group: e.currentTarget.value || undefined })}
            />
          </div>
        </div>
      </div>

      <footer class="mt-auto flex gap-2 border-t border-[var(--ha-divider-color)] p-4">
        <Button variant="outline-destructive" size="sm" onclick={() => removeSensor(s.id)}>
          <Icon name="mdi:trash-can" size={16} />
          {t("common.remove")}
        </Button>
        <Button variant="outline" size="sm" class="ml-auto" onclick={() => (drawerId = null)}>
          {t("common.close")}
        </Button>
      </footer>
    </div>
  {/if}

  <!-- Add-sensor flow -->
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
      aria-label={t("alarm.sensors.add")}
    >
      <header class="flex items-center gap-2 border-b border-[var(--ha-divider-color)] p-4">
        <Icon name="mdi:plus" size={20} class="text-[var(--ha-primary-color)]" />
        <h2 class="flex-1 font-semibold text-[var(--ha-primary-text-color)]">{t("alarm.sensors.add")}</h2>
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
          <div class="flex items-center justify-between">
            <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.sensors.field.device")}</span>
            <label class="flex items-center gap-1.5 text-xs text-[var(--ha-secondary-text-color)]">
              <input type="checkbox" bind:checked={addShowAll} />
              {t("alarm.sensors.add.show_all")}
            </label>
          </div>
          <Input type="search" placeholder={t("common.search")} bind:value={addDeviceSearch} />
          <div class="mt-1 max-h-48 overflow-y-auto rounded-md border border-[var(--ha-divider-color)]">
            {#if addCandidates.length === 0}
              <p class="p-3 text-center text-xs text-[var(--ha-secondary-text-color)]">
                {t("alarm.sensors.add.no_devices")}
              </p>
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
                  <span class="truncate font-mono text-xs text-[var(--ha-secondary-text-color)]">
                    {d.model_label || d.model} · {d.address}
                  </span>
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
            <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.sensors.field.parameter")}</span>
            <Input bind:value={addParameter} />
          </div>
          <div class="flex flex-col gap-1.5">
            <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.sensors.filter.type")}</span>
            <Select
              value={addType}
              onValueChange={(v) => {
                addType = v as AlarmSensorType;
                addParameter = guessParameter(addType);
              }}
              options={SENSOR_TYPES.map((tp) => ({
                value: tp,
                label: t(`alarm.sensor_type.${tp}`),
              }))}
            />
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
