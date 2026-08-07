<script lang="ts">
  import { onMount, untrack } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { ConfigSchemaField, ConfigFieldSource } from "$lib/api/client";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import SectionEditor from "$lib/components/settings/SectionEditor.svelte";
  import UsersAdmin from "$lib/components/settings/UsersAdmin.svelte";
  import TokensAdmin from "$lib/components/settings/TokensAdmin.svelte";
  import VisibilityAdmin from "$lib/components/settings/VisibilityAdmin.svelte";
  import ChangePasswordCard from "$lib/components/settings/ChangePasswordCard.svelte";
  import CentralsAdmin from "$lib/components/settings/CentralsAdmin.svelte";
  import RoomsFunctionsAdmin from "$lib/components/settings/RoomsFunctionsAdmin.svelte";
  import TlsCertCard from "$lib/components/settings/TlsCertCard.svelte";
  import SystemUpdatePanel from "$lib/components/settings/SystemUpdatePanel.svelte";
  import AddonUpdatePanel from "$lib/components/settings/AddonUpdatePanel.svelte";
  import CCUMaintenancePanel from "$lib/components/settings/CCUMaintenancePanel.svelte";
  import ChangesOverview from "$lib/components/settings/ChangesOverview.svelte";
  import ExpertGate from "$lib/components/ui/ExpertGate.svelte";
  import {
    prefs,
    setLocale,
    setExpertMode,
    setTheme,
    setSkin,
    type Theme,
    type Skin,
  } from "$lib/stores/preferences.svelte";
  import { isEmbedded } from "$lib/theme/ha-bridge";
  import { refreshRestartPending } from "$lib/stores/restartPending.svelte";
  import { t } from "$lib/i18n";
  import ConnectivityLights from "$lib/components/settings/ConnectivityLights.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { startRouteStore } from "$lib/stores/startRoute.svelte";
  import { authStore } from "$lib/stores/auth.svelte";
  import { foldedRouteTarget, landingTargets } from "$lib/nav";
  import { matterStore } from "$lib/stores/matter.svelte";
  import { infoStore } from "$lib/stores/info.svelte";

  // The tab named by the URL (`#/settings?tab=users`). Deep links and the
  // redirects from the views that were folded in here both arrive this way.
  let { tab }: { tab?: string } = $props();

  // Schema loaded once; passed down to all SectionEditors.
  let schemaFields = $state<ConfigSchemaField[]>([]);
  let schemaSections = $state<string[]>([]);
  let sources = $state<Record<string, ConfigFieldSource>>({});
  let effectiveConfig = $state<Record<string, unknown>>({});
  // Config field paths changed since the daemon started (boot diff). Drives
  // the conditional "Changed settings" tab.
  let changedFields = $state<string[]>([]);
  let schemaLoading = $state(true);
  let schemaError = $state<string | null>(null);

  // System section state (carried over from read-only phase).
  let startupCaptureEnabled = $state(false);
  let startupCaptureDuration = $state(600);
  let startupCaptureAnonymise = $state(true);
  let startupCaptureSaving = $state(false);
  let restartRequesting = $state(false);
  // Restart-Daemon capability — only when the daemon detected
  // a supervisor (systemd / Docker / Kubernetes / OPENCCU_LOOM_SUPERVISOR=1).
  // Without one the daemon would not come back up after a clean
  // shutdown, so the button stays disabled.
  let restartSupervised = $state(false);

  async function loadSchema() {
    schemaLoading = true;
    schemaError = null;
    try {
      const [schema, effective, changes] = await Promise.all([
        api.getConfigSchema(),
        api.getEffectiveConfig(),
        api.getConfigChanges().catch(() => ({ fields: [] as string[] })),
      ]);
      schemaFields = schema.fields;
      schemaSections = schema.sections;
      sources = effective.sources;
      effectiveConfig = effective.config;
      changedFields = changes.fields ?? [];
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

  // Landing-page candidates, gated exactly like the navigation so the
  // operator is never offered a view they cannot reach.
  const startRouteOptions = $derived(
    landingTargets({
      matterEnabled: matterStore.status?.enabled === true,
      historyEnabled: infoStore.info?.capabilities?.includes("history.v1") ?? false,
      isAdmin: authStore.identity?.role === "admin",
    }),
  );

  // What the selector shows for the stored preference. A route whose view
  // was folded into another one is displayed as its successor, so the
  // selector never sits on a value that has no matching option; anything
  // else unresolvable falls back to the default entry.
  const startRouteValue = $derived.by(() => {
    const stored = foldedRouteTarget(startRouteStore.route) ?? startRouteStore.route;
    const [bare] = stored.split("?");
    return startRouteOptions.some((opt) => opt.href === bare) ? bare : "";
  });

  async function saveStartRoute(next: string) {
    try {
      await startRouteStore.set(next);
      toastStore.success(t("settings.start_route.saved"));
    } catch (err) {
      toastStore.error(
        err instanceof ApiError ? err.message : String(err),
      );
    }
  }

  onMount(() => {
    void loadSchema();
    void startRouteStore.load();
  });

  async function saveStartupCapture() {
    startupCaptureSaving = true;
    try {
      await api.putStartupCapture({
        enabled: startupCaptureEnabled,
        duration_seconds: startupCaptureDuration,
        anonymise: startupCaptureAnonymise,
      });
      toastStore.success(t("settings.startup_capture_saved"));
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    } finally {
      startupCaptureSaving = false;
    }
  }

  // MQTT reload — triggers an atomic broker reconnect against the
  // current config. Saving the section above only persists the
  // values; this button makes them take effect without waiting for
  // the file-watcher poll or a full daemon restart.
  let mqttReloading = $state(false);

  async function reloadMQTT() {
    mqttReloading = true;
    try {
      const res = await api.reloadMQTT();
      toastStore.success(t("settings.mqtt.reload_success", { ms: String(res.took_ms) }));
    } catch (err) {
      toastStore.error(
        err instanceof ApiError
          ? t("settings.mqtt.reload_failed", { err: err.message })
          : String(err),
      );
    } finally {
      mqttReloading = false;
    }
  }

  async function requestRestart() {
    const ok = await confirmStore.ask({
      title: t("settings.restart_daemon"),
      body: t("settings.restart_confirm"),
      confirmLabel: t("settings.restart_daemon"),
      destructive: true,
    });
    if (!ok) return;
    restartRequesting = true;
    try {
      await api.restartDaemon();
      toastStore.success(t("settings.restart_signalled"));
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    } finally {
      restartRequesting = false;
    }
  }

  let cacheClearClearing = $state(false);

  async function clearCcuCache() {
    const ok = await confirmStore.ask({
      title: t("admin.cache_clear.title"),
      body: t("admin.cache_clear.body"),
      confirmLabel: t("admin.cache_clear.confirm"),
      destructive: true,
    });
    if (!ok) return;
    cacheClearClearing = true;
    try {
      const report = await api.clearCache({ kind: "global" });
      toastStore.success(
        t("admin.cache_clear.success", {
          devices: String(report.devices),
          paramsets: String(report.paramsets),
          values: String(report.values),
          centrals: String(report.centrals_reinit),
        }),
      );
    } catch (err) {
      toastStore.error(
        t("admin.cache_clear.error", {
          err: err instanceof ApiError ? err.message : String(err),
        }),
      );
    } finally {
      cacheClearClearing = false;
    }
  }

  // Tab definitions. Expert-only tabs are hidden unless expertMode is on;
  // admin-only tabs carry the gate the standalone access-control view had
  // before it was folded in here, so a viewer is not offered a tab whose
  // every request comes back 403.
  type Tab = {
    id: string;
    label: string;
    expertOnly?: boolean;
    adminOnly?: boolean;
  };

  const ALL_TABS: Tab[] = [
    { id: "general", label: t("settings.tab.general") },
    { id: "changes", label: t("settings.tab.changes") },
    { id: "ccus", label: t("settings.tab.ccus") },
    { id: "mqtt", label: t("settings.tab.mqtt") },
    { id: "matter", label: t("settings.tab.matter") },
    { id: "mcp", label: t("settings.tab.mcp") },
    { id: "discovery", label: t("settings.tab.discovery") },
    { id: "rest", label: t("settings.tab.rest") },
    { id: "oidc", label: t("settings.tab.oidc") },
    { id: "ccu_auth", label: t("settings.tab.ccu_auth") },
    { id: "callback", label: t("settings.tab.callback"), expertOnly: true },
    { id: "reliability", label: t("settings.tab.reliability"), expertOnly: true },
    { id: "persistence", label: t("settings.tab.persistence"), expertOnly: true },
    { id: "visibility", label: t("settings.tab.visibility") },
    { id: "users", label: t("settings.tab.users"), adminOnly: true },
    { id: "groups", label: t("settings.tab.groups") },
    { id: "tokens", label: t("settings.tab.tokens"), adminOnly: true },
    { id: "system", label: t("settings.tab.system") },
  ];

  // Seeded from the URL so a deep link paints its tab directly; later
  // changes to the prop are picked up by the effect below (untrack keeps
  // this a one-time read).
  let activeTab = $state(untrack(() => tab) ?? "general");

  // Sidebar grouping: the flat tab list is bucketed into a handful of
  // top-level categories so the navigation stays recognizable as the
  // number of config sections grows. Each group id maps to a
  // `settings.group.<id>` i18n label; tab order within a group is the
  // order listed here.
  type TabGroup = { id: string; tabIds: string[] };

  const TAB_GROUPS: TabGroup[] = [
    { id: "general", tabIds: ["general", "system"] },
    { id: "bridges", tabIds: ["mqtt", "matter", "mcp", "rest", "discovery"] },
    { id: "ccus", tabIds: ["ccus", "callback"] },
    { id: "security", tabIds: ["oidc", "ccu_auth", "users", "groups", "tokens"] },
    { id: "advanced", tabIds: ["visibility", "reliability", "persistence"] },
  ];

  // "changes" is rendered separately at the end of the nav (only when
  // there are changes), so it is excluded from the normal grouped/flat
  // flow here.
  const isAdmin = $derived(authStore.identity?.role === "admin");

  function isAvailable(candidate: Tab): boolean {
    if (candidate.expertOnly && !prefs.expertMode) return false;
    if (candidate.adminOnly && !isAdmin) return false;
    return true;
  }

  const visibleTabs = $derived(
    ALL_TABS.filter((candidate) => candidate.id !== "changes").filter(isAvailable),
  );

  const changesTab = ALL_TABS.find((tab) => tab.id === "changes");
  const hasChanges = $derived(changedFields.length > 0);

  // Groups with their currently-visible tabs resolved. A group whose
  // tabs are all expert-only collapses to empty when expert mode is off
  // and is dropped entirely.
  const visibleGroups = $derived(
    TAB_GROUPS.map((group) => ({
      id: group.id,
      tabs: group.tabIds
        .map((id) => ALL_TABS.find((candidate) => candidate.id === id))
        .filter((candidate): candidate is Tab => candidate !== undefined)
        .filter(isAvailable),
    })).filter((group) => group.tabs.length > 0),
  );

  // Per-group collapse state. Groups start expanded so every category is
  // visible at a glance; the heading toggles its section closed.
  let collapsedGroups = $state<Record<string, boolean>>({});

  function toggleGroup(id: string) {
    collapsedGroups[id] = !collapsedGroups[id];
  }

  // Follow the URL: a deep link, or a redirect from one of the views that
  // were folded in here, selects its tab.
  $effect(() => {
    if (tab) activeTab = tab;
  });

  // Never sit on a tab that is not offered — expert-only with expert mode
  // off, admin-only for a viewer, "Changed settings" once the changes are
  // gone, or a tab id from a stale link that no longer exists. Each of
  // those would render an empty panel with no way back.
  $effect(() => {
    const reachable =
      visibleTabs.some((candidate) => candidate.id === activeTab) ||
      (activeTab === "changes" && hasChanges);
    if (!reachable) activeTab = "general";
  });

  // Mirror the active tab into the URL so a tab is linkable and survives
  // a reload. replaceState rather than assigning location.hash: this must
  // not push a history entry per click, and must not re-enter the router
  // (which would re-run the unsaved-changes prompt on every tab switch).
  $effect(() => {
    const want = activeTab === "general" ? "#/settings" : `#/settings?tab=${activeTab}`;
    if (location.hash !== want) history.replaceState(null, "", want);
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
    <ErrorState class="mb-4" message={schemaError} onRetry={() => void loadSchema()} />
  {/if}

  {#snippet tabButton(tab: Tab, full: boolean)}
    <button
      type="button"
      class="shrink-0 whitespace-nowrap rounded-md px-3 py-2 text-left text-sm transition {full
        ? 'w-full'
        : ''}
        {activeTab === tab.id
          ? 'bg-brand-50 font-medium text-brand-900 dark:bg-[color-mix(in_srgb,var(--color-brand-900)_20%,transparent)] dark:text-brand-100'
          : 'text-slate-700 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-800'}"
      aria-current={activeTab === tab.id ? "page" : undefined}
      onclick={() => (activeTab = tab.id)}
    >
      {tab.label}
    </button>
  {/snippet}

  <div class="flex flex-col gap-0 rounded-lg border border-slate-200 bg-white shadow-sm md:flex-row dark:border-slate-800 dark:bg-slate-900">
    <!--
      Tab nav. Two render paths share the same tabButton snippet:
        - phones (<md): a flat horizontal scroll strip of all visible tabs,
          keeping the compact single-row layout.
        - md+: a vertical sidebar grouped into collapsible categories so
          the navigation stays scannable as sections grow.
    -->
    <nav
      class="border-b border-slate-200 md:min-w-[12rem] md:border-r md:border-b-0 dark:border-slate-800"
      aria-label={t("settings.title")}
    >
      <!-- Mobile: flat horizontal strip -->
      <div class="flex flex-row gap-1 overflow-x-auto p-2 md:hidden">
        {#each visibleTabs as tab (tab.id)}
          {@render tabButton(tab, false)}
        {/each}
        {#if hasChanges && changesTab}
          {@render tabButton(changesTab, false)}
        {/if}
      </div>

      <!-- Desktop: grouped collapsible sidebar -->
      <div class="hidden flex-col gap-2 p-2 md:flex">
        {#each visibleGroups as group (group.id)}
          <div class="flex flex-col gap-0.5">
            <button
              type="button"
              class="flex items-center gap-1.5 px-2 py-1 text-left text-xs font-semibold tracking-wide text-slate-500 uppercase transition hover:text-slate-700 dark:text-slate-400 dark:hover:text-slate-200"
              aria-expanded={!collapsedGroups[group.id]}
              onclick={() => toggleGroup(group.id)}
            >
              <svg
                class="h-3 w-3 shrink-0 transition-transform {collapsedGroups[group.id]
                  ? '-rotate-90'
                  : ''}"
                viewBox="0 0 12 12"
                fill="none"
                aria-hidden="true"
              >
                <path d="M3 4.5 6 7.5 9 4.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
              </svg>
              <span>{t(`settings.group.${group.id}`)}</span>
            </button>
            {#if !collapsedGroups[group.id]}
              <div class="flex flex-col gap-0.5 pl-1.5">
                {#each group.tabs as tab (tab.id)}
                  {@render tabButton(tab, true)}
                {/each}
              </div>
            {/if}
          </div>
        {/each}
        <!-- Standalone "Changed settings" entry, at the very end and only
             while there are changes. -->
        {#if hasChanges && changesTab}
          <div class="mt-1 border-t border-slate-200 pt-2 dark:border-slate-800">
            {@render tabButton(changesTab, true)}
          </div>
        {/if}
      </div>
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

              <label class="flex items-center gap-3 text-sm">
                <span class="min-w-24">{t("settings.theme")}</span>
                <select
                  class="rounded-md border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
                  value={prefs.theme}
                  onchange={(e) =>
                    setTheme((e.target as HTMLSelectElement).value as Theme)}
                >
                  <option value="light">{t("settings.theme.light")}</option>
                  <option value="dark">{t("settings.theme.dark")}</option>
                  <option value="system">{t("settings.theme.system")}</option>
                </select>
              </label>

              <label class="flex items-center gap-3 text-sm">
                <span class="min-w-24">{t("settings.start_route")}</span>
                <select
                  class="rounded-md border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
                  value={startRouteValue}
                  onchange={(e) =>
                    void saveStartRoute((e.target as HTMLSelectElement).value)}
                >
                  <option value="">{t("settings.start_route.default")}</option>
                  {#each startRouteOptions as opt (opt.href)}
                    <option value={opt.href}>{opt.label}</option>
                  {/each}
                </select>
              </label>
              <p class="-mt-1 text-xs text-[var(--ha-secondary-text-color)]">
                {t("settings.start_route.help")}
              </p>

              <div class="flex items-start gap-3">
                <label class="flex items-center gap-3 text-sm">
                  <span class="min-w-24">{t("settings.appearance.design")}</span>
                  <select
                    class="rounded-md border border-slate-300 bg-white px-2 py-1 text-sm disabled:cursor-not-allowed disabled:opacity-60 dark:border-slate-700 dark:bg-slate-900"
                    value={prefs.skin}
                    disabled={isEmbedded()}
                    onchange={(e) =>
                      setSkin((e.target as HTMLSelectElement).value as Skin)}
                  >
                    <option value="loom">{t("settings.appearance.design.loom")}</option>
                    <option value="ha">{t("settings.appearance.design.ha")}</option>
                  </select>
                </label>
                <p class="pt-1.5 text-xs text-[var(--ha-secondary-text-color)]">
                  {isEmbedded()
                    ? t("settings.appearance.design.embedded_hint")
                    : t("settings.appearance.design.help")}
                </p>
              </div>

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

        <div class="mt-4">
          <ChangePasswordCard />
        </div>

      {:else if activeTab === "changes"}
        {#if schemaLoading}
          <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("common.loading")}</p>
        {:else}
          <ChangesOverview
            changedPaths={changedFields}
            {schemaFields}
            {effectiveConfig}
            allSections={schemaSections}
            onChanged={() => {
              void loadSchema();
              void refreshRestartPending();
            }}
            onNavigate={(tab) => (activeTab = tab)}
          />
        {/if}

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
              <div class="flex flex-wrap items-center gap-3">
                <Button variant="outline" onclick={() => void reloadMQTT()} disabled={mqttReloading}>
                  {mqttReloading
                    ? t("settings.mqtt.reload_running")
                    : t("settings.mqtt.reload")}
                </Button>
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

      {:else if activeTab === "mcp"}
        {#if schemaLoading}
          <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("common.loading")}</p>
        {:else}
          <SectionEditor
            section="north.mcp"
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
          <div class="mt-4">
            <TlsCertCard />
          </div>
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

      {:else if activeTab === "ccu_auth"}
        {#if schemaLoading}
          <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("common.loading")}</p>
        {:else}
          <p class="mb-3 text-sm text-[var(--ha-secondary-text-color)]">
            {t("settings.ccu_auth.hint")}
          </p>
          <SectionEditor
            section="north.rest.auth.ccu"
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

      {:else if activeTab === "visibility"}
        <VisibilityAdmin />

      {:else if activeTab === "users"}
        <UsersAdmin />

      {:else if activeTab === "groups"}
        <RoomsFunctionsAdmin />

      {:else if activeTab === "tokens"}
        <TokensAdmin />

      {:else if activeTab === "system"}
        <div class="space-y-3">
          <div class="rounded border border-slate-200 p-3 dark:border-slate-800">
            <SystemUpdatePanel />
          </div>
          <AddonUpdatePanel />
          <div class="rounded border border-slate-200 p-3 dark:border-slate-800">
            <CCUMaintenancePanel />
          </div>
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
            </div>
          </div>

          <div class="rounded border border-slate-200 p-3 dark:border-slate-800">
            <div class="flex items-center justify-between gap-2">
              <div>
                <h3 class="font-medium">{t("admin.cache_clear.heading")}</h3>
                <p class="text-xs text-[var(--ha-secondary-text-color)]">
                  {t("admin.cache_clear.help")}
                </p>
              </div>
              <Button
                type="button"
                variant="destructive"
                size="sm"
                disabled={cacheClearClearing}
                onclick={() => void clearCcuCache()}
              >
                {cacheClearClearing ? "…" : t("admin.cache_clear.button")}
              </Button>
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
          </div>
        </div>
      {/if}

    </div>
  </div>
</section>
