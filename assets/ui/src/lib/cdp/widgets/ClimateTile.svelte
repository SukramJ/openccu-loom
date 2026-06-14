<!--
  CDP-aware climate tile. Covers `climate_simple` (HM-CC-TC heat-on +
  setpoint), `climate_rf` (HM-CC-RT-DN with the AUTO/BOOST/COMFORT/
  LOWERING action mode picker), and `climate_hmip` (HmIP-BWTH /
  HmIP-WTH / HmIP-eTRV-E with the AUTO/MANU/AWAY mode set and a
  BOOST_MODE / FROST_PROTECTION preset row).

  Writes flow through the semantic CDP operation surface:
  - set_temperature {temperature}
  - set_mode {mode: "auto"/"heat"/"off"/"cool"}
  - enable_boost / disable_boost
  - set_away (timed)
  - disable_away
  - set_profile {profile}
-->
<script lang="ts">
  import { onMount } from "svelte";
  import { api, friendlyError } from "$lib/api/client";
  import type { CustomDPSummary, DataPointSummary } from "$lib/api/types";
  import { subscribe } from "$lib/stores/events.svelte";
  import { t } from "$lib/i18n";
  import ControlTile from "$lib/control/tile/ControlTile.svelte";
  import ControlTileIcon from "$lib/control/tile/ControlTileIcon.svelte";
  import ControlTileInfo from "$lib/control/tile/ControlTileInfo.svelte";
  import TargetTemperatureFeature from "$lib/control/features/TargetTemperatureFeature.svelte";
  import HvacModesFeature from "$lib/control/features/HvacModesFeature.svelte";
  import PresetModesFeature from "$lib/control/features/PresetModesFeature.svelte";
  import StatReadoutFeature from "$lib/control/features/StatReadoutFeature.svelte";
  import ToggleFeature from "$lib/control/features/ToggleFeature.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";

  type Props = {
    address: string;
    cdp: CustomDPSummary;
    title?: string;
  };

  let { address, cdp, title }: Props = $props();
  const displayTitle = $derived(title ?? cdp.name);

  let dataPoints = $state<DataPointSummary[]>([]);
  let error = $state<string | null>(null);

  const channelAddress = $derived(`${address}:${cdp.channel_no}`);

  const caps = $derived(cdp.capabilities ?? {});
  const isSimple = $derived(cdp.kind === "climate_simple");
  const isRf = $derived(cdp.kind === "climate_rf");
  const isHmIP = $derived(cdp.kind === "climate_hmip");

  function dp(name: string): DataPointSummary | undefined {
    return dataPoints.find((d) => d.parameter === name);
  }
  // HmIP setpoint is SET_POINT_TEMPERATURE; RF + simple use SETPOINT.
  const setpointDP = $derived(
    dp("SET_POINT_TEMPERATURE") ?? dp("SETPOINT"),
  );
  const tempDP = $derived(dp("ACTUAL_TEMPERATURE"));
  const humidityDP = $derived(dp("HUMIDITY"));
  // SET_POINT_MODE is HmIP; CONTROL_MODE is the RF read-back.
  const setpointModeDP = $derived(dp("SET_POINT_MODE"));
  const controlModeDP = $derived(dp("CONTROL_MODE"));
  const boostDP = $derived(dp("BOOST_MODE"));
  const frostDP = $derived(dp("FROST_PROTECTION"));
  const stateDP = $derived(dp("STATE")); // HM-CC-TC heat on/off
  // ACTIVE_PROFILE (HmIP) / WEEK_PROGRAM_POINTER (RF) report the active
  // week-program slot as a 1-based integer.
  const activeProfileDP = $derived(
    dp("ACTIVE_PROFILE") ?? dp("WEEK_PROGRAM_POINTER"),
  );
  // Selected preset value for the <select> — mirrors the device's
  // active profile so the dropdown reflects current state instead of
  // defaulting to the first option.
  const selectedPreset = $derived.by(() => {
    if (typeof activeProfileDP?.value === "number" && activeProfileDP.value >= 1) {
      return `week_program_${activeProfileDP.value}`;
    }
    return "";
  });

  const setpoint = $derived(
    typeof setpointDP?.value === "number" ? setpointDP.value : 21,
  );
  const currentTemp = $derived(
    typeof tempDP?.value === "number" ? tempDP.value : undefined,
  );
  const currentHumidity = $derived(humidityDP?.value);
  const isHeatOn = $derived(Boolean(stateDP?.value));

  const observed = $derived(setpointDP?.observed ?? false);

  // Mode label for the secondary line — RF surfaces it as the ENUM
  // value, HmIP as an integer index (read off SET_POINT_MODE).
  const RF_MODE_DE: Record<string, string> = {
    "AUTO-MODE": "Auto",
    "MANU-MODE": "Manuell",
    "PARTY-MODE": "Abwesend",
    "BOOST-MODE": "Boost",
  };
  const HMIP_MODE_DE: Record<number, string> = {
    0: "Auto",
    1: "Manuell",
    2: "Abwesend",
  };
  const currentModeLabel = $derived.by(() => {
    if (isRf && typeof controlModeDP?.value === "string") {
      return RF_MODE_DE[controlModeDP.value] ?? controlModeDP.value;
    }
    if (isHmIP && typeof setpointModeDP?.value === "number") {
      return HMIP_MODE_DE[setpointModeDP.value] ?? "";
    }
    return "";
  });

  const tileColor = $derived(
    observed
      ? "var(--state-climate-auto-color, var(--ha-primary-color))"
      : "var(--ha-secondary-text-color)",
  );

  const secondary = $derived.by(() => {
    if (!observed) return "—";
    const parts: string[] = [];
    if (currentModeLabel) parts.push(currentModeLabel);
    if (isSimple) {
      parts.push(`${setpoint.toFixed(1)} °C${isHeatOn ? " · An" : " · Aus"}`);
    } else if (typeof currentTemp === "number") {
      parts.push(`${currentTemp.toFixed(1)} °C → ${setpoint.toFixed(1)} °C`);
    } else {
      parts.push(`${setpoint.toFixed(1)} °C`);
    }
    return parts.join(" · ");
  });

  // HmIP mode options — mirror aiohomematic's _ModeHmIP (climate.py:76-81).
  const HMIP_MODES = [
    { value: 0, label: "Auto" },
    { value: 1, label: "Manuell" },
    { value: 2, label: "Abwesend" },
  ];

  // Map HA-style mode strings (auto / heat / off / cool) for set_mode.
  // The CDP service-method dispatcher accepts those tokens directly.
  function setMode(mode: string) {
    invoke("set_mode", { mode });
  }

  const hmipPresets = $derived.by(() => {
    const out: { key: string; label: string; value: boolean; writable: boolean }[] = [];
    if (boostDP) {
      out.push({
        key: "boost",
        label: "Boost",
        value: Boolean(boostDP.value),
        writable: boostDP.operations.write,
      });
    }
    if (frostDP) {
      out.push({
        key: "frost",
        label: "Frostschutz",
        value: Boolean(frostDP.value),
        writable: frostDP.operations.write,
      });
    }
    return out;
  });

  function onPresetToggle(key: string, next: boolean) {
    if (key === "boost") {
      invoke(next ? "enable_boost" : "disable_boost");
    } else if (key === "frost") {
      // Frost-protection is a flag, not a service; HmIP wires it
      // through the AWAY profile in aiohomematic — we treat it as
      // a hint only and don't write here until a dedicated service
      // arrives.
    }
  }

  // Preset modes (week_program_1..6, comfort, eco) from the
  // ConfigPayload — populated by the daemon when the DP exposes a
  // ConfigPayload() method. Filter "none" (HA's implicit unset
  // marker) before rendering.
  const presetModes = $derived<string[]>(
    Array.isArray(cdp.config?.preset_modes)
      ? (cdp.config?.preset_modes as string[]).filter(
          (p) => p && p !== "none" && p !== "away" && p !== "boost",
        )
      : [],
  );
  function profileLabel(p: string): string {
    if (p.startsWith("week_program_")) {
      return `Wochenprogramm ${p.slice("week_program_".length)}`;
    }
    return p
      .replace(/_/g, " ")
      .replace(/\b\w/g, (c) => c.toUpperCase());
  }

  // Away-mode state from the configured Capability flag (also
  // surfaced server-side as preset_mode="away" in the aggregated
  // StatePayload — but tiles read DPs only, so we use the HmIP
  // SET_POINT_MODE == 2 fast-path for state and rely on the user's
  // explicit click to trigger set_away_for_duration / disable_away).
  const isAway = $derived.by(() => {
    if (isHmIP && typeof setpointModeDP?.value === "number") {
      return setpointModeDP.value === 2;
    }
    if (isRf && typeof controlModeDP?.value === "string") {
      return controlModeDP.value === "PARTY-MODE";
    }
    return false;
  });

  // Away form state — collapsed by default.
  let awayOpen = $state(false);
  let awayHours = $state<number>(24);
  let awayTemp = $state<number>(12);

  function quickAway24h() {
    invoke("set_away_for_duration", { hours: awayHours, away_temperature: awayTemp });
    awayOpen = false;
  }

  async function load() {
    error = null;
    try {
      dataPoints = await api.listDataPoints(address, cdp.channel_no);
    } catch (err) {
      error = friendlyError(err, t);
    }
  }

  async function invoke(op: string, params: Record<string, unknown> = {}) {
    try {
      await api.invokeCustomDataPoint(address, cdp.name, op, params);
    } catch (err) {
      error = friendlyError(err, t);
    }
  }

  onMount(() => {
    load();
    const unsub = subscribe((ev) => {
      if (ev.type !== "data_point") return;
      const e = ev.payload as { channel_address: string; parameter: string; value: unknown };
      if (e.channel_address !== channelAddress) return;
      const idx = dataPoints.findIndex((d) => d.parameter === e.parameter);
      if (idx < 0) return;
      dataPoints[idx] = { ...dataPoints[idx], value: e.value, observed: true };
    });
    return () => unsub();
  });
