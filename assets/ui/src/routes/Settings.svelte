<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { ConfigSchemaField, ConfigFieldSource } from "$lib/api/client";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import SectionEditor from "$lib/components/settings/SectionEditor.svelte";
  import UsersAdmin from "$lib/components/settings/UsersAdmin.svelte";
  import TokensAdmin from "$lib/components/settings/TokensAdmin.svelte";
  import CentralsAdmin from "$lib/components/settings/CentralsAdmin.svelte";
  import ExpertGate from "$lib/components/ui/ExpertGate.svelte";
  import { prefs, setLocale, setExpertMode } from "$lib/stores/preferences.svelte";
  import { t } from "$lib/i18n";
  import ConnectivityLights from "$lib/components/settings/ConnectivityLights.svelte";

  // Schema loaded once; passed down to all SectionEditors.
  let schemaFields = $state<ConfigSchemaField[]>([]);
  let schemaSections = $state<string[]>([]);
  let sources = $state<Record<string, ConfigFieldSource>>({});
  let effectiveConfig = $state<Record<string, unknown>>({});
  let schemaLoading = $state(true);
  let schemaError = $state<string | null>(null);

  // System section state (carried over from read-only phase).
  let startupCaptureEnabled = $state(false);
  let startupCaptureDuration = $state(600);
  let startupCaptureAnonymise = $state(true);
  let startupCaptureSaving = $state(false);
  let startupCaptureBanner = $state<string | null>(null);
  let restartRequesting = $state(false);
  let restartBanner = $state<string | null>(null);
  // Restart-Daemon capability — only when the daemon detected
  // a supervisor (systemd / Docker / Kubernetes / OPENCCU_LOOM_SUPERVISOR=1).
  // Without one the daemon would not come back up after a clean
  // shutdown, so the button stays disabled.
  let restartSupervised = $state(false);

  async function loadSchema() {
    schemaLoading = true;
    schemaError = null;
    try {
      const [schema, effective] = await Promise.all([
        api.getConfigSchema(),
        api.getEffectiveConfig(),
      ]);
      schemaFields = schema.fields;
      schemaSections = schema.sections;
      sources = effective.sources;
      effectiveConfig = effective.config;
      // Probe daemon capabilities once at mount so the restart
      // button reflects the supervisor state.
      try {
        const info = await api.info();
        restartSupervised = info.capabilities.includes("system.restart.supervised.v1");
      } catch {
        restartSupervised = false;
      }
      // Startup-Capture settings (admin-gated; 403 silently ignored).
      try {
        const sc = await api.getStartupCapture();
        startupCaptureEnabled = sc.enabled;
        startupCaptureDuration = sc.duration_seconds || 600;
        startupCaptureAnonymise = sc.anonymise;
      } catch {
        // viewer/operator — keep defaults
      }
    } catch (err) {
      schemaError = err instanceof ApiError ? err.message : String(err);
    } finally {
      schemaLoading = false;
    }
  }

  onMount(() => void loadSchema());

  async function saveStartupCapture() {
    startupCaptureSaving = true;
    startupCaptureBanner = null;
    try {
      await api.putStartupCapture({
        enabled: startupCaptureEnabled,
        duration_seconds: startupCaptureDuration,
        anonymise: startupCaptureAnonymise,
      });
      startupCaptureBanner = t("settings.startup_capture_saved");
    } catch (err) {
      startupCaptureBanner = err instanceof ApiError ? err.message : String(err);
    } finally {
      startupCaptureSaving = false;
    }
  }

  // MQTT reload — triggers an atomic broker reconnect against the
  // current config. Saving the section above only persists the
  // values; this button makes them take effect without waiting for
  // the file-watcher poll or a full daemon restart.
  let mqttReloading = $state(false);
  let mqttReloadBanner = $state<string | null>(null);
  let mqttReloadBannerVariant = $state<"success" | "error">("success");

  async function reloadMQTT() {
    mqttReloading = true;
    mqttReloadBanner = null;
    try {
      const res = await api.reloadMQTT();
      mqttReloadBannerVariant = "success";
      mqttReloadBanner = t("settings.mqtt.reload_success", { ms: String(res.took_ms) });
    } catch (err) {
      mqttReloadBannerVariant = "error";
      mqttReloadBanner =
        err instanceof ApiError
          ? t("settings.mqtt.reload_failed", { err: err.message })
          : String(err);
    } finally {
      mqttReloading = false;
    }
  }

  async function requestRestart() {
    if (!confirm(t("settings.restart_confirm"))) return;
    restartRequesting = true;
    restartBanner = null;
    try {
      await api.restartDaemon();
      restartBanner = t("settings.restart_signalled");
    } catch (err) {
      restartBanner = err instanceof ApiError ? err.message : String(err);
    } finally {
      restartRequesting = false;
    }
  }

  // Tab definitions. Expert-only tabs are hidden unless expertMode is on.
  type Tab = {
    id: string;
    label: string;
    expertOnly?: boolean;
  };

  const ALL_TABS: Tab[] = [
    { id: "general", label: t("settings.tab.general") },
    { id: "ccus", label: t("settings.tab.ccus") },
    { id: "mqtt", label: t("settings.tab.mqtt") },
    { id: "matter", label: t("settings.tab.matter") },
    { id: "discovery", label: t("settings.tab.discovery") },
    { id: "rest", label: t("settings.tab.rest") },
    { id: "oidc", label: t("settings.tab.oidc") },
    { id: "callback", label: t("settings.tab.callback"), expertOnly: true },
    { id: "reliability", label: t("settings.tab.reliability"), expertOnly: true },
    { id: "persistence", label: t("settings.tab.persistence"), expertOnly: true },
    { id: "users", label: t("settings.tab.users") },
    { id: "tokens", label: t("settings.tab.tokens") },
    { id: "system", label: t("settings.tab.system") },
  ];

  let activeTab = $state("general");

  const visibleTabs = $derived(
    ALL_TABS.filter((tab) => !tab.expertOnly || prefs.expertMode),
  );

  // When expert mode is turned off, switch away from an expert-only tab.
  $effect(() => {
    if (!prefs.expertMode) {
      const current = ALL_TABS.find((t) => t.id === activeTab);
      if (current?.expertOnly) {
        activeTab = "general";
      }
    }
  });
