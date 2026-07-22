<script lang="ts">
  import type { DeviceDetail, LinkableChannel } from "$lib/api/types";
  import { api, ApiError } from "$lib/api/client";
  import Button from "$lib/components/ui/Button.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Label from "$lib/components/ui/Label.svelte";
  import { t } from "$lib/i18n";

  type Props = {
    deviceAddress: string;
    interfaceId: string;
    locale: string;
    onCancel: () => void;
    // Fired after the link is created. Carries both endpoint addresses so
    // the parent can check either side for a WAKEUP / LAZY_CONFIG battery
    // device and show the "pending wakeup" hint.
    onAdded: (result: {
      senderAddress: string;
      receiverAddress: string;
    }) => void;
  };

  let { deviceAddress, interfaceId, locale, onCancel, onAdded }: Props =
    $props();

  // Three-step wizard, port of homematicip-local-frontend's
  // add-link.ts (LitElement) to Svelte 5 runes:
  //   1. select-channel — pick a LINK-capable channel of the device
  //   2. select-peer    — pick role (sender/receiver) then a peer
  //   3. confirm        — summary + optional link name + create
  type Step = "channel" | "peer" | "confirm";
  let step = $state<Step>("channel");

  let device = $state<DeviceDetail | null>(null);
  let loadingDevice = $state(true);
  let loadingPeers = $state(false);
  let submitting = $state(false);
  let error = $state<string | null>(null);

  let selectedChannel = $state<string>("");
  let role = $state<"sender" | "receiver">("sender");
  let peers = $state<LinkableChannel[]>([]);
  let selectedPeer = $state<string>("");
  let search = $state("");
  let linkName = $state("");
  let linkDescription = $state("");

  async function loadDevice() {
    loadingDevice = true;
    error = null;
    try {
      device = await api.getDevice(deviceAddress);
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      loadingDevice = false;
    }
  }

  $effect(() => {
    void loadDevice();
  });

  // Channels eligible as a local endpoint: skip the device channel
  // (":0" carries no LINK paramset) and anything without a numeric
  // suffix (defensive — should not happen in practice).
  const sourceChannels = $derived(
    [...(device?.channels ?? [])]
      .filter((c) => c.number !== 0)
      .sort((a, b) => a.number - b.number),
  );

  async function loadPeers() {
    if (!selectedChannel) return;
    const channelNo = Number(selectedChannel.split(":")[1] ?? 0);
    loadingPeers = true;
    error = null;
    peers = [];
    selectedPeer = "";
    try {
      peers = await api.linkableChannels(
        deviceAddress,
        channelNo,
        role,
        interfaceId,
        locale,
      );
    } catch (err) {
      error =
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err);
    } finally {
      loadingPeers = false;
    }
  }

  const filteredPeers = $derived.by(() => {
    const q = search.trim().toLowerCase();
    if (!q) return peers;
    return peers.filter((c) => {
      const haystack = [
        c.address,
        c.device_address,
        c.device_name,
        c.device_model,
        c.channel_type,
        c.channel_type_label,
        c.channel_name,
      ]
        .filter(Boolean)
        .join(" ")
        .toLowerCase();
      return haystack.includes(q);
    });
  });

  function toChannelLabel(ch: {
    address: string;
    name?: string;
    type?: string;
  }): string {
    const channelNo = ch.address.split(":")[1] ?? "";
    const name = ch.name?.trim();
    if (name && name !== ch.type) return name;
    return ch.type ? `${ch.type} :${channelNo}` : `:${channelNo}`;
  }

  function peerLabel(c: LinkableChannel): string {
    const channelNo = c.address.split(":")[1] ?? "";
    const name = c.channel_name?.trim();
    const head =
      name && name !== c.channel_type_label
        ? name
        : `${c.channel_type_label || c.channel_type || "?"} :${channelNo}`;
    return `${head} — ${c.device_name || c.device_address}`;
  }

  function peerSubtitle(c: LinkableChannel): string {
    return [c.device_model, c.address].filter(Boolean).join(" · ");
  }

  const senderAddress = $derived(
    role === "sender" ? selectedChannel : selectedPeer,
  );
  const receiverAddress = $derived(
    role === "sender" ? selectedPeer : selectedChannel,
  );

  function resolveName(address: string): string {
    if (!address) return "";
    if (device && address.startsWith(`${deviceAddress}:`)) {
      const ch = device.channels.find((c) => c.address === address);
      if (ch) {
        return `${toChannelLabel(ch)} — ${device.name || deviceAddress}`;
      }
    }
    const peer = peers.find((p) => p.address === address);
    if (peer) return peerLabel(peer);
    return address;
  }

  function stepIndex(s: Step): number {
    return s === "channel" ? 0 : s === "peer" ? 1 : 2;
  }

  async function goNextFromChannel() {
    if (!selectedChannel) return;
    step = "peer";
    await loadPeers();
  }

  async function changeRole(next: "sender" | "receiver") {
    if (role === next) return;
    role = next;
    await loadPeers();
  }

  function goNextFromPeer() {
    if (!selectedPeer) return;
    step = "confirm";
  }

  function goBack() {
    error = null;
    if (step === "peer") {
      step = "channel";
      selectedPeer = "";
      peers = [];
      search = "";
      return;
    }
    if (step === "confirm") {
      step = "peer";
      return;
    }
    onCancel();
  }

  async function submit() {
    if (!senderAddress || !receiverAddress) return;
    submitting = true;
    error = null;
    try {
      await api.addLink(deviceAddress, {
        sender_address: senderAddress,
        receiver_address: receiverAddress,
        name: linkName,
        description: linkDescription,
      });
      onAdded({ senderAddress, receiverAddress });
    } catch (err) {
      error =
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err);
    } finally {
      submitting = false;
    }
  }
