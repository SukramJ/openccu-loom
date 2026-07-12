<script lang="ts">
  import { onMount } from "svelte";
  import { matterStore } from "$lib/stores/matter.svelte";
  import { centralStore } from "$lib/stores/centrals.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";
  import { qrPlaceholderSvg } from "$lib/qr";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import { ApiError } from "$lib/api/client";

  // Pull the active pairing window from the daemon so a page refresh,
  // tab switch, or out-of-band `POST /matter/commissioning/window`
  // surfaces the QR + countdown instead of the empty "open window"
  // form.
  onMount(async () => {
    hydrateLoading = true;
    hydrateError = null;
    try {
      await matterStore.hydrateCommissioning();
    } catch (err) {
      hydrateError = err instanceof ApiError ? err.message : String(err);
    } finally {
      hydrateLoading = false;
    }
  });

  let selectedDuration = $state(300); // seconds

  const DURATION_OPTIONS = [
    { label: `5 ${t("matter.pair.minutes")}`, value: 300 },
    { label: `10 ${t("matter.pair.minutes")}`, value: 600 },
    { label: `15 ${t("matter.pair.minutes")}`, value: 900 },
  ];

  const phase = $derived(matterStore.commissioning.phase);
  const window = $derived(matterStore.commissioning.window);
  const remaining = $derived(matterStore.commissioning.remaining);
  const addedLabel = $derived(matterStore.commissioning.addedFabricLabel);

  // Readiness gate ("erlauben + Hinweis"): pairing is available as soon
  // as at least one CCU is ready. Only a fleet with no ready CCU blocks
  // the control; a single not-ready CCU never permanently blocks pairing
  // of a ready one — its devices join the pairing automatically once it
  // finishes loading.
  const noneReady = $derived(!centralStore.anyReady);
  const notReadyCentrals = $derived(centralStore.notReady);

  let hydrateLoading = $state(true);
  let hydrateError = $state<string | null>(null);
  let opening = $state(false);
  let closing = $state(false);

  async function openWindow() {
    opening = true;
    try {
      await matterStore.openWindow(selectedDuration);
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    } finally {
      opening = false;
    }
  }

  async function closeWindow() {
    closing = true;
    try {
      await matterStore.closeWindow();
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    } finally {
      closing = false;
    }
  }

  function reset() {
    matterStore.resetCommissioning();
  }

  // Progress ring for the countdown. SVG circle with stroke-dashoffset.
  const RING_R = 54;
  const RING_C = 2 * Math.PI * RING_R;

  const ringOffset = $derived(() => {
    if (!remaining || !window) return 0;
    const fraction = remaining / window.duration_seconds;
    return RING_C * (1 - fraction);
  });

  const qrSvg = $derived(() => {
    if (!window?.qr_code) return null;
    return qrPlaceholderSvg(window.qr_code);
  });
</script>

