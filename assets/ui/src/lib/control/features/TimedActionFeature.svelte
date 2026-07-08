<!--
  Timed-action feature: a shared control for "on for <duration>" (switch)
  and "open for <duration>" (valve). Renders a small row of quick-preset
  chips plus a free number input so an operator can pick either a common
  duration in one tap or an arbitrary one. Emits the chosen duration in
  whole seconds through onSubmit — the widget forwards it as the CDP
  operation's canonical `seconds` parameter.
-->
<script lang="ts">
  import { untrack } from "svelte";
  import ControlButton from "../controls/ControlButton.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";

  type Props = {
    /** Localised section heading + submit aria-label (e.g. "An für…"). */
    label: string;
    /** Quick presets in seconds. */
    presets?: number[];
    /** Initial value of the free input, in seconds. */
    defaultSeconds?: number;
    color: string;
    disabled?: boolean;
    onSubmit: (seconds: number) => void;
  };

  let {
    label,
    presets = [30, 60, 300],
    defaultSeconds = 30,
    color,
    disabled = false,
    onSubmit,
  }: Props = $props();

  // Seed the free input once from the prop; later edits are the operator's.
  let custom = $state(untrack(() => defaultSeconds));

  // Units "s" / "min" read identically in de + en, so no i18n key needed.
  function fmt(seconds: number): string {
    if (seconds >= 60 && seconds % 60 === 0) {
      return `${seconds / 60} min`;
    }
    return `${seconds} s`;
  }

  function fire(seconds: number) {
    const s = Math.max(1, Math.round(seconds));
    if (!Number.isFinite(s)) return;
    onSubmit(s);
  }
</script>

<div class="space-y-1.5">
  <span class="text-xs text-[var(--ha-secondary-text-color)]">{label}</span>
  <div class="flex flex-wrap items-center gap-2">
    {#each presets as preset (preset)}
      <ControlButton
        {color}
        {disabled}
        label={fmt(preset)}
        onClick={() => fire(preset)}
      >{fmt(preset)}</ControlButton>
    {/each}
    <div class="flex items-center gap-1">
      <input
        type="number"
        min="1"
        step="1"
        bind:value={custom}
        {disabled}
        aria-label={label}
        class="h-10 w-16 rounded-md border border-[var(--ha-divider-color)] bg-transparent px-2 text-right text-sm tabular-nums text-[var(--ha-primary-text-color)] focus:border-[var(--ha-primary-color)] focus:outline-none disabled:cursor-not-allowed disabled:opacity-50"
      />
      <span class="text-sm text-[var(--ha-secondary-text-color)]">s</span>
      <ControlButton
        active
        {color}
        {disabled}
        label={label}
        onClick={() => fire(custom)}
      ><Icon name="mdi:play" size={18} /></ControlButton>
    </div>
  </div>
</div>