</script>

<div
  class="mb-4 rounded-md border border-slate-200 bg-slate-50 p-4 dark:border-slate-800 dark:bg-[color-mix(in_srgb,var(--color-slate-900)_40%,transparent)]"
>
  <header class="mb-4 flex items-center justify-between gap-3">
    <h3 class="text-sm font-semibold">{t("links.add.title2")}</h3>
    <div class="flex items-center gap-2" aria-label={t("links.add.aria_progress")}>
      {#each [0, 1, 2] as idx (idx)}
        {@const current = stepIndex(step)}
        {@const cls =
          idx < current
            ? "bg-brand-500 text-white"
            : idx === current
              ? "bg-brand-100 text-brand-700 ring-2 ring-brand-500 dark:bg-brand-900 dark:text-brand-200"
              : "bg-slate-200 text-slate-500 dark:bg-slate-800"}
        <span
          class="flex h-6 w-6 items-center justify-center rounded-full text-xs font-semibold {cls}"
        >
          {idx + 1}
        </span>
        {#if idx < 2}
          <span
            class="h-px w-6 {idx < current
              ? 'bg-brand-500'
              : 'bg-slate-200 dark:bg-slate-800'}"
          ></span>
        {/if}
      {/each}
    </div>
  </header>

  {#if error}
    <div
      class="mb-3 rounded border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200"
    >
      {error}
    </div>
  {/if}

  {#if step === "channel"}
    <section>
      <p class="mb-2 text-xs text-[var(--ha-secondary-text-color)]">{t("links.add.step1")}</p>
      {#if loadingDevice}
        <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("links.add.loading_channels")}</p>
      {:else if sourceChannels.length === 0}
        <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("links.add.no_linkable")}</p>
      {:else}
        <ul class="max-h-64 overflow-y-auto space-y-1">
          {#each sourceChannels as ch (ch.address)}
            {@const selected = selectedChannel === ch.address}
            <li>
              <button
                type="button"
                class="flex w-full items-center gap-3 rounded-md border px-3 py-2 text-left text-sm transition {selected
                  ? 'border-brand-500 bg-brand-50 dark:bg-[color-mix(in_srgb,var(--color-brand-950)_30%,transparent)]'
                  : 'border-slate-200 hover:border-slate-300 dark:border-slate-800'}"
                onclick={() => (selectedChannel = ch.address)}
              >
                <input
                  type="radio"
                  class="h-4 w-4"
                  checked={selected}
                  readonly
                  tabindex="-1"
                />
                <span class="flex-1">
                  <span class="font-medium">{toChannelLabel(ch)}</span>
                  <span class="ml-2 text-xs text-[var(--ha-secondary-text-color)]">{ch.address}</span>
                </span>
              </button>
            </li>
          {/each}
        </ul>
      {/if}
      <div class="mt-4 flex items-center justify-end gap-2">
        <Button type="button" variant="outline" size="sm" onclick={onCancel}>
          {t("common.cancel")}
        </Button>
        <Button
          type="button"
          size="sm"
          onclick={() => void goNextFromChannel()}
          disabled={!selectedChannel || loadingDevice}
        >
          {t("links.add.next")} →
        </Button>
      </div>
    </section>
  {:else if step === "peer"}
    <section>
      <p class="mb-2 text-xs text-[var(--ha-secondary-text-color)]">{t("links.add.step2")}</p>

      <div class="mb-3 flex items-center gap-2">
        <span class="text-xs font-medium text-slate-600 dark:text-slate-400">
          {t("links.add.role")}:
        </span>
        <div class="inline-flex overflow-hidden rounded-md border border-slate-200 dark:border-slate-700">
          <button
            type="button"
            class="px-3 py-2 text-sm transition {role === 'sender'
              ? 'bg-brand-500 text-white'
              : 'bg-white text-slate-700 dark:bg-slate-900 dark:text-slate-200'}"
            onclick={() => void changeRole("sender")}
          >
            {t("links.sender")}
          </button>
          <button
            type="button"
            class="px-3 py-2 text-sm transition {role === 'receiver'
              ? 'bg-brand-500 text-white'
              : 'bg-white text-slate-700 dark:bg-slate-900 dark:text-slate-200'}"
            onclick={() => void changeRole("receiver")}
          >
            {t("links.receiver")}
          </button>
        </div>
      </div>

      <div class="mb-3">
        <Input
          type="text"
          bind:value={search}
          placeholder={t("links.add.search_peers")}
        />
      </div>

      {#if loadingPeers}
        <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("links.add.loading_peers")}</p>
      {:else if filteredPeers.length === 0}
        <p class="text-sm text-[var(--ha-secondary-text-color)]">
          {search
            ? t("links.add.no_peer_matches")
            : t("links.add.no_compatible")}
        </p>
      {:else}
        <ul class="max-h-72 overflow-y-auto space-y-1">
          {#each filteredPeers as peer (peer.address)}
            {@const selected = selectedPeer === peer.address}
            <li>
              <button
                type="button"
                class="flex w-full items-start gap-3 rounded-md border px-3 py-2 text-left text-sm transition {selected
                  ? 'border-brand-500 bg-brand-50 dark:bg-[color-mix(in_srgb,var(--color-brand-950)_30%,transparent)]'
                  : 'border-slate-200 hover:border-slate-300 dark:border-slate-800'}"
                onclick={() => (selectedPeer = peer.address)}
              >
                <input
                  type="radio"
                  class="mt-1 h-4 w-4"
                  checked={selected}
                  readonly
                  tabindex="-1"
                />
                <span class="flex-1 min-w-0">
                  <span class="block truncate font-medium">
                    {peerLabel(peer)}
                  </span>
                  <span class="block truncate text-xs text-[var(--ha-secondary-text-color)]">
                    {peerSubtitle(peer)}
                  </span>
                </span>
              </button>
            </li>
          {/each}
        </ul>
      {/if}

      <div class="mt-4 flex items-center justify-between gap-2">
        <Button type="button" variant="outline" size="sm" onclick={goBack}>
          ← {t("links.add.back")}
        </Button>
        <div class="flex items-center gap-2">
          <Button type="button" variant="outline" size="sm" onclick={onCancel}>
            {t("common.cancel")}
          </Button>
          <Button
            type="button"
            size="sm"
            onclick={goNextFromPeer}
            disabled={!selectedPeer}
          >
            {t("links.add.next")} →
          </Button>
        </div>
      </div>
    </section>
  {:else}
    <section>
      <p class="mb-3 text-xs text-[var(--ha-secondary-text-color)]">{t("links.add.step3")}</p>

      <div
        class="mb-4 grid grid-cols-1 items-center gap-3 rounded-md border border-slate-200 bg-white p-3 text-sm dark:border-slate-800 dark:bg-slate-900 md:grid-cols-[1fr_auto_1fr]"
      >
        <div>
          <div class="text-xs uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
            {t("links.sender")}
          </div>
          <div class="mt-1 font-medium">{resolveName(senderAddress)}</div>
          <div class="font-mono text-xs text-[var(--ha-secondary-text-color)]">{senderAddress}</div>
        </div>
        <div class="hidden text-center text-2xl text-[var(--ha-secondary-text-color)] md:block">→</div>
        <div>
          <div class="text-xs uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
            {t("links.receiver")}
          </div>
          <div class="mt-1 font-medium">{resolveName(receiverAddress)}</div>
          <div class="font-mono text-xs text-[var(--ha-secondary-text-color)]">{receiverAddress}</div>
        </div>
      </div>

      <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
        <div>
          <Label class="mb-1">{t("links.add.name_optional")}</Label>
          <Input
            type="text"
            bind:value={linkName}
            placeholder={`${senderAddress} → ${receiverAddress}`}
          />
        </div>
        <div>
          <Label class="mb-1">{t("links.add.desc_optional")}</Label>
          <Input type="text" bind:value={linkDescription} />
        </div>
      </div>

      <div class="mt-4 flex items-center justify-between gap-2">
        <Button type="button" variant="outline" size="sm" onclick={goBack}>
          ← {t("links.add.back")}
        </Button>
        <div class="flex items-center gap-2">
          <Button type="button" variant="outline" size="sm" onclick={onCancel}>
            {t("common.cancel")}
          </Button>
          <Button
            type="button"
            size="sm"
            onclick={() => void submit()}
            disabled={submitting ||
              !senderAddress ||
              !receiverAddress}
          >
            {submitting ? t("links.add.creating") : t("links.add.create")}
          </Button>
        </div>
      </div>
    </section>
  {/if}
</div>
