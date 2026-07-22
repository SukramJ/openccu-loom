<script lang="ts">
  import type { UISchemaParameter } from "$lib/api/types";
  import Input from "$lib/components/ui/Input.svelte";
  import Switch from "$lib/components/ui/Switch.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import ParameterLevelField from "./ParameterLevelField.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import { t } from "$lib/i18n";

  type Props = {
    parameter: UISchemaParameter;
    /**
     * Effective working value. Comes from the caller's local pending-
     * changes map; falls back to `parameter.value` only when the user
     * has not touched the field yet. Reading this separately (instead
     * of `parameter.value`) is what keeps the Switch / Input in sync
     * after a toggle — the schema is stale by design, the working
     * value is not.
     */
    value: unknown;
    dirty: boolean;
    error: string | null;
    /**
     * When true the field is disabled on top of the parameter's own
     * writability. Used to lock values that a profile has fixed.
     */
    forceDisabled?: boolean;
    onChange: (value: unknown) => void;
    onAction?: () => void;
    /**
     * When true the machine name (rendered next to the label) is the
     * only thing distinguishing this field from another with an
     * identical label, so it is surfaced as a badge instead of the
     * usual muted inline suffix. Set by ParameterGrid's duplicate-label
     * disambiguation.
     */
    nameBadge?: boolean;
    /**
     * Present only for LINK condition-threshold fields whose sender
     * channel currently reports a brightness reading. Renders a
     * one-click "take current brightness" button that patches the field
     * with the sender's live value via `onChange` (so it respects the
     * editor's dirty tracking / undo stack). `display` is the
     * human-readable value shown in the tooltip. See
     * channel/brightness-helper.ts.
     */
    brightnessHelper?: { value: number; display: string } | null;
  };

  let {
    parameter,
    value,
    dirty,
    error,
    forceDisabled = false,
    onChange,
    onAction,
    nameBadge = false,
    brightnessHelper = null,
  }: Props = $props();

  // Writability model with two distinct axes so the UI can react
  // appropriately to each:
  //   • `isReadOnly` — OPERATIONS.WRITE is off. The parameter is
  //     intrinsically display-only (e.g. a sensor status). Disabled
  //     sliders / inputs are a poor UX here — they look interactive
  //     but do nothing. We render a compact read-only pill instead.
  //   • `isLocked` — OPERATIONS.WRITE is on but a profile apply has
  //     fixed the value. The user can still unlock it, so it makes
  //     sense to keep the real widget (dropdown, switch, …) visible
  //     but disabled with an explicit "profile" badge.
  const isReadOnly = $derived(!parameter.operations.write);
  const isLocked = $derived(!isReadOnly && forceDisabled);
  const interactionDisabled = $derived(isReadOnly || isLocked);
  // `writable` retained for child components that only look at a
  // single boolean; kept truthy only when the widget is fully open.
  const writable = $derived(!interactionDisabled);

  // LEVEL parameters (display_as_percent) render as a 0–100 % slider
  // plus optional "last value" checkbox; routed to a dedicated
  // component so the generic renderer below stays simple.
  const renderAsLevel = $derived(
    parameter.display_as_percent === true &&
      (parameter.type === "FLOAT" || parameter.type === "INTEGER"),
  );

  // --- Read-only formatting helpers --------------------------------
  // Produce a human label without falling back to a disabled input.
  function boolDisplay(v: unknown): string {
    return Boolean(v) ? t("quick.on") : t("quick.off");
  }

  function enumDisplay(v: unknown): string {
    const list = parameter.value_list ?? [];
    if (v == null) return "—";
    const num = Number(v);
    const hit = Number.isFinite(num)
      ? list.find((e) => e.value === num)
      : undefined;
    return hit?.label || hit?.key || String(v);
  }

  function numericDisplay(v: unknown): string {
    if (v == null || v === "") return "—";
    const n = Number(v);
    if (!Number.isFinite(n)) return String(v);
    // INTEGER: show as integer. FLOAT: trim trailing zeros but keep
    // at least one decimal when the step-resolution warrants it.
    if (parameter.type === "INTEGER") return String(Math.round(n));
    const formatted = Number.isInteger(n) ? n.toFixed(1) : String(n);
    return formatted;
  }

  function stringDisplay(v: unknown): string {
    if (v == null || v === "") return "—";
    return String(v);
  }

  // Help-Text rendering. Short hints (≤ 80 chars, no newlines) sit
  // inline below the field — they are quick context. Longer help
  // collapses behind an inline `(i)` icon next to the label that
  // toggles a popover-styled card. Mirrors HA's pattern (info icon
  // → on-click expanded description) instead of an always-present
  // toggle button below the widget.
  let helpExpanded = $state(false);
  const helpInline = $derived.by(() => {
    if (!parameter.help) return false;
    return parameter.help.length <= 80 && !parameter.help.includes("\n");
  });

  // Widget heuristic, port of aiohomematic-config's widgets.py
  // determine_widget():
  //   • ENUMs with ≤4 options → radio group (faster to scan, no
  //     extra click to open a dropdown).
  //   • INTEGER/FLOAT with a finite numeric [min, max] → slider plus
  //     a number input so the user can pick coarsely or precisely.
  //
  // When neither heuristic applies we fall through to the existing
  // Select / Input widgets.
  const renderAsRadio = $derived.by(() => {
    const list = parameter.value_list;
    return !!list && list.length > 0 && list.length <= 4;
  });

  const numericMin = $derived(
    typeof parameter.min === "number" ? parameter.min : null,
  );
  const numericMax = $derived(
    typeof parameter.max === "number" ? parameter.max : null,
  );

  const renderAsSlider = $derived.by(() => {
    if (parameter.type !== "FLOAT" && parameter.type !== "INTEGER") return false;
    if (renderAsLevel) return false;
    if (!parameter.value_list || parameter.value_list.length === 0) {
      return (
        numericMin !== null &&
        numericMax !== null &&
        Number.isFinite(numericMin) &&
        Number.isFinite(numericMax) &&
        numericMax > numericMin
      );
    }
    return false;
  });

  const sliderStep = $derived(parameter.type === "FLOAT" ? "any" : "1");

  // Coerce numbers out of input events before firing onChange. The
  // <input type="number"> emits strings, which we normalise to number
  // for comparison + server round-trip.
  function toNumber(raw: string): number | null {
    if (raw === "" || raw === null || raw === undefined) return null;
    const n = Number(raw);
    return Number.isFinite(n) ? n : null;
  }
