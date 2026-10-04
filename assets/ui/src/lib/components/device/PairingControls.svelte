<script lang="ts">
  import { onDestroy } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { InstallModeInterfaceEntry } from "$lib/api/types";
  import Button from "$lib/components/ui/Button.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import { installModeStore } from "$lib/stores/installMode.svelte";
  import { centralStore } from "$lib/stores/centrals.svelte";
  import {
    isValidHmIPKeyInput,
    normalizeSgtin,
    stripLabelSeparators,
  } from "$lib/hmip";
  import { t } from "$lib/i18n";
  import { toastStore } from "$lib/stores/toast.svelte";

  // The controls that start pairing, structured by what applies to the
  // chosen interface: first the interface, then for a radio one primary
  // "start pairing" action with the countdown and the targeted variants
  // (by serial, HmIP SGTIN + key) folded away as secondary options; for the
  // wired bus a bus search instead of a pairing window.
  //
  // The host owns the install-mode poll (installModeStore.ensurePoll /
  // release) for as long as it shows these controls; this component only
  // reads the store. Anything that may have changed the host's data is
  // reported through onChange: `silent` asks the host not to blank its
  // view, which matters for the pairing tick.
  type Props = {
    /** Prefix for the element ids, so two hosts never collide. */
    idPrefix?: string;
    onChange?: (opts: { silent: boolean }) => void;
  };

  let { idPrefix = "pairing", onChange }: Props = $props();

  // An interface is identified by its central and its name: two centrals
  // can both report HmIP-RF.
  function keyOf(e: InstallModeInterfaceEntry): string {
    return `${e.central ?? ""}/${e.interface}`;
  }

  const multiCentral = $derived(
    new Set(installModeStore.interfaces.map((e) => e.central ?? "")).size > 1,
  );

  function labelOf(e: InstallModeInterfaceEntry): string {
    const base = multiCentral && e.central ? `${e.interface} · ${e.central}` : e.interface;
    return e.active ? `${base} ●` : base;
  }

  let selectedKey = $state("");
  // Keep the selection valid: default to the first interface once the list
  // loads, and recover if the selected interface disappears.
  $effect(() => {
    const list = installModeStore.interfaces;
    if (list.length === 0) return;
    if (!list.some((i) => keyOf(i) === selectedKey)) {
      selectedKey = keyOf(list[0]);
    }
  });
  const scopeEntry = $derived(
    installModeStore.interfaces.find((i) => keyOf(i) === selectedKey),
  );
  const selectedInterface = $derived(scopeEntry?.interface ?? "");
  const scopeActive = $derived(scopeEntry?.active ?? false);
  const scopeRemaining = $derived(scopeEntry?.seconds ?? null);
  const selectedIsHmIP = $derived(selectedInterface.startsWith("HmIP"));
  const selectedIsWired = $derived(selectedInterface === "BidCos-Wired");
  // Address-targeted pairing (by serial) binds the CCU's install mode to one
  // device address. The API contract states HmIP radios have no such
  // pairing on the CCU side and names SGTIN + key as the HmIP way, so the
  // serial form is withheld on the HmIP radio. The contract says nothing
  // about the other interfaces, which keep it.
  const serialOffered = $derived(selectedInterface !== "HmIP-RF");
  // The local teach-in stays offered unless the interface's central lacks
  // it for a lasting reason; an unknown or still-booting central keeps it.
  const localOffered = $derived(
    selectedIsHmIP &&
      !centralStore
        .centralsLacking("install_mode.local")
        .some((c) => c.name === (scopeEntry?.central ?? "")),
  );

  // Active-pairing tick: while the install mode is running on the CCU,
  // the host should reflect freshly-discovered devices without the
  // user having to hit reload. 3 s is fast enough that the operator
  // sees the device shortly after pressing the physical pairing
  // button and slow enough to keep CCU pressure low.
  let pairingTimer: ReturnType<typeof setInterval> | null = null;

  $effect(() => {
    if (installModeStore.active) {
      if (!pairingTimer) {
        pairingTimer = setInterval(() => onChange?.({ silent: true }), 3000);
      }
    } else if (pairingTimer) {
      clearInterval(pairingTimer);
      pairingTimer = null;
    }
  });

  onDestroy(() => {
    if (pairingTimer) {
      clearInterval(pairingTimer);
      pairingTimer = null;
    }
  });

  // Targeted teach-in by serial / device address. Opens a pairing
  // window for exactly one device (CCU WebUI "Gerät per Seriennummer
  // anlernen"). The pairing tick surfaces the device once it reports in.
  let serial = $state("");
  let pairBusy = $state(false);
  async function pairBySerial() {
    const addr = serial.trim();
    if (!addr) return;
    pairBusy = true;
    try {
      await api.pairDeviceInstallMode(addr, 60);
      toastStore.success(t("inbox.pair_serial_started", { addr }));
      serial = "";
      installModeStore.refresh();
    } catch (err) {
      toastStore.error(
        err instanceof ApiError ? `${err.status}: ${err.message}` : String(err),
      );
    } finally {
      pairBusy = false;
    }
  }

  // Keyserver-less HmIP LOCAL teach-in: pairing restricted to exactly
  // one device by SGTIN + device key from the label — works without
  // internet/keyserver access. The daemon re-normalises both inputs
  // authoritatively (incl. the Base32 label-form key conversion).
  let localSgtin = $state("");
  let localKey = $state("");
  let localBusy = $state(false);
  const localSgtinInvalid = $derived(
    localSgtin.trim() !== "" && normalizeSgtin(localSgtin) === null,
  );
  const localKeyInvalid = $derived(
    localKey.trim() !== "" && !isValidHmIPKeyInput(localKey),
  );
  async function startLocalTeachIn() {
    const sgtin = normalizeSgtin(localSgtin);
    if (!sgtin || !isValidHmIPKeyInput(localKey)) return;
    localBusy = true;
    try {
      await api.setInstallModeInterface(selectedInterface, true, {
        seconds: 60,
        central: scopeEntry?.central || undefined,
        local: { sgtin, key: stripLabelSeparators(localKey) },
      });
      toastStore.success(t("inbox.install_mode_local_started"));
      localSgtin = "";
      localKey = "";
      void installModeStore.refresh();
    } catch (err) {
      toastStore.error(
        err instanceof ApiError ? `${err.status}: ${err.message}` : String(err),
      );
    } finally {
      localBusy = false;
    }
  }

  let searchingWired = $state(false);
  async function searchWiredBus() {
    searchingWired = true;
    try {
      const r = await api.searchWiredDevices(
        selectedInterface,
        scopeEntry?.central || undefined,
      );
      toastStore.success(t("inbox.search_wired_done", { count: r.found }));
      // Give ReGa a moment to surface the found (not-yet-accepted)
      // devices, then let the host refetch.
      setTimeout(() => onChange?.({ silent: false }), 1500);
    } catch (err) {
      toastStore.error(
        err instanceof ApiError ? `${err.status}: ${err.message}` : String(err),
      );
    } finally {
      searchingWired = false;
    }
  }

  const inputClass =
    "rounded-md border px-2 py-2 text-sm shadow-sm focus:outline-none";
  const inputOk =
    "border-slate-300 bg-white focus:border-brand-500 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100";
  const inputBad =
    "border-red-400 bg-red-50 text-red-900 dark:border-red-700 dark:bg-red-950 dark:text-red-200";
