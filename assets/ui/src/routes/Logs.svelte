<script lang="ts">
  import { onMount, onDestroy, tick } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { LogRecord } from "$lib/api/types";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import { t } from "$lib/i18n";
  import { toastStore } from "$lib/stores/toast.svelte";

  type Props = { locale: string };
  let { locale }: Props = $props();

  // ── State ─────────────────────────────────────────────────────────
  let records = $state<LogRecord[]>([]);
  let lastSeq = $state(0);
  let view = $state<"aggregated" | "detail">("aggregated");
  let defaultLevel = $state("info");
  let filterText = $state("");
  let following = $state(true);
  let pausedCount = $state(0);
  let loadError = $state<string | null>(null);
  let sseLive = $state(false);
  let sseReconnecting = $state(false);
  let expandedSeqs = $state<Set<number>>(new Set());
  let expandedGroups = $state<Set<string>>(new Set());
  let downloadLimit = $state(500);

  let scrollEl = $state<HTMLDivElement | null>(null);
  let eventSource: EventSource | null = null;

  const SCROLL_THRESHOLD = 60;
  const MAX_RECORDS = 5000;
  const LEVEL_ORDER = ["debug", "info", "warn", "error"] as const;

  // ── Normalise message for dedup key ───────────────────────────────
  function normalizeMsg(msg: string): string {
    // Strip volatile tokens: digit runs, 8+ hex char runs, UUIDs, elapsed_ms patterns
    return msg
      .replace(/\b[0-9a-fA-F]{8,}\b/g, "*")
      .replace(/\b\d+(\.\d+)?(ms|s|m|h)?\b/g, "*")
      .trim();
  }

  function groupKey(r: LogRecord): string {
    return `${r.level}|${r.logger ?? ""}|${normalizeMsg(r.msg)}`;
  }

  // ── Client-side text filter ────────────────────────────────────────
  function matchesFilter(r: LogRecord): boolean {
    const q = filterText.trim().toLowerCase();
    if (!q) return true;
    const hay = [
      r.msg,
      r.logger ?? "",
      r.level,
      JSON.stringify(r.attrs ?? {}),
    ]
      .join(" ")
      .toLowerCase();
    return hay.includes(q);
  }

  // ── Aggregated view rows ──────────────────────────────────────────
  type AggRow = {
    key: string;
    last: LogRecord;
    count: number;
    seqs: number[];
  };

  const aggregatedRows = $derived.by<AggRow[]>(() => {
    const map = new Map<string, AggRow>();
    const levelIdx = (l: string) => {
      const i = LEVEL_ORDER.indexOf(l as typeof LEVEL_ORDER[number]);
      return i === -1 ? 1 : i;
    };
    for (const r of records) {
      if (levelIdx(r.level) < levelIdx("warn")) continue;
      if (!matchesFilter(r)) continue;
      const k = groupKey(r);
      const existing = map.get(k);
      if (existing) {
        existing.count++;
        existing.last = r;
        existing.seqs.push(r.seq);
      } else {
        map.set(k, { key: k, last: r, count: 1, seqs: [r.seq] });
      }
    }
    return Array.from(map.values());
  });

  const detailRows = $derived.by<LogRecord[]>(() =>
    records.filter(matchesFilter),
  );

  // ── Level badge variant ────────────────────────────────────────────
  function levelVariant(level: string): "danger" | "warning" | "muted" | "default" {
    if (level === "error") return "danger";
    if (level === "warn") return "warning";
    if (level === "debug") return "muted";
    return "default";
  }

  // ── Time formatting ────────────────────────────────────────────────
  function formatTime(iso: string): string {
    try {
      const d = new Date(iso);
      const h = String(d.getHours()).padStart(2, "0");
      const m = String(d.getMinutes()).padStart(2, "0");
      const s = String(d.getSeconds()).padStart(2, "0");
      const ms = String(d.getMilliseconds()).padStart(3, "0");
      return `${h}:${m}:${s}.${ms}`;
    } catch {
      return iso;
    }
  }

  // ── Auto-scroll ────────────────────────────────────────────────────
  async function scrollToBottom() {
    await tick();
    if (scrollEl) {
      scrollEl.scrollTop = scrollEl.scrollHeight;
    }
  }

  function isNearBottom(): boolean {
    if (!scrollEl) return true;
    return (
      scrollEl.scrollHeight - scrollEl.scrollTop - scrollEl.clientHeight <=
      SCROLL_THRESHOLD
    );
  }

  function onScroll() {
    if (!scrollEl) return;
    if (isNearBottom()) {
      if (!following) {
        following = true;
        pausedCount = 0;
      }
    } else {
      if (following) {
        following = false;
      }
    }
  }

  // ── Append records from SSE / seed ────────────────────────────────
  function appendRecords(incoming: LogRecord[]) {
    if (incoming.length === 0) return;
    // Dedupe by seq
    const maxSeen = lastSeq;
    const fresh = incoming.filter((r) => r.seq > maxSeen);
    if (fresh.length === 0) return;
    const maxFresh = Math.max(...fresh.map((r) => r.seq));
    if (maxFresh > lastSeq) lastSeq = maxFresh;

    let next = [...records, ...fresh];
    if (next.length > MAX_RECORDS) {
      next = next.slice(next.length - MAX_RECORDS);
    }
    records = next;

    if (following) {
      void scrollToBottom();
    } else {
      pausedCount += fresh.length;
    }
  }

  // ── SSE connection ─────────────────────────────────────────────────
  function openStream() {
    if (eventSource) {
      eventSource.close();
      eventSource = null;
    }
    const url = api.logsStreamUrl({ since: lastSeq, minLevel: "debug" });
    const es = new EventSource(url, { withCredentials: true });
    eventSource = es;

    es.addEventListener("log", (ev: MessageEvent) => {
      sseReconnecting = false;
      sseLive = true;
      try {
        const r = JSON.parse((ev as MessageEvent).data) as LogRecord;
        appendRecords([r]);
      } catch {
        // ignore parse errors
      }
    });

    es.addEventListener("open", () => {
      sseLive = true;
      sseReconnecting = false;
    });

    es.addEventListener("error", () => {
      sseLive = false;
      sseReconnecting = true;
      // EventSource auto-reconnects; we just reflect the state.
    });
  }

  // ── Resume following ───────────────────────────────────────────────
  function resumeLive() {
    following = true;
    pausedCount = 0;
    void scrollToBottom();
  }

  // ── Level change ───────────────────────────────────────────────────
  async function onLevelChange(level: string) {
    defaultLevel = level;
    try {
      await api.setDefaultLogLevel(level);
      toastStore.success(t("logs.level_saved"));
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    }
  }

  // ── Toggle row expansion ───────────────────────────────────────────
  function toggleSeq(seq: number) {
    const next = new Set(expandedSeqs);
    if (next.has(seq)) next.delete(seq);
    else next.add(seq);
    expandedSeqs = next;
  }

  function toggleGroup(key: string) {
    const next = new Set(expandedGroups);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    expandedGroups = next;
  }

  // ── Lifecycle ──────────────────────────────────────────────────────
  onMount(async () => {
    try {
      const [seed, level] = await Promise.all([
        api.getLogs({ limit: 500, minLevel: "debug" }),
        api.getDefaultLogLevel(),
      ]);
      defaultLevel = level;
      // Append first (it derives lastSeq from the records); then take the
      // server's last_seq as the floor so the stream resumes from the
      // true tail even if min-level filtering hid the newest records.
      appendRecords(seed.records);
      lastSeq = Math.max(lastSeq, seed.last_seq);
    } catch (err) {
      if (err instanceof ApiError && err.status === 403) {
        loadError = t("logs.forbidden");
      } else {
        loadError = err instanceof Error ? err.message : String(err);
      }
    }
    openStream();
  });

  onDestroy(() => {
    eventSource?.close();
    eventSource = null;
  });
