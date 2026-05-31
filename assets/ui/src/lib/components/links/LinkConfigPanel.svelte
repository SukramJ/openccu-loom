<script lang="ts">
  import type { Link } from "$lib/api/types";
  import ChannelPanel from "$lib/components/channel/ChannelPanel.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import { t } from "$lib/i18n";

  type Props = {
    link: Link;
    locale: string;
    onBack: () => void;
  };

  let { link, locale, onBack }: Props = $props();

  // LINK paramsets live exclusively on the receiver side; the CCU
  // keys them by the sender channel. This is the same convention
  // aiohomematic-config and homematicip_local follow: description is
  // fetched via getParamsetDescription(receiver, "LINK"), values via
  // getParamset(receiver, sender), writes via putParamset(receiver,
  // sender, values). The link's direction is purely a display hint
  // — it does not change where the paramset is stored.
  const deviceAddress = $derived(link.receiver_address.split(":")[0]);
  const channelNo = $derived(
    Number(link.receiver_address.split(":")[1] ?? 0),
  );
  const peerAddress = $derived(link.sender_address);
</script>

<Card class="p-4">
  <header class="mb-4 flex items-center justify-between gap-3">
    <div>
      <button
        type="button"
        class="text-xs text-[var(--ha-secondary-text-color)] hover:text-brand-700"
        onclick={onBack}
      >
        ← {t("links.config.back_to_list")}
      </button>
      <h2 class="mt-2 text-lg font-semibold">
        {link.name || `${link.sender_address} → ${link.receiver_address}`}
      </h2>
      <p class="text-xs text-[var(--ha-secondary-text-color)]">
        {link.sender_device_name || link.sender_address}
        · {link.sender_channel_type_label || link.sender_channel_type}
        → {link.receiver_device_name || link.receiver_address}
        · {link.receiver_channel_type_label || link.receiver_channel_type}
      </p>
    </div>
    <Button type="button" variant="outline" size="sm" onclick={onBack}>
      {t("common.close")}
    </Button>
  </header>

  <ChannelPanel
    address={deviceAddress}
    channel={channelNo}
    paramset="LINK"
    peer={peerAddress}
    {locale}
  />
</Card>
