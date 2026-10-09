import type { Link } from "$lib/api/types";

/** The "Profileinstellung" page of one direct link. */
export function linkHref(senderAddress: string, receiverAddress: string): string {
  return `#/links/${encodeURIComponent(senderAddress)}/${encodeURIComponent(receiverAddress)}`;
}

/**
 * The link wizard. `sender` / `receiver` pin the first partner to one
 * channel (the "add receiver" / "add sender" actions of a grouped list);
 * `device` opens the first step on one device's channels.
 */
export function newLinkHref(
  anchor: { sender?: string; receiver?: string; device?: string } = {},
): string {
  const qs = new URLSearchParams();
  if (anchor.sender) qs.set("sender", anchor.sender);
  if (anchor.receiver) qs.set("receiver", anchor.receiver);
  if (anchor.device) qs.set("device", anchor.device);
  const q = qs.toString();
  return q ? `#/links/new?${q}` : "#/links/new";
}

/** Device address of a channel address (`ADDR:3` → `ADDR`). */
export function deviceOf(channelAddress: string): string {
  const i = channelAddress.lastIndexOf(":");
  return i < 0 ? channelAddress : channelAddress.slice(0, i);
}

/**
 * How a link end is named in a list: the channel's own name, else the
 * device name with the channel kind, else whatever is known, the address
 * last.
 */
export function partyLabel(link: Link, side: "sender" | "receiver"): string {
  const channel = side === "sender" ? link.sender_channel_name : link.receiver_channel_name;
  const device = side === "sender" ? link.sender_device_name : link.receiver_device_name;
  const kind =
    side === "sender" ? link.sender_channel_type_label : link.receiver_channel_type_label;
  const address = side === "sender" ? link.sender_address : link.receiver_address;
  if (channel?.trim()) return channel.trim();
  if (device?.trim()) return kind ? `${device.trim()} · ${kind}` : device.trim();
  return kind || address;
}

/** The device model of a link end, when known. */
export function partyModel(link: Link, side: "sender" | "receiver"): string {
  return (side === "sender" ? link.sender_device_model : link.receiver_device_model) ?? "";
}
