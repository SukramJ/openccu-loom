// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// The two onboarding steps as the operator surfaces run them. Shared by the
// new-devices view and the add-device dialog, so both accept, release and
// report the same way for the same entry.
//
// The daemon keeps the steps apart (ADR 0082): accepting builds a held
// device, releasing publishes it to MQTT, Matter and the outbound webhook.
// "Accept and release" is the two calls in that order, never one — the
// release must stay the last step, and a failed release must leave the
// device visibly withheld rather than read as a finished onboarding.
import { api, ApiError } from "$lib/api/client";
import type { InboxDevice } from "$lib/api/types";
import { toastStore } from "$lib/stores/toast.svelte";
import { t } from "$lib/i18n";
import type { AcceptConfig } from "./acceptConfig";

export function errorText(err: unknown): string {
  if (err instanceof ApiError) return `${err.status}: ${err.message}`;
  if (err instanceof Error) return err.message;
  return String(err);
}

/**
 * Whether accepting this entry leaves the device withheld, so a release has
 * to follow. Only the daemon's own hold withholds: an entry that only a CCU
 * inbox holds (the hold switched off) is built and published by the accept
 * itself, and a release of a device nothing withholds answers not-found.
 */
export function heldAfterAccept(d: Pick<InboxDevice, "pending_creation">): boolean {
  return d.pending_creation === true;
}

/** How an accept ended. */
export type AcceptOutcome =
  // The accept failed; the entry is still waiting to be accepted.
  | "failed"
  // Accepted; a held device now waits to be released.
  | "accepted"
  // Accepted and released: the device is published.
  | "released"
  // Accepted, but the release failed: the device waits to be released.
  | "release_failed";

/**
 * Accept an entry and, when asked and the device stays held, release it.
 * Every outcome is reported through a toast; a partial success is an error
 * toast that says the device is still waiting to be released.
 *
 * `afterAccept` runs between the two calls (the new-devices view adds the
 * device to a heating group there), so it lands before the device is
 * published. It reports its own failures and never stops the release.
 */
export async function acceptDevice(
  d: Pick<InboxDevice, "address" | "central" | "pending_creation">,
  config: AcceptConfig | undefined,
  opts: { release: boolean; afterAccept?: () => Promise<void> },
): Promise<AcceptOutcome> {
  const central = d.central ?? "";
  const name = config?.name || d.address;
  try {
    await api.acceptInboxDevice(d.address, central, config);
  } catch (err) {
    toastStore.error(errorText(err));
    return "failed";
  }
  if (opts.afterAccept) await opts.afterAccept();
  if (!opts.release || !heldAfterAccept(d)) {
    toastStore.success(t("inbox.accepted", { name }));
    return "accepted";
  }
  try {
    await api.releaseDevice(d.address, central);
  } catch (err) {
    toastStore.error(t("inbox.release_failed_after_accept", { name, error: errorText(err) }));
    return "release_failed";
  }
  toastStore.success(t("inbox.accepted_released", { name }));
  return "released";
}

/** Release an entry that is waiting to be released. True on success. */
export async function releaseDevice(
  d: Pick<InboxDevice, "address" | "central">,
  name?: string,
): Promise<boolean> {
  try {
    await api.releaseDevice(d.address, d.central ?? "");
  } catch (err) {
    toastStore.error(errorText(err));
    return false;
  }
  toastStore.success(t("inbox.released", { name: name || d.address }));
  return true;
}
