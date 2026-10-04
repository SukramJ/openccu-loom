<script lang="ts">
  import { untrack } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { InboxDevice } from "$lib/api/types";
  import Button from "$lib/components/ui/Button.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import DialogFrame from "$lib/components/ui/DialogFrame.svelte";
  import PairingControls from "./PairingControls.svelte";
  import { buildAcceptConfig } from "./acceptConfig";
  import { deviceStore } from "$lib/stores/devices.svelte";
  import { installModeStore } from "$lib/stores/installMode.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";

  // The one place pairing starts, opened from the device list and from the
  // inbox. It does not depend on a CCU inbox: a system without one
  // (openccu-lite) still has an install mode, and the daemon's own hold is
  // listed by the inbox endpoint on every system type.
  //
  // While the dialog is open the operator sees:
  //  - the pairing controls for the chosen interface;
  //  - devices waiting to be accepted — every device the daemon holds back
  //    and every CCU inbox entry that appeared since the dialog opened —
  //    each with an inline accept that takes a name, so pairing never
  //    requires leaving the dialog (rooms, functions and heating groups
  //    stay in the inbox's full accept dialog);
  //  - every device that appeared in the device list since opening,
  //    linking to its page;
  //  - once a pairing window started here has ended empty, the most common
  //    reason: the device is still paired to another central.
  type Props = {
    open: boolean;
    onClose: () => void;
  };

  let { open, onClose }: Props = $props();

  // Addresses known when the dialog opened; null until a loaded device list
  // was available to snapshot, so a list that is still loading does not
  // make every device read as new.
  let knownDevices = $state<Set<string> | null>(null);
  let inbox = $state<InboxDevice[]>([]);
  // Inbox entries at the first load after opening; an entry outside this
  // set appeared while the dialog was open.
  let knownInbox = $state<Set<string> | null>(null);
  // Window tracking for the nothing-joined hint: a window counts only when
  // it started while the dialog was open.
  let lastActive = false;
  let windowStarted = $state(false);
  let windowEnded = $state(false);

  // Inline accept state, per inbox entry key.
  let names = $state<Record<string, string>>({});
  let includeChannels = $state<Record<string, boolean>>({});
  let accepting = $state<string | null>(null);

  function keyOf(d: InboxDevice): string {
    return (d.central ?? "") + "/" + d.address;
  }

  async function loadInbox() {
    try {
      const list = await api.listInbox();
      inbox = list;
      if (knownInbox === null) knownInbox = new Set(list.map(keyOf));
    } catch {
      // Supplementary here: the pairing controls stay usable, and the inbox
      // view reports its own load errors.
    }
  }

  // The device store has no push for a newly created device (its stream
  // patches availability and reloads on a resync), so the pairing tick
  // refreshes it alongside the inbox.
  function refreshAll() {
    void deviceStore.refresh();
    void loadInbox();
  }

  $effect(() => {
    if (!open) return;
    untrack(() => {
      knownDevices = null;
      knownInbox = null;
      inbox = [];
      names = {};
      includeChannels = {};
      windowStarted = false;
      windowEnded = false;
      lastActive = installModeStore.active;
      installModeStore.ensurePoll();
      void loadInbox();
    });
    return () => installModeStore.release();
  });

  // Snapshot the device list once one has been loaded.
  $effect(() => {
    if (!open || knownDevices !== null) return;
    if (deviceStore.lastLoaded === null) return;
    knownDevices = new Set(deviceStore.items.map((d) => d.address));
  });

  $effect(() => {
    if (!open) return;
    const active = installModeStore.active;
    untrack(() => {
      if (active && !lastActive) {
        windowStarted = true;
        windowEnded = false;
      } else if (!active && lastActive && windowStarted) {
        windowEnded = true;
        // A device that joined in the last seconds of the window would
        // otherwise only show after the next manual reload.
        refreshAll();
      }
      lastActive = active;
    });
  });

  const arrived = $derived(
    knownDevices === null
      ? []
      : deviceStore.items.filter((d) => !knownDevices!.has(d.address)),
  );
  const inboxAppeared = $derived(
    knownInbox === null ? [] : inbox.filter((d) => !knownInbox!.has(keyOf(d))),
  );
  // Waiting to be accepted: the daemon's hold whenever it was created, plus
  // CCU inbox entries from this pairing session. An entry awaiting release
  // is already accepted; its remaining step lives in the inbox.
  const acceptable = $derived(
    inbox.filter(
      (d) =>
        !d.awaiting_release &&
        (d.pending_creation || inboxAppeared.some((n) => keyOf(n) === keyOf(d))),
    ),
  );
  const awaitingRelease = $derived(inbox.filter((d) => d.awaiting_release).length);
  const nothingJoined = $derived(
    windowEnded &&
      !installModeStore.active &&
      arrived.length === 0 &&
      inboxAppeared.length === 0,
  );

  async function accept(d: InboxDevice) {
    const key = keyOf(d);
    const name = (names[key] ?? "").trim();
    accepting = key;
    try {
      await api.acceptInboxDevice(
        d.address,
        d.central ?? "",
        buildAcceptConfig({ name: names[key] ?? "", includeChannels: includeChannels[key] ?? false }),
      );
      toastStore.success(t("inbox.accepted", { name: name || d.address }));
      refreshAll();
    } catch (err) {
      toastStore.error(
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err),
      );
    } finally {
      accepting = null;
    }
  }
