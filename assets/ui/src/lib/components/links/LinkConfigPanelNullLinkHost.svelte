<!--
  Test-only host that mirrors how DeviceLinks mounts the link editor: a
  key drives the {#if}, and the strings the panel renders come from a
  snapshot taken when the editor opened.

  The host keeps the *nullable holder* the defect needs — `editing` — and
  derives both from it, so the reproducer exercises the real ordering:
  closing the editor nulls the holder while a save is still in flight.
  Rendering LinkConfigPanel directly and pushing null through rerender
  would assume the very thing under test.
-->
<script lang="ts">
  import { untrack } from "svelte";
  import type { Link } from "$lib/api/types";
  import LinkConfigPanel from "./LinkConfigPanel.svelte";

  type Props = { link: Link; locale: string };
  let { link, locale }: Props = $props();

  let editing = $state<Link | null>(untrack(() => link));
  // untrack keeps this a one-time read, as DeviceLinks' snapshot is.
  const snapshot = untrack(() => ({
    senderAddress: link.sender_address,
    receiverAddress: link.receiver_address,
    name: link.name ?? "",
    senderDeviceLabel: link.sender_device_name || link.sender_address,
    senderChannelLabel:
      link.sender_channel_type_label || link.sender_channel_type || "",
    receiverDeviceLabel: link.receiver_device_name || link.receiver_address,
    receiverChannelLabel:
      link.receiver_channel_type_label || link.receiver_channel_type || "",
  }));
</script>

{#if editing}
  <LinkConfigPanel
    senderAddress={snapshot.senderAddress}
    receiverAddress={snapshot.receiverAddress}
    name={snapshot.name}
    senderDeviceLabel={snapshot.senderDeviceLabel}
    senderChannelLabel={snapshot.senderChannelLabel}
    receiverDeviceLabel={snapshot.receiverDeviceLabel}
    receiverChannelLabel={snapshot.receiverChannelLabel}
    {locale}
    onBack={() => (editing = null)}
  />
{/if}
