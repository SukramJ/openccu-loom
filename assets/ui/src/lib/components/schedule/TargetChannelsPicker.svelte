<script lang="ts">
  import { t } from "$lib/i18n";

  // 8×3 grid of toggle pills for the TARGET_CHANNELS bitmask. Mirrors
  // aiohomematic's ScheduleActorChannel layout: 8 actor channels,
  // each with 3 sub-functions (X_Y notation, X=1..8, Y=1..3). Real
  // CCU devices typically only expose a subset; we render all 24 and
  // let the user pick. An empty selection → CCU default routing.

  type Props = {
    selected: string[];
    onChange: (next: string[]) => void;
  };

  let { selected, onChange }: Props = $props();

  const channels: string[][] = (() => {
    const out: string[][] = [];
    for (let ch = 1; ch <= 8; ch++) {
      const row: string[] = [];
      for (let fn = 1; fn <= 3; fn++) row.push(`${ch}_${fn}`);
      out.push(row);
    }
    return out;
  })();

  function isSelected(name: string): boolean {
    return selected.includes(name);
  }

  function toggle(name: string) {
    if (isSelected(name)) {
      onChange(selected.filter((s) => s !== name));
    } else {
      // Keep canonical sort order so "1_1, 1_2, 2_1" stays stable.
      const next = [...selected, name].sort((a, b) => a.localeCompare(b));
      onChange(next);
    }
  }

  function selectAll() {
    onChange(channels.flat());
  }
  function clear() {
    onChange([]);
  }
</script>

<div class="space-y-2">
  <div class="flex items-center justify-between text-xs">
    <span class="text-[var(--ha-secondary-text-color)]">
      {selected.length === 0
        ? t("schedule.targets.all_default")
        : t("schedule.targets.selected", { count: selected.length })}
    </span>
    <div class="flex gap-2">
      <button
        type="button"
        class="text-brand-700 hover:text-brand-800"
        onclick={selectAll}
      >
        {t("schedule.targets.all")}
      </button>
      <button
        type="button"
        class="text-[var(--ha-secondary-text-color)] hover:text-[var(--ha-primary-text-color)]"
        onclick={clear}
      >
        {t("schedule.targets.none")}
      </button>
    </div>
  </div>
  <div class="grid grid-cols-3 gap-1">
    {#each channels as row (row[0])}
      {#each row as ch (ch)}
        {@const active = isSelected(ch)}
        <button
          type="button"
          class="h-10 rounded-md border text-xs font-mono transition {active
            ? 'border-brand-500 bg-brand-500 text-white'
            : 'border-slate-300 text-slate-600 hover:border-brand-500 dark:border-slate-700 dark:text-slate-300'}"
          aria-pressed={active}
          onclick={() => toggle(ch)}
        >
          {ch}
        </button>
      {/each}
    {/each}
  </div>
</div>
