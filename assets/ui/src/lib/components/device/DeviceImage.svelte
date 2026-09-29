<!--
  Device picture for the device detail header. Loads the model artwork
  from GET /devices/{addr}/icon (served from the daemon's embedded data
  snapshot, or proxied from the CCU for a model the snapshot lacks) and
  falls back to the device-type glyph when the image fails to load — the
  route answers 404 for a device it has no picture for.
-->
<script lang="ts">
  import { apiBase } from "$lib/api/base";
  import { deviceTypeIcon } from "$lib/device-icon";
  import Icon from "$lib/components/ui/Icon.svelte";
  import { t } from "$lib/i18n";

  type Props = {
    address: string;
    model: string;
    productGroup?: string;
  };

  let { address, model, productGroup }: Props = $props();

  // Failure is tracked per address, so navigating from a device without a
  // picture to one with a picture retries the image instead of keeping
  // the previous device's glyph.
  let failedFor = $state<string | null>(null);
  const failed = $derived(failedFor === address);

  // apiBase() carries the Ingress prefix; an absolute "/api/v1/…" URL
  // would bypass the Home Assistant Ingress proxy.
  const src = $derived(`${apiBase()}/devices/${encodeURIComponent(address)}/icon`);
  const glyph = $derived(deviceTypeIcon({ model, product_group: productGroup }));
  const label = $derived(t("device.image_alt", { model }));
</script>

<div
  class="flex h-16 w-16 shrink-0 items-center justify-center rounded-lg bg-white ring-1 ring-slate-200 dark:bg-slate-800 dark:ring-slate-700"
  data-testid="device-image"
  data-state={failed ? "fallback" : "image"}
>
  {#if failed}
    <Icon
      name={glyph}
      size={32}
      class="text-slate-400 dark:text-slate-500"
      aria-label={label}
    />
  {:else}
    <img
      {src}
      alt={label}
      class="h-14 w-14 object-contain"
      loading="lazy"
      onerror={() => (failedFor = address)}
    />
  {/if}
</div>
