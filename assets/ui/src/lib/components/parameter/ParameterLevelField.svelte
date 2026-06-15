<script lang="ts">
  import Input from "$lib/components/ui/Input.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import type { UISchemaParameter } from "$lib/api/types";
  import { t } from "$lib/i18n";

  type Props = {
    parameter: UISchemaParameter;
    value: unknown;
    dirty: boolean;
    error: string | null;
    /**
     * Fully interactive (OPERATIONS.WRITE on and not profile-locked).
     */
    writable: boolean;
    /**
     * OPERATIONS.WRITE is off — the parameter is display-only. We
     * render a percentage progress bar instead of a disabled slider
     * so the user sees the current fill at a glance without the
     * visual noise of dead form controls.
     */
    readOnly: boolean;
    onChange: (value: unknown) => void;
  };

  let {
    parameter,
    value,
    dirty,
    error,
    writable,
    readOnly,
    onChange,
  }: Props = $props();

  // CCU LEVEL parameters carry their value in [0, 1] (float). Dimmer
  // receivers extend the range to 1.005 for the magic "last known
  // level" sentinel — the parameter then advertises max > 1.0 and
  // `has_last_value = true`. We surface the sentinel as a checkbox.
  const lastValueSentinel = 1.005;

  const max = $derived(typeof parameter.max === "number" ? parameter.max : 1);
  const min = $derived(typeof parameter.min === "number" ? parameter.min : 0);

  const numeric = $derived.by(() => {
    if (value == null) return null;
    const n = Number(value);
    return Number.isFinite(n) ? n : null;
  });

  const isLastValue = $derived(
    parameter.has_last_value === true &&
      numeric !== null &&
      numeric > 1 + 1e-6,
  );

  // Percent representation (0–100). Last-value state freezes the
  // slider at 100%, the checkbox tells the user that's what's active.
  const percent = $derived.by(() => {
    if (numeric === null) return 0;
    if (isLastValue) return 100;
    return Math.max(0, Math.min(100, Math.round(numeric * 100)));
  });

  function setPercent(next: number) {
    if (!writable) return;
    const clamped = Math.max(0, Math.min(100, next));
    onChange(clamped / 100);
  }

  function toggleLastValue(checked: boolean) {
    if (!writable) return;
    if (checked) {
      onChange(Math.min(max, lastValueSentinel));
    } else {
      onChange(1);
    }
  }

  const lastValueLabel = $derived(t("parameter.last_value"));
</script>

<div class="flex flex-col gap-1">
  <div class="flex items-baseline gap-2">
    <span class="text-sm font-medium text-slate-700 dark:text-slate-300">
      {parameter.label || parameter.name}
      {#if parameter.label && parameter.name !== parameter.label}
        <span class="ml-1 font-mono text-[10px] text-[var(--ha-secondary-text-color)]">
          {parameter.name}
        </span>
      {/if}
    </span>
    <span class="text-xs text-[var(--ha-secondary-text-color)]">%</span>
    {#if dirty}<Badge variant="warning">{t("parameter.modified")}</Badge>{/if}
    {#if readOnly}
      <Badge variant="muted">{t("parameter.read_only")}</Badge>
    {:else if !writable}
      <Badge variant="muted">{t("parameter.profile_badge")}</Badge>
    {/if}
  </div>

  {#if readOnly}
    <!-- Progress-bar display: a disabled slider here would be
         visually noisy for a pure status value (e.g. current LEVEL
         readout). The bar plus big percentage number conveys the
         same information at a glance. -->
    <div class="flex items-center gap-3">
      <div class="relative h-2 flex-1 overflow-hidden rounded-full bg-slate-200 dark:bg-slate-700">
        <div
          class="absolute inset-y-0 left-0 rounded-full bg-brand-500"
          style="width: {isLastValue ? 100 : percent}%"
        ></div>
      </div>
      <div class="flex items-baseline gap-1">
        <span class="font-mono text-lg font-semibold tabular-nums text-slate-800 dark:text-slate-100">
          {isLastValue ? "—" : percent}
        </span>
        <span class="text-xs text-[var(--ha-secondary-text-color)]">%</span>
      </div>
    </div>
    {#if isLastValue}
      <p class="text-xs text-[var(--ha-secondary-text-color)]">{t("parameter.last_value")}</p>
    {/if}
  {:else}
    <div class="flex items-center gap-3">
      <input
        type="range"
        min="0"
        max="100"
        step="1"
        value={percent}
        disabled={!writable || isLastValue}
        class="h-3 flex-1 cursor-pointer appearance-none rounded-full bg-slate-200 accent-brand-500 dark:bg-slate-700"
        oninput={(e) => setPercent(Number((e.target as HTMLInputElement).value))}
      />
      <div class="w-20">
        <Input
          type="number"
          min={min * 100}
          max={Math.min(100, max * 100)}
          step="1"
          value={percent}
          disabled={!writable || isLastValue}
          oninput={(e) => {
            const n = Number((e.target as HTMLInputElement).value);
            if (Number.isFinite(n)) setPercent(n);
          }}
        />
      </div>
    </div>
  {/if}

  {#if !readOnly && parameter.has_last_value && max > 1 + 1e-6}
    <label class="mt-1 flex items-center gap-2 text-xs text-slate-600 dark:text-slate-400">
      <input
        type="checkbox"
        checked={isLastValue}
        disabled={!writable}
        class="h-4 w-4 rounded border-[var(--ha-divider-color)]"
        onchange={(e) =>
          toggleLastValue((e.target as HTMLInputElement).checked)}
      />
      {lastValueLabel}
    </label>
  {/if}

  {#if parameter.help}
    <p class="text-xs text-[var(--ha-secondary-text-color)]">{parameter.help}</p>
  {/if}
  {#if error}
    <p class="text-xs text-red-600 dark:text-red-400">{error}</p>
  {/if}
</div>
