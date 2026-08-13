<script lang="ts">
  import { onMount } from "svelte";
  import { api } from "$lib/api/client";
  import { t } from "$lib/i18n";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import type {
    MatterCompatibility,
    MatterEndpointInfo,
    MatterMdnsDiagnostics,
    MatterSession,
  } from "$lib/api/matter-types";

  let loading = $state(true);
  let error = $state<string | null>(null);
  let sessions = $state<MatterSession[]>([]);
  let mdns = $state<MatterMdnsDiagnostics | null>(null);
  let endpoints = $state<MatterEndpointInfo[]>([]);
  let compat = $state<MatterCompatibility | null>(null);

  async function load() {
    loading = true;
    error = null;
    try {
      const [s, m, e, c] = await Promise.all([
        api.matterSessions(),
        api.matterMdns(),
        api.matterEndpoints(),
        api.matterCompatibility(),
      ]);
      sessions = s.sessions;
      mdns = m;
      endpoints = e.endpoints;
      compat = c;
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  /** Renders a second count as a compact age an operator can scan. */
  function age(seconds: number): string {
    if (seconds < 60) return t("matter.diag.age_seconds", { n: String(seconds) });
    if (seconds < 3600) return t("matter.diag.age_minutes", { n: String(Math.floor(seconds / 60)) });
    return t("matter.diag.age_hours", { n: String(Math.floor(seconds / 3600)) });
  }

  // A controller that stopped talking leaves its session open, so the
  // peer-side age is what separates a live controller from a departed
  // one. The thresholds are deliberately generous: Apple reports every
  // 30–60 s, so anything under five minutes is ordinary quiet.
  function peerVariant(seconds: number): "success" | "warning" | "danger" {
    if (seconds < 300) return "success";
    if (seconds < 1800) return "warning";
    return "danger";
  }

  const errorFindings = $derived((mdns?.findings ?? []).filter((f) => f.severity === "error"));
  const warningFindings = $derived((mdns?.findings ?? []).filter((f) => f.severity === "warning"));
  const bridgedEndpoints = $derived(endpoints.filter((e) => e.endpoint_id > 1));
</script>

<div class="space-y-6">
  <div class="flex items-center justify-between">
    <h2 class="text-lg font-semibold text-slate-900 dark:text-slate-100">
      {t("matter.diag.title")}
    </h2>
    <Button variant="secondary" onclick={load} disabled={loading}>
      {t("common.refresh")}
    </Button>
  </div>

  {#if loading}
    <LoadingState />
  {:else if error}
    <ErrorState message={error} onRetry={load} />
  {:else}
    <!-- Discovery -->
    <Card class="p-4">
      <h3 class="font-medium text-slate-900 dark:text-slate-100">
        {t("matter.diag.discovery")}
      </h3>
      {#if !mdns?.advertising}
        <p class="mt-2 text-sm text-slate-500 dark:text-slate-400">
          {t("matter.diag.not_advertising")}
        </p>
      {:else}
        {#if errorFindings.length === 0 && warningFindings.length === 0}
          <p class="mt-2 text-sm text-emerald-700 dark:text-emerald-400">
            {t("matter.diag.discovery_ok")}
          </p>
        {/if}
        <ul class="mt-2 space-y-2">
          {#each [...errorFindings, ...warningFindings] as finding (finding.code + (finding.service ?? ""))}
            <li class="flex flex-wrap items-start gap-2 text-sm">
              <Badge variant={finding.severity === "error" ? "danger" : "warning"}>
                {t(`matter.diag.severity.${finding.severity}`)}
              </Badge>
              <span class="flex-1 min-w-[16rem] text-slate-700 dark:text-slate-300">
                {finding.message}
              </span>
            </li>
          {/each}
        </ul>
        <div class="mt-3 space-y-1 text-xs text-slate-500 dark:text-slate-400">
          {#each mdns.services as svc (svc.service_type + svc.instance_name)}
            <div>
              <span class="font-mono">{svc.service_type}</span>
              · {t("matter.diag.port")}&nbsp;{svc.port}
              · {svc.addresses.join(", ")}
              {#if svc.subtypes.length > 0}
                · {svc.subtypes.join(" ")}
              {/if}
            </div>
          {/each}
        </div>
      {/if}
    </Card>

    <!-- Controllers / sessions -->
    <Card class="p-4">
      <h3 class="font-medium text-slate-900 dark:text-slate-100">
        {t("matter.diag.sessions")}
      </h3>
      <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">
        {t("matter.diag.sessions_hint")}
      </p>
      {#if sessions.length === 0}
        <div class="mt-3">
          <EmptyState message={t("matter.diag.no_sessions")} />
        </div>
      {:else}
        <div class="mt-3 overflow-x-auto">
          <table class="w-full text-sm">
            <thead>
              <tr class="text-left text-xs uppercase text-slate-500 dark:text-slate-400">
                <th class="py-2 pr-4">{t("matter.diag.col_session")}</th>
                <th class="py-2 pr-4">{t("matter.diag.col_fabric")}</th>
                <th class="py-2 pr-4">{t("matter.diag.col_peer_idle")}</th>
                <th class="py-2 pr-4">{t("matter.diag.col_subscriptions")}</th>
              </tr>
            </thead>
            <tbody>
              {#each sessions as s (s.session_id)}
                <tr class="border-t border-slate-100 dark:border-slate-800">
                  <td class="py-2 pr-4 font-mono text-slate-700 dark:text-slate-300">
                    {s.session_id}
                    {#if s.is_pase}
                      <Badge variant="muted">{t("matter.diag.pase")}</Badge>
                    {/if}
                  </td>
                  <td class="py-2 pr-4 text-slate-700 dark:text-slate-300">{s.fabric_index}</td>
                  <td class="py-2 pr-4">
                    <Badge variant={peerVariant(s.peer_idle_seconds)}>
                      {age(s.peer_idle_seconds)}
                    </Badge>
                  </td>
                  <td class="py-2 pr-4 text-slate-700 dark:text-slate-300">
                    {s.subscriptions}
                    {#if s.subscriptions === 0 && !s.is_pase}
                      <span class="ml-2 text-xs text-amber-600 dark:text-amber-400">
                        {t("matter.diag.no_subscriptions")}
                      </span>
                    {/if}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </Card>

    <!-- Ecosystem compatibility -->
    {#if compat}
      <Card class="p-4">
        <h3 class="font-medium text-slate-900 dark:text-slate-100">
          {t("matter.diag.compatibility")}
        </h3>
        <div class="mt-2 flex flex-wrap gap-2">
          {#each compat.ecosystems as eco (eco.fabric_index)}
            <Badge variant="muted">
              {t(`matter.diag.ecosystem.${eco.ecosystem}`)}
            </Badge>
          {/each}
        </div>
        {#if compat.findings.length === 0}
          <p class="mt-3 text-sm text-emerald-700 dark:text-emerald-400">
            {t("matter.diag.compat_ok")}
          </p>
        {:else}
          <ul class="mt-3 space-y-2">
            {#each compat.findings as finding (finding.ecosystem + finding.code)}
              <li class="flex flex-wrap items-start gap-2 text-sm">
                <Badge variant="warning">
                  {t(`matter.diag.ecosystem.${finding.ecosystem}`)}
                </Badge>
                <span class="flex-1 min-w-[16rem] text-slate-700 dark:text-slate-300">
                  {finding.message}
                </span>
              </li>
            {/each}
          </ul>
        {/if}
      </Card>
    {/if}

    <!-- Endpoint inspector -->
    <Card class="p-4">
      <h3 class="font-medium text-slate-900 dark:text-slate-100">
        {t("matter.diag.endpoints")}
      </h3>
      <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">
        {t("matter.diag.endpoints_hint")}
      </p>
      {#if bridgedEndpoints.length === 0}
        <div class="mt-3">
          <EmptyState message={t("matter.diag.no_endpoints")} />
        </div>
      {:else}
        <ul class="mt-3 space-y-2">
          {#each bridgedEndpoints as ep (ep.endpoint_id)}
            <li class="rounded border border-slate-200 dark:border-slate-700 p-3">
              <div class="flex flex-wrap items-center gap-2">
                <span class="font-mono text-sm text-slate-700 dark:text-slate-300">
                  #{ep.endpoint_id}
                </span>
                <span class="font-medium text-slate-900 dark:text-slate-100">
                  {ep.friendly_name}
                </span>
                <Badge variant={ep.reachable ? "success" : "muted"}>
                  {ep.reachable ? t("matter.diag.reachable") : t("matter.diag.unreachable")}
                </Badge>
                <span class="text-xs text-slate-500 dark:text-slate-400">
                  {ep.device_type_name || `0x${ep.device_type.toString(16)}`}
                </span>
              </div>
              {#if ep.clusters.length > 0}
                <div class="mt-2 flex flex-wrap gap-1">
                  {#each ep.clusters as cluster (cluster.id)}
                    <span
                      class="rounded bg-slate-100 dark:bg-slate-800 px-2 py-0.5 text-xs text-slate-600 dark:text-slate-300"
                    >
                      {cluster.name || `0x${cluster.id.toString(16)}`}
                    </span>
                  {/each}
                </div>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
    </Card>
  {/if}
</div>
