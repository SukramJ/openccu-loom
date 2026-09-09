<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { alarmPanelStore } from "$lib/stores/alarmPanel.svelte";
  import { t } from "$lib/i18n";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";

  import PageShell from "$lib/components/ui/PageShell.svelte";
  import Tabs from "$lib/components/ui/Tabs.svelte";
  // Section shell for the alarm panel (docs/alarm-concept.md §12). Owns
  // the store lifecycle (WS stream + 1 s countdown ticker + initial
  // fetch) and the tab sub-router; each tab's view is code-split and
  // reads the shared alarmPanelStore. The Setup wizard is not a tab —
  // it is reached from the header action and from the Overview empty
  // state (§12.3, re-runnable per zone).

  type Tab =
    | "overview"
    | "sensors"
    | "outputs"
    | "policies"
    | "codes"
    | "journal"
    | "walktest";

  type Props = {
    subpath?: string;
  };

  let { subpath = "" }: Props = $props();

  const isWizard = $derived(subpath === "/wizard");

  const activeTab = $derived.by<Tab>(() => {
    if (subpath === "/picker") return "sensors";
    if (subpath === "/outputs") return "outputs";
    if (subpath === "/policies") return "policies";
    if (subpath === "/codes") return "codes";
    if (subpath === "/journal") return "journal";
    if (subpath === "/walktest") return "walktest";
    return "overview";
  });

  // Code-split each view so the alarm subtree stays lean and the view
  // agents can fill them independently.
  const loadOverview = () => import("./AlarmOverview.svelte");
  const loadSensors = () => import("./AlarmSensors.svelte");
  const loadOutputs = () => import("./AlarmOutputs.svelte");
  const loadPolicies = () => import("./AlarmPolicies.svelte");
  const loadCodes = () => import("./AlarmCodes.svelte");
  const loadJournal = () => import("./AlarmJournal.svelte");
  const loadWalkTest = () => import("./AlarmWalkTest.svelte");
  const loadWizard = () => import("./AlarmWizard.svelte");

  const tabs: { tab: Tab; href: string }[] = [
    { tab: "overview", href: "#/alarm" },
    { tab: "sensors", href: "#/alarm/picker" },
    { tab: "outputs", href: "#/alarm/outputs" },
    { tab: "policies", href: "#/alarm/policies" },
    { tab: "codes", href: "#/alarm/codes" },
    { tab: "journal", href: "#/alarm/journal" },
    { tab: "walktest", href: "#/alarm/walktest" },
  ];

  onMount(() => {
    alarmPanelStore.ensureStream();
    void alarmPanelStore.refresh();
  });

  onDestroy(() => {
    alarmPanelStore.close();
  });
</script>

<!--
  Fallback for the code-split tab views. Their chunks are content-hashed, so
  a daemon update under an already-open alarm panel invalidates every chunk
  the panel has not fetched yet and the import rejects. Without a catch
  branch the pending block is torn down and the tab body stays empty, with
  no hint that a reload fixes it.
-->
{#snippet tabLoadFailed()}
  <ErrorState message={t("app.route_load_failed")} onRetry={() => location.reload()} />
{/snippet}

<PageShell>
  <PageHeader title={t("alarm.title")} subtitle={t("alarm.subtitle")}>
    {#snippet actions()}
      {#if !isWizard}
        <a href="#/alarm/wizard">
          <Button variant="outline" size="sm">{t("alarm.wizard.launch")}</Button>
        </a>
      {/if}
    {/snippet}
  </PageHeader>

  {#if isWizard}
    {#await loadWizard()}
      <LoadingState />
    {:then { default: AlarmWizard }}
      <AlarmWizard />
    {:catch}
      {@render tabLoadFailed()}
    {/await}
  {:else}
    <Tabs
      class="mt-2"
      active={activeTab}
      items={tabs.map((x) => ({ key: x.tab, label: t(`alarm.tab.${x.tab}`), href: x.href }))}
      fill
    />

    <!-- Per-tab orientation line: what the active view controls and how
         it relates to the other tabs. -->
    <p class="mt-4 text-sm text-[var(--ha-secondary-text-color)]">
      {t(`alarm.intro.${activeTab}`)}
    </p>

    <!-- Tab content -->
    <div class="mt-4">
      {#if activeTab === "overview"}
        {#await loadOverview()}
          <LoadingState />
        {:then { default: AlarmOverview }}
          <AlarmOverview />
        {:catch}
          {@render tabLoadFailed()}
        {/await}
      {:else if activeTab === "sensors"}
        {#await loadSensors()}
          <LoadingState />
        {:then { default: AlarmSensors }}
          <AlarmSensors />
        {:catch}
          {@render tabLoadFailed()}
        {/await}
      {:else if activeTab === "outputs"}
        {#await loadOutputs()}
          <LoadingState />
        {:then { default: AlarmOutputs }}
          <AlarmOutputs />
        {:catch}
          {@render tabLoadFailed()}
        {/await}
      {:else if activeTab === "policies"}
        {#await loadPolicies()}
          <LoadingState />
        {:then { default: AlarmPolicies }}
          <AlarmPolicies />
        {:catch}
          {@render tabLoadFailed()}
        {/await}
      {:else if activeTab === "codes"}
        {#await loadCodes()}
          <LoadingState />
        {:then { default: AlarmCodes }}
          <AlarmCodes />
        {:catch}
          {@render tabLoadFailed()}
        {/await}
      {:else if activeTab === "journal"}
        {#await loadJournal()}
          <LoadingState />
        {:then { default: AlarmJournal }}
          <AlarmJournal />
        {:catch}
          {@render tabLoadFailed()}
        {/await}
      {:else if activeTab === "walktest"}
        {#await loadWalkTest()}
          <LoadingState />
        {:then { default: AlarmWalkTest }}
          <AlarmWalkTest />
        {:catch}
          {@render tabLoadFailed()}
        {/await}
      {/if}
    </div>
  {/if}
</PageShell>
