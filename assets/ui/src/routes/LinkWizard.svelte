<script lang="ts">
  // "Neue Verknüpfung anlegen – Schritt n/3", after the CCU WebUI's link
  // wizard (config/ic_selchannel.cgi): step 1 picks the first link
  // partner, step 2 a compatible second one, step 3 names the link. As in
  // the CCU the partners are role-agnostic — the first may be a sender or
  // a receiver, and step 2 offers the channels that fit the other end.
  //
  // Step 1 lists devices that expand to their linkable channels (the CCU
  // lists channels directly; the daemon has no fleet-wide channel list, so
  // a device's channels are fetched when it is opened). Step 2 asks the
  // daemon for the eligible partners of the chosen channel. Room and
  // function come from the device list — the partner list itself does not
  // carry them.
  import { api, friendlyError } from "$lib/api/client";
  import type { ChannelSummary, DeviceDetail, DeviceSummary, LinkableChannel } from "$lib/api/types";
  import Breadcrumb from "$lib/components/ui/Breadcrumb.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Label from "$lib/components/ui/Label.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import PageShell from "$lib/components/ui/PageShell.svelte";
  import { roleOf, isVirtualChannel } from "$lib/channel/channel-roles";
  import { deviceOf, linkHref } from "$lib/links/link-routes";
  import { notifyWakeupPending } from "$lib/links/wakeup-hint";
  import { deviceStore } from "$lib/stores/devices.svelte";
  import { surfacesStore } from "$lib/stores/surfaces.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";

  type Props = { query: string; locale: string };
  let { query, locale }: Props = $props();

  type Role = "sender" | "receiver";
  type Partner = {
    address: string;
    device: string;
    channel: number;
    role: Role;
    label: string;
    model: string;
    kind: string;
    interfaceId: string;
  };

  const params = $derived(new URLSearchParams(query));
  const anchorSender = $derived(params.get("sender") ?? "");
  const anchorReceiver = $derived(params.get("receiver") ?? "");
  let deviceScope = $state("");
  $effect(() => {
    deviceScope = params.get("device") ?? "";
  });

  const editable = $derived(surfacesStore.opensVisible("nav.links"));

  let step = $state<1 | 2 | 3>(1);
  let first = $state<Partner | null>(null);
  let second = $state<Partner | null>(null);
  let error = $state<string | null>(null);

  // --- device data -------------------------------------------------
  $effect(() => {
    if (deviceStore.items.length === 0 && !deviceStore.loading) void deviceStore.refresh();
  });
  const devicesByAddress = $derived(new Map(deviceStore.items.map((d) => [d.address, d])));

  // Details (channels with link roles), fetched once per device. A plain
  // promise cache, not state: the template awaits it, and handing the
  // same promise back on every render keeps {#await} from restarting.
  const details = new Map<string, Promise<DeviceDetail>>();
  function detailOf(address: string): Promise<DeviceDetail> {
    let p = details.get(address);
    if (!p) {
      p = api.getDevice(address);
      details.set(address, p);
      // A failed fetch may be retried by opening the device again.
      p.catch(() => details.delete(address));
    }
    return p;
  }

  function channelLabel(device: DeviceSummary | DeviceDetail, ch: ChannelSummary): string {
    if (ch.name?.trim()) return ch.name.trim();
    const kind = ch.type_label || ch.type || "";
    return kind ? `${device.name} · ${kind}` : device.name;
  }

  function partnerOf(device: DeviceDetail, ch: ChannelSummary, role: Role): Partner {
    return {
      address: ch.address,
      device: device.address,
      channel: ch.number,
      role,
      label: channelLabel(device, ch),
      model: device.model,
      kind: ch.type_label || ch.type || "",
      interfaceId: device.interface_id,
    };
  }

  // Channels a link can start or end at. The maintenance channel never
  // carries a LINK paramset; virtual channels stay hidden unless asked
  // for, as the CCU's "Virtuelle Kanäle anzeigen" toggle does.
  let showVirtual = $state(false);
  function linkableChannelsOf(d: DeviceDetail): ChannelSummary[] {
    return d.channels.filter(
      (c) =>
        c.number !== 0 &&
        roleOf(c) !== "none" &&
        (showVirtual || !isVirtualChannel(c.number)),
    );
  }

  // --- anchors: "add receiver" / "add sender" on a grouped list -----
  let anchorLoading = $state(false);
  $effect(() => {
    const addr = anchorSender || anchorReceiver;
    if (!addr) return;
    const role: Role = anchorSender ? "sender" : "receiver";
    anchorLoading = true;
    void (async () => {
      try {
        const d = await detailOf(deviceOf(addr));
        const ch = d.channels.find((c) => c.address === addr);
        if (!ch) throw new Error(addr);
        choose(partnerOf(d, ch, role));
      } catch (err) {
        error = friendlyError(err, t);
      } finally {
        anchorLoading = false;
      }
    })();
  });

  function choose(p: Partner) {
    first = p;
    second = null;
    step = 2;
  }

  // --- step 2: compatible partners ----------------------------------
  let candidates = $state<LinkableChannel[]>([]);
  let candidatesLoading = $state(false);
  $effect(() => {
    const f = first;
    if (step !== 2 || !f) return;
    candidatesLoading = true;
    error = null;
    let cancelled = false;
    void (async () => {
      try {
        const list = await api.linkableChannels(f.device, f.channel, f.role, f.interfaceId, locale);
        if (!cancelled) candidates = list;
      } catch (err) {
        if (!cancelled) error = friendlyError(err, t);
      } finally {
        if (!cancelled) candidatesLoading = false;
      }
    })();
    return () => {
      cancelled = true;
    };
  });

  const otherRole = $derived<Role>(first?.role === "sender" ? "receiver" : "sender");

  function pickCandidate(c: LinkableChannel) {
    if (!first) return;
    second = {
      address: c.address,
      device: c.device_address,
      channel: Number(c.address.slice(c.address.lastIndexOf(":") + 1)),
      role: otherRole,
      label: c.channel_name?.trim() || `${c.device_name} · ${c.channel_type_label}`,
      model: c.device_model ?? "",
      kind: c.channel_type_label ?? "",
      interfaceId: first.interfaceId,
    };
    const s = sender!;
    const r = receiver!;
    name = t("links.wizard.default_name", { sender: s.label, receiver: r.label });
    description = t("links.wizard.default_description", { sender: s.kind, receiver: r.kind });
    step = 3;
  }

  const sender = $derived(first?.role === "sender" ? first : second);
  const receiver = $derived(first?.role === "receiver" ? first : second);

  // --- step 3: name, existing link, create ---------------------------
  let name = $state("");
  let description = $state("");
  let exists = $state(false);
  let submitting = $state(false);
  $effect(() => {
    const s = sender;
    const r = receiver;
    if (step !== 3 || !s || !r) return;
    exists = false;
    void api
      .listLinks(s.device, locale)
      .then((links) => {
        exists = links.some((l) => l.sender_address === s.address && l.receiver_address === r.address);
      })
      .catch(() => {
        // The warning is a courtesy; creating still works without it.
      });
  });

  async function create(edit: boolean) {
    const s = sender;
    const r = receiver;
    if (!s || !r || submitting) return;
    submitting = true;
    try {
      await api.addLink(s.device, {
        sender_address: s.address,
        receiver_address: r.address,
        name,
        description,
      });
      // A new link writes configuration to both ends; a battery device
      // applies it only on its next wakeup.
      const wakeupShown = await notifyWakeupPending([s.address, r.address]);
      if (!wakeupShown) toastStore.success(t("links.created"));
      location.hash = edit ? linkHref(s.address, r.address) : backHref;
    } catch (err) {
      toastStore.error(t("links.wizard.create_failed"), friendlyError(err, t));
    } finally {
      submitting = false;
    }
  }

  const backHref = $derived(
    deviceScope ? `#/devices/${encodeURIComponent(deviceScope)}?tab=links` : "#/links",
  );

  function back() {
    if (step === 3) {
      second = null;
      step = 2;
    } else if (step === 2 && !(anchorSender || anchorReceiver)) {
      first = null;
      step = 1;
    }
  }

  // --- tables ------------------------------------------------------
  function joined(list: string[] | undefined): string {
    return (list ?? []).join(", ");
  }

  const deviceRows = $derived(
    deviceScope
      ? deviceStore.items.filter((d) => d.address === deviceScope)
      : deviceStore.items.filter((d) => d.channels_count > 0),
  );

  const deviceColumns = $derived([
    { key: "name", label: t("links.col.name"), sortable: true, title: true, filter: "text" as const, get: (d: DeviceSummary) => d.name },
    { key: "model", label: t("links.col.model"), sortable: true, filter: "text" as const, get: (d: DeviceSummary) => d.model },
    { key: "address", label: t("links.col.serial"), sortable: true, filter: "text" as const, get: (d: DeviceSummary) => d.address, cellClass: "font-mono text-xs" },
    { key: "interface", label: t("links.col.interface"), sortable: true, filter: "text" as const, get: (d: DeviceSummary) => d.interface_id },
    { key: "rooms", label: t("links.col.room"), sortable: true, filter: "text" as const, get: (d: DeviceSummary) => joined(d.rooms) },
    { key: "functions", label: t("links.col.function"), sortable: true, filter: "text" as const, get: (d: DeviceSummary) => joined(d.functions) },
  ] satisfies DataColumn<DeviceSummary>[]);

  function candidateLabel(c: LinkableChannel): string {
    return c.channel_name?.trim() || `${c.device_name} · ${c.channel_type_label}`;
  }

  const candidateColumns = $derived([
    { key: "name", label: t("links.col.name"), sortable: true, title: true, filter: "text" as const, get: candidateLabel },
    { key: "model", label: t("links.col.model"), sortable: true, filter: "text" as const, get: (c: LinkableChannel) => c.device_model },
    { key: "address", label: t("links.col.serial"), sortable: true, filter: "text" as const, get: (c: LinkableChannel) => c.address, cellClass: "font-mono text-xs" },
    { key: "rooms", label: t("links.col.room"), sortable: true, filter: "text" as const, get: (c: LinkableChannel) => joined(devicesByAddress.get(c.device_address)?.rooms) },
    { key: "functions", label: t("links.col.function"), sortable: true, filter: "text" as const, get: (c: LinkableChannel) => joined(devicesByAddress.get(c.device_address)?.functions) },
    { key: "pick", label: t("links.col.action"), cellClass: "reflow-actions" },
  ] satisfies DataColumn<LinkableChannel>[]);

  const visibleCandidates = $derived(
    showVirtual
      ? candidates
      : candidates.filter((c) => !isVirtualChannel(Number(c.address.slice(c.address.lastIndexOf(":") + 1)))),
  );

  function roleName(role: Role): string {
    return role === "sender" ? t("links.sender") : t("links.receiver");
  }
