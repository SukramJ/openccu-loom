import { api, ApiError, friendlyError } from "$lib/api/client";
import type { Link } from "$lib/api/types";
import { confirmStore } from "$lib/stores/confirm.svelte";
import { toastStore } from "$lib/stores/toast.svelte";
import { notifyWakeupPending } from "$lib/links/wakeup-hint";
import { t } from "$lib/i18n";
import { deviceOf, partyLabel } from "./link-routes";

/**
 * Delete a direct link after the operator confirms it. Resolves true when
 * the link is gone. Removing a link rewrites configuration on both ends,
 * and a battery device applies it only on its next wakeup — that hint
 * replaces the plain "removed" toast when it applies.
 */
export async function deleteLink(link: Link): Promise<boolean> {
  const ok = await confirmStore.ask({
    title: t("common.delete"),
    body: t("links.confirm_delete", {
      sender: partyLabel(link, "sender"),
      receiver: partyLabel(link, "receiver"),
    }),
    confirmLabel: t("common.delete"),
    destructive: true,
  });
  if (!ok) return false;
  try {
    await api.removeLink(deviceOf(link.sender_address), link.sender_address, link.receiver_address);
  } catch (err) {
    const msg = err instanceof ApiError ? `${err.status}: ${err.message}` : friendlyError(err, t);
    toastStore.error(t("links.removal_failed"), msg);
    return false;
  }
  const wakeupShown = await notifyWakeupPending([link.sender_address, link.receiver_address]);
  if (!wakeupShown) toastStore.success(t("links.removed"));
  return true;
}
