<!--
  About / system info (#/about). Read-only support surface that
  collects everything a "which version am I running?" question needs
  in one place:

    - GET /api/v1/info (via infoStore.refresh) — daemon version,
      commit, build date, add-on flag, uptime, API version, active
      capabilities.
    - GET /api/v1/system/ccu — per-central identity (model, CCU
      firmware version, serial).
    - GET /api/v1/system/update — pending CCU firmware updates,
      matched onto the central cards by name. Loaded best-effort:
      a failure only hides the update badge, never the page.

  The license/link card mirrors the no-JS /about diagnostic page so
  both surfaces tell the same licensing story.
-->
<script lang="ts">
  import { onMount } from "svelte";
  import { api, type SystemUpdateEntry } from "$lib/api/client";
  import type { SystemCCUEntry } from "$lib/api/types";
  import { infoStore } from "$lib/stores/info.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import { t } from "$lib/i18n";

  const REPO_URL = "https://github.com/SukramJ/openccu-loom";

  let ccus = $state<SystemCCUEntry[]>([]);
  let updates = $state<SystemUpdateEntry[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);

  async function load() {
    loading = true;
    error = null;
    try {
      // Info + centrals decide the page; the update list is a
      // best-effort enhancement (may be forbidden for non-admins).
      const [, entries] = await Promise.all([
        infoStore.refresh(),
        api.getSystemCCUs(),
      ]);
      ccus = entries;
      try {
        updates = await api.getSystemUpdate();
      } catch {
        updates = [];
      }
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  onMount(() => void load());

  const info = $derived(infoStore.info);

  const sortedCcus = $derived(
    [...ccus].sort((a, b) => a.name.localeCompare(b.name)),
  );

  function updateFor(central: string): SystemUpdateEntry | undefined {
    return updates.find((u) => u.central === central);
  }

  // `commit` is "none" for a `go run` / untagged build — no link then.
  const commitUrl = $derived(
    info && info.commit && info.commit !== "none"
      ? `${REPO_URL}/commit/${info.commit}`
      : null,
  );

  function formatStartedAt(iso: string): string {
    const d = new Date(iso);
    return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
  }

  const links = $derived([
    { label: t("about.links.github"), href: REPO_URL },
    { label: t("about.links.releases"), href: `${REPO_URL}/releases` },
    {
      label: t("about.links.notices"),
      href: `${REPO_URL}/blob/main/THIRD-PARTY-NOTICES.md`,
    },
    {
      label: t("about.links.docs"),
      href: `${REPO_URL}/blob/main/docs/user-guide.md`,
    },
  ]);
</script>

<svelte:head>
  <title>{t("page.title.about")}</title>
</svelte:head>

<section class="mx-auto w-full max-w-4xl px-4 py-8 sm:px-6">
  <PageHeader title={t("about.title")} subtitle={t("about.subtitle")} />

  {#if loading}
    <LoadingState />
  {:else if error}
    <ErrorState message={t("about.load_error", { error })} onRetry={() => void load()} />
  {:else}
    <div class="flex flex-col gap-4">
      <Card class="p-4">
        <h2 class="mb-3 text-base font-semibold text-slate-900 dark:text-white">
          {t("about.section.daemon")}
        </h2>
        {#if info}
          <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-sm">
            <dt class="text-slate-500 dark:text-slate-400">{t("about.field.version")}</dt>
            <dd class="font-medium text-slate-900 dark:text-white">v{info.version}</dd>

            <dt class="text-slate-500 dark:text-slate-400">{t("about.field.commit")}</dt>
            <dd class="min-w-0 truncate font-mono text-xs text-slate-700 dark:text-slate-300">
              {#if commitUrl}
                <a
                  href={commitUrl}
                  target="_blank"
                  rel="noopener"
                  class="underline"
                  style="color: var(--ha-primary-color);"
                >
                  {info.commit}
                </a>
              {:else}
                {info.commit}
              {/if}
            </dd>

            <dt class="text-slate-500 dark:text-slate-400">{t("about.field.build_date")}</dt>
            <dd class="text-slate-700 dark:text-slate-300">{info.build_date}</dd>

            <dt class="text-slate-500 dark:text-slate-400">{t("about.field.runtime")}</dt>
            <dd class="text-slate-700 dark:text-slate-300">
              {info.addon_build ? t("about.runtime.addon") : t("about.runtime.standalone")}
            </dd>

            <dt class="text-slate-500 dark:text-slate-400">{t("about.field.started_at")}</dt>
            <dd class="text-slate-700 dark:text-slate-300">
              {formatStartedAt(info.started_at)}
              {#if info.uptime}
                <span class="text-slate-500 dark:text-slate-400">
                  · {t("about.field.uptime")} {info.uptime}
                </span>
              {/if}
            </dd>

            <dt class="text-slate-500 dark:text-slate-400">{t("about.field.api_version")}</dt>
            <dd class="text-slate-700 dark:text-slate-300">{info.api_version}</dd>

            <dt class="text-slate-500 dark:text-slate-400">{t("about.field.capabilities")}</dt>
            <dd class="flex flex-wrap gap-1">
              {#each info.capabilities as cap (cap)}
                <Badge variant="muted">{cap}</Badge>
              {/each}
            </dd>
          </dl>
        {/if}
      </Card>

      <Card class="p-4">
        <h2 class="mb-3 text-base font-semibold text-slate-900 dark:text-white">
          {t("about.section.centrals")}
        </h2>
        {#if sortedCcus.length === 0}
          <p class="text-sm text-slate-500 dark:text-slate-400">{t("about.centrals.empty")}</p>
        {:else}
          <div class="flex flex-col gap-3">
            {#each sortedCcus as ccu (ccu.name)}
              {@const upd = updateFor(ccu.name)}
              <div
                class="rounded-md border p-3"
                style="border-color: var(--ha-divider-color);"
              >
                <div class="mb-1.5 flex items-center justify-between gap-2">
                  <h3 class="min-w-0 truncate text-sm font-semibold text-slate-900 dark:text-white">
                    {ccu.name}
                  </h3>
                  <div class="flex items-center gap-1">
                    {#if upd?.update_available}
                      <Badge variant="warning">
                        {t("about.centrals.update_available", {
                          version: upd.available_firmware ?? "?",
                        })}
                      </Badge>
                    {/if}
                    <Badge variant={ccu.available ? "success" : "danger"}>
                      {ccu.available ? t("fleet.status.online") : t("fleet.status.offline")}
                    </Badge>
                  </div>
                </div>
                <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
                  {#if ccu.model}
                    <dt class="text-slate-500 dark:text-slate-400">{t("fleet.field.model")}</dt>
                    <dd class="text-slate-700 dark:text-slate-300">{ccu.model}</dd>
                  {/if}
                  {#if ccu.version}
                    <dt class="text-slate-500 dark:text-slate-400">{t("about.centrals.firmware")}</dt>
                    <dd class="text-slate-700 dark:text-slate-300">{ccu.version}</dd>
                  {/if}
                  {#if ccu.serial}
                    <dt class="text-slate-500 dark:text-slate-400">{t("fleet.field.serial")}</dt>
                    <dd class="min-w-0 truncate font-mono text-xs text-slate-700 dark:text-slate-300">
                      {ccu.serial}
                    </dd>
                  {/if}
                </dl>
              </div>
            {/each}
          </div>
        {/if}
      </Card>

      <Card class="p-4">
        <h2 class="mb-3 text-base font-semibold text-slate-900 dark:text-white">
          {t("about.section.license")}
        </h2>
        <p class="text-sm text-slate-700 dark:text-slate-300">
          {t("about.license.text")}
        </p>
        <ul class="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-sm">
          {#each links as link (link.href)}
            <li>
              <a
                href={link.href}
                target="_blank"
                rel="noopener"
                class="underline"
                style="color: var(--ha-primary-color);"
              >
                {link.label}
              </a>
            </li>
          {/each}
        </ul>
      </Card>
    </div>
  {/if}
</section>
