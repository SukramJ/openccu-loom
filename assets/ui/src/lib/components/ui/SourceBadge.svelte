<script lang="ts">
  // SourceBadge renders the resolved origin of a config field
  // (bootstrap / db / env / default). Wave-D introduces it as the
  // dezent-but-immer-sichtbar "set by integration"-style pill that
  // sits next to every Settings field.
  //
  // In basic mode (prefs.expertMode = false) the pill is rendered
  // as a small coloured dot without a label; in expert mode the
  // label spells out the source. Hover always reveals the full
  // text via the native title tooltip.
  import { prefs } from "$lib/stores/preferences.svelte";
  import { t } from "$lib/i18n";
  import { cn } from "$lib/utils";

  type Source = "bootstrap" | "db" | "env" | "default";
  type Props = { source: Source; class?: string };

  let { source, class: className }: Props = $props();

  // Colour mapping: bootstrap = blue (operator-pinned via YAML),
  // db = green (managed via this UI), env = amber (resolved at
  // runtime), default = grey (no override).
  const palette: Record<Source, string> = {
    bootstrap: "bg-blue-500",
    db: "bg-emerald-500",
    env: "bg-amber-500",
    default: "bg-slate-400",
  };

  const longLabel = $derived(t(`config.source.${source}`));
  const shortLabel = $derived(t(`config.source.short.${source}`));
</script>

<span
  class={cn(
    "inline-flex items-center gap-1 text-[11px] text-slate-500 dark:text-slate-400",
    className,
  )}
  title={longLabel}
>
  <span class={cn("inline-block size-2 rounded-full", palette[source])}></span>
  {#if prefs.expertMode}
    <span class="font-medium uppercase tracking-wide">{shortLabel}</span>
  {/if}
</span>
