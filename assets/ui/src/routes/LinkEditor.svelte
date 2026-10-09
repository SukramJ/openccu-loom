<script lang="ts">
  // "Profileinstellung" — one page per direct link, laid out like the CCU
  // WebUI's link editor (config/ic_setprofiles.cgi): a header naming
  // Sender | Verknüpfung | Empfänger, the sender's and the receiver's
  // profile settings side by side, and one save bar for the whole link.
  //
  // The two sides are two LINK paramsets on two devices, each under its
  // own edit lock, so "Übernehmen" is two writes, not one transaction:
  // both are attempted, and a side that fails is named in the result.
  import { api, friendlyError } from "$lib/api/client";
  import type { Link } from "$lib/api/types";
  import ChannelPanel from "$lib/components/channel/ChannelPanel.svelte";
  import DeviceImage from "$lib/components/device/DeviceImage.svelte";
  import Breadcrumb from "$lib/components/ui/Breadcrumb.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import PageShell from "$lib/components/ui/PageShell.svelte";
  import { notifyWakeupPending } from "$lib/links/wakeup-hint";
  import { deleteLink } from "$lib/links/link-actions";
  import { partyLabel } from "$lib/links/link-routes";
  import { dirty } from "$lib/stores/dirty.svelte";
  import { surfacesStore } from "$lib/stores/surfaces.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";

  // Channel addresses straight from the route. Strings, never the Link
  // object: a save still running when the operator leaves reads its
  // panel's props after the await, and a string cannot have been nulled.
  type Props = { sender: string; receiver: string; locale: string };
  let { sender, receiver, locale }: Props = $props();

  function split(channelAddress: string): { device: string; channel: number } {
    const i = channelAddress.lastIndexOf(":");
    return i < 0
      ? { device: channelAddress, channel: 0 }
      : { device: channelAddress.slice(0, i), channel: Number(channelAddress.slice(i + 1)) };
  }
  const senderParts = $derived(split(sender));
  const receiverParts = $derived(split(receiver));

  // Editing a link is the configure surface Home Assistant owns when the
  // SPA runs embedded; the page then says so instead of offering edits.
  const editable = $derived(surfacesStore.opensVisible("nav.links"));

  let link = $state<Link | null>(null);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let notFound = $state(false);

  // Name and description, saved with the paramsets by the same
  // "Übernehmen" through the link's own PATCH.
  let name = $state("");
  let description = $state("");
  const metaDirty = $derived(
    !!link && (name !== (link.name ?? "") || description !== (link.description ?? "")),
  );

  async function load() {
    loading = true;
    loadError = null;
    notFound = false;
    try {
      const links = await api.listLinks(senderParts.device, locale);
      const found =
        links.find((l) => l.sender_address === sender && l.receiver_address === receiver) ?? null;
      link = found;
      notFound = found === null;
      name = found?.name ?? "";
      description = found?.description ?? "";
    } catch (err) {
      loadError = friendlyError(err, t);
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    void sender;
    void receiver;
    void load();
  });

  // Panels and their unsaved-edit counts.
  let receiverPanel = $state<ChannelPanel | null>(null);
  let senderPanel = $state<ChannelPanel | null>(null);
  let receiverDirty = $state(0);
  let senderDirty = $state(0);
  // -1 = not probed yet. A sender without a LINK paramset for this peer
  // (most actuator senders, many HmIP buttons) reports 0.
  let senderParamCount = $state(-1);

  const pending = $derived(receiverDirty + senderDirty + (metaDirty ? 1 : 0));
  let saving = $state(false);

  $effect(() => {
    const id = `link-meta:${sender}->${receiver}`;
    dirty.set(id, metaDirty);
    return () => dirty.clear(id);
  });

  function discard() {
    receiverPanel?.discard();
    senderPanel?.discard();
    name = link?.name ?? "";
    description = link?.description ?? "";
  }

  async function apply() {
    if (!link || saving) return;
    saving = true;
    const failed: string[] = [];
    try {
      if (receiverDirty > 0 && !(await receiverPanel?.save())) {
        failed.push(t("links.editor.receiver"));
      }
      if (senderDirty > 0 && !(await senderPanel?.save())) {
        failed.push(t("links.editor.sender"));
      }
      if (metaDirty) {
        try {
          await api.updateLink(senderParts.device, {
            sender_address: sender,
            receiver_address: receiver,
            name,
            description,
          });
          link = { ...link, name, description };
        } catch (err) {
          toastStore.error(t("links.rename_failed"), friendlyError(err, t));
          failed.push(t("links.editor.meta"));
        }
      }
      if (failed.length > 0) {
        toastStore.error(
          t("links.editor.partial_title"),
          t("links.editor.partial_body", { sides: failed.join(", ") }),
        );
        return;
      }
      // A LINK write reaches a battery device only on its next wakeup;
      // that hint replaces the plain success toast when it applies.
      const wakeupShown = await notifyWakeupPending([sender, receiver]);
      if (!wakeupShown) toastStore.success(t("links.editor.saved"));
    } finally {
      saving = false;
    }
  }

  async function remove() {
    if (!link || !(await deleteLink(link))) return;
    // The panels' unsaved edits belong to a link that no longer exists.
    discard();
    location.hash = "#/links";
  }

  const senderLabel = $derived(link ? partyLabel(link, "sender") : sender);
  const receiverLabel = $derived(link ? partyLabel(link, "receiver") : receiver);
  const title = $derived(link?.name || `${senderLabel} → ${receiverLabel}`);
</script>

