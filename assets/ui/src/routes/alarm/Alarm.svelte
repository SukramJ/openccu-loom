<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { alarmPanelStore } from "$lib/stores/alarmPanel.svelte";
  import { t } from "$lib/i18n";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";

  // Section shell for the alarm panel (docs/alarm-concept.md §12). Owns
  // the store lifecycle (WS stream + 1 s countdown ticker + initial
  // fetch) and the tab sub-router; each tab's view is code-split and
  // reads the shared alarmPanelStore. The Setup wizard is not a tab —
  // it is reached from the header action and from the Overview empty
  // state (§12.3, re-runnable per area).

  type Tab = "overview" | "sensors" | "outputs" | "journal" | "walktest";

  type Props = {
    subpath?: string;
  };

  let { subpath = "" }: Props = $props();

  const isWizard = $derived(subpath === "/wizard");

  const activeTab = $derived.by<Tab>(() => {
    if (subpath === "/picker") return "sensors";
    if (subpath === "/outputs") return "outputs";
    if (subpath === "/journal") return "journal";
    if (subpath === "/walktest") return "walktest";
    return "overview";
  });

  // Code-split each view so the alarm subtree stays lean and the view
  // agents can fill them independently.
  const loadOverview = () => import("./AlarmOverview.svelte");
  const loadSensors = () => import("./AlarmSensors.svelte");
  const loadOutputs = () => import("./AlarmOutputs.svelte");
  const loadJournal = () => import("./AlarmJournal.svelte");
  const loadWalkTest = () => import("./AlarmWalkTest.svelte");
  const loadWizard = () => import("./AlarmWizard.svelte");

  const tabs: { tab: Tab; href: string }[] = [
    { tab: "overview", href: "#/alarm" },
    { tab: "sensors", href: "#/alarm/picker" },
    { tab: "outputs", href: "#/alarm/outputs" },
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

<section class="mx-auto max-w-6xl px-4 sm:px-6 py-8">
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
    {/await}
  {:else}
    <!-- Tab bar -->
    <div
      class="mt-2 flex gap-1 overflow-x-auto border-b"
      style="border-color: var(--ha-divider-color);"
      role="tablist"
    >
      {#each tabs as { tab, href } (tab)}
        {@const active = activeTab === tab}
        <a
          {href}
          role="tab"
          aria-selected={active}
          class="flex-1 whitespace-nowrap border-b-2 -mb-px px-4 py-3 text-center text-sm font-medium transition {active
            ? 'border-brand-600 text-brand-600 dark:border-brand-400 dark:text-brand-400'
            : 'border-transparent text-slate-500 dark:text-slate-400'}"
        >
          {t(`alarm.tab.${tab}`)}
        </a>
      {/each}
    </div>

    <!-- Tab content -->
    <div class="mt-4">
      {#if activeTab === "overview"}
        {#await loadOverview()}
          <LoadingState />
        {:then { default: AlarmOverview }}
          <AlarmOverview />
        {/await}
      {:else if activeTab === "sensors"}
        {#await loadSensors()}
          <LoadingState />
        {:then { default: AlarmSensors }}
          <AlarmSensors />
        {/await}
      {:else if activeTab === "outputs"}
        {#await loadOutputs()}
          <LoadingState />
        {:then { default: AlarmOutputs }}
          <AlarmOutputs />
        {/await}
      {:else if activeTab === "journal"}
        {#await loadJournal()}
          <LoadingState />
        {:then { default: AlarmJournal }}
          <AlarmJournal />
        {/await}
      {:else if activeTab === "walktest"}
        {#await loadWalkTest()}
          <LoadingState />
        {:then { default: AlarmWalkTest }}
          <AlarmWalkTest />
        {/await}
      {/if}
    </div>
  {/if}
</section>