</script>

<DialogFrame {open} title={t("add_device.title")} {onClose}>
  <p class="mb-4 text-sm" style="color: var(--ha-secondary-text-color);">
    {t("add_device.intro")}
  </p>

  <PairingControls idPrefix="add-device" onChange={refreshAll} />

  {#if acceptable.length > 0}
    <section class="mb-4" data-testid="add-device-acceptable">
      <h3 class="mb-2 text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
        {t("add_device.acceptable_title")}
      </h3>
      <ul class="flex flex-col gap-3">
        {#each acceptable as d (keyOf(d))}
          {@const key = keyOf(d)}
          <li class="rounded-md border border-[var(--ha-divider-color)] p-3">
            <div class="mb-2 flex flex-wrap items-center gap-2">
              <span class="font-mono text-sm font-semibold">{d.address}</span>
              <Badge variant="muted">{d.model}</Badge>
              {#if d.central}
                <Badge variant="muted">{d.central}</Badge>
              {/if}
            </div>
            <form
              class="flex flex-wrap items-center gap-2"
              onsubmit={(e) => {
                e.preventDefault();
                void accept(d);
              }}
            >
              <Input
                class="w-full sm:w-56"
                value={names[key] ?? ""}
                oninput={(e: Event) =>
                  (names = { ...names, [key]: (e.currentTarget as HTMLInputElement).value })}
                placeholder={t("inbox.accept_dialog.name_placeholder")}
                aria-label={t("inbox.accept_dialog.name_label")}
                disabled={accepting === key}
              />
              <label
                class="flex items-center gap-2 text-sm"
                class:opacity-50={(names[key] ?? "").trim() === ""}
              >
                <input
                  type="checkbox"
                  checked={includeChannels[key] ?? false}
                  onchange={(e) =>
                    (includeChannels = { ...includeChannels, [key]: e.currentTarget.checked })}
                  disabled={accepting === key || (names[key] ?? "").trim() === ""}
                  class="h-4 w-4 rounded border-[var(--ha-divider-color)] text-brand-600 focus:ring-brand-500"
                />
                {t("inbox.accept_dialog.include_channels")}
              </label>
              <Button type="submit" size="sm" disabled={accepting === key}>
                {accepting === key ? "…" : t("inbox.accept")}
              </Button>
            </form>
          </li>
        {/each}
      </ul>
      <p class="mt-2 text-xs text-[var(--ha-secondary-text-color)]">
        <a
          href="#/inbox"
          class="text-[var(--ha-primary-color)] hover:underline"
          onclick={onClose}
        >{t("add_device.more_options")}</a>
      </p>
    </section>
  {/if}

  {#if awaitingRelease > 0}
    <p class="mb-4 text-sm" data-testid="add-device-awaiting-release">
      {t("add_device.awaiting_release", { count: awaitingRelease })}
      ·
      <a
        href="#/inbox"
        class="font-medium text-[var(--ha-primary-color)] hover:underline"
        onclick={onClose}
      >{t("add_device.waiting_link")}</a>
    </p>
  {/if}

  <section class="mb-2">
    <h3 class="mb-2 text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
      {t("add_device.arrived_title")}
    </h3>
    {#if arrived.length === 0}
      <p class="text-sm text-[var(--ha-secondary-text-color)]">
        {t("add_device.arrived_empty")}
      </p>
    {:else}
      <ul class="flex flex-col gap-1">
        {#each arrived as d (d.address)}
          <li>
            <a
              href="#/devices/{encodeURIComponent(d.address)}"
              class="font-medium text-[var(--ha-primary-color)] hover:underline"
              onclick={onClose}
            >{d.name || d.address}</a>
            <span class="font-mono text-xs text-[var(--ha-secondary-text-color)]">
              {d.address} · {d.model_label || d.model}
            </span>
          </li>
        {/each}
      </ul>
    {/if}
  </section>

  {#if nothingJoined}
    <div
      class="mt-4 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-900 dark:border-amber-900/60 dark:bg-amber-950/40 dark:text-amber-200"
      role="note"
    >
      {t("add_device.nothing_joined")}
    </div>
  {/if}

  {#snippet footer()}
    <Button type="button" variant="outline" onclick={onClose}>
      {t("common.close")}
    </Button>
  {/snippet}
</DialogFrame>
