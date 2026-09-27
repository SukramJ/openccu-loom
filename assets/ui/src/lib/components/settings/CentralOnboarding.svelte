<script lang="ts">
  import { api, type CentralProbeResult } from "$lib/api/client";
  import { t } from "$lib/i18n";
  import { apiErrorMessage } from "$lib/features";
  import { createPairing } from "$lib/onboarding/pairing.svelte";
  import type { OnboardingValue } from "$lib/onboarding/onboarding";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import Spinner from "$lib/components/ui/Spinner.svelte";
  import Switch from "$lib/components/ui/Switch.svelte";
  import Tabs from "$lib/components/ui/Tabs.svelte";

  // Identifies the system at an address and, for an openccu-lite box,
  // settles how the daemon reaches and authenticates to it: HTTPS with the
  // certificate the operator compared, and either a pairing the box's
  // administrator approves or a pasted API token. Shared by the setup
  // wizard (the session-less `/setup/*` variants) and the central form.
  type Props = {
    host: string;
    /** The web server port as typed; "" for the default. */
    port?: string;
    setup?: boolean;
    value: OnboardingValue;
    /** Editing a central whose token is already stored. */
    tokenStored?: boolean;
  };

  let { host, port = "", setup = false, value = $bindable(), tokenStored = false }: Props = $props();

  let probing = $state(false);
  let probeError = $state("");
  let probe = $state<CentralProbeResult | null>(null);
  let access = $state<"full" | "control" | "read">("full");

  const pairing = createPairing({
    start: (req) => api.startCentralPairing(req, setup),
    status: (id, wait) => api.getCentralPairing(id, wait, setup),
    cancel: (id) => api.cancelCentralPairing(id, setup),
  });

  // Only an approved pairing is a credential; anything else clears it.
  $effect(() => {
    const id = pairing.approvedId;
    if (value.pairingId !== id) value = { ...value, pairingId: id };
  });

  // A different address is a different system: what was learnt about the
  // previous one no longer applies.
  let probedFor = $state("");
  $effect(() => {
    const key = `${host.trim()}|${port.trim()}`;
    if (probedFor !== "" && key !== probedFor) {
      probedFor = "";
      probe = null;
      probeError = "";
      void pairing.cancel();
      pairing.reset();
      value = { ...value, systemType: "", tlsFingerprint: "", fingerprintConfirmed: false, pairingId: "" };
    }
  });

  function portNumber(): number | undefined {
    const n = Number(port.trim());
    return port.trim() !== "" && Number.isInteger(n) && n > 0 ? n : undefined;
  }

  async function runProbe() {
    const h = host.trim();
    if (!h) return;
    probing = true;
    probeError = "";
    try {
      let res = await api.probeCentral({ host: h, port: portNumber(), tls: value.tls }, setup);
      // A box found over plain HTTP is asked once more over HTTPS, so the
      // operator can pin its certificate instead of sending the token in
      // the clear. A custom port is left as the operator typed it.
      let tls = value.tls;
      if (res.system_type === "openccu-lite" && !tls && portNumber() === undefined) {
        try {
          const secure = await api.probeCentral({ host: h, tls: true }, setup);
          if (secure.system_type === "openccu-lite") {
            res = secure;
            tls = true;
          }
        } catch {
          // No HTTPS: the plain answer stands.
        }
      }
      probe = res;
      probedFor = `${h}|${port.trim()}`;
      value = {
        ...value,
        systemType: res.system_type,
        tls,
        tlsFingerprint: res.tls_fingerprint ?? "",
        fingerprintConfirmed: false,
      };
    } catch (err) {
      probe = null;
      probeError = apiErrorMessage(err);
    } finally {
      probing = false;
    }
  }

  function startPairing() {
    void pairing.start({
      host: host.trim(),
      port: portNumber(),
      tls: value.tls,
      tls_fingerprint: value.tls ? value.tlsFingerprint || undefined : undefined,
      access,
    });
  }

  const isLite = $derived(value.systemType === "openccu-lite");
  const pairingOffered = $derived(probe?.lite?.pairing_available ?? true);
  const accessOptions = $derived([
    { value: "full", label: t("onboarding.access.full") },
    { value: "control", label: t("onboarding.access.control") },
    { value: "read", label: t("onboarding.access.read") },
  ]);
  const tabs = $derived([
    { key: "pair", label: t("onboarding.tab.pair") },
    { key: "token", label: t("onboarding.tab.token") },
  ]);
</script>