<div class="max-w-lg px-4 sm:px-0">
  {#if hydrateLoading}
    <p class="text-sm text-slate-500 dark:text-slate-400">{t("matter.pair.loading")}</p>
  {:else if hydrateError}
    <p class="text-sm text-red-600 dark:text-red-400">{t("matter.pair.load_error")}</p>
  {:else if phase === "idle"}
    <!-- Step 1 -->
    <Card class="p-6">
      <h2 class="text-base font-semibold mb-4 text-slate-900 dark:text-slate-100">
        {t("matter.pair.window_open_duration")}
      </h2>
      {#if noneReady}
        <p class="mb-4 text-sm font-medium text-slate-500 dark:text-slate-400">
          {t("matter.readiness.waiting")}
        </p>
      {:else if notReadyCentrals.length > 0}
        {#each notReadyCentrals as c (c.name)}
          <p class="mb-3 text-sm text-slate-500 dark:text-slate-400">
            {t("matter.readiness.partial", { name: c.name })}
          </p>
        {/each}
      {/if}
      <div class="flex flex-wrap items-center gap-3 mb-4">
        <select
          class="h-10 rounded-md border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 px-2 text-base sm:text-sm sm:h-9"
          bind:value={selectedDuration}
        >
          {#each DURATION_OPTIONS as opt}
            <option value={opt.value}>{opt.label}</option>
          {/each}
        </select>
        <Button class="w-full sm:w-auto" disabled={opening || noneReady} onclick={() => void openWindow()}>
          {opening ? t("common.saving") : t("matter.pair.window_open")}
        </Button>
      </div>
      <p class="text-xs text-slate-500 dark:text-slate-400">
        {t("matter.pair.qr_caption")}
      </p>
    </Card>
  {:else if phase === "open" && window}
    <!-- Step 2: QR + countdown -->
    <Card class="p-6">
      <h2 class="text-base font-semibold mb-4 text-slate-900 dark:text-slate-100">
        {t("matter.pair.qr_caption")}
      </h2>
      <div class="flex flex-col items-center gap-4">
        <!-- Countdown ring -->
        {#if remaining !== null}
          <div class="relative flex items-center justify-center">
            <svg width="120" height="120" viewBox="0 0 120 120">
              <!-- Background track -->
              <circle
                cx="60" cy="60" r={RING_R}
                fill="none"
                stroke="var(--ha-divider-color)"
                stroke-width="8"
              />
              <!-- Progress arc -->
              <circle
                cx="60" cy="60" r={RING_R}
                fill="none"
                stroke="var(--ha-primary-color)"
                stroke-width="8"
                stroke-dasharray={RING_C}
                stroke-dashoffset={ringOffset()}
                stroke-linecap="round"
                transform="rotate(-90 60 60)"
                style="transition: stroke-dashoffset 1s linear;"
              />
            </svg>
            <span class="absolute text-xl font-semibold text-slate-900 dark:text-slate-100">
              {remaining}s
            </span>
          </div>
        {/if}

        <!-- QR code -->
        {#if qrSvg()}
          <div
            class="border border-slate-200 dark:border-slate-700 rounded p-2 max-w-full"
            aria-label={t("matter.pair.qr_caption")}
          >
            <!-- eslint-disable-next-line svelte/no-at-html-tags -->
            <div class="max-w-full h-auto">{@html qrSvg()}</div>
          </div>
        {/if}

        <!-- Manual code -->
        <div class="text-center">
          <p class="text-xs mb-1 text-slate-500 dark:text-slate-400">
            {t("matter.pair.manual_code")}
          </p>
          <p class="text-lg font-mono font-semibold tracking-widest whitespace-nowrap overflow-x-auto text-slate-900 dark:text-slate-100">
            {window.manual_code}
          </p>
        </div>

        <!-- QR payload (raw, for debugging / alternate scan) -->
        <details class="w-full">
          <summary class="text-xs cursor-pointer text-slate-500 dark:text-slate-400">
            {t("matter.pair.qr_payload")}
          </summary>
          <p class="mt-1 text-xs font-mono break-all text-slate-500 dark:text-slate-400">
            {window.qr_code}
          </p>
        </details>

        <Button variant="outline" size="sm" disabled={closing} onclick={() => void closeWindow()}>
          {closing ? t("common.saving") : t("matter.pair.close_window")}
        </Button>
      </div>
    </Card>
  {:else if phase === "success"}
    <!-- Step 3: success -->
    <Card class="p-6 text-center">
      <div class="text-4xl mb-3">✓</div>
      <h2 class="text-base font-semibold mb-2 text-slate-900 dark:text-slate-100">
        {t("matter.pair.success")}
      </h2>
      {#if addedLabel}
        <p class="text-sm mb-4 text-slate-500 dark:text-slate-400">
          {addedLabel}
        </p>
      {/if}
      <Button onclick={reset}>{t("common.close")}</Button>
    </Card>
  {/if}
</div>
