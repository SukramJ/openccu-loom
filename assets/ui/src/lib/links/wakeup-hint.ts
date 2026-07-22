// Post-link "pending wakeup" hint.
//
// The CCU only transfers a direct link or its LINK/MASTER paramset to a
// battery-powered device when that device next wakes up (a button press, a
// cyclic wake interval). Mains devices apply the change immediately. After a
// successful add-link / remove-link / link-paramset write the SPA therefore
// checks whether any affected device advertises a WAKEUP / LAZY_CONFIG rx
// mode and, if so, replaces the plain success toast with an info toast that
// reminds the operator the change is queued until the device wakes up.
//
// Mirrors the CCU WebUI's config/ic_ifacecmd.cgi `cmd_ShowConfigPendingMsg`,
// which pops a "configuration data ready for transmission" dialog for the
// sender and receiver after an add-link.
import type { RxMode } from "$lib/api/types";
import { api } from "$lib/api/client";
import { toastStore } from "$lib/stores/toast.svelte";
import { t } from "$lib/i18n";

// How long the pending-wakeup info toast stays on screen. Longer than the
// default 4 s info lifetime so the operator has time to read the two-line
// hint before it auto-dismisses.
const WAKEUP_TOAST_TTL_MS = 9000;

/**
 * hasWakeupRxMode reports whether a device rx_mode marks a battery device
 * that only applies pending configuration on its next wakeup — i.e. the
 * WAKEUP or LAZY_CONFIG bit is set. Undefined / null rx_mode (the CCU
 * reported none, or the device is mains-powered) yields false.
 */
export function hasWakeupRxMode(rxMode: RxMode | undefined | null): boolean {
  return !!rxMode && (rxMode.wakeup === true || rxMode.lazy_config === true);
}

/**
 * anyDeviceNeedsWakeup fetches each distinct device (channel suffixes are
 * stripped and duplicates collapsed) and reports whether any advertises a
 * WAKEUP / LAZY_CONFIG rx mode. A fetch failure for a device is treated as
 * "no wakeup" so the caller still shows its normal success feedback rather
 * than swallowing the result.
 */
export async function anyDeviceNeedsWakeup(
  addresses: readonly string[],
): Promise<boolean> {
  const deviceAddresses = [
    ...new Set(addresses.map((a) => a.split(":")[0]).filter(Boolean)),
  ];
  const flags = await Promise.all(
    deviceAddresses.map(async (addr) => {
      try {
        const dev = await api.getDevice(addr);
        return hasWakeupRxMode(dev.rx_mode);
      } catch {
        return false;
      }
    }),
  );
  return flags.some(Boolean);
}

/**
 * notifyWakeupPending checks the given device/channel addresses and, when at
 * least one is a WAKEUP / LAZY_CONFIG battery device, surfaces an info toast
 * reminding the operator the change transfers only on the next wakeup.
 * Returns true when the hint was shown, so the caller can suppress its plain
 * success toast (the hint conveys success too).
 */
export async function notifyWakeupPending(
  addresses: readonly string[],
): Promise<boolean> {
  const needsWakeup = await anyDeviceNeedsWakeup(addresses);
  if (needsWakeup) {
    toastStore.push(
      "info",
      t("links.wakeup_pending.title"),
      t("links.wakeup_pending.body"),
      WAKEUP_TOAST_TTL_MS,
    );
  }
  return needsWakeup;
}