<div class="space-y-3" data-testid="central-onboarding">
  <div class="flex flex-wrap items-center gap-2">
    <Button type="button" variant="outline" size="sm" onclick={() => void runProbe()} disabled={probing || host.trim() === ""}>
      {#if probing}
        <Spinner size={14} />
        {t("onboarding.probing")}
      {:else}
        {t("onboarding.probe")}
      {/if}
    </Button>
    {#if value.systemType === "ccu"}
      <Badge variant="success">{t("onboarding.type.ccu")}</Badge>
    {:else if isLite}
      <Badge variant="success">{t("onboarding.type.openccu-lite")}</Badge>
      {#if probe?.lite?.implementation}
        <span class="text-xs text-[var(--ha-secondary-text-color)]">{probe.lite.implementation}</span>
      {/if}
    {:else if value.systemType === "unknown"}
      <Badge variant="warning">{t("onboarding.type.unknown")}</Badge>
    {/if}
  </div>
  {#if probeError}
    <p class="text-sm text-red-600 dark:text-red-400" role="alert">{probeError}</p>
  {/if}

  {#if isLite}
    {#if probe && !probe.ready}
      <p class="text-sm text-amber-700 dark:text-amber-300">{t("onboarding.lite.starting")}</p>
    {/if}
    {#if probe?.lite?.hmip_key_mode}
      <p class="text-xs text-[var(--ha-secondary-text-color)]">
        {t("onboarding.lite.keymode", {
          mode: probe.lite.hmip_key_mode.keyserver_mode,
          keys: probe.lite.hmip_key_mode.device_keys,
        })}
      </p>
    {/if}

    <label class="flex items-center gap-3">
      <Switch
        checked={value.tls}
        onCheckedChange={(v) => (value = { ...value, tls: v, fingerprintConfirmed: false })}
        disabled={pairing.phase === "pending"}
      />
      <span class="text-sm">{t("onboarding.tls")}</span>
    </label>
    {#if value.tls && value.tlsFingerprint}
      <div class="space-y-1 rounded-md bg-[var(--ha-secondary-background-color)] p-3">
        <p class="text-xs font-medium">{t("onboarding.fingerprint")}</p>
        <code class="block break-all font-mono text-xs">{value.tlsFingerprint}</code>
        <label class="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            class="h-4 w-4 rounded border-slate-300 text-brand-500 focus:ring-brand-500 dark:border-slate-600 dark:bg-slate-800"
            checked={value.fingerprintConfirmed}
            onchange={(e) => (value = { ...value, fingerprintConfirmed: e.currentTarget.checked })}
          />
          {t("onboarding.fingerprint.confirm")}
        </label>
      </div>
    {:else if !value.tls && probe}
      <p class="text-xs text-[var(--ha-secondary-text-color)]">{t("onboarding.fingerprint.none")}</p>
    {/if}

    <Tabs
      items={tabs}
      active={value.mode}
      variant="segmented"
      ariaLabel={t("onboarding.credential")}
      onSelect={(k) => (value = { ...value, mode: k === "token" ? "token" : "pair" })}
    />

    {#if value.mode === "pair"}
      {#if !pairingOffered}
        <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("onboarding.pair.unavailable")}</p>
      {:else if pairing.phase === "idle" || pairing.phase === "cancelled"}
        {#if pairing.phase === "cancelled"}
          <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("onboarding.pair.cancelled")}</p>
        {/if}
        {#if tokenStored}
          <p class="text-xs text-[var(--ha-secondary-text-color)]">{t("onboarding.pair.replaces")}</p>
        {/if}
        <div class="flex flex-wrap items-end gap-2">
          <label class="text-sm">
            <span class="mb-1 block font-medium">{t("onboarding.access")}</span>
            <Select
              class="w-44"
              options={accessOptions}
              value={access}
              onValueChange={(v) => (access = v === "control" || v === "read" ? v : "full")}
            />
          </label>
          <Button
            type="button"
            size="sm"
            onclick={startPairing}
            disabled={value.tls && value.tlsFingerprint !== "" && !value.fingerprintConfirmed}
          >
            {t("onboarding.pair.start")}
          </Button>
        </div>
      {:else if pairing.phase === "starting" || pairing.phase === "pending"}
        <div class="space-y-2 rounded-md border border-[var(--ha-divider-color)] p-3 text-center">
          {#if pairing.code}
            <p class="text-sm">{t("onboarding.pair.code_hint")}</p>
            <p class="text-3xl font-semibold tabular-nums tracking-[0.3em]" data-testid="pairing-code">
              {pairing.code}
            </p>
          {/if}
          <p class="flex items-center justify-center gap-2 text-sm text-[var(--ha-secondary-text-color)]">
            <Spinner size={14} />
            {t("onboarding.pair.waiting")}
          </p>
          <Button type="button" variant="outline" size="sm" onclick={() => void pairing.cancel()}>
            {t("common.cancel")}
          </Button>
        </div>
      {:else if pairing.phase === "approved"}
        <div class="space-y-1">
          <Badge variant="success">{t("onboarding.pair.approved")}</Badge>
          {#if pairing.scopes.length > 0}
            <p class="text-xs text-[var(--ha-secondary-text-color)]">
              {t("onboarding.pair.scopes", { scopes: pairing.scopes.join(", ") })}
            </p>
          {/if}
        </div>
      {:else}
        <div class="space-y-2">
          <p class="text-sm text-red-600 dark:text-red-400" role="alert">
            {pairing.phase === "rejected"
              ? t("onboarding.pair.rejected")
              : pairing.phase === "expired"
                ? t("onboarding.pair.expired")
                : t("onboarding.pair.error", { error: pairing.error })}
          </p>
          <Button type="button" variant="outline" size="sm" onclick={() => pairing.reset()}>
            {t("onboarding.pair.retry")}
          </Button>
        </div>
      {/if}
    {:else}
      <label class="block text-sm">
        <span class="mb-1 block font-medium">{t("onboarding.token")}</span>
        <Input
          type="password"
          autocomplete="off"
          value={value.apiToken}
          oninput={(e) => (value = { ...value, apiToken: (e.target as HTMLInputElement).value })}
        />
        <span class="mt-1 block text-xs text-[var(--ha-secondary-text-color)]">
          {tokenStored ? t("onboarding.token.unchanged") : t("onboarding.token.hint")}
        </span>
      </label>
    {/if}
  {/if}
</div>
