<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { matterStore } from "$lib/stores/matter.svelte";
  import { centralStore } from "$lib/stores/centrals.svelte";
  import { t } from "$lib/i18n";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import PageShell from "$lib/components/ui/PageShell.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import Tabs from "$lib/components/ui/Tabs.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import MatterExposureList from "./MatterExposureList.svelte";
  import MatterFabrics from "./MatterFabrics.svelte";
  import MatterPair from "./MatterPair.svelte";
  import MatterDiagnostics from "./MatterDiagnostics.svelte";

  type Tab = "expose" | "fabrics" | "pair" | "diagnostics";

  type Props = {
    subpath?: string;
  };

  let { subpath = "" }: Props = $props();

  const activeTab = $derived.by<Tab>(() => {
    if (subpath === "/fabrics") return "fabrics";
    if (subpath === "/pair") return "pair";
    if (subpath === "/diagnostics") return "diagnostics";
    return "expose";
  });

  onMount(async () => {
    matterStore.ensureStream();
    // The fleet drives the readiness gate: matter status must know
    // whether at least one CCU is ready before it can tell a 503
    // "disabled" apart from a 503 "still initializing".
    centralStore.ensureStream();
    await centralStore.refresh();
    await matterStore.loadStatus();
  });

  onDestroy(() => {
    matterStore.close();
    centralStore.close();
  });

  const statusEnabled = $derived(matterStore.status?.enabled === true);
</script>

<PageShell>
  <PageHeader title={t("nav.matter")} subtitle={t("matter.subtitle")} />

  {#if matterStore.statusLoading}
    <LoadingState message={t("common.loading")} />
  {:else if !statusEnabled && matterStore.waitingForCcu}
    <Card class="p-4">
      <p class="text-sm font-medium text-slate-500 dark:text-slate-400">
        {t("matter.readiness.waiting")}
      </p>
    </Card>
  {:else if !statusEnabled}
    <Card class="p-4">
      <p class="text-sm font-medium text-slate-500 dark:text-slate-400">
        {t("matter.status.disabled")}
      </p>
    </Card>
  {:else}
    <!-- Status card -->
    {@const s = matterStore.status!}
    <Card class="p-4">
      <div class="flex flex-wrap items-center gap-4">
        <span class="font-medium text-slate-900 dark:text-slate-100">
          {t("matter.status.enabled")}
        </span>
        <Badge variant={s.listening ? "success" : "muted"}>
          {s.listening ? t("matter.status.listening") : t("matter.status.not_listening")}
        </Badge>
        <span class="text-sm text-slate-500 dark:text-slate-400">
          {t("matter.status.endpoints", { count: String(s.endpoint_count) })}
        </span>
        <span class="text-sm text-slate-500 dark:text-slate-400">
          {t("matter.status.fabrics", { count: String(s.fabric_count) })}
        </span>
        {#if s.advertising}
          <Badge variant="warning">{t("matter.status.advertising")}</Badge>
        {/if}
        {#if s.commissioning_window_open}
          <Badge variant="warning">
            {t("matter.pair.window_open")}
            {#if s.commissioning_window_duration_seconds > 0}
              ({s.commissioning_window_duration_seconds}s)
            {/if}
          </Badge>
        {/if}
      </div>
    </Card>

    <Tabs
      class="mt-4"
      active={activeTab}
      items={[
        { key: "expose", label: t("matter.tab.expose"), href: "#/matter/expose" },
        { key: "fabrics", label: t("matter.tab.fabrics"), href: "#/matter/fabrics" },
        { key: "pair", label: t("matter.tab.pair"), href: "#/matter/pair" },
        { key: "diagnostics", label: t("matter.tab.diagnostics"), href: "#/matter/diagnostics" },
      ]}
      fill
    />

    <!-- Tab content -->
    <div class="mt-4">
      {#if activeTab === "expose"}
        <MatterExposureList />
      {:else if activeTab === "fabrics"}
        <MatterFabrics />
      {:else if activeTab === "diagnostics"}
        <MatterDiagnostics />
      {:else}
        <MatterPair />
      {/if}
    </div>
  {/if}
</PageShell>
