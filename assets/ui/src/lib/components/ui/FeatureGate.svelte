<script lang="ts">
  import type { Snippet } from "svelte";
  import { t } from "$lib/i18n";
  import { centralStore } from "$lib/stores/centrals.svelte";
  import { centralFeatureReason, featureName } from "$lib/features";
  import EmptyState from "./EmptyState.svelte";
  import Icon from "./Icon.svelte";

  // Renders a view that needs a per-central feature. When no central
  // offers it, the view is replaced by the shared empty state naming each
  // central's reason; when only some lack it, a notice names those above
  // the view. Until the fleet has loaded the view renders as it is, so
  // nothing blanks during the first paint.
  type Props = {
    feature: string;
    /** Restrict the question to one central. */
    central?: string;
    children: Snippet;
  };

  let { feature, central, children }: Props = $props();

  const relevant = $derived(
    central ? centralStore.items.filter((c) => c.name === central) : centralStore.items,
  );
  const lacking = $derived(
    centralStore.centralsLacking(feature).filter((c) => relevant.some((r) => r.name === c.name)),
  );
  const none = $derived(relevant.length > 0 && lacking.length === relevant.length);
  const rows = $derived(
    lacking.map((c) => t("feature.gate.row", { central: c.name, reason: centralFeatureReason(c, feature) })),
  );
</script>

{#if none}
  <EmptyState
    icon="mdi:cancel"
    message={t("feature.gate.none", { feature: featureName(feature) })}
    description={rows.join(" · ")}
  />
{:else}
  {#if lacking.length > 0}
    <div
      class="mb-4 flex items-start gap-2 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-900 dark:border-amber-900/60 dark:bg-amber-950/40 dark:text-amber-200"
      role="note"
    >
      <Icon name="mdi:information-outline" class="mt-0.5 shrink-0" aria-label="" />
      <div>
        <p class="font-medium">{t("feature.gate.some", { feature: featureName(feature) })}</p>
        <ul class="mt-0.5">
          {#each rows as row (row)}
            <li>{row}</li>
          {/each}
        </ul>
      </div>
    </div>
  {/if}
  {@render children()}
{/if}
