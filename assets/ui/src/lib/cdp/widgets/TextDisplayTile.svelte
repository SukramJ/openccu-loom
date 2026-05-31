<!--
  CDP-aware text-display tile (HmIP wall-display Row N text). Has a
  single-row text input + write button; the row id, icon, and colour
  are exposed via "Erweitert" — most users want to push one line
  quickly.

  Service operation: write { id, text, icon?, color? }.
-->
<script lang="ts">
  import type { CustomDPSummary } from "$lib/api/types";
  import { api, friendlyError } from "$lib/api/client";
  import { t } from "$lib/i18n";
  import ControlTile from "$lib/control/tile/ControlTile.svelte";
  import ControlTileIcon from "$lib/control/tile/ControlTileIcon.svelte";
  import ControlTileInfo from "$lib/control/tile/ControlTileInfo.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";

  type Props = {
    address: string;
    cdp: CustomDPSummary;
    title?: string;
  };

  let { address, cdp, title }: Props = $props();
  const displayTitle = $derived(title ?? cdp.name);

  let rowId = $state(1);
  let text = $state("");
  let error = $state<string | null>(null);
  let busy = $state(false);
  let advanced = $state(false);
  let iconValue = $state<string>("");
  let color = $state<string>("");

  const tileColor = "var(--ha-secondary-text-color)";

  async function write() {
    busy = true;
    error = null;
    try {
      const params: Record<string, unknown> = { id: rowId, text };
      if (iconValue) params.icon = iconValue;
      if (color) {
        const n = Number(color);
        if (Number.isFinite(n)) params.color = n;
      }
      await api.invokeCustomDataPoint(address, cdp.name, "write", params);
    } catch (err) {
      error = friendlyError(err, t);
    } finally {
      busy = false;
    }
  }
</script>

<ControlTile {tileColor}>
  {#snippet icon()}
    <ControlTileIcon active={false} label={displayTitle}>
      <Icon name="mdi:pencil" size={22} />
    </ControlTileIcon>
  {/snippet}
  {#snippet info()}
    <ControlTileInfo primary={displayTitle} secondary={`Zeile ${rowId}`} />
  {/snippet}
  {#snippet features()}
    <div class="flex flex-col gap-2">
      <label class="flex items-center gap-2 text-xs text-[var(--ha-secondary-text-color)]">
        Zeile
        <input
          type="number"
          min="1"
          max="10"
          bind:value={rowId}
          class="h-8 w-16 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-2 text-sm text-[var(--ha-primary-text-color)]"
        />
      </label>
      <textarea
        rows="2"
        bind:value={text}
        placeholder="Text…"
        class="rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-2 py-1 text-sm text-[var(--ha-primary-text-color)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-offset-1"
      ></textarea>
      <div class="flex items-center justify-between gap-2">
        <button
          type="button"
          class="text-xs text-[var(--ha-secondary-text-color)] underline"
          onclick={() => (advanced = !advanced)}
        >
          {advanced ? "Weniger" : "Erweitert"}
        </button>
        <button
          type="button"
          disabled={busy || !text}
          class="rounded-md bg-[var(--ha-primary-color)] px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50"
          onclick={write}
        >
          {busy ? "Sendet…" : "Schreiben"}
        </button>
      </div>
      {#if advanced}
        <div class="flex flex-col gap-2 text-xs">
          <label class="flex items-center gap-2 text-[var(--ha-secondary-text-color)]">
            Icon
            <input
              bind:value={iconValue}
              placeholder="z.B. 0"
              class="h-7 flex-1 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-2 text-[var(--ha-primary-text-color)]"
            />
          </label>
          <label class="flex items-center gap-2 text-[var(--ha-secondary-text-color)]">
            Farbe
            <input
              bind:value={color}
              placeholder="Index 0–N"
              class="h-7 flex-1 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-2 text-[var(--ha-primary-text-color)]"
            />
          </label>
        </div>
      {/if}
      {#if error}
        <p class="text-xs text-[var(--ha-error-color)]">{error}</p>
      {/if}
    </div>
  {/snippet}
</ControlTile>