</script>

{#if installModeStore.interfaces.length > 1}
  <div class="mb-4 flex flex-wrap items-center gap-2">
    <span class="text-xs text-[var(--ha-secondary-text-color)]">
      {t("add_device.interface_label")}
    </span>
    <Select
      class="w-auto"
      bind:value={selectedKey}
      ariaLabel={t("add_device.interface_label")}
      options={installModeStore.interfaces.map((e) => ({
        value: keyOf(e),
        label: labelOf(e),
      }))}
    />
  </div>
{/if}

{#if installModeStore.interfaces.length === 0}
  <p class="mb-4 text-sm text-[var(--ha-secondary-text-color)]">
    {t("add_device.no_interfaces")}
  </p>
{:else if selectedIsWired}
  <!-- BidCos-Wired: scan the bus for new devices (no pairing window). -->
  <div class="mb-4 flex flex-wrap items-center gap-2">
    <Button
      type="button"
      onclick={() => void searchWiredBus()}
      disabled={searchingWired}
      title={t("inbox.search_wired_title")}
    >
      {searchingWired ? t("inbox.search_wired_running") : t("inbox.search_wired")}
    </Button>
    <span class="text-xs text-[var(--ha-secondary-text-color)]">
      {t("inbox.search_wired_hint")}
    </span>
  </div>
{:else}
  <div class="mb-4 flex flex-wrap items-center gap-2">
    <Button
      type="button"
      onclick={() =>
        void installModeStore.toggle({
          interface: selectedInterface,
          central: scopeEntry?.central ?? "",
        })}
      disabled={installModeStore.busy}
      title={scopeActive
        ? t("inbox.install_mode_active_title")
        : t("inbox.install_mode_start_title")}
    >
      {#if scopeActive}
        {t("inbox.install_mode_pairing", { seconds: scopeRemaining ?? "…" })}
      {:else}
        {t("add_device.start_pairing")}
      {/if}
    </Button>
    {#if installModeStore.banner && !installModeStore.active}
      <span class="text-xs text-[var(--ha-secondary-text-color)]">{installModeStore.banner}</span>
    {/if}
  </div>

  <!-- Targeted variants: fewer operators need them, so they stay folded
       below the primary action. -->
  {#if serialOffered || localOffered}
  <details class="mb-4 rounded-md border border-[var(--ha-divider-color)] px-3 py-2">
    <summary class="cursor-pointer text-sm text-[var(--ha-secondary-text-color)]">
      {t("add_device.targeted_options")}
    </summary>

    {#if serialOffered}
    <form
      class="mt-3 flex flex-wrap items-center gap-2"
      onsubmit={(e) => {
        e.preventDefault();
        void pairBySerial();
      }}
    >
      <label class="text-xs text-[var(--ha-secondary-text-color)]" for="{idPrefix}-serial">
        {t("inbox.pair_serial_label")}
      </label>
      <input
        id="{idPrefix}-serial"
        type="text"
        bind:value={serial}
        placeholder={t("inbox.pair_serial_placeholder")}
        class="w-56 {inputClass} {inputOk}"
        disabled={pairBusy}
      />
      <Button type="submit" variant="outline" disabled={pairBusy || serial.trim() === ""}>
        {t("inbox.pair_serial_submit")}
      </Button>
    </form>
    {/if}

    {#if localOffered}
      <!-- Keyserver-less HmIP LOCAL teach-in (SGTIN + device key). -->
      <form
        class="mt-3 flex flex-wrap items-center gap-2"
        onsubmit={(e) => {
          e.preventDefault();
          void startLocalTeachIn();
        }}
      >
        <label class="text-xs text-[var(--ha-secondary-text-color)]" for="{idPrefix}-local-sgtin">
          {t("inbox.install_mode_local_label")}
        </label>
        <input
          id="{idPrefix}-local-sgtin"
          type="text"
          bind:value={localSgtin}
          placeholder={t("inbox.install_mode_local_sgtin_placeholder")}
          aria-label={t("inbox.install_mode_local_sgtin_label")}
          class="w-64 font-mono {inputClass} {localSgtinInvalid ? inputBad : inputOk}"
          disabled={localBusy}
        />
        <input
          id="{idPrefix}-local-key"
          type="text"
          bind:value={localKey}
          placeholder={t("inbox.install_mode_local_key_placeholder")}
          aria-label={t("inbox.install_mode_local_key_label")}
          class="w-64 font-mono {inputClass} {localKeyInvalid ? inputBad : inputOk}"
          disabled={localBusy}
        />
        <Button
          type="submit"
          variant="outline"
          disabled={localBusy ||
            normalizeSgtin(localSgtin) === null ||
            localKey.trim() === "" ||
            !isValidHmIPKeyInput(localKey)}
        >
          {t("inbox.install_mode_local_submit")}
        </Button>
        <span class="w-full text-xs text-[var(--ha-secondary-text-color)]">
          {t("inbox.install_mode_local_hint")}
        </span>
      </form>
    {/if}
  </details>
  {/if}
{/if}