</script>

<svelte:head>
  <title>{t("page.title.link_wizard")}</title>
</svelte:head>

{#snippet partnerCard(p: Partner | null, caption: string)}
  <div class="min-w-0">
    <p class="text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]">{caption}</p>
    {#if p}
      <p class="font-medium break-words">{p.label}</p>
      <p class="font-mono text-xs text-[var(--ha-secondary-text-color)]">{p.address}{p.model ? ` · ${p.model}` : ""}</p>
    {:else}
      <p class="text-sm text-[var(--ha-disabled-text-color)]">{t("links.wizard.not_chosen")}</p>
    {/if}
  </div>
{/snippet}

{#snippet channelPicker(d: DeviceDetail)}
  {@const chans = linkableChannelsOf(d)}
  {#if chans.length === 0}
    <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("links.wizard.no_linkable_channels")}</p>
  {:else}
    <ul class="divide-y divide-[var(--ha-divider-color)]">
      {#each chans as ch (ch.address)}
        {@const role = roleOf(ch)}
        <li class="flex flex-wrap items-center gap-3 py-2">
          <span class="w-14 font-mono text-xs text-[var(--ha-secondary-text-color)]">Ch. {ch.number}</span>
          <span class="min-w-0 flex-1">
            <span class="block font-medium">{channelLabel(d, ch)}</span>
            <span class="block text-xs text-[var(--ha-secondary-text-color)]">
              {t("links.col.category")}: {role === "both" ? `${t("links.sender")} / ${t("links.receiver")}` : roleName(role as Role)}
            </span>
          </span>
          {#if role === "sender" || role === "both"}
            <Button size="sm" variant="outline" onclick={() => choose(partnerOf(d, ch, "sender"))}>
              {role === "both" ? t("links.wizard.pick_as_sender") : t("links.wizard.pick")}
            </Button>
          {/if}
          {#if role === "receiver" || role === "both"}
            <Button size="sm" variant="outline" onclick={() => choose(partnerOf(d, ch, "receiver"))}>
              {role === "both" ? t("links.wizard.pick_as_receiver") : t("links.wizard.pick")}
            </Button>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
{/snippet}

{#snippet expandDevice(d: DeviceSummary)}
  {#await detailOf(d.address)}
    <LoadingState />
  {:then detail}
    {@render channelPicker(detail)}
  {:catch err}
    <ErrorState message={friendlyError(err, t)} />
  {/await}
{/snippet}

{#snippet candidateCell(c: LinkableChannel, col: DataColumn<LinkableChannel>)}
  {#if col.key === "name"}
    <span class="block font-medium">{candidateLabel(c)}</span>
    <span class="block text-xs text-[var(--ha-secondary-text-color)]">{c.channel_type_label}</span>
  {:else if col.key === "pick"}
    <Button size="sm" variant="outline" onclick={() => pickCandidate(c)}>{t("links.wizard.pick")}</Button>
  {:else}
    {col.get?.(c) || "—"}
  {/if}
{/snippet}

<PageShell>
  <PageHeader title={t("links.new")} class="mb-4">
    {#snippet above()}
      <Breadcrumb
        items={[
          { label: t("links.title"), href: "#/links" },
          { label: t("links.wizard.crumb", { step }) },
        ]}
        class="mb-2"
      />
    {/snippet}
  </PageHeader>

  {#if !editable}
    <EmptyState message={t("links.editor_hidden")} />
  {:else}
    <ol class="mb-6 flex flex-wrap items-center gap-x-4 gap-y-2" aria-label={t("links.add.aria_progress")}>
      {#each [1, 2, 3] as n (n)}
        <li
          class="flex items-center gap-2 text-sm {step === n
            ? 'font-semibold text-[var(--ha-primary-color)]'
            : step > n
              ? 'text-[var(--ha-secondary-text-color)]'
              : 'text-[var(--ha-disabled-text-color)]'}"
          aria-current={step === n ? "step" : undefined}
        >
          <span class="grid h-6 w-6 place-items-center rounded-full border border-current text-xs">
            {step > n ? "✓" : n}
          </span>
          {n === 1 ? t("links.wizard.step1") : n === 2 ? t("links.wizard.step2") : t("links.wizard.step3")}
        </li>
      {/each}
    </ol>

    {#if error}
      <ErrorState message={error} class="mb-4" />
    {/if}

    {#if step > 1}
      <Card class="mb-4 grid gap-4 p-4 sm:grid-cols-[1fr_auto_1fr] sm:items-center">
        {@render partnerCard(sender, t("links.sender"))}
        <span class="hidden text-xl text-[var(--ha-primary-color)] sm:block" aria-hidden="true">→</span>
        {@render partnerCard(receiver, t("links.receiver"))}
      </Card>
    {/if}

    {#if step === 1}
      <p class="mb-3 text-sm text-[var(--ha-secondary-text-color)]">{t("links.wizard.step1_hint")}</p>
      {#if anchorLoading || (deviceStore.loading && deviceStore.items.length === 0)}
        <LoadingState />
      {:else if deviceScope}
        {@const d = devicesByAddress.get(deviceScope)}
        <Card class="p-4">
          {#if d}
            <p class="mb-2 font-semibold">{d.name} <span class="font-mono text-xs text-[var(--ha-secondary-text-color)]">{d.address}</span></p>
          {/if}
          {#await detailOf(deviceScope)}
            <LoadingState />
          {:then detail}
            {@render channelPicker(detail)}
          {:catch err}
            <ErrorState message={friendlyError(err, t)} />
          {/await}
        </Card>
      {:else}
        <Card class="p-4">
          <DataTable
            rows={deviceRows}
            columns={deviceColumns}
            rowKey={(d) => d.address}
            columnFilters
            persistKey="link-wizard-devices"
            initialSort={{ key: "name", asc: true }}
            emptyMessage={t("links.wizard.no_devices")}
            expand={expandDevice}
          />
        </Card>
      {/if}
    {:else if step === 2}
      <p class="mb-3 text-sm text-[var(--ha-secondary-text-color)]">
        {t("links.wizard.step2_hint", { role: roleName(otherRole) })}
      </p>
      {#if candidatesLoading}
        <LoadingState />
      {:else}
        <Card class="p-4">
          <DataTable
            rows={visibleCandidates}
            columns={candidateColumns}
            rowKey={(c) => c.address}
            cell={candidateCell}
            columnFilters
            persistKey="link-wizard-partners"
            initialSort={{ key: "name", asc: true }}
            emptyMessage={t("links.add.no_compatible")}
          />
        </Card>
      {/if}
    {:else}
      {#if exists}
        <p
          class="mb-4 rounded-md border border-[var(--ha-warning-color)] px-3 py-2 text-sm text-[var(--ha-warning-color)]"
          role="alert"
        >
          {t("links.wizard.exists")}
        </p>
      {/if}
      <Card class="grid max-w-2xl gap-4 p-4">
        <div>
          <Label class="mb-1">{t("links.rename.name")}</Label>
          <Input type="text" bind:value={name} aria-label={t("links.rename.name")} />
        </div>
        <div>
          <Label class="mb-1">{t("links.rename.description")}</Label>
          <Input type="text" bind:value={description} aria-label={t("links.rename.description")} />
        </div>
      </Card>
    {/if}

    <div class="mt-6 flex flex-wrap items-center gap-2">
      <Button variant="outline" onclick={() => (location.hash = backHref)}>{t("common.cancel")}</Button>
      {#if step > 1 && !(step === 2 && (anchorSender || anchorReceiver))}
        <Button variant="outline" onclick={back}>{t("links.add.back")}</Button>
      {/if}
      {#if step === 1 && deviceScope}
        <Button variant="ghost" onclick={() => (deviceScope = "")}>{t("links.wizard.all_devices")}</Button>
      {/if}
      {#if step < 3}
        <Button variant="ghost" onclick={() => (showVirtual = !showVirtual)}>
          {showVirtual ? t("links.wizard.hide_virtual") : t("links.wizard.show_virtual")}
        </Button>
      {/if}
      {#if step === 3}
        <span class="flex-1"></span>
        <Button variant="outline" onclick={() => void create(false)} disabled={submitting}>
          {submitting ? t("links.add.creating") : t("links.add.create")}
        </Button>
        <Button onclick={() => void create(true)} disabled={submitting}>
          {t("links.add.create_and_edit")}
        </Button>
      {/if}
    </div>
  {/if}
</PageShell>
