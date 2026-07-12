<script lang="ts">
  import Select from "$lib/components/ui/Select.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import ParameterField from "./ParameterField.svelte";
  import {
    derivePairLabel,
    matchPresetIndexIn,
    presetLabel,
    presetsFor,
    type TimePair,
  } from "$lib/channel/time-pairs";
  import { t } from "$lib/i18n";

  type Props = {
    pair: TimePair;
    locale: string;
    unitValue: unknown;
    valueValue: unknown;
    unitDirty: boolean;
    valueDirty: boolean;
    unitError: string | null;
    valueError: string | null;
    onChange: (name: string, value: unknown) => void;
  };

  let {
    pair,
    locale,
    unitValue,
    valueValue,
    unitDirty,
    valueDirty,
    unitError,
    valueError,
    onChange,
  }: Props = $props();

  // The LINK classifier attaches a selector-specific preset list
  // (TIME_ON_OFF / DELAY / RAMP_ON_OFF) via pair.presets; outside LINK
  // we fall back to the generic name-heuristic list.
  const presets = $derived(pair.presets ?? presetsFor(pair.shape));

  const matched = $derived(matchPresetIndexIn(presets, unitValue, valueValue));

  const writable = $derived(
    pair.unitParam.operations.write && pair.valueParam.operations.write,
  );

  // customMode is entered either explicitly (user picks "Custom") or
  // implicitly (server-side state does not match any preset).
  let forceCustom = $state(false);
  const customMode = $derived(forceCustom || matched < 0);

  const selectedValue = $derived(
    customMode ? "__custom__" : String(matched),
  );

  const selectOptions = $derived([
    ...presets.map((p, i) => ({
      value: String(i),
      label: presetLabel(p),
    })),
    {
      value: "__custom__",
      label: t("parameter.custom"),
    },
  ]);

  function onPresetChange(raw: string) {
    if (raw === "__custom__") {
      forceCustom = true;
      return;
    }
    const idx = Number(raw);
    const preset = presets[idx];
    if (!preset) return;
    forceCustom = false;
    onChange(pair.unitParam.name, preset.a);
    onChange(pair.valueParam.name, preset.b);
  }

  const dirty = $derived(unitDirty || valueDirty);
  const combinedError = $derived(unitError || valueError);
  const label = $derived(derivePairLabel(pair, locale));
</script>

<div class="flex flex-col gap-1">
  <div class="flex items-baseline gap-2">
    <span class="text-sm font-medium text-slate-700 dark:text-slate-300">
      {label}
      <span class="ml-1 font-mono text-[10px] text-[var(--ha-secondary-text-color)]">
        {pair.unitParam.name} / {pair.valueParam.name}
      </span>
    </span>
    {#if dirty}<Badge variant="warning">{t("parameter.modified")}</Badge>{/if}
    {#if !writable}
      <Badge variant="muted">{t("parameter.read_only")}</Badge>
    {/if}
  </div>

  {#if !writable}
    <!-- Show the matched preset label (or the raw pair) as plain
         text; a disabled select would imply interactivity. -->
    {@const activePreset = matched >= 0 ? presets[matched] : null}
    <span class="inline-flex w-fit items-center rounded-md bg-slate-100 px-2 py-1 text-sm text-slate-800 dark:bg-slate-800 dark:text-slate-100">
      {#if activePreset}
        {presetLabel(activePreset)}
      {:else}
        <span class="font-mono">
          {unitValue ?? "—"} / {valueValue ?? "—"}
        </span>
      {/if}
    </span>
  {:else}
    <Select
      options={selectOptions}
      value={selectedValue}
      onValueChange={(v) => onPresetChange(v)}
    />
  {/if}

  {#if customMode && writable}
    <!-- Two raw inputs for edge cases the preset list does not cover.
         Matches the "Benutzerdefiniert" fallback from
         config-form.ts:_renderCustomFields. -->
    <div class="mt-2 grid grid-cols-1 gap-2 md:grid-cols-2">
      <ParameterField
        parameter={pair.unitParam}
        value={unitValue}
        dirty={unitDirty}
        error={unitError}
        onChange={(v) => onChange(pair.unitParam.name, v)}
      />
      <ParameterField
        parameter={pair.valueParam}
        value={valueValue}
        dirty={valueDirty}
        error={valueError}
        onChange={(v) => onChange(pair.valueParam.name, v)}
      />
    </div>
  {/if}

  {#if combinedError}
    <p class="text-xs text-red-600 dark:text-red-400">{combinedError}</p>
  {/if}
</div>
