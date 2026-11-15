import type { CentralProbeResult, CentralRow } from "$lib/api/client";

/**
 * The pure half of moving a stored CCU central to the openccu-lite box that
 * now runs on the same machine: which warnings offer the move, the update
 * body that switches the central over, and what the switch leaves behind.
 * The dialog that drives the pairing renders these.
 */

/** Why the automatic move did not happen, as the warning names it. */
export type LiteMoveReason = "no_token" | "ambiguous" | "failed";

const WARNING_PREFIX = "lite-move:";
const MESSAGE_PREFIX = "warnings.lite_move_pending.";
const REASONS: readonly LiteMoveReason[] = ["no_token", "ambiguous", "failed"];

/** Whether a warning id is a pending lite move. */
export function isLiteMoveWarning(id: string): boolean {
  return id.startsWith(WARNING_PREFIX);
}

/**
 * The reason a pending-move warning carries in its message key; null for any
 * other key, and for a reason this client does not know, so an unknown one
 * is never offered a dialog written for another.
 */
export function liteMoveReason(messageKey: string): LiteMoveReason | null {
  if (!messageKey.startsWith(MESSAGE_PREFIX)) return null;
  const reason = messageKey.slice(MESSAGE_PREFIX.length);
  return (REASONS as readonly string[]).includes(reason) ? (reason as LiteMoveReason) : null;
}

/**
 * The box's own web UI, where its administrator approves the pairing. The
 * onboarder only records centrals whose host is this machine, so the page
 * the operator is on already names the box's address.
 */
export function boxLink(loc: { protocol: string; hostname: string }): string {
  return `${loc.protocol}//${loc.hostname}/`;
}

/** Whether a probe found a box that is up and can be paired with. */
export function probeAllowsMove(probe: CentralProbeResult): boolean {
  return probe.system_type === "openccu-lite" && probe.ready && probe.lite?.pairing_available === true;
}

/**
 * The interfaces the box offers, as the probe names them; [] when it names
 * none.
 */
export function offeredInterfaces(probe: CentralProbeResult): string[] {
  return probe.lite?.interfaces ?? [];
}

/**
 * The stored interfaces the box does not offer — typically CUxD, which an
 * openccu-lite box does not run. Their devices leave with the move.
 */
export function lostInterfaces(row: CentralRow, offered: string[]): string[] {
  const keep = new Set(offered);
  return row.interfaces.map((i) => i.name).filter((name) => !keep.has(name));
}

/**
 * The update body that switches a stored central to openccu-lite with an
 * approved pairing. Every field the move changes is named explicitly: the
 * update keeps the stored value of an omitted key, and leaving the system
 * type clears only the account and the RPC ports — the JSON-RPC port, the
 * TLS settings and the primary interface would otherwise survive the move.
 * Visibility and behaviour are omitted on purpose, so they carry over.
 */
export function liteMoveBody(row: CentralRow, probe: CentralProbeResult, pairingId: string): CentralRow {
  const offered = offeredInterfaces(probe);
  const primary = row.primary_interface ?? "";
  return {
    name: row.name,
    host: row.host,
    enabled: row.enabled,
    system_type: "openccu-lite",
    pairing_id: pairingId,
    json_rpc_port: 0,
    tls: false,
    tls_insecure_skip_verify: false,
    primary_interface: offered.includes(primary) ? primary : "",
    interfaces: offered.map((name) => ({ name })),
  };
}
