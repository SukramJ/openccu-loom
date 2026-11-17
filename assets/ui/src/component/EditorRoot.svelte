<script lang="ts">
  // The editor as the element mounts it: the SPA's own device page, so the
  // SPA route and the component are the same code. The host's surface set
  // leaves out everything that is not the configuration editor or the
  // change log; the shell's overlays (toasts, the confirm dialog) come
  // along because the element has no shell around it.
  import { BitsConfig } from "bits-ui";
  import DeviceDetail from "../routes/DeviceDetail.svelte";
  import Toaster from "$lib/components/ui/Toaster.svelte";
  import ConfirmDialog from "$lib/components/ui/ConfirmDialog.svelte";

  type Props = {
    address: string;
    channel?: number;
    sub?: string;
    locale: string;
    /** The node inside the shadow root that selects, popovers and tooltips render into. */
    portal: HTMLElement;
  };

  let { address, channel, sub, locale, portal }: Props = $props();
</script>

<BitsConfig defaultPortalTo={portal}>
  {#key address}
    <DeviceDetail {address} {channel} {sub} {locale} />
  {/key}
  <Toaster />
  <ConfirmDialog />
</BitsConfig>
