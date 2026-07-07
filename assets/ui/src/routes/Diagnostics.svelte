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
  } from "$lib/api/types";
  import type { ReliabilityRow, ValuesCacheStats } from "$lib/api/client";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import { t } from "$lib/i18n";
  import { prefs } from "$lib/stores/preferences.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";
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

  // Reliability + values-cache admin panel. Loaded independently of the
  // rest of the page so a broken breaker/cache read never blocks the
  // interfaces table above it.
  let reliability = $state<ReliabilityRow[]>([]);
  let reliabilityLoading = $state(true);
  let reliabilityError = $state<string | null>(null);
  let valuesCacheStats = $state<ValuesCacheStats | null>(null);
  let valuesCacheLoading = $state(true);
  let valuesCacheError = $state<string | null>(null);
  let valuesCacheResetting = $state(false);

  async function loadReliability() {
    reliabilityLoading = true;
    reliabilityError = null;
    try {
      reliability = await api.getReliability();
    } catch (err) {
      reliabilityError = err instanceof ApiError ? err.message : String(err);
    } finally {
      reliabilityLoading = false;
    }
  }

  async function loadValuesCacheStats() {
    valuesCacheLoading = true;
    valuesCacheError = null;
    try {
      valuesCacheStats = await api.getValuesCacheStats();
    } catch (err) {
      valuesCacheError = err instanceof ApiError ? err.message : String(err);
    } finally {
      valuesCacheLoading = false;
    }
  }

  async function resetValuesCache() {
    const ok = await confirmStore.ask({
      title: t("diagnostics.values_cache.reset_confirm_title"),
      body: t("diagnostics.values_cache.reset_confirm_body"),
      confirmLabel: t("diagnostics.values_cache.reset"),
      destructive: true,
    });
    if (!ok) return;
    valuesCacheResetting = true;
    try {
      await api.resetValuesCache();
      toastStore.success(t("diagnostics.values_cache.reset_success"));
      await loadValuesCacheStats();
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    } finally {
      valuesCacheResetting = false;
    }
  }

  function circuitLabel(state: number): string {
    if (state === 1) return t("diagnostics.reliability.circuit.open");
    if (state === 2) return t("diagnostics.reliability.circuit.half_open");
    return t("diagnostics.reliability.circuit.closed");
  }

  function circuitVariant(state: number): "success" | "danger" | "muted" {
    if (state === 1) return "danger";
    if (state === 2) return "muted";
    return "success";
  }

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
    void loadReliability();
    void loadValuesCacheStats();
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

  // Column definitions for the three DataTable subtables.

  const interfaceCols: DataColumn<InterfaceInfo>[] = $derived([
    { key: "id", label: t("diagnostics.col.interface"), sortable: true, title: true, get: (i) => i.id },
    { key: "type", label: t("diagnostics.col.type"), sortable: true, get: (i) => i.interface },
    { key: "status", label: t("diagnostics.col.status"), sortable: true, get: (i) => (i.connected ? 1 : 0) },
    {
      key: "host",
      label: t("diagnostics.col.host"),
      sortable: true,
      get: (i) => [i.central_id, i.host, i.note].filter(Boolean).join(" · "),
    },
    { key: "action", label: t("diagnostics.col.action"), align: "right", cellClass: "reflow-actions" },
  ]);

  const reliabilityCols: DataColumn<ReliabilityRow>[] = $derived([
    { key: "central", label: t("diagnostics.reliability.col.central"), sortable: true, title: true, get: (r) => r.central },
    { key: "interface", label: t("diagnostics.reliability.col.interface"), sortable: true, get: (r) => r.interface },
    { key: "circuit", label: t("diagnostics.reliability.col.circuit"), sortable: true, get: (r) => r.circuit_state },
    { key: "state", label: t("diagnostics.reliability.col.state"), sortable: true, get: (r) => r.state?.state ?? "" },
    {
      key: "requests",
      label: t("diagnostics.reliability.col.requests"),
      align: "right",
      get: (r) => r.state?.total_requests ?? 0,
    },
    {
      key: "last_failure",
      label: t("diagnostics.reliability.col.last_failure"),
      sortable: true,
      get: (r) => r.state?.last_failure_at ?? "",
    },
    {
      key: "last_callback",
      label: t("diagnostics.reliability.col.last_callback"),
      sortable: true,
      get: (r) => r.state?.last_callback_at ?? "",
    },
  ]);

  const clientCols: DataColumn<DiagnosticsClient>[] = $derived([
    { key: "name", label: t("diagnostics.col.client"), sortable: true, title: true, get: (c) => c.name },
    { key: "status", label: t("diagnostics.col.status"), sortable: true, get: (c) => c.status },
    { key: "score", label: t("diagnostics.col.score"), sortable: true, align: "right", get: (c) => c.score },
    {
      key: "last_ok",
      label: t("diagnostics.last_ok"),
      sortable: true,
      get: (c) => c.last_successful_request ?? "",
    },
    {
      key: "last_fail",
      label: t("diagnostics.last_fail"),
      sortable: true,
      get: (c) => c.last_failed_request ?? "",
    },
    {
      key: "last_event",
      label: t("diagnostics.last_event"),
      sortable: true,
      get: (c) => c.last_event_received ?? "",
    },
    { key: "consec_failures", label: t("diagnostics.consecutive_failures"), sortable: true, align: "right", get: (c) => c.consecutive_failures },
    { key: "reconnect_attempts", label: t("diagnostics.reconnect_attempts"), sortable: true, align: "right", get: (c) => c.reconnect_attempts },
  ]);

  const recordingCols: DataColumn<UnifiedRow>[] = $derived([
    { key: "type", label: t("diagnostics.recordings.col_type"), sortable: true, title: true, get: (r) => r.kind },
    { key: "ccu", label: t("diagnostics.recordings.col_scope"), sortable: true, get: (r) => r.ccu },
    { key: "start", label: t("diagnostics.recordings.col_start"), sortable: true, get: (r) => r.startedAt },
    { key: "size", label: t("diagnostics.recordings.col_size"), sortable: true, get: (r) => r.sizeLabel },
    { key: "action", label: t("diagnostics.recordings.col_action"), align: "right", cellClass: "reflow-actions" },
  ]);
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
      <DataTable
        rows={clients}
        columns={clientCols}
        rowKey={(c) => c.name}
        emptyMessage={t("diagnostics.empty.components")}
      >
        {#snippet cell(row, col)}
          {#if col.key === "name"}
            <span class="font-mono text-sm">{row.name}</span>
          {:else if col.key === "status"}
            <div class="flex flex-wrap items-center gap-1">
              <Badge variant={statusVariant(row.status)}>{row.status}</Badge>
              {#if row.in_recovery}
                <Badge variant="warning">{t("diagnostics.in_recovery")}</Badge>
              {/if}
            </div>
          {:else if col.key === "score"}
            <span class="font-semibold tabular-nums">{row.score}%</span>
          {:else if col.key === "last_ok"}
            <span class="text-xs">{row.last_successful_request ? formatDate(row.last_successful_request) : "—"}</span>
          {:else if col.key === "last_fail"}
            <span class="text-xs">{row.last_failed_request ? formatDate(row.last_failed_request) : "—"}</span>
          {:else if col.key === "last_event"}
            <span class="text-xs">{row.last_event_received ? formatDate(row.last_event_received) : "—"}</span>
          {:else if col.key === "consec_failures"}
            <span class="tabular-nums">{row.consecutive_failures}</span>
          {:else if col.key === "reconnect_attempts"}
            <span class="tabular-nums">{row.reconnect_attempts}</span>
          {/if}
        {/snippet}
      </DataTable>
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
    <DataTable
      rows={interfaces}
      columns={interfaceCols}
      rowKey={(i) => i.id}
      emptyMessage={t("diagnostics.empty.interfaces")}
    >
      {#snippet cell(row, col)}
        {#if col.key === "id"}
          <span class="font-mono text-sm">{row.id}</span>
        {:else if col.key === "type"}
          <Badge variant="muted">{row.interface}</Badge>
        {:else if col.key === "status"}
          {#if row.connected}
            <Badge variant="success">{t("diagnostics.connected")}</Badge>
          {:else}
            <Badge variant="danger">{t("diagnostics.disconnected")}</Badge>
          {/if}
        {:else if col.key === "host"}
          <span class="text-xs text-[var(--ha-secondary-text-color)]">
            {[row.central_id, row.host, row.note].filter(Boolean).join(" · ")}
          </span>
        {:else if col.key === "action"}
          <Button
            type="button"
            variant="outline"
            size="sm"
            onclick={() => void reconnect(row.id)}
            disabled={reconnecting === row.id}
          >
            {reconnecting === row.id ? "…" : t("diagnostics.reconnect")}
          </Button>
        {/if}
      {/snippet}
    </DataTable>
  </Card>

  <Card class="p-4">
    <header class="mb-3 flex items-center justify-between gap-2">
      <h2 class="text-lg font-semibold">{t("diagnostics.reliability.title")}</h2>
      <span class="text-xs text-[var(--ha-secondary-text-color)]">{reliability.length}</span>
    </header>
    <p class="mb-3 text-sm text-[var(--ha-secondary-text-color)]">
      {t("diagnostics.reliability.help")}
    </p>
    {#if reliabilityLoading}
      <LoadingState />
    {:else if reliabilityError}
      <ErrorState message={reliabilityError} onRetry={() => void loadReliability()} />
    {:else}
      <DataTable
        rows={reliability}
        columns={reliabilityCols}
        rowKey={(r) => `${r.central}/${r.interface}`}
        emptyMessage={t("diagnostics.reliability.empty")}
      >
        {#snippet cell(row, col)}
          {#if col.key === "central"}
            <span class="font-mono text-sm">{row.central}</span>
          {:else if col.key === "interface"}
            <span class="font-mono text-sm">{row.interface}</span>
          {:else if col.key === "circuit"}
            <Badge variant={circuitVariant(row.circuit_state)}>{circuitLabel(row.circuit_state)}</Badge>
          {:else if col.key === "state"}
            <span class="text-xs">{row.state?.state ?? "—"}</span>
          {:else if col.key === "requests"}
            <span class="text-xs tabular-nums">
              {row.state?.total_requests ?? 0} / {row.state?.executed_requests ?? 0} / {row.state?.pending_requests ?? 0}
            </span>
          {:else if col.key === "last_failure"}
            <span class="text-xs">{row.state?.last_failure_at ? formatDate(row.state.last_failure_at) : "—"}</span>
          {:else if col.key === "last_callback"}
            <span class="text-xs">{row.state?.last_callback_at ? formatDate(row.state.last_callback_at) : "—"}</span>
          {/if}
        {/snippet}
      </DataTable>
    {/if}
  </Card>

  <Card class="p-4">
    <header class="mb-3 flex items-center justify-between gap-2">
      <h2 class="text-lg font-semibold">{t("diagnostics.values_cache.title")}</h2>
      <Button
        type="button"
        variant="destructive"
        size="sm"
        onclick={() => void resetValuesCache()}
        disabled={valuesCacheResetting || valuesCacheLoading || !!valuesCacheError}
      >
        {valuesCacheResetting ? "…" : t("diagnostics.values_cache.reset")}
      </Button>
    </header>
    <p class="mb-3 text-sm text-[var(--ha-secondary-text-color)]">
      {t("diagnostics.values_cache.help")}
    </p>
    {#if valuesCacheLoading}
      <LoadingState />
    {:else if valuesCacheError}
      <ErrorState message={valuesCacheError} onRetry={() => void loadValuesCacheStats()} />
    {:else if valuesCacheStats}
      <dl class="grid grid-cols-2 gap-3 text-sm sm:grid-cols-4">
        <div>
          <dt class="text-xs text-[var(--ha-secondary-text-color)]">{t("diagnostics.values_cache.rows")}</dt>
          <dd class="font-semibold tabular-nums">{valuesCacheStats.rows}</dd>
        </div>
        <div>
          <dt class="text-xs text-[var(--ha-secondary-text-color)]">{t("diagnostics.values_cache.bytes")}</dt>
          <dd class="font-semibold tabular-nums">{valuesCacheStats.value_json_bytes}</dd>
        </div>
        <div>
          <dt class="text-xs text-[var(--ha-secondary-text-color)]">{t("diagnostics.values_cache.restored")}</dt>
          <dd class="font-semibold tabular-nums">{valuesCacheStats.restored_rows}</dd>
        </div>
        <div>
          <dt class="text-xs text-[var(--ha-secondary-text-color)]">{t("diagnostics.values_cache.cast_failures")}</dt>
          <dd class="font-semibold tabular-nums">{valuesCacheStats.cast_failures}</dd>
        </div>
        <div>
          <dt class="text-xs text-[var(--ha-secondary-text-color)]">{t("diagnostics.values_cache.gc_deleted")}</dt>
          <dd class="font-semibold tabular-nums">{valuesCacheStats.gc_rows_deleted}</dd>
        </div>
        <div>
          <dt class="text-xs text-[var(--ha-secondary-text-color)]">{t("diagnostics.values_cache.flush_batches")}</dt>
          <dd class="font-semibold tabular-nums">{valuesCacheStats.flush_batches}</dd>
        </div>
        <div>
          <dt class="text-xs text-[var(--ha-secondary-text-color)]">{t("diagnostics.values_cache.flushed_entries")}</dt>
          <dd class="font-semibold tabular-nums">{valuesCacheStats.flushed_entries}</dd>
        </div>
      </dl>
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
    <DataTable
      rows={unifiedList}
      columns={recordingCols}
      rowKey={(r) => r.kind + ":" + r.id}
      emptyMessage={t("diagnostics.recordings.empty")}
    >
      {#snippet cell(row, col)}
        {#if col.key === "type"}
          {#if row.kind === "log"}
            <Badge variant="default">{t("diagnostics.recording_type.debug_log")}</Badge>
          {:else}
            <Badge variant="muted">RPC</Badge>
          {/if}
        {:else if col.key === "ccu"}
          <span class="font-mono text-xs">{row.ccu}</span>
        {:else if col.key === "start"}
          <Badge variant={row.statusVariantKey}>{row.status}</Badge>
          {#if row.startedAt}
            <span class="ml-1 text-xs text-[var(--ha-secondary-text-color)]">{formatDate(row.startedAt)}</span>
          {/if}
        {:else if col.key === "size"}
          <span class="tabular-nums text-xs">{row.sizeLabel}</span>
        {:else if col.key === "action"}
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
            <a class="text-xs underline" href={row.downloadHref} download rel="noopener">
              {t("common.download")}
            </a>
          {/if}
        {/if}
      {/snippet}
    </DataTable>
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