</script>

<section class="mx-auto max-w-6xl px-4 py-6 sm:px-6">
  <!-- Header -->
  <header class="mb-4 flex flex-wrap items-center justify-between gap-3">
    <div>
      <h1 class="text-2xl font-semibold">{t("logs.title")}</h1>
      <p class="text-sm" style="color: var(--ha-secondary-text-color);">
        {t("logs.subtitle")}
      </p>
    </div>
    <!-- SSE connection badge -->
    <div class="flex items-center gap-2">
      <span class="flex items-center gap-1 text-xs">
        <span
          class="inline-block h-2 w-2 rounded-full {sseLive
            ? 'bg-emerald-500'
            : sseReconnecting
              ? 'animate-pulse bg-amber-400'
              : 'bg-slate-400'}"
        ></span>
        <span style="color: var(--ha-secondary-text-color);">
          {sseLive
            ? t("logs.connection.live")
            : sseReconnecting
              ? t("logs.connection.reconnecting")
              : "—"}
        </span>
      </span>
    </div>
  </header>

  <!-- Toolbar -->
  <div
    class="mb-3 flex flex-wrap items-center gap-2 rounded-lg border p-2"
    style="background-color: var(--ha-secondary-background-color); border-color: var(--ha-divider-color);"
  >
    <!-- View toggle -->
    <div
      class="flex overflow-hidden rounded-md border text-xs"
      style="border-color: var(--ha-divider-color);"
    >
      <button
        type="button"
        class="px-3 py-1.5 font-medium transition"
        style="background-color: {view === 'aggregated'
          ? 'var(--ha-primary-color)'
          : 'transparent'}; color: {view === 'aggregated'
          ? 'white'
          : 'var(--ha-primary-text-color)'};"
        onclick={() => (view = "aggregated")}
      >
        {t("logs.view.aggregated")}
      </button>
      <button
        type="button"
        class="px-3 py-1.5 font-medium transition"
        style="background-color: {view === 'detail'
          ? 'var(--ha-primary-color)'
          : 'transparent'}; color: {view === 'detail'
          ? 'white'
          : 'var(--ha-primary-text-color)'};"
        onclick={() => (view = "detail")}
      >
        {t("logs.view.detail")}
      </button>
    </div>

    <!-- Default level select -->
    <div class="flex items-center gap-1">
      <span class="text-xs" style="color: var(--ha-secondary-text-color);"
        >{t("logs.default_level")}:</span
      >
      <select
        value={defaultLevel}
        onchange={(e) => void onLevelChange((e.target as HTMLSelectElement).value)}
        class="rounded border px-2 py-1 text-xs"
        style="border-color: var(--ha-divider-color); background-color: var(--ha-card-background-color); color: var(--ha-primary-text-color);"
      >
        {#each ["debug", "info", "warn", "error"] as lvl (lvl)}
          <option value={lvl}>{lvl}</option>
        {/each}
      </select>
    </div>

    <!-- Text filter -->
    <input
      type="search"
      placeholder={t("logs.filter_placeholder")}
      bind:value={filterText}
      class="w-full rounded border px-2 py-1 text-xs sm:w-56"
      style="border-color: var(--ha-divider-color); background-color: var(--ha-card-background-color); color: var(--ha-primary-text-color);"
    />

    <!-- Live indicator -->
    <span class="flex items-center gap-1 text-xs font-medium">
      <span
        class="inline-block h-2 w-2 rounded-full {following
          ? 'animate-pulse bg-emerald-500'
          : 'bg-slate-400'}"
      ></span>
      {following ? t("logs.live") : t("logs.paused")}
    </span>

    <!-- Download + diagnostics: grouped so they wrap together on small screens -->
    <div class="ml-auto flex flex-wrap items-center gap-2">
      <div class="flex items-center gap-1">
        <span class="text-xs" style="color: var(--ha-secondary-text-color);"
          >{t("logs.download")}:</span
        >
        <select
          bind:value={downloadLimit}
          class="rounded border px-1 py-1 text-xs"
          style="border-color: var(--ha-divider-color); background-color: var(--ha-card-background-color); color: var(--ha-primary-text-color);"
        >
          {#each [100, 200, 500, 1000, 2000, 5000] as n (n)}
            <option value={n}>{t("logs.download_last", { count: n })}</option>
          {/each}
        </select>
        <a
          href={api.logsDownloadUrl({ limit: downloadLimit, minLevel: defaultLevel })}
          download
          class="rounded border px-2 py-1 text-xs font-medium transition hover:opacity-80"
          style="border-color: var(--ha-divider-color); color: var(--ha-primary-text-color);"
        >
          ↓
        </a>
      </div>
      <a
        href="#/diagnostics"
        class="text-xs"
        style="color: var(--ha-primary-color);"
      >
        ↪ {t("diagnostics.capture")}…
      </a>
    </div>
  </div>

  <!-- Error state -->
  {#if loadError}
    <Card class="p-4 text-sm" style="color: var(--ha-error-color, #dc2626);">
      {loadError}
    </Card>
  {:else if records.length === 0}
    <Card class="p-6 text-center text-sm" style="color: var(--ha-secondary-text-color);">
      {t("logs.empty")}
    </Card>
  {:else}
    <!-- Scrollable log window -->
    <div
      class="relative overflow-hidden rounded-lg border"
      style="border-color: var(--ha-divider-color);"
    >
      <div
        bind:this={scrollEl}
        onscroll={onScroll}
        class="h-[calc(100vh-300px)] min-h-[320px] overflow-y-auto font-mono text-xs"
        style="background-color: var(--ha-card-background-color);"
      >
        {#if view === "detail"}
          {#each detailRows as r (r.seq)}
            {@const expanded = expandedSeqs.has(r.seq)}
            <div
              class="border-b px-3 py-1 hover:bg-slate-50 dark:hover:bg-slate-800/50"
              style="border-color: var(--ha-divider-color);"
            >
              <button
                type="button"
                class="flex w-full flex-wrap items-baseline gap-2 text-left"
                onclick={() => r.attrs && toggleSeq(r.seq)}
              >
                <span class="w-20 shrink-0 text-[var(--ha-secondary-text-color)] sm:w-28">
                  {formatTime(r.time)}
                </span>
                <Badge variant={levelVariant(r.level)}>
                  {r.level}
                </Badge>
                {#if r.logger}
                  <span class="text-[var(--ha-secondary-text-color)]"
                    >{r.logger}</span
                  >
                {/if}
                <span class="flex-1 break-all">{r.msg}</span>
                {#if r.attrs && Object.keys(r.attrs).length > 0}
                  <span class="text-[10px] text-[var(--ha-disabled-text-color)]"
                    >+attrs</span
                  >
                {/if}
              </button>
              {#if expanded && r.attrs}
                <pre
                  class="mt-1 overflow-x-auto rounded bg-slate-100 p-2 text-[10px] dark:bg-slate-900"
                >{JSON.stringify(r.attrs, null, 2)}</pre>
              {/if}
            </div>
          {/each}
        {:else}
          <!-- Aggregated -->
          {#each aggregatedRows as row (row.key)}
            {@const groupExpanded = expandedGroups.has(row.key)}
            <div
              class="border-b px-3 py-1 hover:bg-slate-50 dark:hover:bg-slate-800/50"
              style="border-color: var(--ha-divider-color);"
            >
              <button
                type="button"
                class="flex w-full flex-wrap items-baseline gap-2 text-left"
                onclick={() => toggleGroup(row.key)}
              >
                <span class="w-20 shrink-0 text-[var(--ha-secondary-text-color)] sm:w-28">
                  {formatTime(row.last.time)}
                </span>
                <Badge variant={levelVariant(row.last.level)}>
                  {row.last.level}
                </Badge>
                {#if row.last.logger}
                  <span class="text-[var(--ha-secondary-text-color)]"
                    >{row.last.logger}</span
                  >
                {/if}
                <span class="flex-1 break-all">{row.last.msg}</span>
                {#if row.count > 1}
                  <Badge variant="muted" class="ml-auto shrink-0">
                    {t("logs.repeated", { count: row.count })}
                  </Badge>
                {/if}
              </button>
              {#if groupExpanded && row.count > 1}
                <div class="ml-28 mt-1 space-y-0.5">
                  {#each row.seqs as seq (seq)}
                    {@const rec = records.find((r) => r.seq === seq)}
                    {#if rec}
                      <div class="flex gap-2 text-[10px] text-[var(--ha-secondary-text-color)]">
                        <span>{formatTime(rec.time)}</span>
                        <span>{rec.msg}</span>
                      </div>
                    {/if}
                  {/each}
                </div>
              {/if}
            </div>
          {/each}
        {/if}
      </div>

      <!-- Floating pill: paused / new records -->
      {#if !following && pausedCount > 0}
        <div class="absolute bottom-4 left-1/2 -translate-x-1/2">
          <button
            type="button"
            class="rounded-full px-4 py-2 text-xs font-semibold shadow-lg transition hover:opacity-90"
            style="background-color: var(--ha-primary-color); color: white;"
            onclick={resumeLive}
          >
            {t("logs.to_live", { count: pausedCount })}
          </button>
        </div>
      {/if}
    </div>

    <!-- Record count footer -->
    <p class="mt-1 text-right text-[10px]" style="color: var(--ha-secondary-text-color);">
      {#if view === "detail"}
        {detailRows.length} / {records.length}
      {:else}
        {aggregatedRows.length}
        {locale === "de" ? "Gruppen" : "groups"} / {records.length}
      {/if}
    </p>
  {/if}
</section>
