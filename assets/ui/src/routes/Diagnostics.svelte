<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type {
    CaptureSummary,
    DiagnosticsClient,
    HealthSnapshot,
    Incident,
    InterfaceInfo,
    LogLevelsResponse,
    RpcRecordingStatus,
    RSSIDevice,
  } from "$lib/api/types";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import { t } from "$lib/i18n";
  import { prefs } from "$lib/stores/preferences.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  // `t()` reads prefs.locale reactively; date formatting reads it directly.

  let health = $state<HealthSnapshot | null>(null);
  let interfaces = $state<InterfaceInfo[]>([]);
  let incidents = $state<Incident[]>([]);
  let logLevels = $state<LogLevelsResponse | null>(null);
  let captures = $state<CaptureSummary[]>([]);
  let healthScore = $state<number | null>(null);
  let clients = $state<DiagnosticsClient[]>([]);
  let primaryHealthy = $state<boolean | null>(null);
  let centralScores = $state<Record<string, number>>({});
  let gauges = $state<Record<string, number>>({});
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let reconnecting = $state<string | null>(null);

  // Log-Level toggle form state
  let newLevelPath = $state("openccu-loom.client.transport.xmlrpc");
  let newLevel = $state<"debug" | "info" | "warn" | "error">("debug");
  let newLevelTTL = $state(300);
  let levelSaving = $state(false);

  // Capture form state
  let captureDuration = $state(300);
  let captureAnonymise = $state(true);
  let captureStopping = $state<string | null>(null);

  // RPC recording state
  let rpcRecordings = $state<RpcRecordingStatus[]>([]);
  let rpcRecordingStopping = $state(false);
  let rpcPollTimer = $state<ReturnType<typeof setInterval> | null>(null);

  // Unified recordings hub state
  let recType = $state<"log" | "rpc" | "both">("log");
  let rpcScope = $state<string>("");
  let recordingStarting = $state(false);

  // RSSI matrix — loaded on demand (a fresh rssiInfo hits the CCU radio),
  // not on every diagnostics open.
  let rssiDevices = $state<RSSIDevice[]>([]);
  let rssiLoading = $state(false);
  let rssiLoaded = $state(false);
  let rssiError = $state<string | null>(null);

  async function loadRSSI() {
    rssiLoading = true;
    rssiError = null;
    try {
      const matrix = await api.rssiInfo();
      rssiDevices = matrix.devices;
      rssiLoaded = true;
    } catch (err) {
      rssiError = err instanceof ApiError ? err.message : String(err);
    } finally {
      rssiLoading = false;
    }
  }

  async function load() {
    loading = true;
    loadError = null;
    try {
      const [h, i, inc, ll, caps, recs] = await Promise.all([
        api.health(),
        api.listInterfaces(),
        api.incidents(),
        api.listLogLevels().catch(() => null),
        api.listCaptures().catch(() => []),
        api.listRpcRecordings().catch(() => []),
      ]);
      health = h;
      interfaces = i;
      incidents = inc;
      logLevels = ll;
      captures = caps;
      rpcRecordings = recs;
      // Pull the numeric score + per-client detail via the
      // diagnostics dump. anonymize=0 keeps interface names and
      // device addresses in clear text — the operator viewing
      // their own daemon needs the real names to correlate; the
      // anonymised variant is reserved for the "share with
      // support" download button.
      try {
        const diag = await api.diagnostics(false);
        healthScore = diag.health?.score ?? null;
        clients = diag.health?.clients ?? [];
        primaryHealthy = diag.health?.primary_client_healthy ?? null;
        centralScores = diag.health?.central_scores ?? {};
        gauges = diag.health?.gauges ?? {};
      } catch {
        healthScore = null;
        clients = [];
        primaryHealthy = null;
        centralScores = {};
        gauges = {};
      }
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  async function applyLevel() {
    if (!newLevelPath.trim()) return;
    levelSaving = true;
    try {
      await api.setLogLevel(newLevelPath.trim(), newLevel, newLevelTTL);
      logLevels = await api.listLogLevels();
      toastStore.success(t("diagnostics.log_level_applied"));
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    } finally {
      levelSaving = false;
    }
  }

  async function resetLevel(path: string) {
    try {
      await api.resetLogLevel(path);
      logLevels = await api.listLogLevels();
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    }
  }

  // Diagnose-Dump-Download. anonymise=true hashes operator-
  // identifying fields (login subject, username) where they appear;
  // interface names, device addresses, and host IPs stay in clear
  // text either way — those are operations data the operator needs
  // to make sense of their own daemon.
  async function downloadDiagnostics(anonymise: boolean) {
    try {
      const dump = await api.diagnostics(anonymise);
      const blob = new Blob([JSON.stringify(dump, null, 2)], {
        type: "application/json",
      });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      const suffix = anonymise ? "anonymised" : "raw";
      a.download = `openccu-loom-diagnostics-${suffix}-${new Date()
        .toISOString()
        .replace(/[:.]/g, "-")}.json`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    }
  }

  async function stopCapture(id: string) {
    captureStopping = id;
    try {
      await api.stopCapture(id);
      captures = await api.listCaptures();
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    } finally {
      captureStopping = null;
    }
  }

  async function reconnect(id: string) {
    reconnecting = id;
    try {
      await api.reconnectInterface(id);
      toastStore.success(t("diagnostics.reconnect_done", { id }));
      await load();
    } catch (err) {
      toastStore.error(
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err),
      );
    } finally {
      reconnecting = null;
    }
  }

  async function refreshRpcRecordings() {
    try {
      rpcRecordings = await api.listRpcRecordings();
    } catch {
      // silent — errors are surfaced by the start/stop callers
    }
  }

  async function stopRpcRecording() {
    rpcRecordingStopping = true;
    try {
      rpcRecordings = await api.stopRpcRecording();
      toastStore.success(t("diagnostics.rpc_recording.stopped"));
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    } finally {
      rpcRecordingStopping = false;
    }
  }

  const anyRpcActive = $derived(rpcRecordings.some((r) => r.active));
  const anyLogRunning = $derived(captures.some((c) => c.status === "running"));

  async function startRecording() {
    recordingStarting = true;
    try {
      if (recType === "log" || recType === "both") {
        await api.startCapture({
          duration_seconds: captureDuration,
          anonymise: captureAnonymise,
        });
        captures = await api.listCaptures();
      }
      if (recType === "rpc" || recType === "both") {
        rpcRecordings = await api.startRpcRecording(rpcScope ? [rpcScope] : undefined, captureDuration, captureAnonymise);
      }
      toastStore.success(t("diagnostics.rpc_recording.started"));
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    } finally {
      recordingStarting = false;
    }
  }

  type UnifiedRow = {
    kind: "log" | "rpc";
    id: string;
    ccu: string;
    status: string;
    statusVariantKey: "success" | "warning" | "danger" | "muted" | "default";
    startedAt: string;
    sizeLabel: string;
    canStop: boolean;
    canDownload: boolean;
    downloadHref: string;
    // rpc-only
    rpcDownloadMapHref?: string;
    rpcDownloadGoldenHref?: string;
    rpcEndsAt?: string;
    rpcRandomize?: boolean;
  };

  const unifiedList = $derived.by<UnifiedRow[]>(() => {
    const rows: UnifiedRow[] = [];
    for (const c of captures) {
      rows.push({
        kind: "log",
        id: c.id,
        ccu: t("diagnostics.all_ccus"),
        status: c.status,
        statusVariantKey: c.status === "running" ? "warning" : "default",
        startedAt: c.started_at ?? "",
        sizeLabel:
          c.status === "running"
            ? `${Math.round(c.buffer_bytes / 1024)} kB`
            : c.archive_size
              ? `${Math.round(c.archive_size / 1024)} kB`
              : `${Math.round(c.buffer_bytes / 1024)} kB`,
        canStop: c.status === "running",
        canDownload: c.status !== "running" && !!c.archive_size,
        downloadHref: api.captureDownloadURL(c.id),
      });
    }
    for (const r of rpcRecordings) {
      rows.push({
        kind: "rpc",
        id: r.central,
        ccu: r.central,
        status: r.active
          ? t("diagnostics.rpc_recording.active")
          : t("diagnostics.rpc_recording.inactive"),
        statusVariantKey: r.active ? "warning" : "muted",
        startedAt: "",
        sizeLabel: String(r.entries),
        canStop: false,
        canDownload: r.entries > 0,
        downloadHref: api.rpcRecordingDownloadUrl(r.central, "map"),
        rpcDownloadMapHref: r.entries > 0 ? api.rpcRecordingDownloadUrl(r.central, "map") : undefined,
        rpcDownloadGoldenHref: r.entries > 0 ? api.rpcRecordingDownloadUrl(r.central, "golden") : undefined,
        rpcEndsAt: r.ends_at,
        rpcRandomize: r.randomize,
      });
    }
    return rows;
  });

  const rpcCentralNames = $derived(
    [...new Set(rpcRecordings.map((r) => r.central))].sort(),
  );

  onMount(() => {
    void load();
    // Poll RPC recording status every 5 s while any recording is active.
    rpcPollTimer = setInterval(() => {
      void refreshRpcRecordings();
    }, 5000);
    return () => {
      if (rpcPollTimer !== null) clearInterval(rpcPollTimer);
    };
  });

  function statusVariant(s: string): "success" | "warning" | "danger" | "muted" {
    if (s === "healthy") return "success";
    if (s === "degraded") return "warning";
    if (s === "unhealthy") return "danger";
    return "muted";
  }

  function severityVariant(
    s: string,
  ): "default" | "warning" | "danger" | "muted" {
    if (s === "error" || s === "critical") return "danger";
    if (s === "warn" || s === "warning") return "warning";
    if (s === "info") return "default";
    return "muted";
  }

  function formatDate(iso: string | undefined): string {
    if (!iso) return "";
    try {
      return new Date(iso).toLocaleString(prefs.locale === "de" ? "de-DE" : "en-US");
    } catch {
      return iso;
    }
  }

  function formatTimeOnly(iso: string | undefined): string {
    if (!iso) return "";
    try {
      return new Date(iso).toLocaleTimeString(prefs.locale === "de" ? "de-DE" : "en-US");
    } catch {
      return iso;
    }
  }

  const incidentsSorted = $derived(
    [...incidents].sort((a, b) => b.when.localeCompare(a.when)),
  );

  // Gauge-Gruppierung: dot-prefix bestimmt die Sektion (rest, scheduler,
  // ws, audit, event_bus, …). Innerhalb einer Sektion alphabetisch.
  type GaugeGroup = { prefix: string; entries: { key: string; value: number }[] };
  const gaugeGroups = $derived.by<GaugeGroup[]>(() => {
    const groups = new Map<string, { key: string; value: number }[]>();
    for (const [k, v] of Object.entries(gauges)) {
      const prefix = k.includes(".") ? k.split(".")[0] : "other";
      const list = groups.get(prefix) ?? [];
      list.push({ key: k, value: v });
      groups.set(prefix, list);
    }
    const out: GaugeGroup[] = [];
    for (const [prefix, list] of groups.entries()) {
      list.sort((a, b) => a.key.localeCompare(b.key));
      out.push({ prefix, entries: list });
    }
    out.sort((a, b) => a.prefix.localeCompare(b.prefix));
    return out;
  });

  const centralScoreEntries = $derived(
    Object.entries(centralScores).sort(([a], [b]) => a.localeCompare(b)),
  );

  function scoreVariant(score: number): "success" | "warning" | "danger" | "muted" {
    if (score >= 90) return "success";
    if (score >= 50) return "warning";
    if (score > 0) return "danger";
    return "muted";
  }

  function formatGaugeValue(v: number): string {
    if (Number.isInteger(v)) {
      if (Math.abs(v) >= 1_000_000) return (v / 1_000_000).toFixed(1) + "M";
      if (Math.abs(v) >= 1_000) return (v / 1_000).toFixed(1) + "k";
      return String(v);
    }
    return v.toFixed(2);
  }
</script>

<svelte:head>
  <title>{t("page.title.diagnostics")}</title>
</svelte:head>

<section class="mx-auto max-w-6xl px-4 sm:px-6 py-6 space-y-6">
  <header class="flex flex-wrap items-center justify-between gap-3">
    <div>
      <h1 class="text-2xl font-semibold">{t("diagnostics.title")}</h1>
      <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("diagnostics.subtitle")}</p>
    </div>
    <div class="flex flex-wrap items-center gap-2">
      <Button
        type="button"
        variant="outline"
        size="sm"
        onclick={() => void downloadDiagnostics(false)}
      >
        {t("diagnostics.download_dump")}
      </Button>
      <Button
        type="button"
        variant="outline"
        size="sm"
        onclick={() => void load()}
        disabled={loading}
      >
        {t("common.reload")}
      </Button>
    </div>
  </header>

  {#if loadError}
    <ErrorState message={loadError} onRetry={() => void load()} />
  {/if}

  {#if centralScoreEntries.length > 0}
    <section class="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
      {#each centralScoreEntries as [name, score] (name)}
        <Card class="p-3">
          <div class="text-xs uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
            {t("diagnostics.central")}
          </div>
          <div class="mt-1 flex items-baseline justify-between gap-2">
            <span class="truncate font-mono text-sm">{name}</span>
            <span class="text-2xl font-bold tabular-nums">{score}%</span>
          </div>
          <div class="mt-1">
            <Badge variant={scoreVariant(score)}>
              {score >= 90 ? t("diagnostics.healthy") : score >= 50 ? "degraded" : score > 0 ? t("diagnostics.unhealthy") : "unknown"}
            </Badge>
          </div>
        </Card>
      {/each}
    </section>
  {/if}

  <Card class="p-4">
    <header class="mb-3 flex items-center justify-between gap-2">
      <h2 class="text-lg font-semibold">{t("diagnostics.health")}</h2>
      <div class="flex items-center gap-3">
        {#if healthScore !== null}
          <span class="text-2xl font-bold tabular-nums" title={t("diagnostics.health_score")}>
            {healthScore}%
          </span>
        {/if}
        {#if health}
          <Badge variant={statusVariant(health.status)}>{health.status}</Badge>
        {/if}
      </div>
    </header>
    {#if health}
      {#if health.components.length === 0}
        <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("diagnostics.empty.components")}</p>
      {:else}
        <ul class="divide-y divide-slate-200 text-sm dark:divide-slate-800">
          {#each health.components as c (c.name)}
            <li class="flex flex-wrap items-center justify-between gap-2 py-2">
              <div>
                <span class="font-medium">{c.name}</span>
                {#if c.note}
                  <span class="ml-2 text-xs text-[var(--ha-secondary-text-color)]">{c.note}</span>
                {/if}
              </div>
              <div class="flex items-center gap-2">
                <Badge variant={statusVariant(c.status)}>{c.status}</Badge>
                {#if c.recorded_at}
                  <span class="text-xs text-[var(--ha-secondary-text-color)]">{formatDate(c.recorded_at)}</span>
                {/if}
              </div>
            </li>
          {/each}
        </ul>
      {/if}
    {:else if !loading}
      <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("diagnostics.empty.components")}</p>
    {/if}
  </Card>

  {#if clients.length > 0}
    <Card class="p-4">
      <header class="mb-3 flex items-center justify-between gap-2">
        <h2 class="text-lg font-semibold">{t("diagnostics.client_health")}</h2>
        {#if primaryHealthy !== null}
          <Badge variant={primaryHealthy ? "success" : "danger"}>
            {t("diagnostics.primary")}: {primaryHealthy ? t("diagnostics.healthy") : t("diagnostics.unhealthy")}
          </Badge>
        {/if}
      </header>
      <ul class="space-y-2">
        {#each clients as c (c.name)}
          <li class="rounded border border-slate-200 p-2 dark:border-slate-800">
            <div class="flex flex-wrap items-baseline gap-2">
              <span class="font-mono text-sm">{c.name}</span>
              <Badge variant={statusVariant(c.status)}>{c.status}</Badge>
              <span class="text-xs font-semibold tabular-nums">{c.score}%</span>
              {#if c.in_recovery}
                <Badge variant="warning">{t("diagnostics.in_recovery")}</Badge>
              {/if}
            </div>
            <dl class="mt-1 grid grid-cols-2 gap-x-3 gap-y-1 text-xs text-[var(--ha-secondary-text-color)] sm:grid-cols-3">
              {#if c.last_successful_request}
                <div>
                  <dt class="inline font-medium">{t("diagnostics.last_ok")}:</dt>
                  <dd class="inline ml-1">{formatDate(c.last_successful_request)}</dd>
                </div>
              {/if}
              {#if c.last_failed_request}
                <div>
                  <dt class="inline font-medium">{t("diagnostics.last_fail")}:</dt>
                  <dd class="inline ml-1">{formatDate(c.last_failed_request)}</dd>
                </div>
              {/if}
              {#if c.last_event_received}
                <div>
                  <dt class="inline font-medium">{t("diagnostics.last_event")}:</dt>
                  <dd class="inline ml-1">{formatDate(c.last_event_received)}</dd>
                </div>
              {/if}
              <div>
                <dt class="inline font-medium">{t("diagnostics.consecutive_failures")}:</dt>
                <dd class="inline ml-1 tabular-nums">{c.consecutive_failures}</dd>
              </div>
              <div>
                <dt class="inline font-medium">{t("diagnostics.reconnect_attempts")}:</dt>
                <dd class="inline ml-1 tabular-nums">{c.reconnect_attempts}</dd>
              </div>
            </dl>
          </li>
        {/each}
      </ul>
    </Card>
  {/if}

  {#if gaugeGroups.length > 0}
    <Card class="p-4">
      <header class="mb-3 flex items-center justify-between">
        <h2 class="text-lg font-semibold">{t("diagnostics.system_gauges")}</h2>
        <span class="text-xs text-[var(--ha-secondary-text-color)]">
          {Object.keys(gauges).length}
        </span>
      </header>
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {#each gaugeGroups as group (group.prefix)}
          <div class="rounded border border-slate-200 p-2 dark:border-slate-800">
            <div class="mb-1 font-mono text-xs uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
              {group.prefix}
            </div>
            <dl class="space-y-0.5 text-sm">
              {#each group.entries as entry (entry.key)}
                <div class="flex items-baseline justify-between gap-2">
                  <dt class="truncate font-mono text-xs text-[var(--ha-secondary-text-color)]">
                    {entry.key.startsWith(group.prefix + ".") ? entry.key.slice(group.prefix.length + 1) : entry.key}
                  </dt>
                  <dd class="font-semibold tabular-nums">{formatGaugeValue(entry.value)}</dd>
                </div>
              {/each}
            </dl>
          </div>
        {/each}
      </div>
    </Card>
  {/if}

  <Card class="p-4">
    <header class="mb-3 flex items-center justify-between">
      <h2 class="text-lg font-semibold">{t("diagnostics.interfaces")}</h2>
      <span class="text-xs text-[var(--ha-secondary-text-color)]">{interfaces.length}</span>
    </header>
    {#if interfaces.length === 0 && !loading}
      <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("diagnostics.empty.interfaces")}</p>
    {:else}
      <ul class="space-y-2">
        {#each interfaces as i (i.id)}
          <li class="flex flex-wrap items-center justify-between gap-2 rounded border border-slate-200 p-2 dark:border-slate-800">
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2">
                <span class="font-mono text-sm">{i.id}</span>
                <Badge variant="muted">{i.interface}</Badge>
                {#if i.connected}
                  <Badge variant="success">{t("diagnostics.connected")}</Badge>
                {:else}
                  <Badge variant="danger">{t("diagnostics.disconnected")}</Badge>
                {/if}
              </div>
              {#if i.host || i.central_id || i.note}
                <p class="mt-1 text-xs text-[var(--ha-secondary-text-color)]">
                  {#if i.central_id}<span>{i.central_id}</span>{/if}
                  {#if i.host}<span> · {i.host}</span>{/if}
                  {#if i.note}<span> · {i.note}</span>{/if}
                </p>
              {/if}
            </div>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onclick={() => void reconnect(i.id)}
              disabled={reconnecting === i.id}
            >
              {reconnecting === i.id ? "…" : t("diagnostics.reconnect")}
            </Button>
          </li>
        {/each}
      </ul>
    {/if}
  </Card>

  <Card class="p-4">
    <header class="mb-3 flex items-center justify-between gap-2">
      <h2 class="text-lg font-semibold">{t("diagnostics.rssi.title")}</h2>
      <Button
        type="button"
        variant="outline"
        size="sm"
        onclick={() => void loadRSSI()}
        disabled={rssiLoading}
      >
        {rssiLoading ? "…" : rssiLoaded ? t("common.reload") : t("common.load")}
      </Button>
    </header>
    <p class="mb-3 text-xs text-[var(--ha-secondary-text-color)]">{t("diagnostics.rssi.hint")}</p>
    {#if rssiError}
      <ErrorState message={rssiError} onRetry={loadRSSI} />
    {:else if !rssiLoaded}
      <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("diagnostics.rssi.not_loaded")}</p>
    {:else if rssiDevices.length === 0}
      <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("diagnostics.rssi.empty")}</p>
    {:else}
      <ul class="space-y-3">
        {#each rssiDevices as d (d.central + "/" + d.address)}
          <li class="rounded border border-slate-200 p-2 dark:border-slate-800">
            <div class="flex flex-wrap items-center gap-2">
              <span class="text-sm font-medium">{d.name || d.address}</span>
              <span class="font-mono text-xs text-[var(--ha-secondary-text-color)]">{d.address}</span>
              <Badge variant="muted">{d.interface_id}</Badge>
            </div>
            <table class="mt-2 w-full text-left text-xs">
              <thead class="text-[var(--ha-secondary-text-color)]">
                <tr>
                  <th class="py-1 pr-2 font-normal">{t("diagnostics.rssi.partner")}</th>
                  <th class="py-1 pr-2 font-normal">{t("diagnostics.rssi.device_dbm")}</th>
                  <th class="py-1 font-normal">{t("diagnostics.rssi.peer_dbm")}</th>
                </tr>
              </thead>
              <tbody>
                {#each d.partners as p (p.address)}
                  <tr class="border-t border-slate-100 dark:border-slate-800">
                    <td class="py-1 pr-2">
                      <span class="font-mono">{p.address}</span>
                      {#if p.name}<span class="text-[var(--ha-secondary-text-color)]"> · {p.name}</span>{/if}
                    </td>
                    <td class="py-1 pr-2 font-mono">{p.rssi_device ?? "—"}</td>
                    <td class="py-1 font-mono">{p.rssi_peer ?? "—"}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </li>
        {/each}
      </ul>
    {/if}
  </Card>

  <Card class="p-4">
    <header class="mb-3 flex items-center justify-between gap-2">
      <h2 class="text-lg font-semibold">{t("diagnostics.logging")}</h2>
      {#if logLevels}
        <Badge variant="muted">{t("diagnostics.log_default")}: {logLevels.default}</Badge>
      {/if}
    </header>
    {#if logLevels}
      {#if logLevels.overrides.length === 0}
        <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("diagnostics.no_overrides")}</p>
      {:else}
        <ul class="mb-3 space-y-1">
          {#each logLevels.overrides as ov (ov.path)}
            <li class="flex items-center justify-between gap-2 text-sm">
              <div class="min-w-0 flex-1">
                <span class="font-mono">{ov.path}</span>
                <Badge variant="default">{ov.level}</Badge>
                {#if ov.permanent}
                  <span class="text-xs text-[var(--ha-secondary-text-color)]">{t("diagnostics.permanent")}</span>
                {:else if ov.remaining_ms}
                  <span class="text-xs text-[var(--ha-secondary-text-color)]">
                    {Math.round(ov.remaining_ms / 1000)}s
                  </span>
                {/if}
              </div>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onclick={() => void resetLevel(ov.path)}
              >
                {t("common.reset")}
              </Button>
            </li>
          {/each}
        </ul>
      {/if}
      <div class="flex flex-wrap items-end gap-2 border-t border-slate-200 pt-3 dark:border-slate-800">
        <label class="flex flex-col text-xs">
          <span class="text-[var(--ha-secondary-text-color)]">{t("diagnostics.log_path")}</span>
          <input
            type="text"
            bind:value={newLevelPath}
            class="w-full sm:w-64 rounded border border-slate-300 px-2 py-1 text-sm font-mono dark:border-slate-700 dark:bg-slate-900"
          />
        </label>
        <label class="flex flex-col text-xs">
          <span class="text-[var(--ha-secondary-text-color)]">{t("diagnostics.log_level")}</span>
          <select
            bind:value={newLevel}
            class="rounded border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
          >
            <option value="debug">debug</option>
            <option value="info">info</option>
            <option value="warn">warn</option>
            <option value="error">error</option>
          </select>
        </label>
        <label class="flex flex-col text-xs">
          <span class="text-[var(--ha-secondary-text-color)]">{t("diagnostics.ttl_seconds")}</span>
          <input
            type="number"
            min="0"
            max="86400"
            bind:value={newLevelTTL}
            class="w-24 rounded border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
          />
        </label>
        <Button
          type="button"
          variant="default"
          size="sm"
          disabled={levelSaving}
          onclick={() => void applyLevel()}
        >
          {t("diagnostics.apply")}
        </Button>
      </div>
    {:else}
      <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("diagnostics.unavailable")}</p>
    {/if}
  </Card>

  <!-- Unified Aufzeichnungen hub -->
  <Card class="p-4">
    <header class="mb-3 flex items-center justify-between gap-2">
      <h2 class="text-lg font-semibold">{t("diagnostics.recordings.new_title")}</h2>
    </header>

    <!-- Type segmented control -->
    <div class="mb-3 flex flex-col gap-2">
      <span class="text-xs text-[var(--ha-secondary-text-color)]">{t("diagnostics.recordings.type")}</span>
      <div class="flex gap-1" role="group" aria-label={t("diagnostics.recordings.type")}>
        {#each (["log", "rpc", "both"] as const) as typ (typ)}
          <button
            type="button"
            class={[
              "rounded border px-3 py-1 text-sm font-medium transition-colors focus:outline-none focus-visible:ring-2",
              recType === typ
                ? "border-[var(--ha-primary-color)] bg-[var(--ha-primary-color)] text-white"
                : "border-slate-300 bg-white text-[var(--ha-primary-text-color)] hover:bg-slate-50 dark:border-slate-600 dark:bg-slate-800 dark:hover:bg-slate-700",
            ].join(" ")}
            onclick={() => { recType = typ; }}
            aria-pressed={recType === typ}
          >
            {t(`diagnostics.recordings.type.${typ}`)}
          </button>
        {/each}
      </div>
    </div>

    <!-- Conditional fields -->
    <div class="mb-3 flex flex-wrap items-end gap-3">
      <label class="flex flex-col text-xs">
        <span class="text-[var(--ha-secondary-text-color)]">{t("diagnostics.duration_seconds")}</span>
        <input
          type="number"
          min="0"
          max="3600"
          bind:value={captureDuration}
          class="w-24 rounded border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
        />
        <span class="mt-0.5 text-xs text-[var(--ha-secondary-text-color)]">
          {t("diagnostics.recordings.duration_open_hint")}
        </span>
      </label>
      <div class="flex flex-col gap-0.5">
        <label class="flex items-center gap-1 text-xs">
          <input type="checkbox" bind:checked={captureAnonymise} />
          <span>{t("diagnostics.anonymise")}</span>
        </label>
        <p class="text-xs text-[var(--ha-secondary-text-color)] max-w-xs leading-snug">
          {t("diagnostics.recordings.anonymise_hint")}
        </p>
      </div>
      {#if recType === "rpc" || recType === "both"}
        <label class="flex flex-col text-xs">
          <span class="text-[var(--ha-secondary-text-color)]">{t("diagnostics.recordings.scope")}</span>
          <select
            bind:value={rpcScope}
            class="rounded border border-slate-300 px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900"
          >
            <option value="">{t("diagnostics.recordings.scope_all")}</option>
            {#each rpcCentralNames as name (name)}
              <option value={name}>{name}</option>
            {/each}
          </select>
        </label>
      {/if}
    </div>

    <p class="mb-3 text-xs text-[var(--ha-secondary-text-color)] leading-relaxed">
      {t("diagnostics.recordings.retention_hint")}
    </p>

    <Button
      type="button"
      variant="default"
      size="sm"
      disabled={recordingStarting || (recType !== "log" && anyRpcActive)}
      onclick={() => void startRecording()}
    >
      {recordingStarting ? "…" : t("diagnostics.recordings.start")}
    </Button>
  </Card>

  <!-- Active recording banner -->
  {#if anyLogRunning || anyRpcActive}
    <Card class="p-3">
      <div class="flex items-center gap-2 flex-wrap">
        <span class="inline-block h-2.5 w-2.5 flex-none rounded-full bg-red-500"></span>
        <span class="text-sm font-medium">{t("diagnostics.recordings.running_title")}</span>
        {#if anyRpcActive}
          <span class="text-xs text-[var(--ha-secondary-text-color)]">
            · {t("diagnostics.rpc_recording.running_hint")}
          </span>
          {#each rpcRecordings.filter((r) => r.active && r.ends_at) as rec (rec.central)}
            <Badge variant="muted">
              {rec.central}: {t("diagnostics.recordings.until", { time: formatTimeOnly(rec.ends_at) })}
            </Badge>
          {/each}
        {/if}
        {#if anyRpcActive}
          <div class="ml-auto">
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={rpcRecordingStopping}
              onclick={() => void stopRpcRecording()}
            >
              {rpcRecordingStopping ? "…" : t("diagnostics.rpc_recording.stop")}
            </Button>
          </div>
        {/if}
      </div>
    </Card>
  {/if}

  <!-- Unified recordings list -->
  <Card class="p-4">
    <header class="mb-3 flex items-center justify-between gap-2">
      <h2 class="text-lg font-semibold">{t("diagnostics.recordings.section_title")}</h2>
      <span class="text-xs text-[var(--ha-secondary-text-color)]">{unifiedList.length}</span>
    </header>
    {#if unifiedList.length === 0}
      <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("diagnostics.recordings.empty")}</p>
    {:else}
      <table class="table-reflow w-full text-sm">
        <thead>
          <tr class="border-b border-slate-200 text-left text-xs text-[var(--ha-secondary-text-color)] dark:border-slate-800">
            <th class="pb-1 pr-3 font-medium">{t("diagnostics.recordings.col_type")}</th>
            <th class="pb-1 pr-3 font-medium">{t("diagnostics.recordings.col_scope")}</th>
            <th class="pb-1 pr-3 font-medium">{t("diagnostics.recordings.col_start")}</th>
            <th class="pb-1 pr-3 font-medium">{t("diagnostics.recordings.col_size")}</th>
            <th class="pb-1 font-medium">{t("diagnostics.recordings.col_action")}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-slate-100 dark:divide-slate-800">
          {#each unifiedList as row (row.kind + ":" + row.id)}
            <tr>
              <td class="reflow-title py-2 pr-3">
                {#if row.kind === "log"}
                  <Badge variant="default">{t("diagnostics.recording_type.debug_log")}</Badge>
                {:else}
                  <Badge variant="muted">RPC</Badge>
                {/if}
              </td>
              <td class="py-2 pr-3 font-mono text-xs" data-label={t("diagnostics.recordings.col_scope")}>{row.ccu}</td>
              <td class="py-2 pr-3" data-label={t("diagnostics.recordings.col_start")}>
                <Badge variant={row.statusVariantKey}>{row.status}</Badge>
                {#if row.startedAt}
                  <span class="ml-1 text-xs text-[var(--ha-secondary-text-color)]">{formatDate(row.startedAt)}</span>
                {/if}
              </td>
              <td class="py-2 pr-3 tabular-nums text-xs" data-label={t("diagnostics.recordings.col_size")}>{row.sizeLabel}</td>
              <td class="reflow-actions py-2">
                {#if row.canStop}
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled={captureStopping === row.id}
                    onclick={() => void stopCapture(row.id)}
                  >
                    {captureStopping === row.id ? "…" : t("diagnostics.stop")}
                  </Button>
                {:else if row.kind === "rpc" && row.canDownload}
                  <div class="flex flex-wrap items-center gap-2">
                    {#if row.rpcDownloadMapHref}
                      <a class="text-xs underline" href={row.rpcDownloadMapHref} download rel="noopener">
                        {t("diagnostics.recordings.format_map")}
                      </a>
                    {/if}
                    {#if row.rpcDownloadGoldenHref}
                      <a class="text-xs underline" href={row.rpcDownloadGoldenHref} download rel="noopener">
                        {t("diagnostics.recordings.format_golden")}
                      </a>
                    {/if}
                    {#if row.rpcRandomize}
                      <Badge variant="muted">{t("diagnostics.recordings.anonymised")}</Badge>
                    {/if}
                  </div>
                {:else if row.canDownload}
                  <a
                    class="text-xs underline"
                    href={row.downloadHref}
                    download
                    rel="noopener"
                  >
                    {t("common.download")}
                  </a>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </Card>

  <Card class="p-4">
    <header class="mb-3 flex items-center justify-between">
      <h2 class="text-lg font-semibold">{t("diagnostics.incidents")}</h2>
      <span class="text-xs text-[var(--ha-secondary-text-color)]">{incidents.length}</span>
    </header>
    {#if incidents.length === 0 && !loading}
      <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("diagnostics.empty.incidents")}</p>
    {:else}
      <ul class="space-y-2">
        {#each incidentsSorted as i (i.id)}
          <li class="rounded border border-slate-200 p-2 dark:border-slate-800">
            <div class="flex flex-wrap items-baseline gap-2">
              <Badge variant={severityVariant(i.severity)}>{i.severity}</Badge>
              <span class="font-medium">{i.summary}</span>
              <span class="text-xs text-[var(--ha-secondary-text-color)]">{i.component}</span>
              <span class="text-xs text-[var(--ha-secondary-text-color)]">{formatDate(i.when)}</span>
            </div>
            {#if i.detail}
              <p class="mt-1 whitespace-pre-wrap font-mono text-xs text-slate-600 dark:text-slate-300">{i.detail}</p>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  </Card>
</section>
