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

  // A direct link carries a LINK paramset on BOTH ends, each keyed by
  // the opposite channel: the receiver side holds the actuator
  // behaviour (getParamset(receiver, peer=sender)) and the sender side
  // holds the keypress/transmit behaviour (getParamset(sender,
  // peer=receiver)). Many links have only a receiver-side paramset
  // (e.g. actuator→actuator); senders such as push-buttons add
  // SHORT_/LONG_ keypress parameters. We render the receiver side
  // unconditionally and the sender side only when its channel reports a
  // non-empty paramset. Mirrors config-panel link-config.ts:66-91,
  // which fetches both schemas and treats the sender side as optional.
  const receiverDevice = $derived(link.receiver_address.split(":")[0]);
  const receiverChannelNo = $derived(
    Number(link.receiver_address.split(":")[1] ?? 0),
  );
  const senderDevice = $derived(link.sender_address.split(":")[0]);
  const senderChannelNo = $derived(
    Number(link.sender_address.split(":")[1] ?? 0),
  );

  // -1 = not yet probed, 0 = no sender-side paramset (section hidden),
  // >0 = sender carries parameters (section shown).
  let senderParamCount = $state(-1);
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

  <section>
    <h3
      class="mb-2 text-sm font-semibold text-[var(--ha-secondary-text-color)]"
    >
      {t("links.config.receiver_section")}
    </h3>
    <ChannelPanel
      address={receiverDevice}
      channel={receiverChannelNo}
      paramset="LINK"
      peer={link.sender_address}
      {locale}
    />
  </section>

  <!--
    Sender side: always mounted so its paramset is probed, but kept
    hidden until it reports parameters. Links without a sender-side
    paramset (count 0) or whose sender errors stay collapsed.
  -->
  <section
    class="mt-6"
    style:display={senderParamCount > 0 ? "" : "none"}
    aria-hidden={senderParamCount > 0 ? undefined : "true"}
  >
    <h3
      class="mb-2 text-sm font-semibold text-[var(--ha-secondary-text-color)]"
    >
      {t("links.config.sender_section")}
    </h3>
    <ChannelPanel
      address={senderDevice}
      channel={senderChannelNo}
      paramset="LINK"
      peer={link.receiver_address}
      {locale}
      onLoaded={(info) => (senderParamCount = info.error ? 0 : info.count)}
    />
  </section>
</Card>
