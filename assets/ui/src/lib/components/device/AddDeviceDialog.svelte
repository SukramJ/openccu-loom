<script lang="ts">
  import { untrack } from "svelte";
  import { api } from "$lib/api/client";
  import type { InboxDevice } from "$lib/api/types";
  import Button from "$lib/components/ui/Button.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import DialogFrame from "$lib/components/ui/DialogFrame.svelte";
  import PairingControls from "./PairingControls.svelte";
  import AcceptConfigFields from "./AcceptConfigFields.svelte";
  import { buildAcceptConfig, emptyAcceptDraft, type AcceptDraft } from "./acceptConfig";
  import { acceptDevice, heldAfterAccept, releaseDevice } from "./onboarding";
  import { createRoomFunctionCatalog } from "./roomFunctionCatalog.svelte";
  import { deviceStore } from "$lib/stores/devices.svelte";
  import { installModeStore } from "$lib/stores/installMode.svelte";
  import { t } from "$lib/i18n";

  // The one place pairing starts, opened from the device list and from the
  // new-devices view. It does not depend on a CCU inbox: a system without
  // one (openccu-lite) still has an install mode, and the daemon's own hold
  // is listed by the inbox endpoint on every system type.
  //
  // While the dialog is open the operator sees:
  //  - the pairing controls for the chosen interface;
  //  - devices waiting to be accepted — every device the daemon holds back
  //    and every CCU inbox entry that appeared since the dialog opened —
  //    each with its first-time configuration (name, rooms, functions) and
  //    "accept and release" as the one action that finishes onboarding
  //    (ADR 0082). Plain "accept" builds the device and keeps it withheld,
  //    for an operator who wants to configure more before publishing; the
  //    heating group stays in the new-devices view's full dialog;
  //  - devices waiting to be released, each with its release;
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

  // Per-entry first-time configuration and the entry a request runs for.
  let drafts = $state<Record<string, AcceptDraft>>({});
  let busy = $state<string | null>(null);
  const catalog = createRoomFunctionCatalog();

  function keyOf(d: InboxDevice): string {
    return (d.central ?? "") + "/" + d.address;
  }

  // Element ids for one entry's fields; a key carries a slash.
  function idsOf(key: string) {
    const base = "add-device-" + key.replace(/[^A-Za-z0-9_-]/g, "-");
    return { name: base + "-name", rooms: base + "-rooms", functions: base + "-functions" };
  }

  async function loadInbox() {
    try {
      const list = await api.listInbox();
      inbox = list;
      if (knownInbox === null) knownInbox = new Set(list.map(keyOf));
    } catch {
      // Supplementary here: the pairing controls stay usable, and the
      // new-devices view reports its own load errors.
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
      drafts = {};
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
  // CCU inbox entries from this pairing session.
  const acceptable = $derived(
    inbox.filter(
      (d) =>
        !d.awaiting_release &&
        (d.pending_creation || inboxAppeared.some((n) => keyOf(n) === keyOf(d))),
    ),
  );
  const awaitingRelease = $derived(inbox.filter((d) => d.awaiting_release));
  const nothingJoined = $derived(
    windowEnded &&
      !installModeStore.active &&
      arrived.length === 0 &&
      inboxAppeared.length === 0,
  );

  // Every acceptable entry gets its own draft, created when it shows up.
  $effect(() => {
    const missing = acceptable.filter((d) => !(keyOf(d) in untrack(() => drafts)));
    if (missing.length === 0) return;
    untrack(() => {
      const next = { ...drafts };
      for (const d of missing) next[keyOf(d)] = emptyAcceptDraft();
      drafts = next;
    });
  });

  // A held device is built on the server; its name lives in the device
  // list, which the inbox entry does not carry.
  function nameOf(d: InboxDevice): string {
    return deviceStore.items.find((x) => x.address === d.address)?.name || d.address;
  }

  // Moves an entry between the two lists right away; the reload that
  // follows reconciles it with the daemon.
  function markAwaitingRelease(key: string) {
    inbox = inbox.map((e) =>
      keyOf(e) === key ? { ...e, pending_creation: false, awaiting_release: true } : e,
    );
  }

  function dropEntry(key: string) {
    inbox = inbox.filter((e) => keyOf(e) !== key);
  }

  async function accept(d: InboxDevice, release: boolean) {
    const key = keyOf(d);
    const draft = drafts[key] ?? emptyAcceptDraft();
    busy = key;
    try {
      const outcome = await acceptDevice(d, buildAcceptConfig(draft), { release });
      if (outcome === "failed") return;
      if (outcome === "released" || !heldAfterAccept(d)) dropEntry(key);
      else markAwaitingRelease(key);
      refreshAll();
    } finally {
      busy = null;
    }
  }

  async function release(d: InboxDevice) {
    const key = keyOf(d);
    busy = key;
    try {
      if (await releaseDevice(d, nameOf(d))) {
        dropEntry(key);
        refreshAll();
      }
    } finally {
      busy = null;
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
          {@const held = heldAfterAccept(d)}
          <li class="rounded-md border border-[var(--ha-divider-color)] p-3">
            <div class="mb-3 flex flex-wrap items-center gap-2">
              <span class="font-mono text-sm font-semibold">{d.address}</span>
              <Badge variant="muted">{d.model}</Badge>
              {#if d.central}
                <Badge variant="muted">{d.central}</Badge>
              {/if}
            </div>
            {#if drafts[key]}
              <form
                onsubmit={(e) => {
                  e.preventDefault();
                  void accept(d, held);
                }}
              >
                <AcceptConfigFields
                  bind:draft={drafts[key]}
                  central={d.central ?? ""}
                  {catalog}
                  disabled={busy === key}
                  ids={idsOf(key)}
                />
                <div class="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
                  {#if held}
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      class="w-full sm:w-auto"
                      title={t("inbox.accept_only_title")}
                      onclick={() => void accept(d, false)}
                      disabled={busy === key}
                    >
                      {t("inbox.accept_dialog.submit")}
                    </Button>
                    <Button type="submit" size="sm" class="w-full sm:w-auto" disabled={busy === key}>
                      {busy === key ? "…" : t("inbox.accept_release")}
                    </Button>
                  {:else}
                    <Button type="submit" size="sm" class="w-full sm:w-auto" disabled={busy === key}>
                      {busy === key ? "…" : t("inbox.accept_dialog.submit")}
                    </Button>
                  {/if}
                </div>
              </form>
            {/if}
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

  {#if awaitingRelease.length > 0}
    <section class="mb-4" data-testid="add-device-awaiting-release">
      <h3 class="mb-2 text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
        {t("add_device.awaiting_release", { count: awaitingRelease.length })}
      </h3>
      <ul class="flex flex-col gap-2">
        {#each awaitingRelease as d (keyOf(d))}
          {@const key = keyOf(d)}
          <li
            class="flex flex-col gap-2 rounded-md border border-[var(--ha-divider-color)] p-3 sm:flex-row sm:items-center sm:justify-between"
          >
            <span class="min-w-0">
              <span class="block truncate font-medium">{nameOf(d)}</span>
              <span class="block truncate font-mono text-xs text-[var(--ha-secondary-text-color)]">
                {d.address} · {d.model}{#if d.central} · {d.central}{/if}
              </span>
            </span>
            <span class="flex shrink-0 items-center gap-3">
              <a
                href="#/devices/{encodeURIComponent(d.address)}"
                class="text-sm font-medium text-[var(--ha-primary-color)] hover:underline"
                onclick={onClose}
              >{t("inbox.configure")}</a>
              <Button
                type="button"
                size="sm"
                onclick={() => void release(d)}
                disabled={busy === key}
              >
                {busy === key ? "…" : t("inbox.release")}
              </Button>
            </span>
          </li>
        {/each}
      </ul>
    </section>
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
