<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { InboxDevice, ReplaceCandidate, GroupEntry } from "$lib/api/types";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import PageShell from "$lib/components/ui/PageShell.svelte";
  import AddDeviceDialog from "$lib/components/device/AddDeviceDialog.svelte";
  import AcceptConfigFields from "$lib/components/device/AcceptConfigFields.svelte";
  import {
    buildAcceptConfig,
    emptyAcceptDraft,
    type AcceptDraft,
  } from "$lib/components/device/acceptConfig";
  import {
    acceptDevice,
    heldAfterAccept,
    releaseDevice as releaseEntry,
  } from "$lib/components/device/onboarding";
  import { createRoomFunctionCatalog } from "$lib/components/device/roomFunctionCatalog.svelte";
  import { installModeStore } from "$lib/stores/installMode.svelte";
  import { centralStore } from "$lib/stores/centrals.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { t } from "$lib/i18n";
  import { loadLS, saveLS } from "$lib/utils";
  import { prefs } from "$lib/stores/preferences.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";

  // New devices: every newly paired device the daemon holds back, in its
  // two phases (waiting to be accepted, waiting to be released), plus the
  // entries a CCU's own inbox holds. The hold exists on every system type,
  // so the view does not depend on the CCU inbox feature (ADR 0082); on a
  // CCU its inbox only feeds the list.

  let entries = $state<InboxDevice[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let centralFilter = $state(loadLS("inbox:central"));
  $effect(() => saveLS("inbox:central", centralFilter));
  let accepting = $state<string | null>(null);
  let releasing = $state<string | null>(null);

  // Pairing starts in the add-device dialog, the one place that hosts the
  // pairing controls; the inbox opens it and reloads its list on close.
  let addDeviceOpen = $state(false);

  // Active-pairing tick: while the install mode is running on the CCU,
  // the inbox should reflect freshly-discovered candidates without the
  // user having to hit reload. 3 s is fast enough that the operator
  // sees the device shortly after pressing the physical pairing
  // button and slow enough to keep CCU pressure low.
  let pairingTimer: ReturnType<typeof setInterval> | null = null;

  $effect(() => {
    if (installModeStore.active) {
      if (!pairingTimer) {
        pairingTimer = setInterval(() => void load({ silent: true }), 3000);
      }
    } else if (pairingTimer) {
      clearInterval(pairingTimer);
      pairingTimer = null;
    }
  });

  // The pairing tick must not blank the table: `loading` swaps the whole
  // result for the loading placeholder, so a poll that raises it destroys
  // and recreates the table — including the search input, which loses focus
  // mid-keystroke — roughly twenty times per teach-in window, precisely
  // while the operator is watching for the device they just paired.
  async function load(opts: { silent?: boolean } = {}) {
    if (!opts.silent) loading = true;
    loadError = null;
    try {
      entries = await api.listInbox();
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  // Accept dialog — first-time configuration (name, rooms, functions)
  // applied with the accept. A null target means the dialog is closed;
  // leaving every field empty and confirming performs a plain accept.
  let acceptTarget = $state<InboxDevice | null>(null);
  let acceptDraft = $state<AcceptDraft>(emptyAcceptDraft());
  let acceptSubmitting = $state(false);
  // GR05: optionally assign the accepted device to a heating group.
  let acceptGroups = $state<GroupEntry[]>([]);
  let acceptGroupId = $state<number | "">("");

  // Replace dialog — swap a paired device for the new (inbox) one.
  // Mirrors the CCU WebUI: the action lives on the new device's row and
  // is offered only for BidCos interfaces (HmIP cannot be replaced).
  let replaceTarget = $state<{ address: string; central: string } | null>(
    null,
  );
  let replaceCandidates = $state<ReplaceCandidate[]>([]);
  let replaceLoading = $state(false);
  let replaceLoadError = $state<string | null>(null);
  let replaceSubmitting = $state(false);

  // Focus-trap bookkeeping for the two hand-rolled dialogs, mirroring
  // ConfirmDialog.svelte. Without it focus stays on the row button that
  // opened the overlay: assistive technology never enters the aria-modal
  // dialog, a keyboard user has to tab through the table behind it, and the
  // overlay's own Escape handler is unreachable because the keydown never
  // travels through the overlay's subtree. Only one of the two can be open
  // at a time, so a single window handler dispatches to whichever it is.
  let acceptDialogEl = $state<HTMLDivElement | null>(null);
  let replaceDialogEl = $state<HTMLDivElement | null>(null);
  let dialogOpener: HTMLElement | null = null;

  function dialogFocusables(el: HTMLDivElement | null): HTMLElement[] {
    if (!el) return [];
    return Array.from(
      el.querySelectorAll<HTMLElement>(
        'input, button, select, textarea, [tabindex]:not([tabindex="-1"])',
      ),
    ).filter((e) => !e.hasAttribute("disabled"));
  }

  $effect(() => {
    if (!acceptTarget && !replaceTarget) return;
    dialogOpener = document.activeElement as HTMLElement | null;
    // The dialog's DOM is inserted by the {#if} this effect depends on;
    // queue past the current microtask so Svelte has committed it.
    queueMicrotask(() =>
      dialogFocusables(acceptTarget ? acceptDialogEl : replaceDialogEl)[0]?.focus(),
    );
    return () => {
      dialogOpener?.focus();
      dialogOpener = null;
    };
  });

  function onDialogKey(e: KeyboardEvent) {
    // The shared confirm dialog opens on top of the replace dialog, which
    // stays mounted underneath it. While it is pending the keyboard
    // belongs to it alone: trapping here as well would pull every Tab
    // back into the backgrounded dialog — leaving Confirm and Cancel
    // unreachable — and Escape would dismiss both layers at once.
    if (confirmStore.pending) return;
    const busy = acceptTarget ? acceptSubmitting : replaceSubmitting;
    const el = acceptTarget
      ? acceptDialogEl
      : replaceTarget
        ? replaceDialogEl
        : null;
    if (!el) return;
    if (e.key === "Escape") {
      if (busy) return;
      e.preventDefault();
      if (acceptTarget) closeAccept();
      else closeReplace();
      return;
    }
    if (e.key === "Tab") {
      const els = dialogFocusables(el);
      if (els.length === 0) return;
      const first = els[0];
      const last = els[els.length - 1];
      const active = document.activeElement;
      const atEdge = e.shiftKey ? active === first : active === last;
      const outside = !els.includes(active as HTMLElement);
      if (atEdge || outside) {
        e.preventDefault();
        (e.shiftKey ? last : first).focus();
      }
    }
  }

  function isReplaceable(d: InboxDevice): boolean {
    // Replace swaps a device that is WAITING to be taken into service for
    // an existing one. An entry awaiting release has already been taken
    // into service — it is materialised and configured — so the action
    // does not apply and offering it would only confuse the last step.
    if (d.awaiting_release) return false;
    // The CCU exposes replaceDevice on BidCos only; HmIP throws
    // NotImplementedException, so hide the action there (the server
    // still enforces it). An unknown interface stays hidden.
    return d.interface === "BidCos-RF" || d.interface === "BidCos-Wired";
  }

  async function openReplace(address: string, central: string) {
    replaceTarget = { address, central };
    replaceCandidates = [];
    replaceLoadError = null;
    replaceLoading = true;
    try {
      replaceCandidates = await api.listReplaceCandidates(
        address,
        central || undefined,
      );
    } catch (err) {
      replaceLoadError =
        err instanceof ApiError ? `${err.status}: ${err.message}` : String(err);
    } finally {
      replaceLoading = false;
    }
  }

  function closeReplace() {
    replaceTarget = null;
  }

  async function confirmReplace(candidate: ReplaceCandidate) {
    if (!replaceTarget) return;
    const target = replaceTarget;
    const ok = await confirmStore.ask({
      title: t("inbox.replace.confirm_title"),
      body: t("inbox.replace.confirm_text", {
        old: candidate.name || candidate.address,
        new: target.address,
      }),
      confirmLabel: t("inbox.replace.confirm_label"),
      destructive: true,
    });
    if (!ok) return;
    replaceSubmitting = true;
    try {
      await api.replaceDevice(
        target.address,
        candidate.address,
        target.central || undefined,
      );
      toastStore.success(t("inbox.replace.success"));
      closeReplace();
      await load();
      installModeStore.refresh();
    } catch (err) {
      toastStore.error(
        err instanceof ApiError ? `${err.status}: ${err.message}` : String(err),
      );
    } finally {
      replaceSubmitting = false;
    }
  }

  // Room / function catalogues for the multi-selects, shared with the
  // add-device dialog's accept.
  const catalog = createRoomFunctionCatalog();

  function openAccept(d: InboxDevice) {
    acceptTarget = d;
    acceptDraft = emptyAcceptDraft();
    acceptGroups = [];
    acceptGroupId = "";
    void loadAcceptGroups(d.central ?? "");
  }

  // GR05: load the target central's heating groups so the accept dialog can
  // offer one. A failure leaves the picker empty (assignment stays optional).
  async function loadAcceptGroups(central: string) {
    try {
      const entries = await api.getGroups(central);
      acceptGroups = entries.flatMap((e) => e.groups);
    } catch {
      acceptGroups = [];
    }
  }

  // Add the just-accepted device to the chosen group: find the device's
  // channels that are assignable to the group's type and extend the roster.
  async function assignAcceptedToGroup(deviceAddress: string, central: string) {
    if (acceptGroupId === "") return;
    const g = acceptGroups.find((x) => x.id === acceptGroupId);
    if (!g) return;
    const suitable = await api.groupSuitableMembers(g.type_id, central);
    const channels = suitable.assignable
      .map((m) => m.address)
      .filter((a) => a.startsWith(deviceAddress + ":"));
    if (channels.length === 0) {
      toastStore.error(t("inbox.group_assign.no_channel"));
      return;
    }
    const members = [
      ...new Set([...(g.members ?? []).map((m) => m.address), ...channels]),
    ];
    await api.updateGroup(
      g.id,
      {
        name: g.name,
        forbid_single_operation: g.forbid_single_operation ?? false,
        members,
      },
      central,
    );
    toastStore.success(t("inbox.group_assign.done", { group: g.name }));
  }

  function closeAccept() {
    acceptTarget = null;
  }

  // Accept, optionally followed by the release. The heating-group
  // assignment runs between the two, so a released device is published
  // with its group already set.
  async function confirmAccept(release: boolean) {
    if (!acceptTarget) return;
    const target = acceptTarget;
    const address = target.address;
    const central = target.central ?? "";
    accepting = address;
    acceptSubmitting = true;
    try {
      const outcome = await acceptDevice(target, buildAcceptConfig(acceptDraft), {
        release,
        afterAccept: async () => {
          // Best-effort: the device is already accepted, so a
          // group-assign failure only warns.
          if (acceptGroupId === "") return;
          try {
            await assignAcceptedToGroup(address, central);
          } catch (err) {
            toastStore.error(
              err instanceof ApiError
                ? `${err.status}: ${err.message}`
                : t("inbox.group_assign.failed"),
            );
          }
        },
      });
      if (outcome !== "failed") {
        acceptTarget = null;
        await load();
      }
    } finally {
      acceptSubmitting = false;
      accepting = null;
    }
  }

  // The wizard's last step. An entry flagged awaiting_release is already
  // accepted and materialised — it has been named and placed by now — and
  // only this call publishes it to Home Assistant, Matter and any
  // webhook.
  async function releaseDevice(d: InboxDevice) {
    releasing = d.address;
    try {
      if (await releaseEntry(d)) await load();
    } finally {
      releasing = null;
    }
  }

  onMount(() => {
    void load();
    installModeStore.ensurePoll();
  });

  onDestroy(() => {
    if (pairingTimer) {
      clearInterval(pairingTimer);
      pairingTimer = null;
    }
    installModeStore.release();
  });

  const centrals = $derived.by(() => {
    const set = new Set<string>();
    for (const d of entries) if (d.central) set.add(d.central);
    return Array.from(set).sort((a, b) =>
      a.localeCompare(b, undefined, { sensitivity: "base" }),
    );
  });

  const visibleEntries = $derived(
    centralFilter ? entries.filter((d) => d.central === centralFilter) : entries,
  );

  function formatTs(secs: number | undefined): string {
    if (!secs) return "";
    try {
      return new Date(secs * 1000).toLocaleString(
        prefs.locale === "de" ? "de-DE" : "en-US",
      );
    } catch {
      return String(secs);
    }
  }

  const columns: DataColumn<InboxDevice>[] = $derived([
    {
      key: "address",
      label: t("inbox.col.address"),
      sortable: true,
      title: true,
      get: (d) => d.address,
    },
    {
      key: "model",
      label: t("inbox.col.model"),
      sortable: true,
      get: (d) => d.model,
    },
    {
      key: "serial",
      label: t("inbox.col.serial"),
      sortable: true,
      get: (d) => d.serial ?? "",
    },
    {
      key: "first_seen",
      label: t("inbox.col.first_seen"),
      sortable: true,
      get: (d) => d.first_seen ?? 0,
    },
    {
      key: "actions",
      label: t("inbox.col.actions"),
      align: "right",
      cellClass: "reflow-actions",
    },
  ]);
</script>

<svelte:window onkeydown={onDialogKey} />

{#snippet inboxList()}
  {#if loadError}
    <ErrorState message={loadError} onRetry={load} class="mb-4" />
  {/if}

  {#if loading}
    <LoadingState />
  {:else}
    <Card class="p-4">
      <DataTable
        rows={visibleEntries}
        {columns}
        rowKey={(d) => (d.central ?? "") + "/" + d.address}
        search
        searchPlaceholder={t("common.search")}
        persistKey="inbox"
        initialSort={{ key: "first_seen", asc: false }}
        emptyMessage={t("inbox.empty")}
        emptyIcon="mdi:server"
      >
        {#snippet cell(d, col)}
          {#if col.key === "address"}
            <span class="font-mono font-semibold">{d.address}</span>
            {#if centrals.length > 1 && d.central}
              <Badge variant="muted">{d.central}</Badge>
            {/if}
            {#if d.awaiting_release}
              <!-- Already accepted and materialised: it can be renamed and
                   placed right now, and only the release publishes it to
                   Home Assistant, Matter and any webhook. -->
              <Badge variant="success" title={t("inbox.awaiting_release_hint")}>
                {t("inbox.awaiting_release_badge")}
              </Badge>
            {:else if d.pending_creation}
              <!-- The daemon parked this device (delay_new_device_creation):
                   it has no data points here until it is accepted. -->
              <Badge variant="warning" title={t("inbox.pending_creation_hint")}>
                {t("inbox.pending_creation_badge")}
              </Badge>
            {/if}
          {:else if col.key === "model"}
            <Badge variant="muted">{d.model}</Badge>
            {#if d.manufacturer}
              <span class="block text-xs text-slate-500 dark:text-slate-400">{d.manufacturer}</span>
            {/if}
          {:else if col.key === "serial"}
            {#if d.serial}
              <span class="font-mono text-xs">{d.serial}</span>
            {:else}
              <span class="text-slate-400 dark:text-slate-500">—</span>
            {/if}
          {:else if col.key === "first_seen"}
            {#if d.first_seen}
              <span class="text-xs text-slate-500 dark:text-slate-400">{formatTs(d.first_seen)}</span>
            {:else}
              <span class="text-slate-400 dark:text-slate-500">—</span>
            {/if}
          {:else if col.key === "actions"}
            {#if d.awaiting_release}
              <!-- Offering "accept" here would ask the operator to accept a
                   device that is already accepted. The remaining step is
                   publishing it. -->
              <Button
                type="button"
                size="sm"
                onclick={() => void releaseDevice(d)}
                disabled={releasing === d.address}
              >
                {releasing === d.address ? "…" : t("inbox.release")}
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onclick={() => (location.hash = `#/devices/${encodeURIComponent(d.address)}`)}
              >
                {t("inbox.configure")}
              </Button>
            {:else}
              <Button
                type="button"
                size="sm"
                onclick={() => openAccept(d)}
                disabled={accepting === d.address}
              >
                {accepting === d.address ? "…" : t("inbox.accept")}
              </Button>
            {/if}
            {#if isReplaceable(d)}
              <Button
                type="button"
                variant="outline"
                size="sm"
                onclick={() => void openReplace(d.address, d.central ?? "")}
              >
                {t("inbox.replace.button")}
              </Button>
            {/if}
          {/if}
        {/snippet}
      </DataTable>
    </Card>
  {/if}
{/snippet}

<PageShell>
  <PageHeader title={t("inbox.title")} subtitle={t("inbox.subtitle")}>
    {#snippet actions()}
      {#if centrals.length > 1}
        <Select
          class="w-auto"
          bind:value={centralFilter}
          options={[
            { value: "", label: t("common.all_ccus") },
            ...centrals.map((c) => ({ value: c, label: c })),
          ]}
        />
      {/if}
      <Button type="button" variant="outline" onclick={() => void load()} disabled={loading}>
        {t("common.reload")}
      </Button>
      {#if centralStore.featureAvailable("install_mode")}
        <Button
          type="button"
          onclick={() => (addDeviceOpen = true)}
          title={t("devicelist.add_device_title")}
        >
          {t("devicelist.add_device")}
        </Button>
      {/if}
    {/snippet}
  </PageHeader>

  {#if installModeStore.active}
    <div class="mb-4 flex items-center gap-2 rounded border border-brand-300 bg-brand-50 p-3 text-sm text-brand-900 dark:border-brand-800 dark:bg-brand-950 dark:text-brand-200">
      <Badge variant="default">{t("inbox.install_mode_badge")}</Badge>
      <span>
        {t("inbox.install_mode_running")}
        {#if installModeStore.remainingSeconds !== null}
          · {installModeStore.remainingSeconds}&nbsp;{t("inbox.install_mode_seconds_left")}
        {/if}
      </span>
    </div>
  {/if}

  <!-- Rendered on every system: the daemon's hold exists without a CCU
       inbox, and an empty list is the shared empty state, not a feature
       gate's explanation. -->
  {@render inboxList()}
</PageShell>

<AddDeviceDialog
  open={addDeviceOpen}
  onClose={() => {
    addDeviceOpen = false;
    void load({ silent: true });
  }}
/>

{#if acceptTarget}
  <!-- Accept dialog: optional first-time configuration before the device
       joins the running registry. Confirming with everything blank is a
       plain accept. -->
  <div
    class="modal-safe-pad fixed inset-0 z-50 flex items-center justify-center"
    style="background-color: rgb(0 0 0 / 0.45);"
    role="dialog"
    aria-modal="true"
    aria-label={t("inbox.accept_dialog.title")}
    tabindex="-1"
    onclick={(e) => {
      if (e.target === e.currentTarget && !acceptSubmitting) closeAccept();
    }}
    onkeydown={(e) => {
      if (e.key === "Escape" && !acceptSubmitting) closeAccept();
    }}
  >
    <div
      bind:this={acceptDialogEl}
      class="max-h-[90vh] w-full max-w-lg overflow-y-auto p-5"
      style="background-color: var(--ha-card-background-color); color: var(--ha-primary-text-color); border-radius: var(--ha-radius-card); box-shadow: var(--ha-elevation-modal);"
    >
      <h2 class="mb-1 text-lg font-semibold">{t("inbox.accept_dialog.title")}</h2>
      <p class="mb-4 text-sm" style="color: var(--ha-secondary-text-color);">
        {t("inbox.accept_dialog.subtitle", { address: acceptTarget.address })}
      </p>

      <form
        onsubmit={(e) => {
          e.preventDefault();
          void confirmAccept(heldAfterAccept(acceptTarget!));
        }}
      >
        <AcceptConfigFields
          bind:draft={acceptDraft}
          central={acceptTarget.central ?? ""}
          {catalog}
          disabled={acceptSubmitting}
          ids={{ name: "accept-name", rooms: "inbox-rooms", functions: "inbox-functions" }}
        />

        {#if acceptGroups.length > 0}
          <div class="mb-5">
            <span class="mb-1 block text-sm font-medium">
              {t("inbox.accept_dialog.group_label")}
            </span>
            <Select
              class="w-full"
              disabled={acceptSubmitting}
              value={acceptGroupId === "" ? "" : String(acceptGroupId)}
              onValueChange={(v) => {
                acceptGroupId = v === "" ? "" : Number(v);
              }}
              ariaLabel={t("inbox.accept_dialog.group_label")}
              options={[
                { value: "", label: t("inbox.accept_dialog.group_none") },
                ...acceptGroups.map((g) => ({ value: String(g.id), label: g.name })),
              ]}
            />
            <p class="mt-1 text-xs text-[var(--ha-secondary-text-color)]">
              {t("inbox.accept_dialog.group_hint")}
            </p>
          </div>
        {/if}

        <div class="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
          <Button
            type="button"
            variant="outline"
            class="w-full sm:w-auto"
            onclick={closeAccept}
            disabled={acceptSubmitting}
          >
            {t("common.cancel")}
          </Button>
          {#if heldAfterAccept(acceptTarget)}
            <!-- Held by the daemon: accepting builds it and keeps it
                 withheld; the primary action also publishes it. -->
            <Button
              type="button"
              variant="outline"
              class="w-full sm:w-auto"
              title={t("inbox.accept_only_title")}
              onclick={() => void confirmAccept(false)}
              disabled={acceptSubmitting}
            >
              {t("inbox.accept_dialog.submit")}
            </Button>
            <Button type="submit" class="w-full sm:w-auto" disabled={acceptSubmitting}>
              {acceptSubmitting ? "…" : t("inbox.accept_release")}
            </Button>
          {:else}
            <Button type="submit" class="w-full sm:w-auto" disabled={acceptSubmitting}>
              {acceptSubmitting ? "…" : t("inbox.accept_dialog.submit")}
            </Button>
          {/if}
        </div>
      </form>
    </div>
  </div>
{/if}

{#if replaceTarget}
  <!-- Replace dialog: pick the paired device the new device replaces.
       The CCU migrates links / teams / ReGa references; the old device
       is unpaired. -->
  <div
    class="modal-safe-pad fixed inset-0 z-50 flex items-center justify-center"
    style="background-color: rgb(0 0 0 / 0.45);"
    role="dialog"
    aria-modal="true"
    aria-label={t("inbox.replace.title")}
    tabindex="-1"
    onclick={(e) => {
      if (e.target === e.currentTarget && !replaceSubmitting) closeReplace();
    }}
    onkeydown={(e) => {
      if (e.key === "Escape" && !replaceSubmitting) closeReplace();
    }}
  >
    <div
      bind:this={replaceDialogEl}
      class="max-h-[90vh] w-full max-w-lg overflow-y-auto p-5"
      style="background-color: var(--ha-card-background-color); color: var(--ha-primary-text-color); border-radius: var(--ha-radius-card); box-shadow: var(--ha-elevation-modal);"
    >
      <h2 class="mb-1 text-lg font-semibold">{t("inbox.replace.title")}</h2>
      <p class="mb-4 text-sm" style="color: var(--ha-secondary-text-color);">
        {t("inbox.replace.intro", { address: replaceTarget.address })}
      </p>

      {#if replaceLoading}
        <LoadingState />
      {:else if replaceLoadError}
        <ErrorState
          message={replaceLoadError}
          onRetry={() =>
            void openReplace(replaceTarget!.address, replaceTarget!.central)}
        />
      {:else if replaceCandidates.length === 0}
        <EmptyState
          message={t("inbox.replace.empty")}
          description={t("inbox.replace.empty_description")}
        />
      {:else}
        <ul class="flex flex-col gap-2">
          {#each replaceCandidates as candidate (candidate.address)}
            <li>
              <button
                type="button"
                class="flex w-full items-center justify-between gap-3 rounded-md border border-[var(--ha-divider-color)] p-3 text-left transition hover:bg-[var(--ha-secondary-background-color)] disabled:opacity-50"
                disabled={replaceSubmitting}
                onclick={() => void confirmReplace(candidate)}
              >
                <span class="min-w-0">
                  <span class="block truncate font-medium">
                    {candidate.name || candidate.address}
                  </span>
                  <span
                    class="block truncate text-xs"
                    style="color: var(--ha-secondary-text-color);"
                  >
                    <span class="font-mono">{candidate.address}</span>
                    {#if candidate.model}· {candidate.model}{/if}
                  </span>
                </span>
                <Badge variant={candidate.model_matches ? "success" : "muted"}>
                  {candidate.model_matches
                    ? t("inbox.replace.same_type")
                    : t("inbox.replace.compatible_type")}
                </Badge>
              </button>
            </li>
          {/each}
        </ul>
      {/if}

      <div class="mt-4 flex justify-end">
        <Button
          type="button"
          variant="outline"
          onclick={closeReplace}
          disabled={replaceSubmitting}
        >
          {t("common.cancel")}
        </Button>
      </div>
    </div>
  </div>
{/if}
