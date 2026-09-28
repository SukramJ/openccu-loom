<!--
  Licenses / SBOM (#/licenses). Renders the CycloneDX 1.6 software bill of
  materials the release build embeds (GET /api/v1/sbom): the Go module
  graph of the daemon plus the npm dependency tree of this Config UI, with
  license identifiers. A development build carries no SBOM and answers
  404 — that is a property of the build, not an error state, so it renders
  as EmptyState rather than ErrorState (see api.getSBOM in
  $lib/api/client.ts).
-->
<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import { infoStore } from "$lib/stores/info.svelte";
  import { parseSBOM, type SbomLink, type SbomRow } from "$lib/sbom";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import Card from "$lib/components/ui/Card.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import PageShell from "$lib/components/ui/PageShell.svelte";
  import { t } from "$lib/i18n";

  const NOTICES_URL = "https://github.com/SukramJ/openccu-loom/blob/main/THIRD-PARTY-NOTICES.md";
  const PRIVACY_URL = "https://sukramj.github.io/openccu-loom/privacy/";

  // Quoted verbatim from the repository's LICENSE (MIT). This paragraph is
  // the legal text itself, not application copy, so it is deliberately not
  // routed through t() — it reads the same in both locales.
  const WARRANTY_DISCLAIMER =
    'THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR ' +
    "IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, " +
    "FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE " +
    "AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER " +
    "LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, " +
    "OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE " +
    "SOFTWARE.";

  let rows = $state<SbomRow[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  // A build without an embedded SBOM (dev build) answers 404 — distinct
  // from a genuine fetch error, and rendered as EmptyState, not ErrorState.
  let noSbom = $state(false);

  async function load() {
    loading = true;
    loadError = null;
    noSbom = false;
    try {
      const [, doc] = await Promise.all([infoStore.refresh(), api.getSBOM()]);
      if (doc === null) {
        noSbom = true;
        rows = [];
      } else {
        rows = parseSBOM(doc);
      }
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  onMount(() => void load());

  const info = $derived(infoStore.info);
  const downloadUrl = $derived(api.sbomDownloadUrl());

  const columns: DataColumn<SbomRow>[] = $derived([
    {
      key: "name",
      label: t("licenses.col.name"),
      sortable: true,
      title: true,
      get: (r) => r.name,
    },
    {
      key: "version",
      label: t("licenses.col.version"),
      sortable: true,
      get: (r) => r.version,
    },
    {
      key: "license",
      label: t("licenses.col.license"),
      sortable: true,
      get: (r) => r.license,
    },
    {
      key: "author",
      label: t("licenses.col.author"),
      sortable: true,
      get: (r) => r.author,
    },
  ]);

  function linkLabel(type: SbomLink["type"]): string {
    if (type === "website") return t("licenses.links.website");
    if (type === "vcs") return t("licenses.links.vcs");
    return t("licenses.links.distribution");
  }
</script>

<svelte:head>
  <title>{t("page.title.licenses")}</title>
</svelte:head>

<PageShell>
  <PageHeader title={t("licenses.title")} subtitle={t("licenses.subtitle")}>
    {#snippet actions()}
      <a
        href={downloadUrl}
        download="openccu-loom-sbom.cdx.json"
        class="rounded-md border border-slate-300 px-3 py-1.5 text-xs shadow-sm hover:bg-slate-50 dark:border-slate-700 dark:hover:bg-slate-800"
      >
        {t("licenses.download")}
      </a>
    {/snippet}
  </PageHeader>

  {#if loading}
    <LoadingState />
  {:else if loadError}
    <ErrorState message={loadError} onRetry={() => void load()} />
  {:else}
    <div class="flex flex-col gap-4">
      <Card class="p-4">
        <p class="text-sm font-medium text-slate-900 dark:text-white">
          {t("licenses.product_line", { version: info?.version ?? "" })}
        </p>
        <dl class="kv-grid mt-2 gap-x-4 gap-y-1 text-sm" style="--kv-label: 6rem">
          <dt class="text-slate-500 dark:text-slate-400">{t("licenses.field.license")}</dt>
          <dd class="text-slate-700 dark:text-slate-300">MIT</dd>
          <dt class="text-slate-500 dark:text-slate-400">{t("licenses.field.author")}</dt>
          <dd class="text-slate-700 dark:text-slate-300">SukramJ</dd>
        </dl>
        <ul class="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-sm">
          <li>
            <a
              href={NOTICES_URL}
              target="_blank"
              rel="noopener"
              class="underline"
              style="color: var(--ha-primary-color);"
            >
              {t("licenses.links.notices")}
            </a>
          </li>
          <li>
            <a
              href={PRIVACY_URL}
              target="_blank"
              rel="noopener"
              class="underline"
              style="color: var(--ha-primary-color);"
            >
              {t("licenses.links.privacy")}
            </a>
          </li>
        </ul>
      </Card>

      <Card class="p-4">
        <h2 class="mb-3 text-base font-semibold text-slate-900 dark:text-white">
          {t("licenses.disclaimer.title")}
        </h2>
        <p class="text-xs leading-relaxed text-slate-700 dark:text-slate-300">
          {WARRANTY_DISCLAIMER}
        </p>
      </Card>

      {#if noSbom}
        <Card class="p-4">
          <EmptyState
            message={t("licenses.no_sbom")}
            description={t("licenses.no_sbom.description")}
            icon="mdi:text-box-search-outline"
          />
        </Card>
      {:else}
        <Card class="p-4">
          <DataTable
            {rows}
            {columns}
            rowKey={(r) => r.key}
            search
            searchPlaceholder={t("licenses.search_placeholder")}
            persistKey="licenses"
            columnFilters
            initialSort={{ key: "name", asc: true }}
            emptyMessage={t("licenses.empty")}
            emptyDescription={t("licenses.empty.description")}
            emptyIcon="mdi:format-list-bulleted"
          >
            {#snippet cell(row, col)}
              {#if col.key === "license"}
                {row.license || "—"}
              {:else if col.key === "author"}
                <div class="flex flex-col gap-0.5">
                  {#if row.author}
                    <span>{row.author}</span>
                  {/if}
                  {#if row.links.length > 0}
                    <div class="flex flex-wrap gap-x-2 text-xs">
                      {#each row.links as link (link.type + link.url)}
                        <a
                          href={link.url}
                          target="_blank"
                          rel="noopener"
                          class="underline"
                          style="color: var(--ha-primary-color);"
                        >
                          {linkLabel(link.type)}
                        </a>
                      {/each}
                    </div>
                  {/if}
                </div>
              {:else}
                {col.get?.(row) ?? "—"}
              {/if}
            {/snippet}
          </DataTable>
        </Card>
      {/if}
    </div>
  {/if}
</PageShell>
