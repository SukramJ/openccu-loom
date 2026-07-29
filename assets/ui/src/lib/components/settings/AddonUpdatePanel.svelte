<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { addonUpdateStore } from "$lib/stores/addonUpdate.svelte";
  import { infoStore } from "$lib/stores/info.svelte";
  import { status as wsStatus } from "$lib/stores/events.svelte";
  import { prefs } from "$lib/stores/preferences.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Spinner from "$lib/components/ui/Spinner.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";

  // CCU add-on self-update card (ADR 0057). Capability-gated end to end:
  // the whole card — not a disabled-looking stub — is absent unless the
  // daemon advertises the `addon_self_update` info capability (add-on
  // build + firmware installer present). Stock CCU3 has no self-installer,
  // so nothing here would ever work there; the manual WebUI flow stays
  // the only path on that platform.
  const CAPABILITY = "addon_self_update";
  const enabled = $derived(
    infoStore.info?.capabilities?.includes(CAPABILITY) ?? false,
  );

  const status = $derived(addonUpdateStore.status);
  const isInstalling = $derived(status?.state === "installing");
  const checkBusy = $derived(
    addonUpdateStore.checking || status?.state === "checking",
  );

  onMount(() => {
    void infoStore.ensure();
  });

  // The mechanism (fetch + WS subscription) only activates once the
  // capability is confirmed present — probing GET /system/addon-update
  // on every unsupported daemon would defeat the point of gating on the
  // capability in the first place.
  let streamed = false;
  $effect(() => {
    if (enabled && !streamed) {
      streamed = true;
      void addonUpdateStore.refresh();
      addonUpdateStore.ensureStream();
    }
  });

  onDestroy(() => {
    if (streamed) addonUpdateStore.close();
  });

  // An install stops the daemon that triggered it (ADR 0057 §3), so the
  // WS socket drops and reconnects on its own. Track the installing→
  // reconnected transition so a successful restart surfaces a toast
  // instead of silently repainting the card underneath the operator.
  //
  // Split into two effects that each own a disjoint set of writes — an
  // effect that both reads and writes the *same* piece of reactive state
  // (e.g. one arming a flag the other disarms) re-triggers itself/the
  // other indefinitely (Svelte's `effect_update_depth_exceeded`) whenever
  // the arming condition (here: `state === "installing"`) stays true
  // across several ticks, which it does for the whole restart window.
  // `pendingInstallVersion` is the only reactive bridge between the two;
  // the edge-detection (`previousInstallState`) and the fire-once dedupe
  // (`toastedForVersion`) are plain, non-reactive locals private to their
  // own effect, so writing them never re-schedules either effect.
  let pendingInstallVersion = $state<string | null>(null);
  let previousInstallState: string | undefined;
  let toastedForVersion: string | null = null;

  $effect(() => {
    if (
      status &&
      status.state === "installing" &&
      previousInstallState !== "installing"
    ) {
      pendingInstallVersion = status.current_version;
      // A retry after a failed install can start from the same version —
      // allow that cycle to report again instead of being silently
      // deduped against the earlier attempt's outcome.
      toastedForVersion = null;
    }
    previousInstallState = status?.state;
  });

  $effect(() => {
    const ws = wsStatus();
    const pending = pendingInstallVersion;
    if (ws === "open" && pending !== null && toastedForVersion !== pending) {
      toastedForVersion = pending;
      void (async () => {
        await addonUpdateStore.refresh();
        const s = addonUpdateStore.status;
        if (!s) return;
        if (s.state === "failed") {
          toastStore.error(t("addon_update.toast.failed"), s.error ?? "");
        } else if (s.current_version !== pending) {
          toastStore.success(
            t("addon_update.toast.installed", { version: s.current_version }),
          );
        }
      })();
    }
  });

  function formatDate(iso?: string): string {
    if (!iso) return t("addon_update.never_checked");
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return iso;
    return d.toLocaleString(prefs.locale === "de" ? "de-DE" : "en-US");
  }

  async function check() {
    const ok = await addonUpdateStore.check();
    if (!ok) {
      toastStore.error(t("addon_update.toast.check_failed"), addonUpdateStore.error ?? "");
    }
  }

  async function install() {
    const ok = await confirmStore.ask({
      title: t("addon_update.confirm_title"),
      body: t("addon_update.confirm_body"),
      confirmLabel: t("addon_update.install"),
      destructive: true,
    });
    if (!ok) return;
    const started = await addonUpdateStore.install();
    if (!started) {
      toastStore.error(
        t("addon_update.toast.install_trigger_failed"),
        addonUpdateStore.error ?? "",
      );
    }
  }
</script>

{#if enabled}
  <!-- The bordered wrapper lives in this component (not the Settings tab
       layout) so an unsupported daemon renders nothing at all here — no
       stray empty box where the mechanism would be. -->
  <div class="space-y-4 rounded border border-slate-200 p-3 dark:border-slate-800">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h3 class="text-sm font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
        {t("addon_update.title")}
      </h3>
      <Button
        type="button"
        variant="outline"
        size="sm"
        disabled={checkBusy || isInstalling}
        onclick={() => void check()}
      >
        {#if checkBusy}
          <Spinner size={14} />
        {/if}
        {checkBusy ? t("addon_update.checking") : t("addon_update.check")}
      </Button>
    </div>
    <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("addon_update.subtitle")}</p>

    {#if status && status.state === "failed" && status.error}
      <ErrorState message={status.error} onRetry={() => void check()} />
    {/if}

    {#if isInstalling}
      <div
        class="flex items-center gap-2 rounded-md p-3 text-sm text-[var(--ha-warning-color)]"
        style="background-color: color-mix(in srgb, var(--ha-warning-color) 12%, transparent);"
        role="status"
        aria-live="polite"
      >
        <Spinner size={16} />
        <span>{t("addon_update.installing_notice")}</span>
      </div>
    {:else if status}
      <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-sm">
        <dt class="text-[var(--ha-secondary-text-color)]">
          {t("addon_update.field.current_version")}
        </dt>
        <dd class="font-mono">{status.current_version || "—"}</dd>

        <dt class="text-[var(--ha-secondary-text-color)]">
          {t("addon_update.field.latest_version")}
        </dt>
        <dd class="font-mono">
          {#if status.latest_version}
            {status.latest_version}
            {#if status.release_url}
              <a
                href={status.release_url}
                target="_blank"
                rel="noopener"
                class="ml-1 text-xs underline"
                style="color: var(--ha-primary-color);"
              >
                {t("addon_update.release_notes")}
              </a>
            {/if}
          {:else}
            —
          {/if}
        </dd>

        <dt class="text-[var(--ha-secondary-text-color)]">
          {t("addon_update.field.last_check")}
        </dt>
        <dd>{formatDate(status.last_check)}</dd>
      </dl>

      <div class="flex items-center gap-2">
        {#if status.update_available}
          <Badge variant="warning">{t("addon_update.available")}</Badge>
          <Button
            type="button"
            variant="default"
            size="sm"
            disabled={addonUpdateStore.installing}
            onclick={() => void install()}
          >
            {addonUpdateStore.installing
              ? t("addon_update.install_starting")
              : t("addon_update.install")}
          </Button>
        {:else if status.latest_version}
          <!-- Only claim "up to date" once a check has actually run — before
               that latest_version is empty and the badge would be a guess. -->
          <Badge variant="muted">{t("addon_update.up_to_date")}</Badge>
        {/if}
      </div>
    {/if}
  </div>
{/if}