{#snippet party(
  caption: string,
  label: string,
  deviceName: string | undefined,
  model: string | undefined,
  address: string,
  parts: { device: string; channel: number },
)}
  <div class="min-w-0 p-4">
    <p class="mb-2 text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
      {caption}
    </p>
    <div class="flex items-start gap-3">
      <DeviceImage address={parts.device} model={model ?? ""} />
      <div class="min-w-0">
        <p class="font-medium break-words">{label}</p>
        {#if deviceName}
          <p class="text-xs text-[var(--ha-secondary-text-color)] break-words">
            {deviceName}{model ? ` · ${model}` : ""}
          </p>
        {/if}
        <p class="mt-1 font-mono text-xs text-[var(--ha-secondary-text-color)]">{address}</p>
        <a
          class="mt-1 inline-block text-xs font-medium text-[var(--ha-primary-color)] hover:underline"
          href={`#/devices/${encodeURIComponent(parts.device)}/channels/${parts.channel}?tab=channels`}
        >
          {t("links.editor.channel_params")} ›
        </a>
      </div>
    </div>
  </div>
{/snippet}

<PageShell>
  <PageHeader {title} class="mb-4">
    {#snippet above()}
      <Breadcrumb
        items={[
          { label: t("links.title"), href: "#/links" },
          { label: t("links.editor.title") },
        ]}
        class="mb-2"
      />
    {/snippet}
  </PageHeader>

  {#if loading && !link}
    <LoadingState />
  {:else if loadError}
    <ErrorState message={loadError} onRetry={load} />
  {:else if notFound || !link}
    <EmptyState message={t("links.editor.not_found")} />
  {:else if !editable}
    <EmptyState message={t("links.editor_hidden")} />
  {:else}
    <div
      class="mb-6 grid overflow-hidden rounded-lg border border-[var(--ha-divider-color)] md:grid-cols-[1fr_1.15fr_1fr]"
      data-testid="link-peer-header"
    >
      {@render party(
        t("links.sender"),
        senderLabel,
        link.sender_device_name,
        link.sender_device_model,
        sender,
        senderParts,
      )}
      <div
        class="min-w-0 border-y border-[var(--ha-divider-color)] bg-[color-mix(in_srgb,var(--ha-primary-color)_8%,var(--ha-card-background-color))] p-4 md:border-x md:border-y-0"
      >
        <p class="mb-2 text-xs font-semibold uppercase tracking-wide text-[var(--ha-primary-color)]">
          {t("links.editor.link")}
        </p>
        <div class="space-y-2">
          <Input
            type="text"
            bind:value={name}
            aria-label={t("links.rename.name")}
            placeholder={t("links.rename.name_placeholder")}
          />
          <Input
            type="text"
            bind:value={description}
            aria-label={t("links.rename.description")}
            placeholder={t("links.rename.description_placeholder")}
          />
          <Button variant="outline-destructive" size="sm" onclick={() => void remove()}>
            {t("links.editor.delete")}
          </Button>
        </div>
      </div>
      {@render party(
        t("links.receiver"),
        receiverLabel,
        link.receiver_device_name,
        link.receiver_device_model,
        receiver,
        receiverParts,
      )}
    </div>

    <!-- Side by side, as in the CCU WebUI, once each panel still has room
         for the short/long table; stacked below that. -->
    <div class="grid gap-6 2xl:grid-cols-2">
      <section class="min-w-0" aria-labelledby="link-sender-heading">
        <h2 id="link-sender-heading" class="mb-2 text-base font-semibold">
          {t("links.editor.sender_profile")}
        </h2>
        {#if senderParamCount === 0}
          <p
            class="rounded-lg border border-[var(--ha-divider-color)] p-6 text-center text-sm text-[var(--ha-secondary-text-color)]"
          >
            {t("links.editor.sender_empty")}
          </p>
        {/if}
        <!-- Mounted even while empty so its paramset gets probed. -->
        <div class:hidden={senderParamCount === 0}>
          <ChannelPanel
            bind:this={senderPanel}
            address={senderParts.device}
            channel={senderParts.channel}
            paramset="LINK"
            peer={receiver}
            {locale}
            hosted
            linkRole="sender"
            onDirtyChange={(n) => (senderDirty = n)}
            onLoaded={(info) => (senderParamCount = info.error ? 0 : info.count)}
          />
        </div>
      </section>
      <section class="min-w-0" aria-labelledby="link-receiver-heading">
        <h2 id="link-receiver-heading" class="mb-2 text-base font-semibold">
          {t("links.editor.receiver_profile")}
        </h2>
        <ChannelPanel
          bind:this={receiverPanel}
          address={receiverParts.device}
          channel={receiverParts.channel}
          paramset="LINK"
          peer={sender}
          {locale}
          hosted
          linkRole="receiver"
          onDirtyChange={(n) => (receiverDirty = n)}
        />
      </section>
    </div>

    {#if pending > 0 || saving}
      <div
        class="sticky bottom-0 z-20 mt-6 flex flex-wrap items-center gap-2 border-t border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] py-3"
        role="region"
        aria-label={t("channel.unsaved")}
      >
        <span class="mr-auto text-sm">
          <span class="font-semibold text-[var(--ha-warning-color)]">
            {t("links.editor.pending", { count: pending })}
          </span>
          <span class="text-[var(--ha-secondary-text-color)]"> · {t("links.editor.pending_hint")}</span>
        </span>
        <Button variant="outline" size="sm" onclick={discard} disabled={saving}>
          {t("common.cancel")}
        </Button>
        <Button size="sm" onclick={() => void apply()} disabled={saving || pending === 0}>
          {saving ? t("common.saving") : t("links.editor.apply")}
        </Button>
      </div>
    {/if}
  {/if}
</PageShell>
