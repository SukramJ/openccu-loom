<script lang="ts">
  // Settings → Navigation & views: the surface-profile editor.
  //
  // Reading order is deliberate: what this is NOT (the disclaimer),
  // which mode is live (the master toggle), which profile is being
  // edited, then the rows. The preview answers the question every
  // toggle raises without needing a save.
  //
  // See notes/concepts/ui-surface-profiles.md.
  import { onMount } from "svelte";
  import { surfacesStore } from "$lib/stores/surfaces.svelte";
  import { infoStore } from "$lib/stores/info.svelte";
  import { matterStore } from "$lib/stores/matter.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { t } from "$lib/i18n";
  import type { ProfileName, SurfaceInfo } from "$lib/api/surface-types";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Switch from "$lib/components/ui/Switch.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";

  type Filter = "all" | "visible" | "hidden" | "changed";

  let query = $state("");
  let filter = $state<Filter>("all");
  let saving = $state(false);

  const GROUP_ORDER = [
    "overview",
    "automation",
    "diagnose",
    "bridges",
    "system",
    "settings",
    "device",
  ] as const;

  onMount(() => {
    void surfacesStore.load();
  });

  const editing = $derived(surfacesStore.editing);
  const surfaces = $derived(surfacesStore.surfaces);

  /**
   * The i18n key of a surface's label — the one the navigation itself
   * uses, not a copy.
   *
   * A second set of labels under `surface.label.*` was the first design,
   * and it had drifted in 26 of 48 rows before it ever shipped: the
   * editor called the fleet view "Fleet" while the sidebar said "CCUs".
   * A label is a name, and a name needs one source.
   */
  function labelKey(id: string): string {
    if (id.startsWith("nav.")) return `nav.${id.slice(4)}`;
    if (id.startsWith("settings.")) return `settings.tab.${id.slice(9)}`;
    if (id.startsWith("device.configure.")) {
      // Sub-tab keys use underscores where the surface id uses hyphens.
      return `device.subtab.${id.slice("device.configure.".length).replace(/-/g, "_")}`;
    }
    if (id.startsWith("device.")) return `device.toptab.${id.slice(7)}`;
    return id;
  }

  function label(s: SurfaceInfo): string {
    return t(labelKey(s.id));
  }

  function description(s: SurfaceInfo): string {
    return t(`surface.desc.${s.id}`);
  }

  function matchesFilter(s: SurfaceInfo): boolean {
    const on = surfacesStore.draftVisible(s.id);
    if (filter === "visible" && !on) return false;
    if (filter === "hidden" && on) return false;
    if (filter === "changed" && !surfacesStore.isChanged(s.id)) return false;
    if (query.trim()) {
      const hay = `${label(s)} ${description(s)} ${s.id}`.toLowerCase();
      if (!hay.includes(query.trim().toLowerCase())) return false;
    }
    return true;
  }

  const groups = $derived(
    GROUP_ORDER.map((g) => {
      const all = surfaces.filter((s) => s.group === g);
      return {
        id: g,
        all,
        rows: all.filter(matchesFilter),
        visible: all.filter((s) => surfacesStore.draftVisible(s.id)).length,
      };
    }).filter((g) => g.rows.length > 0),
  );

  // Navigation preview of the EDITED profile, so an operator preparing
  // the inactive profile sees what they are preparing.
  const previewClusters = $derived(
    (["overview", "automation", "diagnose", "bridges", "system"] as const)
      .map((g) => ({
        id: g,
        items: surfaces.filter(
          (s) => s.group === g && surfacesStore.draftVisible(s.id) && gateOpen(s),
        ),
      }))
      .filter((c) => c.items.length > 0),
  );

  const previewSettings = $derived(
    surfaces.filter(
      (s) => s.group === "settings" && surfacesStore.draftVisible(s.id) && gateOpen(s),
    ),
  );

  const previewDevice = $derived(
    surfaces.filter(
      (s) => s.group === "device" && surfacesStore.draftVisible(s.id) && gateOpen(s),
    ),
  );

  // Capability gates. A gated surface stays absent while its feature is
  // off however the profile is configured, so the preview must not
  // promise it. These are the same two sources the navigation reads, so
  // the preview and the sidebar cannot disagree.
  const matterEnabled = $derived(matterStore.status?.enabled === true);
  const historyEnabled = $derived(
    infoStore.info?.capabilities?.includes("history.v1") ?? false,
  );

  function gateAvailable(s: SurfaceInfo): boolean {
    if (s.gate === "matter") return matterEnabled;
    if (s.gate === "history") return historyEnabled;
    return true;
  }

  const gateOpen = gateAvailable;

  // The registry declares WHICH condition a hide hangs on; the copy
  // spells out the consequence. The confirmation is deliberately not
  // conditional on live state — an alarm system that is disarmed while
  // the operator edits can be armed a minute later, and a warning that
  // only appears half the time teaches nothing.
  function warnBody(s: SurfaceInfo): string | null {
    if (!s.warn) return null;
    if (s.warn_profile && s.warn_profile !== editing) return null;
    return t(`navviews.warn.${s.warn}`);
  }

  /**
   * Whether this row's embedded default was widened because the daemon
   * serves more than one CCU.
   *
   * Without the line it renders, the row would read as an unexplained
   * contradiction: the docs say Home Assistant owns the paramset editor,
   * and here it is shown as visible by default. The reason is that a
   * Home Assistant config entry addresses one CCU — for the others this
   * UI is the only editor there is.
   */
  function multiCentralApplies(s: SurfaceInfo): boolean {
    return editing === "embedded" && !!s.multi_central_visible && surfacesStore.centrals > 1;
  }

  /**
   * The read-only overview that hands off to this row's editor, if any.
   *
   * The `opens` relation is declared on the overview; reading it in
   * reverse is what lets the row an operator is actually clicking — the
   * editor — say what else changes. Without it the coupling is only
   * discoverable by visiting the other view and noticing the links are
   * gone.
   */
  function openedBy(s: SurfaceInfo): SurfaceInfo | undefined {
    return surfaces.find((x) => x.opens === s.id);
  }

  function floorReason(s: SurfaceInfo): string {
    switch (s.id) {
      case "nav.devices":
        return t("navviews.why.core");
      case "nav.settings":
        return t("navviews.why.settings");
      case "settings.navviews":
        return t("navviews.why.editor");
      case "nav.about":
        return t("navviews.why.about");
      default:
        return t("navviews.why.identity");
    }
  }

  async function onToggle(s: SurfaceInfo) {
    const on = surfacesStore.draftVisible(s.id);
    if (on) {
      const warn = warnBody(s);
      if (warn) {
        const ok = await confirmStore.ask({
          title: t("navviews.dlg.hide_title", { surface: label(s) }),
          body: warn,
          confirmLabel: t("navviews.dlg.hide_ok"),
        });
        if (!ok) return;
      }
    }
    surfacesStore.toggle(s.id);
  }

  async function onMasterToggle(next: boolean) {
    const target: ProfileName = next ? "embedded" : "standalone";
    const hiddenNav = surfaces.filter(
      (s) => s.id.startsWith("nav.") && !surfacesStore.draftVisible(s.id, target),
    ).length;
    const hiddenTabs = surfaces.filter(
      (s) => s.id.startsWith("settings.") && !surfacesStore.draftVisible(s.id, target),
    ).length;
    const body = next
      ? `${t("navviews.dlg.mode_on_text")}\n\n${t("navviews.dlg.will_hide", {
          views: String(hiddenNav),
          tabs: String(hiddenTabs),
        })}`
      : `${t("navviews.dlg.mode_off_text")}\n\n${t("navviews.dlg.will_show", {
          views: String(hiddenNav),
          tabs: String(hiddenTabs),
        })}`;
    const ok = await confirmStore.ask({
      title: next ? t("navviews.dlg.mode_on_title") : t("navviews.dlg.mode_off_title"),
      body,
      confirmLabel: next
        ? t("navviews.dlg.mode_on_ok")
        : t("navviews.dlg.mode_off_ok"),
    });
    if (!ok) return;
    try {
      await surfacesStore.setEmbedded(next);
      toastStore.success(
        next ? t("navviews.toast.mode_on") : t("navviews.toast.mode_off"),
      );
    } catch (e) {
      toastStore.error(
        `${t("navviews.toast.error")}: ${e instanceof Error ? e.message : String(e)}`,
      );
    }
  }

  async function onResetProfile() {
    const ok = await confirmStore.ask({
      title: t("navviews.dlg.reset_title", { profile: profileLabel(editing) }),
      body: t("navviews.dlg.reset_text", {
        count: String(surfacesStore.deviationCount()),
      }),
      confirmLabel: t("navviews.dlg.reset_ok"),
    });
    if (!ok) return;
    surfacesStore.resetProfile();
    toastStore.success(t("navviews.toast.reset"));
  }

  async function onSave() {
    saving = true;
    try {
      await surfacesStore.save();
      toastStore.success(t("navviews.toast.saved"));
    } catch (e) {
      toastStore.error(
        `${t("navviews.toast.error")}: ${e instanceof Error ? e.message : String(e)}`,
      );
    } finally {
      saving = false;
    }
  }

  function onDiscard() {
    surfacesStore.discard();
    toastStore.success(t("navviews.toast.discarded"));
  }

  function setGroup(groupID: string, next: boolean) {
    for (const s of surfaces.filter((x) => x.group === groupID)) {
      surfacesStore.set(s.id, next);
    }
  }

  function profileLabel(p: ProfileName): string {
    return p === "embedded" ? t("navviews.profile.embedded") : t("navviews.profile.standalone");
  }
