<script lang="ts">
  import type { DeviceSummary } from "$lib/api/types";
  import { apiBase } from "$lib/api/base";
  import Icon from "$lib/components/ui/Icon.svelte";
  import { deviceTypeIcon } from "$lib/device-icon";
  import { maintenanceStore } from "$lib/stores/maintenance.svelte";
  import { t } from "$lib/i18n";

  type Props = {
    device: DeviceSummary;
    selected?: boolean;
    onToggleSelect?: (selected: boolean) => void;
  };
  let { device, selected = false, onToggleSelect }: Props = $props();

  const subtitle = $derived(device.model_label || device.model);
  const typeIcon = $derived(deviceTypeIcon(device));
  // Real eQ-3 device image, proxied from the CCU. Falls back to the type
  // glyph when the CCU has no icon for this model or is offline. The URL
  // MUST carry the ingress prefix (apiBase) — a hard-coded /api/v1 bypasses
  // the Home Assistant Ingress proxy and hits the HA origin instead, so
  // every icon 404s and shows only the fallback glyph when the daemon runs
  // as an HA add-on (it works untouched as a CCU add-on, where the prefix
  // is empty). See lib/api/base.ts.
  const iconUrl = $derived(`${apiBase()}/devices/${encodeURIComponent(device.address)}/icon`);
  let iconFailed = $state(false);
  // Reset the failure flag if this card instance is reused for another
  // device (defensive — the list is keyed by address).
  $effect(() => {
    device.address;
    iconFailed = false;
  });

  // Live maintenance values from the WS bus. `null` until the daemon
  // ships an event for this device — keeps the icons honest about
  // what we know vs. don't know.
  maintenanceStore.bind();
  const maintenance = $derived(maintenanceStore.all()[device.address] ?? null);

  // Tone helpers — green for "good", amber for "warn", red for "bad".
  function tone(active: boolean | null, badIsTrue: boolean): string {
    if (active === null) return "color: var(--ha-disabled-text-color);";
    if (active === badIsTrue) return "color: var(--ha-error-color);";
    return "color: var(--ha-success-color);";
  }
</script>

<div
  class="group flex items-start gap-3 rounded-lg border p-4 shadow-sm transition hover:border-brand-500 hover:shadow-md"
  style="background-color: var(--ha-card-background-color); border-color: {selected ? 'var(--ha-primary-color)' : 'var(--ha-divider-color)'};"
  class:ring-2={selected}
  class:ring-brand-300={selected}
>
  {#if onToggleSelect}
    <label class="flex min-h-10 min-w-10 flex-shrink-0 items-center justify-center">
      <input
        type="checkbox"
        class="h-4 w-4 cursor-pointer accent-brand-500"
        checked={selected}
        onchange={(e) => onToggleSelect((e.target as HTMLInputElement).checked)}
        aria-label={t("device.list.select_aria")}
      />
    </label>
  {/if}
  <a
    href="#/devices/{encodeURIComponent(device.address)}"
    class="flex min-w-0 flex-1 items-start gap-3"
  >
    <!-- Leading device icon with a reachability dot at the corner.
         Prefer the real eQ-3 image proxied from the CCU; fall back to a
         type glyph when it is unavailable. -->
    <div class="relative mt-0.5 flex-shrink-0" style="color: var(--ha-secondary-text-color);">
      <!-- Icon plate. The CCU device artwork is monochrome line-art: some
           models ship a transparent PNG with dark strokes, others a PNG
           with a baked-in white background. A white plate keeps both
           legible in light mode. In dark mode that white plate is a
           glaring box, so the plate goes transparent and the grayscale art
           is inverted (dark strokes -> light) to sit cleanly on the dark
           card. The type-glyph fallback (a currentColor SVG) shares the
           plate so the two states keep the same footprint — it must NOT be
           inverted, so dark:invert lives on the <img> only. -->
      <div
        class="flex h-9 w-9 items-center justify-center rounded-lg bg-white p-1 ring-1 ring-slate-200 dark:bg-transparent dark:ring-slate-700"
      >
        {#if iconFailed}
          <Icon name={typeIcon} size={22} aria-label={subtitle} title={subtitle} />
        {:else}
          <img
            src={iconUrl}
            alt={subtitle}
            title={subtitle}
            width="22"
            height="22"
            loading="lazy"
            class="h-[22px] w-[22px] object-contain dark:invert"
            onerror={() => (iconFailed = true)}
          />
        {/if}
      </div>
      <span
        class="absolute -right-0.5 -bottom-0.5 h-2.5 w-2.5 rounded-full"
        class:bg-emerald-500={device.available}
        class:bg-slate-400={!device.available}
        style="box-shadow: 0 0 0 2px var(--ha-card-background-color);"
        title={device.available
          ? t("device.list.reachable")
          : t("device.list.unreachable")}
      ></span>
    </div>
    <div class="min-w-0 flex-1">
      <h3
        class="break-words font-medium"
        style="color: var(--ha-primary-text-color);"
      >
        {device.name || device.address}
      </h3>
      <!-- Description split into two lines: model on its own row,
           transport metadata on a second smaller row. Wrapping is on
           by default (`break-words`) so long model labels or rare
           multi-CCU strings don't get clipped at the card edge. -->
      <p
        class="mt-0.5 break-words text-xs"
        style="color: var(--ha-secondary-text-color);"
      >
        {subtitle}
      </p>
      <p
        class="mt-0.5 break-words text-[11px]"
        style="color: var(--ha-disabled-text-color);"
      >
        {device.interface}{#if device.central}
          · {device.central}
        {/if} · {device.channels_count}&nbsp;{t("device.list.channels")}
      </p>
      {#if device.rooms && device.rooms.length > 0}
        <p
          class="mt-1 break-words text-xs"
          style="color: var(--ha-secondary-text-color);"
        >
          {device.rooms.join(", ")}
        </p>
      {/if}
    </div>
    <div class="flex flex-shrink-0 flex-col items-end gap-1.5">
      {#if device.update_available}
        <span
          class="rounded-full px-2 py-0.5 text-[11px] font-medium"
          style="background-color: rgb(251 191 36 / 0.18); color: var(--ha-warning-color);"
          title={t("device.list.firmware_available")}
        >
          FW
        </span>
      {/if}
      {#if maintenance}
        <div class="flex items-center gap-1">
          {#if maintenance.LOW_BAT !== undefined}
            <span style={tone(Boolean(maintenance.LOW_BAT), true)}>
              <Icon
                name="mdi:battery-alert"
                size={16}
                aria-label={t("device.maintenance.low_bat")}
                title={t("device.maintenance.low_bat")}
              />
            </span>
          {/if}
          {#if maintenance.DUTY_CYCLE !== undefined && Boolean(maintenance.DUTY_CYCLE)}
            <span style="color: var(--ha-warning-color);">
              <Icon
                name="mdi:alert-triangle"
                size={16}
                aria-label={t("device.maintenance.duty_cycle")}
                title={t("device.maintenance.duty_cycle")}
              />
            </span>
          {/if}
          {#if maintenance.CONFIG_PENDING !== undefined && Boolean(maintenance.CONFIG_PENDING)}
            <span style="color: var(--ha-info-color);">
              <Icon
                name="mdi:information-outline"
                size={16}
                aria-label={t("device.maintenance.config_pending")}
                title={t("device.maintenance.config_pending")}
              />
            </span>
          {/if}
        </div>
      {/if}
    </div>
  </a>
</div>