</script>

{#if error}
  <div class="mb-2 rounded-md border border-[var(--ha-error-color)] bg-[var(--ha-card-background-color)] p-2 text-xs text-[var(--ha-error-color)]">
    {error}
  </div>
{/if}
<ControlTile {tileColor}>
    {#snippet icon()}
      <ControlTileIcon active={observed} label={displayTitle}>
        <Icon name="mdi:thermometer" size={22} />
      </ControlTileIcon>
    {/snippet}
    {#snippet info()}
      <ControlTileInfo primary={displayTitle} {secondary} />
    {/snippet}
    {#snippet features()}
      {#if isSimple && stateDP}
        <ToggleFeature
          value={isHeatOn}
          color={tileColor}
          disabled={!stateDP.operations.write}
          labelOff="Aus"
          labelOn="An"
          onChange={(v) => setMode(v ? "heat" : "off")}
        />
      {/if}

      {#if setpointDP}
        <TargetTemperatureFeature
          value={setpoint}
          color={tileColor}
          disabled={!setpointDP.operations.write}
          onChange={(v) => invoke("set_temperature", { temperature: v })}
        />
      {/if}

      {#if isHmIP && setpointModeDP}
        <HvacModesFeature
          value={typeof setpointModeDP.value === "number" ? setpointModeDP.value : 0}
          options={HMIP_MODES}
          color={tileColor}
          onChange={(v) =>
            invoke("set_mode", {
              mode: typeof v === "number" && v === 0 ? "auto" : "heat",
            })}
        />
      {/if}

      {#if isHmIP && hmipPresets.length > 0}
        <PresetModesFeature
          presets={hmipPresets}
          color={tileColor}
          onToggle={onPresetToggle}
        />
      {/if}

      {#if isRf}
        <!-- RF mode picker — each press fires a dedicated service. -->
        <div class="flex flex-wrap gap-2">
          <button
            type="button"
            class="min-h-11 rounded-md border border-[var(--ha-divider-color)] px-3 py-2 text-sm"
            onclick={() => setMode("auto")}
          >Auto</button>
          <button
            type="button"
            class="min-h-11 rounded-md border border-[var(--ha-divider-color)] px-3 py-2 text-sm"
            onclick={() => invoke("enable_boost")}
          >Boost</button>
          <button
            type="button"
            class="min-h-11 rounded-md border border-[var(--ha-divider-color)] px-3 py-2 text-sm"
            onclick={() => setMode("heat")}
          >Manuell</button>
        </div>
      {/if}

      {#if presetModes.length > 0}
        <label class="flex flex-col gap-1">
          <span class="text-xs text-[var(--ha-secondary-text-color)]">Profil</span>
          <!-- `selected` on each <option> is the portable way to drive
               the current value of an HTML <select> in Svelte 5 without
               a two-way bind; <select value=…> alone does not propagate
               to the option list. -->
          <select
            class="h-9 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-2 text-sm text-[var(--ha-primary-text-color)]"
            onchange={(e) => invoke("set_profile", { profile: (e.target as HTMLSelectElement).value })}
          >
            {#if selectedPreset === "" || !presetModes.includes(selectedPreset)}
              <option value="" selected disabled>—</option>
            {/if}
            {#each presetModes as p (p)}
              <option value={p} selected={p === selectedPreset}>{profileLabel(p)}</option>
            {/each}
          </select>
        </label>
      {/if}

      {#if caps.away}
        <div class="flex flex-col gap-2 rounded-md border border-[var(--ha-divider-color)] p-2">
          <div class="flex items-center justify-between gap-2">
            <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">
              Abwesenheit{isAway ? " · aktiv" : ""}
            </span>
            <div class="flex gap-2">
              {#if isAway}
                <button
                  type="button"
                  class="min-h-11 rounded-md border border-[var(--ha-divider-color)] px-3 py-2 text-sm"
                  onclick={() => invoke("disable_away")}
                >Anwesend</button>
              {:else}
                <button
                  type="button"
                  class="min-h-11 rounded-md border border-[var(--ha-divider-color)] px-3 py-2 text-sm"
                  onclick={quickAway24h}
                >24 h abwesend</button>
                <button
                  type="button"
                  class="min-h-11 rounded-md border border-[var(--ha-divider-color)] px-3 py-2 text-sm"
                  onclick={() => (awayOpen = !awayOpen)}
                >{awayOpen ? "−" : "+"}</button>
              {/if}
            </div>
          </div>
          {#if awayOpen && !isAway}
            <div class="flex flex-wrap items-end gap-2 text-xs">
              <label class="flex flex-col gap-1">
                <span class="text-[var(--ha-secondary-text-color)]">Dauer (h)</span>
                <input
                  type="number"
                  min="1"
                  max="720"
                  bind:value={awayHours}
                  class="h-10 w-20 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-2 text-[var(--ha-primary-text-color)]"
                />
              </label>
              <label class="flex flex-col gap-1">
                <span class="text-[var(--ha-secondary-text-color)]">Temperatur (°C)</span>
                <input
                  type="number"
                  min="4.5"
                  max="30"
                  step="0.5"
                  bind:value={awayTemp}
                  class="h-10 w-24 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-2 text-[var(--ha-primary-text-color)]"
                />
              </label>
              <button
                type="button"
                class="min-h-10 rounded-md bg-[var(--ha-primary-color)] px-3 text-sm font-medium text-white"
                onclick={quickAway24h}
              >Aktivieren</button>
            </div>
          {/if}
        </div>
      {/if}

      {#if tempDP || humidityDP}
        <div class="grid grid-cols-2 gap-2">
          {#if tempDP}
            <StatReadoutFeature label="Ist-Temperatur" value={currentTemp} unit="°C" />
          {/if}
          {#if humidityDP}
            <StatReadoutFeature label="Luftfeuchte" value={currentHumidity} unit="%" />
          {/if}
        </div>
      {/if}
    {/snippet}
  </ControlTile>
