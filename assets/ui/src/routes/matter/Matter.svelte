<!-- The Matter area: loom's page frame and readiness gate around the node
     pages of @sukramj/fabric-ui. The pages read the fabric-ui context
     provided here; the tabs map them onto loom's hash routes. -->
<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import {
    NodeDiagnostics,
    NodeEndpoints,
    NodeFabrics,
    NodePair,
    NodeSelection,
    NodeStatus,
    NodeSurface,
    provideFabricUI,
  } from "@sukramj/fabric-ui";
  import { fabricUI, matterRoutes } from "$lib/stores/fabric-node.svelte";
  import { centralStore } from "$lib/stores/centrals.svelte";
  import { t } from "$lib/i18n";
  import Card from "$lib/components/ui/Card.svelte";
  import PageShell from "$lib/components/ui/PageShell.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import Tabs from "$lib/components/ui/Tabs.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";

  type Props = {
    subpath?: string;
  };

  let { subpath = "" }: Props = $props();

  const ui = provideFabricUI(fabricUI);
  const node = ui.node;

  const pages = {
    status: NodeStatus,
    pair: NodePair,
    fabrics: NodeFabrics,
    selection: NodeSelection,
    endpoints: NodeEndpoints,
    surface: NodeSurface,
    diagnostics: NodeDiagnostics,
  };
  type Page = keyof typeof pages;

  const activeTab = $derived.by<Page>(() => {
    const key = subpath.replace(/^\//, "").split(/[/?]/)[0];
    // The selection page was the "expose" tab; old bookmarks keep working.
    if (key === "expose") return "selection";
    return key in pages ? (key as Page) : "status";
  });

  onMount(async () => {
    node.connect();
    // The fleet drives the readiness gate: the daemon answers the node
    // status with an error while no CCU has finished its readiness-gated
    // bring-up, which must not read as "disabled".
    centralStore.ensureStream();
    await centralStore.refresh();
    await node.loadStatus();
  });

  onDestroy(() => {
    node.close();
    centralStore.close();
  });

  const statusEnabled = $derived(node.status?.enabled === true);
  const waitingForCcu = $derived(
    node.status === null && node.statusError !== null && !centralStore.anyReady,
  );

  const tabs = $derived(
    (Object.keys(pages) as Page[])
      // The selection page needs a provider; a node without one has none.
      .filter((p) => p !== "selection" || node.status?.exposure)
      .map((p) => ({ key: p, label: ui.t(`node.tab.${p}`), href: matterRoutes[p] })),
  );

  const Current = $derived(pages[activeTab]);
</script>

<PageShell>
  <PageHeader title={t("nav.matter")} subtitle={t("matter.subtitle")} />

  {#if node.statusLoading && node.status === null}
    <LoadingState message={t("common.loading")} />
  {:else if !statusEnabled && waitingForCcu}
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
    <Tabs class="mt-4" active={activeTab} items={tabs} fill />
    <div class="mt-4">
      <Current />
    </div>
  {/if}
</PageShell>
