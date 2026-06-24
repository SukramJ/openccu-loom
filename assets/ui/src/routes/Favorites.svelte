<script lang="ts">
  import { onMount } from "svelte";
  import { favoritesStore } from "$lib/stores/favorites.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import { t } from "$lib/i18n";
  import { toastStore } from "$lib/stores/toast.svelte";

  // Start page: the user's pinned devices and system variables, served
  // from server-side per-user preferences so they follow the operator
  // across browsers. Pin/unpin happens on the device-detail page; this
  // view lists and links to what was pinned.

  onMount(() => {
    if (!favoritesStore.loaded) void favoritesStore.load();
  });

  function hrefFor(type: string, id: string): string {
    if (type === "device") return `#/devices/${encodeURIComponent(id)}`;
    return "#/sysvars";
  }

  async function unpin(type: "device" | "sysvar", id: string, label: string) {
    try {
      await favoritesStore.remove(type, id);
      toastStore.success(t("favorites.removed", { label }));
    } catch (err) {
      toastStore.error(err instanceof Error ? err.message : String(err));
    }
  }
</script>

<section class="mx-auto max-w-6xl px-4 py-6 sm:px-6">
  <header class="mb-4">
    <h1 class="text-2xl font-semibold">{t("favorites.title")}</h1>
    <p class="text-sm text-slate-500 dark:text-slate-400">
      {t("favorites.subtitle")}
    </p>
  </header>

  {#if !favoritesStore.loaded}
    <LoadingState />
  {:else if favoritesStore.items.length === 0}
    <EmptyState message={t("favorites.empty")} icon="mdi:star-outline" />
  {:else}
    <ul class="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
      {#each favoritesStore.items as fav (fav.type + ":" + fav.id)}
        <li>
          <Card class="flex items-center justify-between gap-2 p-3">
            <a
              href={hrefFor(fav.type, fav.id)}
              class="flex min-w-0 items-center gap-2 hover:text-brand-700"
            >
              <Icon
                name={fav.type === "device" ? "mdi:home" : "mdi:zap"}
                class="shrink-0"
              />
              <span class="min-w-0">
                <span class="block truncate text-sm font-medium">{fav.label}</span>
                <span class="block truncate font-mono text-xs text-slate-500 dark:text-slate-400"
                  >{fav.id}</span
                >
              </span>
            </a>
            <div class="flex shrink-0 items-center gap-2">
              <Badge variant="muted">{t(`favorites.kind.${fav.type}`)}</Badge>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onclick={() => void unpin(fav.type, fav.id, fav.label)}
                title={t("favorites.unpin")}
              >
                <Icon name="mdi:star" />
              </Button>
            </div>
          </Card>
        </li>
      {/each}
    </ul>
  {/if}
</section>
