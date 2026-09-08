import type { ChannelSummary } from "$lib/api/types";
import { t } from "$lib/i18n";

/**
 * Channel-table derivations: the direct-link role a channel can play, whether
 * it is one of the CCU's virtual channels, and the header line the channel
 * editor is titled with.
 *
 * The role comes from the raw CCU `LINK_SOURCE_ROLES` / `LINK_TARGET_ROLES`
 * token lists, which the REST `ChannelSummary` carries since API 11.2.0. The
 * tokens themselves are never shown — only which of the two sides is
 * non-empty, which is what an operator asks when wiring a direct link.
 */

export type ChannelRole = "sender" | "receiver" | "both" | "none";

/** Minimal shape the derivations need, so callers may pass partial rows. */
type RoleFields = Pick<ChannelSummary, "link_source_roles" | "link_target_roles">;

export function roleOf(ch: RoleFields): ChannelRole {
  const sends = (ch.link_source_roles?.length ?? 0) > 0;
  const receives = (ch.link_target_roles?.length ?? 0) > 0;
  if (sends && receives) return "both";
  if (sends) return "sender";
  if (receives) return "receiver";
  return "none";
}

export function roleLabel(role: ChannelRole): string {
  return t(`device.channel.role.${role}`);
}

/**
 * Channels numbered 50 and up are the CCU's virtual channels (the ones a
 * device exposes for internal links rather than for hardware), which the
 * channel strip has always marked separately.
 */
export function isVirtualChannel(no: number): boolean {
  return no >= 50;
}

/**
 * Week-profile channels exist purely to store a weekly program; they route to
 * the schedule sub-tab instead of opening the parameter editor. The device
 * listing filters the types that *start* with the token, so the ones that
 * reach a channel row are those that merely end with it.
 */
export function isWeekProfileChannel(type: string | undefined): boolean {
  return (type ?? "").toUpperCase().endsWith("WEEK_PROFILE");
}

/**
 * The channel editor's heading: `Türschlossantrieb (HmIP-DLP, Kanal 12)`.
 *
 * The leading half is the channel *type* label, not the operator's channel
 * name: the name is already in the table's Name column and in the rename
 * field right beside this heading, whereas what the type is stays otherwise
 * invisible once a channel has been renamed. It falls back to the raw CCU
 * type and then to the channel number, so a channel the translation table
 * does not know still gets a heading rather than an empty one.
 */
export function channelHeader(
  ch: Pick<ChannelSummary, "number" | "type" | "type_label">,
  model: string,
): string {
  const label =
    ch.type_label?.trim() ||
    ch.type?.trim() ||
    t("device.channel_n", { n: ch.number });
  const channel = t("device.channel_n", { n: ch.number });
  if (!model.trim()) return `${label} (${channel})`;
  return t("device.channel.header", { label, model, channel });
}
