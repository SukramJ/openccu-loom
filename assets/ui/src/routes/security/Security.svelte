<script lang="ts">
  import { t } from "$lib/i18n";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";

  // Section shell for the Security & Safety domain
  // (docs/security-safety-concept.md §7.8). Runs independently of the
  // alarm engine — a smoke/water/gas-only installation still gets the
  // hazard classes and the fault ledger, it just never has zones. Owns
  // the tab sub-router only; each tab fetches its own data (there is no
  // WS push for this domain yet, so no shared store like alarmPanelStore).

  type Tab = "overview" | "sources" | "faults";

  type Props = {
    subpath?: string;
  };

  let { subpath = "" }: Props = $props();

  const activeTab = $derived.by<Tab>(() => {
    if (subpath === "/sources") return "sources";
    if (subpath === "/faults") return "faults";
    return "overview";
  });

  // Code-split each view so installs that never open this domain don't
  // pay for its JS, mirroring the alarm subtree.
  const loadOverview = () => import("./SecurityOverview.svelte");
  const loadSources = () => import("./SecuritySources.svelte");
  const loadFaults = () => import("./SecurityFaults.svelte");

  const tabs: { tab: Tab; href: string }[] = [
    { tab: "overview", href: "#/security" },
    { tab: "sources", href: "#/security/sources" },
    { tab: "faults", href: "#/security/faults" },
  ];
</script>

<section class="mx-auto max-w-6xl px-4 sm:px-6 py-8">
  <PageHeader title={t("security.title")} subtitle={t("security.subtitle")} />

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
        {t(`security.tab.${tab}`)}
      </a>
    {/each}
  </div>

  <!-- Per-tab orientation line. -->
  <p class="mt-4 text-sm text-[var(--ha-secondary-text-color)]">
    {t(`security.intro.${activeTab}`)}
  </p>

  <!-- Tab content -->
  <div class="mt-4">
    {#if activeTab === "overview"}
      {#await loadOverview()}
        <LoadingState />
      {:then { default: SecurityOverview }}
        <SecurityOverview />
      {/await}
    {:else if activeTab === "sources"}
      {#await loadSources()}
        <LoadingState />
      {:then { default: SecuritySources }}
        <SecuritySources />
      {/await}
    {:else if activeTab === "faults"}
      {#await loadFaults()}
        <LoadingState />
      {:then { default: SecurityFaults }}
        <SecurityFaults />
      {/await}
    {/if}
  </div>
</section>