</script>

<section class="mx-auto max-w-6xl space-y-0 px-4 py-6">
  <header class="mb-5 flex flex-wrap items-center justify-between gap-3">
    <div class="space-y-1">
      <h1 class="text-2xl font-semibold">{t("settings.title")}</h1>
      <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("settings.subtitle")}</p>
      <ConnectivityLights />
    </div>
    <Button
      type="button"
      variant="outline"
      size="sm"
      onclick={() => void loadSchema()}
      disabled={schemaLoading}
    >
      {t("common.reload")}
    </Button>
  </header>

  {#if schemaError}
    <Card class="mb-4 p-3">
      <p class="text-sm text-red-600 dark:text-red-400">
        {t("common.error")} {schemaError}
      </p>
    </Card>
  {/if}

  <div class="flex gap-0 rounded-lg border border-slate-200 bg-white shadow-sm dark:border-slate-800 dark:bg-slate-900">
    <!-- Vertical tab sidebar -->
    <nav
      class="flex min-w-[10rem] flex-col gap-0.5 border-r border-slate-200 p-2 dark:border-slate-800"
      aria-label={t("settings.title")}
    >
      {#each visibleTabs as tab (tab.id)}
        <button
          type="button"
          class="rounded-md px-3 py-2 text-left text-sm transition
            {activeTab === tab.id
              ? 'bg-brand-50 font-medium text-brand-900 dark:bg-brand-900/20 dark:text-brand-100'
              : 'text-slate-700 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-800'}"
          onclick={() => (activeTab = tab.id)}
        >
          {tab.label}
        </button>
      {/each}
    </nav>

    <!-- Tab content panel -->
    <div class="min-w-0 flex-1 p-5">

      {#if activeTab === "general"}
        <div class="space-y-5">
          <div>
            <h2 class="mb-3 text-base font-semibold">{t("settings.interface")}</h2>
            <div class="space-y-3">
              <label class="flex items-center gap-3 text-sm">
                <span class="min-w-24">{t("settings.language")}</span>
                <select
                  class="rounded-md border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
                  value={prefs.locale}
                  onchange={(e) =>
                    setLocale((e.target as HTMLSelectElement).value === "de" ? "de" : "en")}
                >
                  <option value="de">Deutsch</option>
                  <option value="en">English</option>
                </select>
              </label>

              <div class="flex items-start gap-3">
                <div class="min-w-24 pt-0.5">
                  <label class="flex items-center gap-2 text-sm">
                    <input
                      type="checkbox"
                      checked={prefs.expertMode}
                      onchange={(e) =>
                        setExpertMode((e.target as HTMLInputElement).checked)}
                      class="h-4 w-4"
                    />
                    <span>{t("settings.expert_mode")}</span>
                  </label>
                </div>
                <p class="text-xs text-[var(--ha-secondary-text-color)]">
                  {t("settings.expert_mode_hint")}
                </p>
              </div>
            </div>
          </div>
        </div>

      {:else if activeTab === "ccus"}
        <CentralsAdmin />

      {:else if activeTab === "mqtt"}
        {#if schemaLoading}
          <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("common.loading")}</p>
        {:else}
          <SectionEditor
            section="north.mqtt"
            {schemaFields}
            {sources}
            allSections={schemaSections}
            {effectiveConfig}
          />
          <Card class="mt-4">
            <div class="space-y-2 p-4">
              <h3 class="text-sm font-semibold">{t("settings.mqtt.reload_title")}</h3>
              <p class="text-sm text-[var(--ha-secondary-text-color)]">
                {t("settings.mqtt.reload_description")}
              </p>
              <div class="flex items-center gap-3">
                <Button onclick={() => void reloadMQTT()} disabled={mqttReloading}>
                  {mqttReloading
                    ? t("settings.mqtt.reload_running")
                    : t("settings.mqtt.reload")}
                </Button>
                {#if mqttReloadBanner}
                  <span
                    class="text-sm"
                    class:text-green-700={mqttReloadBannerVariant === "success"}
                    class:text-red-700={mqttReloadBannerVariant === "error"}
                  >
                    {mqttReloadBanner}
                  </span>
                {/if}
              </div>
            </div>
          </Card>
        {/if}

      {:else if activeTab === "matter"}
        {#if schemaLoading}
          <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("common.loading")}</p>
        {:else}
          <SectionEditor
            section="north.matter"
            {schemaFields}
            {sources}
            allSections={schemaSections}
            {effectiveConfig}
          />
        {/if}

      {:else if activeTab === "discovery"}
        {#if schemaLoading}
          <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("common.loading")}</p>
        {:else}
          <SectionEditor
            section="north.discovery"
            {schemaFields}
            {sources}
            allSections={schemaSections}
            {effectiveConfig}
          />
        {/if}

      {:else if activeTab === "rest"}
        {#if schemaLoading}
          <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("common.loading")}</p>
        {:else}
          <SectionEditor
            section="north.rest"
            {schemaFields}
            {sources}
            allSections={schemaSections}
            {effectiveConfig}
          />
        {/if}

      {:else if activeTab === "oidc"}
        {#if schemaLoading}
          <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("common.loading")}</p>
        {:else}
          <SectionEditor
            section="north.rest.auth.oidc"
            {schemaFields}
            {sources}
            allSections={schemaSections}
            {effectiveConfig}
          />
        {/if}

      {:else if activeTab === "callback"}
        <ExpertGate>
          {#if schemaLoading}
            <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("common.loading")}</p>
          {:else}
            <SectionEditor
              section="callback"
              {schemaFields}
              {sources}
              allSections={schemaSections}
              {effectiveConfig}
            />
          {/if}
        </ExpertGate>

      {:else if activeTab === "reliability"}
        <ExpertGate>
          {#if schemaLoading}
            <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("common.loading")}</p>
          {:else}
            <SectionEditor
              section="reliability"
              {schemaFields}
              {sources}
              allSections={schemaSections}
              {effectiveConfig}
            />
          {/if}
        </ExpertGate>

      {:else if activeTab === "persistence"}
        <ExpertGate>
          {#if schemaLoading}
            <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("common.loading")}</p>
          {:else}
            <SectionEditor
              section="persistence"
              {schemaFields}
              {sources}
              allSections={schemaSections}
              {effectiveConfig}
            />
          {/if}
        </ExpertGate>

      {:else if activeTab === "users"}
        <UsersAdmin />

      {:else if activeTab === "tokens"}
        <TokensAdmin />

      {:else if activeTab === "system"}
        <div class="space-y-3">
          <div class="rounded border border-slate-200 p-3 dark:border-slate-800">
            <div class="mb-2 flex items-center justify-between gap-2">
              <div>
                <h3 class="font-medium">{t("settings.startup_capture")}</h3>
                <p class="text-xs text-[var(--ha-secondary-text-color)]">
                  {t("settings.startup_capture_help")}
                </p>
              </div>
              <label class="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  bind:checked={startupCaptureEnabled}
                  disabled={startupCaptureSaving}
                />
                <span>{t("settings.enabled")}</span>
              </label>
            </div>
            <div class="flex flex-wrap items-end gap-2">
              <label class="flex flex-col text-xs">
                <span class="text-[var(--ha-secondary-text-color)]">{t("diagnostics.duration_seconds")}</span>
                <input
                  type="number"
                  min="30"
                  max="1800"
                  bind:value={startupCaptureDuration}
                  disabled={startupCaptureSaving}
                  class="w-24 rounded border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
                />
              </label>
              <label class="flex items-center gap-1 text-xs">
                <input
                  type="checkbox"
                  bind:checked={startupCaptureAnonymise}
                  disabled={startupCaptureSaving}
                />
                <span>{t("diagnostics.anonymise")}</span>
              </label>
              <Button
                type="button"
                variant="default"
                size="sm"
                disabled={startupCaptureSaving}
                onclick={() => void saveStartupCapture()}
              >
                {t("common.save")}
              </Button>
              {#if startupCaptureBanner}
                <span class="text-xs text-[var(--ha-secondary-text-color)]">{startupCaptureBanner}</span>
              {/if}
            </div>
          </div>

          <div class="rounded border border-slate-200 p-3 dark:border-slate-800">
            <div class="flex items-center justify-between gap-2">
              <div>
                <h3 class="font-medium">{t("settings.restart_daemon")}</h3>
                <p class="text-xs text-[var(--ha-secondary-text-color)]">
                  {t("settings.restart_daemon_help")}
                </p>
                {#if !restartSupervised}
                  <p class="mt-1 text-xs text-amber-700 dark:text-amber-300">
                    {t("settings.restart_daemon_unsupervised")}
                  </p>
                {/if}
              </div>
              <Button
                type="button"
                variant="destructive"
                size="sm"
                disabled={restartRequesting || !restartSupervised}
                onclick={() => void requestRestart()}
                title={!restartSupervised ? t("settings.restart_daemon_unsupervised") : undefined}
              >
                {restartRequesting ? t("settings.restarting") : t("settings.restart_daemon")}
              </Button>
            </div>
            {#if restartBanner}
              <p class="mt-2 text-xs text-[var(--ha-secondary-text-color)]">{restartBanner}</p>
            {/if}
          </div>
        </div>
      {/if}

    </div>
  </div>
</section>
