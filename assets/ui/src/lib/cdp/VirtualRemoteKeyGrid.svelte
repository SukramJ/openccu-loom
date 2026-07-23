<!--
  Key grid for the CCU's virtual remotes (HM-RCV-50 / HMW-RCV-50 /
  HmIP-RCV-50): one cell per KEY channel with short / long press
  buttons. A press is a single boolean write of PRESS_SHORT /
  PRESS_LONG through the ordinary data-point value path — the CCU
  WebUI's own key control performs exactly one set per click, long
  press included (there is no hold protocol). A cell flashes when the
  CCU echoes the press back as a device.trigger broadcast, confirming
  the round trip like a physical keypress would.
-->
<script lang="ts">
  import { onMount } from "svelte";
  import { api } from "$lib/api/client";
  import type { DeviceDetail } from "$lib/api/types";
  import Button from "$lib/components/ui/Button.svelte";
  import { subscribe } from "$lib/stores/events.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";

  type Props = {
    detail: DeviceDetail;
  };

  let { detail }: Props = $props();

  const keys = $derived(
    [...detail.channels]
      .filter((c) => c.number > 0)
      .sort((a, b) => a.number - b.number),
  );

  let busyKey = $state<string | null>(null);
  let flashed = $state<Record<number, number>>({});

  async function press(no: number, param: "PRESS_SHORT" | "PRESS_LONG") {
    busyKey = `${no}:${param}`;
    try {
      await api.setValue(detail.address, no, param, true);
    } catch (err) {
      toastStore.error(
        t("remote.press_failed"),
        err instanceof Error ? err.message : String(err),
      );
    } finally {
      busyKey = null;
    }
  }

  onMount(() =>
    subscribe((ev) => {
      if (ev.type !== "device.trigger") return;
      const p = ev.payload as {
        device_address?: string;
        channel?: number;
      };
      if (p?.device_address !== detail.address || typeof p.channel !== "number") {
        return;
      }
      const no = p.channel;
      flashed = { ...flashed, [no]: Date.now() };
      setTimeout(() => {
        const at = flashed[no];
        if (at && Date.now() - at >= 900) {
          const next = { ...flashed };
          delete next[no];
          flashed = next;
        }
      }, 1000);
    }),
  );
</script>

<section aria-label={t("remote.key_grid_title")}>
  <h3 class="mb-2 text-sm font-medium text-slate-900 dark:text-white">
    {t("remote.key_grid_title")}
  </h3>
  <div class="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
    {#each keys as ch (ch.address)}
      {@const isFlashing = flashed[ch.number] !== undefined}
      <div
        class="rounded-md border p-2 transition-shadow {isFlashing
          ? 'border-brand-400 shadow-[0_0_0_2px_var(--ha-primary-color)] dark:border-brand-500'
          : 'border-slate-200 dark:border-slate-700'}"
      >
        <div
          class="truncate text-xs font-medium text-slate-700 dark:text-slate-200"
          title={ch.address}
        >
          {ch.name?.trim() || t("remote.key_n", { n: ch.number })}
          <span class="ml-1 text-slate-400 dark:text-slate-500">({ch.number})</span>
        </div>
        <div class="mt-1.5 flex gap-1.5">
          <Button
            type="button"
            variant="outline"
            size="sm"
            class="flex-1"
            disabled={busyKey === `${ch.number}:PRESS_SHORT`}
            aria-label={t("remote.press_short_aria", { n: ch.number })}
            onclick={() => void press(ch.number, "PRESS_SHORT")}
          >
            {t("remote.press_short")}
          </Button>
          <Button
            type="button"
            variant="outline"
            size="sm"
            class="flex-1"
            disabled={busyKey === `${ch.number}:PRESS_LONG`}
            aria-label={t("remote.press_long_aria", { n: ch.number })}
            onclick={() => void press(ch.number, "PRESS_LONG")}
          >
            {t("remote.press_long")}
          </Button>
        </div>
      </div>
    {/each}
  </div>
</section>
