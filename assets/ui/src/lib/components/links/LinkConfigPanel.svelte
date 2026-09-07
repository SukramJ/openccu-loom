<script lang="ts">
  import ChannelPanel from "$lib/components/channel/ChannelPanel.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import { t } from "$lib/i18n";

  // Primitives rather than the Link object, and deliberately so.
  //
  // The panel used to take the Link straight from the parent's nullable
  // `editing` holder. ChannelPanel.save() reads its props again after
  // `await api.putLinkParamset(...)` — its `peer` is the sender address
  // this component derives — so an operator who left the editor while
  // the PUT was in flight nulled that holder underneath a running save.
  // The CCU had accepted the write; the UI reported "channel.save_failed:
  // Cannot read properties of null (reading 'sender_address')".
  //
  // A string prop cannot be dereferenced, so no read after any await can
  // fail however the parent's own state moves on.
  type Props = {
    senderAddress: string;
    receiverAddress: string;
    name: string;
    senderDeviceLabel: string;
    senderChannelLabel: string;
    receiverDeviceLabel: string;
    receiverChannelLabel: string;
    locale: string;
    onBack: () => void;
  };

  let {
    senderAddress,
    receiverAddress,
    name,
    senderDeviceLabel,
    senderChannelLabel,
    receiverDeviceLabel,
    receiverChannelLabel,
    locale,
    onBack,
  }: Props = $props();

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
  const receiverDevice = $derived(receiverAddress.split(":")[0]);
  const receiverChannelNo = $derived(
    Number(receiverAddress.split(":")[1] ?? 0),
  );
  const senderDevice = $derived(senderAddress.split(":")[0]);
  const senderChannelNo = $derived(Number(senderAddress.split(":")[1] ?? 0));

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
        {name || `${senderAddress} → ${receiverAddress}`}
      </h2>
      <p class="text-xs text-[var(--ha-secondary-text-color)]">
        {senderDeviceLabel}
        · {senderChannelLabel}
        → {receiverDeviceLabel}
        · {receiverChannelLabel}
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
      peer={senderAddress}
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
      peer={receiverAddress}
      {locale}
      onLoaded={(info) => (senderParamCount = info.error ? 0 : info.count)}
    />
  </section>
</Card>