</script>

{#if renderAsLevel}
  <ParameterLevelField
    {parameter}
    {value}
    {dirty}
    {error}
    {writable}
    readOnly={isReadOnly}
    {onChange}
  />
{:else}
<div class="flex flex-col gap-1">
  <div class="flex flex-wrap items-baseline gap-2">
    <span class="min-w-0 break-words text-sm font-medium text-slate-700 dark:text-slate-300">
      {parameter.label || parameter.name}
      {#if parameter.label && parameter.name !== parameter.label}
        {#if nameBadge}
          <Badge variant="muted" class="ml-1 align-middle font-mono">{parameter.name}</Badge>
        {:else}
          <span class="ml-1 font-mono text-[10px] text-[var(--ha-secondary-text-color)]">
            {parameter.name}
          </span>
        {/if}
      {/if}
    </span>
    {#if parameter.unit}
      <span class="text-xs text-[var(--ha-secondary-text-color)]">{parameter.unit}</span>
    {/if}
    {#if parameter.help && !helpInline}
      <button
        type="button"
        class="inline-flex items-center justify-center rounded-full p-0.5 text-slate-400 hover:bg-slate-100 hover:text-brand-700 dark:hover:bg-slate-800"
        aria-expanded={helpExpanded}
        aria-label={t("parameter.help")}
        title={t("parameter.help")}
        onclick={() => (helpExpanded = !helpExpanded)}
      >
        <Icon name="mdi:information-outline" size={14} />
      </button>
    {/if}
    {#if dirty}<Badge variant="warning">{t("parameter.modified")}</Badge>{/if}
    {#if isLocked}
      <Badge variant="muted">{t("parameter.profile_badge")}</Badge>
    {:else if isReadOnly}
      <Badge variant="muted">{t("parameter.read_only")}</Badge>
    {/if}
  </div>

  {#if parameter.help && !helpInline && helpExpanded}
    <!-- Popover-style help box: anchored under the label, looks like
         a small card so it stands out from the input row. Click the
         (i) icon again to close, or anywhere outside takes another
         click — kept intentionally simple, no portal positioning. -->
    <div
      class="mt-1 rounded-md border p-2 text-xs"
      style="background-color: var(--ha-secondary-background-color); border-color: var(--ha-divider-color); color: var(--ha-secondary-text-color);"
      role="note"
    >
      <p class="whitespace-pre-line">{parameter.help}</p>
    </div>
  {/if}

  {#if isReadOnly}
    <!-- Compact display widget: no disabled inputs, just the value.
         Slider / switch / select would all look interactive despite
         being dead, which is bad UX for pure status parameters. -->
    {#if parameter.type === "ACTION"}
      <span class="text-sm italic text-[var(--ha-secondary-text-color)]">
        {t("parameter.not_triggerable")}
      </span>
    {:else if parameter.type === "BOOL"}
      <div class="inline-flex items-center gap-2">
        <span
          aria-hidden="true"
          class="h-2 w-2 rounded-full {Boolean(value)
            ? 'bg-emerald-500'
            : 'bg-slate-400'}"
        ></span>
        <span class="text-sm font-medium text-slate-700 dark:text-slate-200">
          {boolDisplay(value)}
        </span>
      </div>
    {:else if parameter.value_list && parameter.value_list.length > 0}
      <span class="inline-flex w-fit items-center rounded-md bg-slate-100 px-2 py-1 text-sm text-slate-800 dark:bg-slate-800 dark:text-slate-100">
        {enumDisplay(value)}
      </span>
    {:else if parameter.type === "FLOAT" || parameter.type === "INTEGER"}
      <div class="flex items-baseline gap-2">
        <span class="font-mono text-lg font-semibold tabular-nums text-slate-800 dark:text-slate-100">
          {numericDisplay(value)}
        </span>
        {#if parameter.unit}
          <span class="text-xs text-[var(--ha-secondary-text-color)]">{parameter.unit}</span>
        {/if}
      </div>
    {:else if parameter.type === "STRING"}
      <span class="text-sm text-slate-700 dark:text-slate-200">
        {stringDisplay(value)}
      </span>
    {:else}
      <span class="font-mono text-xs text-[var(--ha-secondary-text-color)]">
        {value == null ? "—" : JSON.stringify(value)}
      </span>
    {/if}
  {:else if parameter.type === "ACTION"}
    <div>
      <Button
        type="button"
        variant="outline"
        size="sm"
        disabled={interactionDisabled}
        onclick={() => onAction?.()}
      >
        {t("parameter.execute")}
      </Button>
    </div>
  {:else if parameter.type === "BOOL"}
    <Switch
      checked={Boolean(value)}
      disabled={interactionDisabled}
      onCheckedChange={(v) => onChange(v)}
    />
  {:else if renderAsRadio && parameter.value_list}
    <div class="flex flex-wrap gap-2" role="radiogroup">
      {#each parameter.value_list as entry (entry.value)}
        {@const selected = value != null && Number(value) === entry.value}
        <label
          class="inline-flex min-h-10 cursor-pointer items-center gap-2 rounded-md border px-3 py-1.5 text-sm transition {selected
            ? 'border-brand-500 bg-brand-50 dark:bg-[color-mix(in_srgb,var(--color-brand-950)_30%,transparent)]'
            : 'border-slate-200 hover:border-slate-300 dark:border-slate-800'}"
        >
          <input
            type="radio"
            class="h-4 w-4"
            name={parameter.name}
            value={entry.value}
            checked={selected}
            disabled={interactionDisabled}
            onchange={() => onChange(entry.value)}
          />
          <span>{entry.label || entry.key}</span>
        </label>
      {/each}
    </div>
  {:else if parameter.value_list && parameter.value_list.length > 0}
    <Select
      options={parameter.value_list.map((e) => ({
        value: String(e.value),
        label: e.label || e.key,
      }))}
      value={value != null ? String(value) : ""}
      disabled={interactionDisabled}
      onValueChange={(v) => onChange(Number(v))}
    />
  {:else if renderAsSlider}
    <div class="flex items-center gap-3">
      <input
        type="range"
        min={numericMin ?? undefined}
        max={numericMax ?? undefined}
        step={sliderStep}
        value={value != null ? String(value) : numericMin ?? 0}
        disabled={interactionDisabled}
        class="h-3 flex-1 cursor-pointer appearance-none rounded-full bg-slate-200 accent-brand-500 dark:bg-slate-700"
        oninput={(e) => {
          const n = toNumber((e.target as HTMLInputElement).value);
          if (n !== null) onChange(n);
        }}
      />
      <div class="w-24">
        <Input
          type="number"
          step={sliderStep}
          min={numericMin ?? undefined}
          max={numericMax ?? undefined}
          value={value as number | null}
          disabled={interactionDisabled}
          oninput={(e) => {
            const n = toNumber((e.target as HTMLInputElement).value);
            if (n !== null) onChange(n);
          }}
        />
      </div>
    </div>
  {:else if parameter.type === "FLOAT" || parameter.type === "INTEGER"}
    <Input
      type="number"
      step={parameter.type === "FLOAT" ? "any" : "1"}
      min={parameter.min as number | undefined}
      max={parameter.max as number | undefined}
      value={value as number | null}
      disabled={interactionDisabled}
      oninput={(e) => {
        const n = toNumber((e.target as HTMLInputElement).value);
        if (n !== null) onChange(n);
      }}
    />
  {:else if parameter.type === "STRING"}
    <Input
      type="text"
      value={value as string | null}
      disabled={interactionDisabled}
      oninput={(e) => onChange((e.target as HTMLInputElement).value)}
    />
  {:else}
    <div class="text-xs italic text-[var(--ha-secondary-text-color)]">
      {t("parameter.unknown_type", { type: parameter.type })}
    </div>
  {/if}

  {#if brightnessHelper && !interactionDisabled}
    {@const reading = brightnessHelper}
    <!-- Motion-detector brightness helper: fills this LINK condition
         threshold with the sender channel's current reading. Mirrors
         the CCU WebUI's "use current brightness" button
         (config/ic_md.cgi). Writes through onChange so the edit is
         tracked / undoable like any manual change. -->
    <div>
      <Button
        type="button"
        variant="outline"
        size="sm"
        onclick={() => onChange(reading.value)}
        title={t("channel.brightness.apply_tooltip", { value: reading.display })}
      >
        <Icon name="mdi:sun" size={14} />
        {t("channel.brightness.apply", { value: reading.display })}
      </Button>
    </div>
  {/if}

  {#if !interactionDisabled && parameter.presets && parameter.presets.length > 0}
    <!-- EasyMode-Presets: clickable chips that patch the field to
         the preset value. Mirrors aiohomematic-config's preset
         picker; useful for ENUM/INTEGER/FLOAT-shaped MASTER fields. -->
    <div class="flex flex-wrap gap-1.5">
      {#each parameter.presets as preset, i (`${preset.label}-${i}`)}
        {@const selected =
          JSON.stringify(value) === JSON.stringify(preset.value)}
        <button
          type="button"
          class="rounded-full border px-2 py-1.5 text-[11px] transition {selected
            ? 'border-brand-500 bg-brand-50 text-brand-900 dark:bg-[color-mix(in_srgb,var(--color-brand-950)_40%,transparent)] dark:text-brand-100'
            : 'border-slate-200 text-slate-700 hover:border-brand-400 dark:border-slate-700 dark:text-slate-200'}"
          onclick={() => onChange(preset.value)}
          aria-pressed={selected}
        >
          {preset.label}
        </button>
      {/each}
    </div>
  {/if}

  {#if parameter.help && helpInline}
    <p class="text-xs" style="color: var(--ha-secondary-text-color);">{parameter.help}</p>
  {/if}
  {#if error}
    <p class="text-xs text-red-600 dark:text-red-400">{error}</p>
  {/if}
</div>
{/if}
