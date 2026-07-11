<script lang="ts">
  // SourceBadge renders the resolved origin of a config field
  // (bootstrap / db / env / default) as a small coloured dot next
  // to every Settings field.
  //
  // In basic mode (prefs.expertMode = false) the pill is rendered
  // as a small coloured dot without a visible label; in expert mode
  // the label spells out the source. The dot alone is not
  // self-explanatory (colour-only signal), so it always carries a
  // native title tooltip plus a screen-reader-only text describing
  // the source in full.
  import { prefs } from "$lib/stores/preferences.svelte";
  import { t } from "$lib/i18n";
  import { cn } from "$lib/utils";

  type Source = "bootstrap" | "db" | "env" | "default";
  type Props = { source: Source; class?: string };

  let { source, class: className }: Props = $props();

  // Colour mapping: bootstrap = violet (operator-pinned via YAML),
  // db = green (managed via this UI), env = amber (resolved at
  // runtime), default = grey (no override). Violet, not blue — the
  // brand colour is teal and a blue dot would read as a second,
  // competing accent.
  const palette: Record<Source, string> = {
    bootstrap: "bg-violet-500",
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
  <span class={cn("inline-block size-2 rounded-full", palette[source])}>
    <span class="sr-only">{longLabel}</span>
  </span>
  {#if prefs.expertMode}
    <span class="font-medium uppercase tracking-wide">{shortLabel}</span>
  {/if}
</span>
