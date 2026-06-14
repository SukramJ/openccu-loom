<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { matterStore } from "$lib/stores/matter.svelte";
  import { t } from "$lib/i18n";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import MatterExposureList from "./MatterExposureList.svelte";
  import MatterFabrics from "./MatterFabrics.svelte";
  import MatterPair from "./MatterPair.svelte";

  type Tab = "expose" | "fabrics" | "pair";

  type Props = {
    subpath?: string;
  };

  let { subpath = "" }: Props = $props();

  const activeTab = $derived.by<Tab>(() => {
    if (subpath === "/fabrics") return "fabrics";
    if (subpath === "/pair") return "pair";
    return "expose";
  });

  onMount(async () => {
    matterStore.ensureStream();
    await matterStore.loadStatus();
  });

  onDestroy(() => {
    matterStore.close();
  });

  const statusEnabled = $derived(matterStore.status?.enabled === true);
</script>

<section class="mx-auto max-w-6xl px-4 sm:px-6 py-8">
  <h1 class="text-2xl font-semibold" style="color: var(--ha-primary-text-color);">
    {t("nav.matter")}
  </h1>

  {#if matterStore.statusLoading}
    <p class="mt-4 text-sm" style="color: var(--ha-secondary-text-color);">
      {t("common.loading")}
    </p>
  {:else if !statusEnabled}
    <Card class="mt-6 p-6">
      <p class="text-sm font-medium" style="color: var(--ha-secondary-text-color);">
        {t("matter.status.disabled")}
      </p>
    </Card>
  {:else}
    <!-- Status card -->
    {@const s = matterStore.status!}
    <Card class="mt-4 p-4">
      <div class="flex flex-wrap items-center gap-4">
        <span class="font-medium" style="color: var(--ha-primary-text-color);">
          {t("matter.status.enabled")}
        </span>
        <Badge variant={s.listening ? "success" : "muted"}>
          {s.listening ? t("matter.status.listening") : t("matter.status.not_listening")}
        </Badge>
        <span class="text-sm" style="color: var(--ha-secondary-text-color);">
          {t("matter.status.endpoints", { count: String(s.endpoint_count) })}
        </span>
        <span class="text-sm" style="color: var(--ha-secondary-text-color);">
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

    <!-- Tab bar -->
    <div
      class="mt-4 flex gap-1 border-b overflow-x-auto"
      style="border-color: var(--ha-divider-color);"
      role="tablist"
    >
      {#each ([["expose", "#/matter/expose"], ["fabrics", "#/matter/fabrics"], ["pair", "#/matter/pair"]] as const) as [tab, href]}
        {@const active = activeTab === tab}
        <a
          {href}
          role="tab"
          aria-selected={active}
          class="flex-1 text-center px-4 py-3 text-sm font-medium transition border-b-2 -mb-px whitespace-nowrap"
          style="color: {active ? 'var(--ha-primary-color)' : 'var(--ha-secondary-text-color)'}; border-color: {active ? 'var(--ha-primary-color)' : 'transparent'};"
        >
          {t(`matter.tab.${tab}`)}
        </a>
      {/each}
    </div>

    <!-- Tab content -->
    <div class="mt-4">
      {#if activeTab === "expose"}
        <MatterExposureList />
      {:else if activeTab === "fabrics"}
        <MatterFabrics />
      {:else}
        <MatterPair />
      {/if}
    </div>
  {/if}
</section>