</script>

{#if surfacesStore.loading && !surfacesStore.loaded}
  <LoadingState />
{:else if surfacesStore.error && !surfacesStore.loaded}
  <ErrorState message={surfacesStore.error} onRetry={() => surfacesStore.load()} />
{:else}
  <div class="space-y-4 pb-24">
    <!-- What this is not. One banner, first, because "hidden" reads as
         "forbidden" to almost everyone. -->
    <div
      class="flex gap-3 rounded-xl border border-sky-300/60 bg-sky-50 p-4 text-sm dark:border-sky-800 dark:bg-sky-950/40"
    >
      <Icon name="mdi:information-outline" class="mt-0.5 shrink-0 text-sky-600 dark:text-sky-400" />
      <p class="text-slate-700 dark:text-slate-200">
        {t("navviews.banner")}
      </p>
    </div>

    <!-- Master toggle -->
    <Card class="p-5">
      <div class="flex items-start justify-between gap-6">
        <div class="min-w-0">
          <h3 class="text-base font-semibold text-slate-900 dark:text-slate-100">
            {t("navviews.mode.title")}
          </h3>
          <p class="mt-1.5 max-w-prose text-sm text-slate-600 dark:text-slate-400">
            {t("navviews.mode.desc")}
          </p>
        </div>
        <Switch
          checked={surfacesStore.embedded}
          onCheckedChange={(v) => void onMasterToggle(v)}
        />
      </div>
      <div
        class="mt-4 flex flex-wrap items-center gap-x-5 gap-y-2 border-t border-slate-200 pt-3 text-sm text-slate-600 dark:border-slate-800 dark:text-slate-400"
      >
        <span>
          {t("navviews.mode.live")}:
          <strong class="text-slate-900 dark:text-slate-100">
            {profileLabel(surfacesStore.profile)}
          </strong>
        </span>
        <span class="tabular-nums">
          {t("navviews.mode.views_visible", {
            visible: String(surfaces.filter((s) => surfacesStore.visible(s.id)).length),
            total: String(surfaces.length),
          })}
        </span>
      </div>
    </Card>

    <!-- Profile switcher -->
    <div class="flex flex-wrap items-center gap-3">
      <span class="text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400">
        {t("navviews.profile.editing")}
      </span>
      <div
        class="inline-flex gap-1 rounded-lg border border-slate-200 bg-slate-50 p-1 dark:border-slate-800 dark:bg-slate-900"
        role="tablist"
      >
        {#each ["standalone", "embedded"] as const as p (p)}
          <button
            type="button"
            role="tab"
            aria-selected={editing === p}
            onclick={() => surfacesStore.setEditing(p)}
            class="inline-flex items-center gap-2 rounded-md px-3 py-1.5 text-sm transition-colors {editing ===
            p
              ? 'bg-white font-semibold text-slate-900 shadow-sm dark:bg-slate-800 dark:text-slate-100'
              : 'text-slate-600 hover:text-slate-900 dark:text-slate-400 dark:hover:text-slate-200'}"
          >
            {profileLabel(p)}
            {#if surfacesStore.profile === p}
              <Badge variant="success">{t("navviews.profile.live")}</Badge>
            {/if}
          </button>
        {/each}
      </div>
      <span class="grow"></span>
      {#if surfacesStore.deviationCount() > 0}
        <span class="text-sm tabular-nums text-slate-600 dark:text-slate-400">
          {t("navviews.mode.deviations", { count: String(surfacesStore.deviationCount()) })}
        </span>
      {/if}
      <Button
        variant="ghost"
        disabled={surfacesStore.deviationCount() === 0}
        onclick={() => void onResetProfile()}
      >
        <Icon name="mdi:refresh" />
        {t("navviews.profile.reset")}
      </Button>
    </div>

    <!-- Search + filters -->
    <div class="flex flex-wrap items-center gap-2">
      <div class="min-w-[220px] grow">
        <Input bind:value={query} placeholder={t("navviews.search")} />
      </div>
      <div class="flex flex-wrap gap-1.5" role="radiogroup" aria-label={t("navviews.filter.label")}>
        {#each ["all", "visible", "hidden", "changed"] as const as f (f)}
          <button
            type="button"
            role="radio"
            aria-checked={filter === f}
            onclick={() => (filter = f)}
            class="rounded-full border px-3 py-1.5 text-xs transition-colors {filter === f
              ? 'border-brand-500 bg-brand-500 text-white dark:text-slate-900'
              : 'border-slate-200 bg-white text-slate-600 hover:border-slate-300 dark:border-slate-800 dark:bg-slate-900 dark:text-slate-400'}"
          >
            {t(`navviews.filter.${f}`)}
          </button>
        {/each}
      </div>
    </div>

    <div class="grid gap-4 lg:grid-cols-[minmax(0,1fr)_18rem]">
      <!-- Rows -->
      <div class="space-y-3">
        {#each groups as group (group.id)}
          <Card class="overflow-hidden p-0">
            <header
              class="flex flex-wrap items-center gap-3 border-b border-slate-200 bg-slate-50 px-4 py-3 dark:border-slate-800 dark:bg-slate-900/60"
            >
              <h3 class="text-sm font-semibold text-slate-900 dark:text-slate-100">
                {t(`navviews.group.${group.id}`)}
              </h3>
              <span class="text-xs tabular-nums text-slate-500 dark:text-slate-400">
                {t("navviews.group.count", {
                  visible: String(group.visible),
                  total: String(group.all.length),
                })}
              </span>
              <span class="grow"></span>
              <Button variant="ghost" size="sm" onclick={() => setGroup(group.id, true)}>
                {t("navviews.group.show_all")}
              </Button>
              <Button variant="ghost" size="sm" onclick={() => setGroup(group.id, false)}>
                {t("navviews.group.hide_all")}
              </Button>
            </header>

            {#each group.rows as s (s.id)}
              {@const locked = surfacesStore.isFloor(s.id)}
              {@const changed = surfacesStore.isChanged(s.id)}
              {@const available = gateAvailable(s)}
              {@const openedFrom = openedBy(s)}
              <div
                class="flex items-start justify-between gap-4 border-b border-slate-200 py-3 pr-4 last:border-b-0 dark:border-slate-800 {s.parent
                  ? 'border-l-2 border-l-slate-200 pl-8 dark:border-l-slate-700'
                  : 'pl-4'}"
              >
                <div class="min-w-0">
                  <div class="flex flex-wrap items-center gap-2">
                    {#if locked}
                      <Icon name="mdi:lock" class="text-slate-400" />
                    {/if}
                    <span
                      class="text-sm font-semibold {available
                        ? 'text-slate-900 dark:text-slate-100'
                        : 'text-slate-500 dark:text-slate-400'}"
                    >
                      {label(s)}
                    </span>
                    <code class="text-[11px] text-slate-400 dark:text-slate-500">{s.id}</code>
                  </div>
                  <p
                    id="surface-desc-{s.id}"
                    class="mt-0.5 max-w-prose text-sm text-slate-600 dark:text-slate-400"
                  >
                    {description(s)}
                  </p>
                  {#if editing === "embedded" && s.ha_owns && !multiCentralApplies(s)}
                    <p class="mt-1 text-xs italic text-slate-500 dark:text-slate-500">
                      {t("navviews.row.ha_owns")}
                    </p>
                  {/if}
                  {#if multiCentralApplies(s)}
                    <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">
                      {t("navviews.row.multi_central", {
                        count: String(surfacesStore.centrals),
                      })}
                    </p>
                  {/if}
                  {#if locked}
                    <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">
                      {t("navviews.row.locked", { why: floorReason(s) })}
                    </p>
                  {/if}
                  {#if !available}
                    <p class="mt-1 text-xs text-amber-600 dark:text-amber-400">
                      {t("navviews.row.unavailable", {
                        why: t(`navviews.gate.${s.gate}`),
                      })}
                    </p>
                  {/if}
                  {#if s.role_admin}
                    <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">
                      {t("navviews.row.role_admin")}
                    </p>
                  {/if}
                  {#if s.opens && !surfacesStore.draftVisible(s.opens)}
                    <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">
                      {t("navviews.row.opens_hidden", { target: t(labelKey(s.opens)) })}
                    </p>
                  {/if}
                  {#if openedFrom && !surfacesStore.draftVisible(s.id)}
                    <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">
                      {t("navviews.row.opened_by_hidden", { source: label(openedFrom) })}
                    </p>
                  {/if}
                  {#if changed}
                    <p class="mt-1 flex items-center gap-1.5 text-xs text-brand-600 dark:text-brand-400">
                      <span class="inline-block h-1.5 w-1.5 rounded-full bg-brand-500"></span>
                      {t("navviews.row.changed_from", {
                        default: surfacesStore.defaultOf(s.id)
                          ? t("navviews.row.default_visible")
                          : t("navviews.row.default_hidden"),
                      })}
                    </p>
                  {/if}
                </div>
                <div class="flex shrink-0 items-center gap-2 pt-0.5">
                  {#if changed && !locked}
                    <button
                      type="button"
                      class="rounded p-1 text-slate-400 transition-colors hover:text-brand-600 dark:hover:text-brand-400"
                      title={t("navviews.row.reset_one")}
                      aria-label={t("navviews.row.reset_one")}
                      onclick={() => surfacesStore.resetSurface(s.id)}
                    >
                      <Icon name="mdi:refresh" />
                    </button>
                  {/if}
                  <Switch
                    checked={surfacesStore.draftVisible(s.id)}
                    disabled={locked}
                    ariaLabel={label(s)}
                    ariaDescribedby="surface-desc-{s.id}"
                    onCheckedChange={() => void onToggle(s)}
                  />
                </div>
              </div>
            {/each}
          </Card>
        {/each}
      </div>

      <!-- Preview -->
      <aside class="lg:sticky lg:top-4">
        <Card class="p-4" aria-live="polite">
          <h3 class="text-sm font-semibold text-slate-900 dark:text-slate-100">
            {t("navviews.preview.title")}
          </h3>
          <p class="mb-3 text-xs text-slate-500 dark:text-slate-400">
            {editing === surfacesStore.profile
              ? t("navviews.preview.sub_live")
              : t("navviews.preview.sub_other", { profile: profileLabel(editing) })}
          </p>
          {#each previewClusters as cluster (cluster.id)}
            <div class="mb-3">
              <p
                class="mb-1 text-[10px] font-medium uppercase tracking-wider text-slate-400 dark:text-slate-500"
              >
                {t(`navviews.group.${cluster.id}`)}
              </p>
              {#each cluster.items as item (item.id)}
                <div class="px-1 py-0.5 text-xs text-slate-600 dark:text-slate-400">
                  {label(item)}
                </div>
              {/each}
            </div>
          {/each}
          <div class="mb-3">
            <p
              class="mb-1 text-[10px] font-medium uppercase tracking-wider text-slate-400 dark:text-slate-500"
            >
              {t("navviews.group.settings")}
            </p>
            {#if previewSettings.length === 0}
              <p class="text-xs italic text-slate-400">{t("navviews.preview.none")}</p>
            {:else}
              <div class="flex flex-wrap gap-1">
                {#each previewSettings as item (item.id)}
                  <span
                    class="rounded-full border border-slate-200 bg-slate-50 px-2 py-0.5 text-[11px] text-slate-600 dark:border-slate-800 dark:bg-slate-900 dark:text-slate-400"
                  >
                    {label(item)}
                  </span>
                {/each}
              </div>
            {/if}
          </div>
          <div>
            <p
              class="mb-1 text-[10px] font-medium uppercase tracking-wider text-slate-400 dark:text-slate-500"
            >
              {t("navviews.group.device")}
            </p>
            {#if previewDevice.length === 0}
              <p class="text-xs italic text-slate-400">{t("navviews.preview.none")}</p>
            {:else}
              <div class="flex flex-wrap gap-1">
                {#each previewDevice as item (item.id)}
                  <span
                    class="rounded-full border border-slate-200 bg-slate-50 px-2 py-0.5 text-[11px] text-slate-600 dark:border-slate-800 dark:bg-slate-900 dark:text-slate-400"
                  >
                    {label(item)}
                  </span>
                {/each}
              </div>
            {/if}
          </div>
        </Card>
      </aside>
    </div>
  </div>

  {#if surfacesStore.hasChanges()}
    <div
      class="fixed inset-x-0 bottom-0 z-20 flex items-center gap-4 border-t border-slate-200 bg-white px-6 py-3 shadow-lg md:left-[240px] dark:border-slate-800 dark:bg-slate-950"
    >
      <span class="text-sm font-semibold tabular-nums text-slate-900 dark:text-slate-100">
        {t("navviews.save.count", { count: String(surfacesStore.changeCount()) })}
      </span>
      <span class="grow"></span>
      <Button variant="outline" onclick={onDiscard} disabled={saving}>
        {t("navviews.save.discard")}
      </Button>
      <Button onclick={() => void onSave()} disabled={saving}>
        {t("navviews.save.save")}
      </Button>
    </div>
  {/if}
{/if}
