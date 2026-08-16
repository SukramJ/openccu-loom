<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import { onResync, subscribe } from "$lib/stores/events.svelte";
  import type { SysvarChangedEvent, SysvarEntry } from "$lib/api/types";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Switch from "$lib/components/ui/Switch.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import SysvarChannelPicker from "$lib/components/SysvarChannelPicker.svelte";
  import { t } from "$lib/i18n";
  import { loadLS, saveLS } from "$lib/utils";
  import {
    sysvarWidget,
    sysvarNumberStep,
    isListSysvar,
    isNumberSysvar,
    isBoolSysvar,
  } from "$lib/sysvar-widget";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";

  let sysvars = $state<SysvarEntry[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let centralFilter = $state(loadLS("sysvars:central"));
  $effect(() => saveLS("sysvars:central", centralFilter));
  let drafts = $state<Record<string, unknown>>({});
  let savingName = $state<string | null>(null);
  let creating = $state(false);
  let createCentral = $state("");
  let createForm = $state<{
    name: string;
    value_type: string;
    unit: string;
    min: string;
    max: string;
    value_list: string;
    value_name_0: string;
    value_name_1: string;
  }>({
    name: "",
    value_type: "BOOL",
    unit: "",
    min: "",
    max: "",
    value_list: "",
    value_name_0: "",
    value_name_1: "",
  });

  let createDescription = $state("");
  // Channel assignment ("Kanalzuordnung") for the create form; empty = none.
  let createChannel = $state("");

  let editing = $state<SysvarEntry | null>(null);
  // Channel assignment for the edit dialog. editChannelDirty gates whether a
  // save sends channel_address at all, so an untouched picker never rewrites
  // a name-derived channel into an explicit CCU assignment.
  let editChannel = $state("");
  let editChannelDirty = $state(false);
  let editForm = $state({
    name: "",
    unit: "",
    min: "",
    max: "",
    value_list: "",
    description: "",
    value_name_0: "",
    value_name_1: "",
    is_visible: true,
    is_logged: false,
  });

  // Focus-trap bookkeeping for the edit dialog, mirroring
  // ConfirmDialog.svelte: `dialogEl` roots the search for the dialog's
  // focusable controls, `previouslyFocused` is the ⚙ button that opened it
  // so focus is restored there on close instead of stranded on <body>.
  let dialogEl = $state<HTMLDivElement | null>(null);
  let previouslyFocused: HTMLElement | null = null;

  function focusableEls(): HTMLElement[] {
    if (!dialogEl) return [];
    return Array.from(
      dialogEl.querySelectorAll<HTMLElement>(
        'input, button, select, textarea, [tabindex]:not([tabindex="-1"])',
      ),
    ).filter((el) => !el.hasAttribute("disabled"));
  }

  $effect(() => {
    if (editing) {
      previouslyFocused = document.activeElement as HTMLElement | null;
      // The dialog's DOM is inserted by the {#if} block guarding this
      // effect's dependency; queue past the current microtask so Svelte
      // has committed it before the query runs.
      queueMicrotask(() => focusableEls()[0]?.focus());
    } else if (previouslyFocused) {
      previouslyFocused.focus();
      previouslyFocused = null;
    }
  });

  // Escape and Tab belong to the dialog as a whole, so they are handled on
  // the window: focus starts on the trigger outside the overlay, and a
  // handler on the overlay element never sees a key pressed there.
  function onDialogKey(e: KeyboardEvent) {
    if (!editing) return;
    if (e.key === "Escape") {
      e.preventDefault();
      editing = null;
    } else if (e.key === "Tab") {
      const els = focusableEls();
      if (els.length === 0) return;
      const first = els[0];
      const last = els[els.length - 1];
      const active = document.activeElement;
      const atEdge = e.shiftKey ? active === first : active === last;
      const outside = !els.includes(active as HTMLElement);
      if (atEdge || outside) {
        e.preventDefault();
        (e.shiftKey ? last : first).focus();
      }
    }
  }

  // Renders a declared numeric bound for the edit form. An absent bound
  // stays an empty field, which is also what the CCU reads as "leave the
  // stored bound alone" on save.
  function boundText(bound: number | undefined): string {
    return bound === undefined ? "" : String(bound);
  }

  function startEdit(sv: SysvarEntry) {
    editing = sv;
    editChannel = sv.channel ?? "";
    editChannelDirty = false;
    editForm = {
      name: sv.name,
      unit: sv.unit ?? "",
      min: boundText(sv.min),
      max: boundText(sv.max),
      value_list: (sv.value_list ?? []).join(";"),
      description: sv.description ?? "",
      value_name_0: sv.value_name_0 ?? "",
      value_name_1: sv.value_name_1 ?? "",
      is_visible: sv.is_visible ?? true,
      is_logged: sv.is_logged ?? false,
    };
  }

  async function saveEdit() {
    if (!editing) return;
    // A numeric system variable on the CCU always carries both bounds:
    // the CCU's own WebUI writes ValueMin and ValueMax on every save, and
    // ReGa coerces a blank one to 0 rather than dropping the constraint,
    // so "no bound" is not a state the CCU can be put into. An emptied
    // field is therefore an edit that cannot be performed — and it used
    // to be dropped in silence, leaving the operator with a success toast
    // and the old bound still in place.
    if (
      isNumberSysvar(editing.value_type) &&
      ((boundText(editing.min) !== "" && editForm.min.trim() === "") ||
        (boundText(editing.max) !== "" && editForm.max.trim() === ""))
    ) {
      toastStore.error(t("sysvars.edit.bound_required"));
      return;
    }
    savingName = editing.name;
    try {
      const body: Record<string, unknown> = {};
      const newName = editForm.name.trim();
      if (newName && newName !== editing.name) body.name = newName;
      if (editForm.unit) body.unit = editForm.unit;
      // Bounds are pre-filled from the CCU, so only a genuinely changed
      // value is sent — an untouched field must not rewrite the declared
      // bound. An emptied field never reaches here; the guard above
      // refuses it, because the CCU cannot drop a bound.
      if (editForm.min && editForm.min !== boundText(editing.min))
        body.min = editForm.min;
      if (editForm.max && editForm.max !== boundText(editing.max))
        body.max = editForm.max;
      if (editForm.description) body.description = editForm.description;
      if (editForm.value_list && isListSysvar(editing.value_type)) {
        body.value_list = editForm.value_list
          .split(";")
          .map((s) => s.trim())
          .filter(Boolean);
      }
      if (isBoolSysvar(editing.value_type)) {
        // A blank label leaves the CCU value untouched; only send a
        // genuinely changed, non-empty label.
        const vn0 = editForm.value_name_0.trim();
        const vn1 = editForm.value_name_1.trim();
        if (vn0 && vn0 !== (editing.value_name_0 ?? "")) body.value_name_0 = vn0;
        if (vn1 && vn1 !== (editing.value_name_1 ?? "")) body.value_name_1 = vn1;
      }
      // Visibility / archive flags: send only when the operator changed
      // them, so an unrelated edit does not re-toggle the CCU flag.
      if (editForm.is_visible !== (editing.is_visible ?? true))
        body.is_visible = editForm.is_visible;
      if (editForm.is_logged !== (editing.is_logged ?? false))
        body.is_logged = editForm.is_logged;
      // Channel assignment: only when the operator touched the picker. An
      // empty string clears the assignment; a channel address assigns it.
      if (editChannelDirty) body.channel_address = editChannel;
      await api.patchSysvar(editing.name, body, editing.central);
      toastStore.success(
        t("sysvars.updated", { name: (body.name as string) ?? editing.name }),
      );
      editing = null;
      await load();
    } catch (err) {
      toastStore.error(
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err),
      );
    } finally {
      savingName = null;
    }
  }

  async function load() {
    loading = true;
    loadError = null;
    try {
      sysvars = await api.listSysvars();
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  // Reload = force the daemon to re-pull the sysvar catalogue from the
  // CCU first, then read the refreshed list. A plain load() only serves
  // the daemon's periodic-poll state (up to one sysvar-scan interval
  // stale), so a value just changed at the CCU would not show up. A
  // failing re-pull (CCU unreachable) surfaces as a toast but never
  // blocks reading the current daemon state.
  async function reload() {
    loading = true;
    try {
      await api.fetchSysvars();
    } catch (err) {
      toastStore.error(
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err),
      );
    }
    await load();
  }

  function draftKey(sv: SysvarEntry): string {
    return (sv.central ?? "") + "/" + sv.name;
  }

  async function save(sv: SysvarEntry) {
    const key = draftKey(sv);
    if (!(key in drafts)) return;
    savingName = sv.name;
    try {
      await api.setSysvar(sv.name, drafts[key], sv.central);
      delete drafts[key];
      drafts = { ...drafts };
      toastStore.success(t("sysvars.saved", { name: sv.name }));
      await load();
    } catch (err) {
      toastStore.error(
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err),
      );
    } finally {
      savingName = null;
    }
  }

  function discardDraft(sv: SysvarEntry) {
    const key = draftKey(sv);
    delete drafts[key];
    drafts = { ...drafts };
  }

  async function createSv() {
    if (!createForm.name) return;
    savingName = "__create__";
    try {
      const binary =
        createForm.value_type === "BOOL" || createForm.value_type === "ALARM";
      await api.createSysvar(
        {
          name: createForm.name,
          value_type: createForm.value_type,
          unit: createForm.unit || undefined,
          min: createForm.min || undefined,
          max: createForm.max || undefined,
          description: createDescription || undefined,
          value_list: createForm.value_type === "ENUM" && createForm.value_list
            ? createForm.value_list.split(";").map((s) => s.trim()).filter(Boolean)
            : undefined,
          value_name_0: binary && createForm.value_name_0 ? createForm.value_name_0 : undefined,
          value_name_1: binary && createForm.value_name_1 ? createForm.value_name_1 : undefined,
          channel_address: createChannel || undefined,
        },
        createCentral,
      );
      toastStore.success(t("sysvars.created"));
      creating = false;
      createForm = {
        name: "",
        value_type: "BOOL",
        unit: "",
        min: "",
        max: "",
        value_list: "",
        value_name_0: "",
        value_name_1: "",
      };
      createDescription = "";
      createChannel = "";
      await load();
    } catch (err) {
      toastStore.error(
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err),
      );
    } finally {
      savingName = null;
    }
  }

  async function deleteSv(sv: SysvarEntry) {
    // Best-effort: warn the operator when programs reference this variable
    // (the CCU auto-removes it from them on delete). A usage-lookup failure
    // must never block the delete — fall through to a plain confirm.
    let body: string | undefined;
    try {
      const usage = await api.getSysvarUsage(sv.name, sv.central);
      if (usage.programs && usage.programs.length > 0) {
        const names = usage.programs
          .map((p) => p.name + (p.is_internal ? ` (${t("sysvars.usage.internal")})` : ""))
          .join(", ");
        body = t("sysvars.usage.warning", { count: usage.programs.length }) + "\n" + names;
      }
    } catch {
      // Ignore — the confirm proceeds without a usage warning.
    }
    const ok = await confirmStore.ask({
      title: t("sysvars.confirm_remove", { name: sv.name }),
      body,
      confirmLabel: t("common.remove"),
      destructive: true,
    });
    if (!ok) return;
    savingName = sv.name;
    try {
      await api.deleteSysvar(sv.name, sv.central);
      toastStore.success(t("sysvars.removed", { name: sv.name }));
      await load();
    } catch (err) {
      toastStore.error(
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err),
      );
    } finally {
      savingName = null;
    }
  }

  onMount(() => {
    void load();
    // Live values. The daemon pushes every system-variable change on the
    // event stream; without this the table shows the values of the last
    // REST read until the operator hits reload, even though the frames
    // are already arriving.
    const unsubEvents = subscribe((ev) => {
      if (ev.type !== "sysvar") return;
      const p = ev.payload as SysvarChangedEvent;
      const i = sysvars.findIndex(
        (sv) => sv.name === p.name && (sv.central ?? "") === p.central,
      );
      if (i < 0) return;
      sysvars[i] = { ...sysvars[i], value: p.value };
    });
    // A resync says the stream could not carry this client to the current
    // state — a daemon restart, a boot snapshot, a queue that fell too far
    // behind. The values that changed during that gap are never replayed
    // as `sysvar` frames, so the patched rows above would keep showing
    // pre-gap values as if they were live. Read the list again instead.
    const unsubResync = onResync(() => void load());
    return () => {
      unsubEvents();
      unsubResync();
    };
  });

  const centrals = $derived.by(() => {
    const set = new Set<string>();
    for (const s of sysvars) if (s.central) set.add(s.central);
    return Array.from(set).sort((a, b) =>
      a.localeCompare(b, undefined, { sensitivity: "base" }),
    );
  });

  const filtered = $derived(
    centralFilter ? sysvars.filter((s) => s.central === centralFilter) : sysvars,
  );

  const columns: DataColumn<SysvarEntry>[] = $derived([
    { key: "name", label: t("sysvars.col.name"), sortable: true, title: true, get: (s) => s.name },
    { key: "type", label: t("sysvars.col.type"), sortable: true, get: (s) => s.value_type },
    { key: "value", label: t("sysvars.col.value"), sortable: true, get: (s) => (s.value == null ? "" : String(s.value)) },
    { key: "actions", label: t("sysvars.col.actions"), align: "right", cellClass: "reflow-actions" },
  ]);

  function currentValue(s: SysvarEntry): unknown {
    const key = draftKey(s);
    return key in drafts ? drafts[key] : s.value;
  }

  function setDraft(s: SysvarEntry, v: unknown) {
    const key = draftKey(s);
    drafts = { ...drafts, [key]: v };
  }

  function isDirty(s: SysvarEntry): boolean {
    const key = draftKey(s);
    if (!(key in drafts)) return false;
    return JSON.stringify(drafts[key]) !== JSON.stringify(s.value);
  }
</script>

<section class="mx-auto max-w-6xl px-4 py-6 sm:px-6">
  <PageHeader
    title={t("sysvars.title")}
    subtitle={loading ? t("common.loading") : t("sysvars.count", { count: sysvars.length })}
  >
    {#snippet actions()}
      {#if centrals.length > 1}
        <select
          bind:value={centralFilter}
          class="rounded-md border border-slate-300 bg-white px-2 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
          title="CCU"
        >
          <option value="">{t("common.all_ccus")}</option>
          {#each centrals as c (c)}
            <option value={c}>{c}</option>
          {/each}
        </select>
      {/if}
      <Button type="button" variant="outline" size="sm" onclick={() => void reload()} disabled={loading}>
        {t("common.reload")}
      </Button>
      <Button type="button" size="sm" onclick={() => (creating = !creating)}>
        {creating ? t("common.cancel") : t("common.new")}
      </Button>
    {/snippet}
  </PageHeader>

  {#if creating}
    <Card class="mb-4 p-4">
      <h2 class="mb-2 text-lg font-semibold">{t("sysvars.create.title")}</h2>
      <div class="grid grid-cols-1 gap-2 md:grid-cols-2">
        {#if centrals.length > 1}
          <label class="text-sm md:col-span-2">
            <span class="block text-xs text-slate-500 dark:text-slate-400">CCU</span>
            <select
              bind:value={createCentral}
              class="w-full rounded-md border border-slate-300 bg-white px-2 py-2 text-sm dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
            >
              <option value="">{t("common.select_placeholder")}</option>
              {#each centrals as c (c)}
                <option value={c}>{c}</option>
              {/each}
            </select>
          </label>
        {/if}
        <label class="text-sm">
          <span class="block text-xs text-slate-500 dark:text-slate-400">{t("sysvars.create.name")}</span>
          <Input bind:value={createForm.name} />
        </label>
        <label class="text-sm">
          <span class="block text-xs text-slate-500 dark:text-slate-400">{t("sysvars.create.type")}</span>
          <select
            bind:value={createForm.value_type}
            class="w-full rounded-md border border-slate-300 bg-white px-2 py-2 text-sm dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
          >
            <option value="BOOL">BOOL</option>
            <option value="INTEGER">INTEGER</option>
            <option value="FLOAT">FLOAT</option>
            <option value="STRING">STRING</option>
            <option value="ENUM">ENUM</option>
            <option value="ALARM">ALARM</option>
          </select>
        </label>
        {#if createForm.value_type === "ALARM"}
          <p class="text-xs text-slate-500 md:col-span-2 dark:text-slate-400">
            {t("sysvars.create.alarm_hint")}
          </p>
        {/if}
        {#if createForm.value_type === "BOOL" || createForm.value_type === "ALARM"}
          <div class="grid grid-cols-2 gap-2 md:col-span-2">
            <label class="text-sm">
              <span class="block text-xs text-slate-500 dark:text-slate-400">{t("sysvars.labels.value0")}</span>
              <Input bind:value={createForm.value_name_0} placeholder="false" />
            </label>
            <label class="text-sm">
              <span class="block text-xs text-slate-500 dark:text-slate-400">{t("sysvars.labels.value1")}</span>
              <Input bind:value={createForm.value_name_1} placeholder="true" />
            </label>
          </div>
        {/if}
        <label class="text-sm">
          <span class="block text-xs text-slate-500 dark:text-slate-400">{t("sysvars.create.unit")}</span>
          <Input bind:value={createForm.unit} />
        </label>
        <label class="text-sm md:col-span-2">
          <span class="block text-xs text-slate-500 dark:text-slate-400">{t("sysvars.edit.description")}</span>
          <Input bind:value={createDescription} />
        </label>
        {#if createForm.value_type === "INTEGER" || createForm.value_type === "FLOAT"}
          <div class="grid grid-cols-2 gap-2">
            <label class="text-sm">
              <span class="block text-xs text-slate-500 dark:text-slate-400">{t("common.min")}</span>
              <Input bind:value={createForm.min} />
            </label>
            <label class="text-sm">
              <span class="block text-xs text-slate-500 dark:text-slate-400">{t("common.max")}</span>
              <Input bind:value={createForm.max} />
            </label>
          </div>
        {/if}
        {#if createForm.value_type === "ENUM"}
          <label class="text-sm md:col-span-2">
            <span class="block text-xs text-slate-500 dark:text-slate-400">{t("sysvars.create.values")}</span>
            <Input bind:value={createForm.value_list} placeholder={t("sysvars.create.values_placeholder")} />
          </label>
        {/if}
        <div class="text-sm md:col-span-2">
          <span class="block text-xs text-slate-500 dark:text-slate-400">{t("sysvars.channel.label")}</span>
          <SysvarChannelPicker
            value={createChannel}
            central={createCentral || undefined}
            onChange={(v) => (createChannel = v)}
          />
          <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">{t("sysvars.channel.hint")}</p>
        </div>
      </div>
      <div class="mt-3 flex justify-end gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onclick={() => (creating = false)}
        >
          {t("common.cancel")}
        </Button>
        <Button
          type="button"
          size="sm"
          onclick={() => void createSv()}
          disabled={!createForm.name || savingName === "__create__"}
        >
          {savingName === "__create__" ? "…" : t("common.add")}
        </Button>
      </div>
    </Card>
  {/if}

  {#if loadError}
    <ErrorState message={loadError} onRetry={load} class="mb-4" />
  {/if}

  {#if loading}
    <LoadingState />
  {:else}
    <Card class="p-4">
      <DataTable
        rows={filtered}
        {columns}
        rowKey={(s) => (s.central ?? "") + "/" + s.name}
        search
        searchPlaceholder={t("common.search")}
        persistKey="sysvars"
        initialSort={{ key: "name", asc: true }}
        emptyMessage={t("sysvars.empty")}
        emptyIcon="mdi:sliders"
      >
        {#snippet cell(sv, col)}
          {#if col.key === "name"}
            <span class="font-mono text-sm font-semibold">{sv.name}</span>
            {#if isDirty(sv)}<Badge variant="warning">{t("common.modified")}</Badge>{/if}
            {#if centrals.length > 1 && sv.central}<Badge variant="muted">{sv.central}</Badge>{/if}
            {#if sv.description}
              <span class="block text-xs text-slate-500 dark:text-slate-400">{sv.description}</span>
            {/if}
          {:else if col.key === "type"}
            <Badge variant="muted">{sv.value_type}</Badge>
            {#if sv.unit}<span class="ml-1 text-xs text-slate-500 dark:text-slate-400">{sv.unit}</span>{/if}
          {:else if col.key === "value"}
            {@const widget = sysvarWidget(sv)}
            {#if widget === "switch"}
              {@const on = Boolean(currentValue(sv))}
              <span class="inline-flex items-center gap-2">
                <Switch checked={on} onCheckedChange={(v) => setDraft(sv, v)} />
                {#if sv.value_name_0 || sv.value_name_1}
                  <span class="text-xs text-slate-500 dark:text-slate-400">
                    {on ? (sv.value_name_1 ?? "") : (sv.value_name_0 ?? "")}
                  </span>
                {/if}
              </span>
            {:else if widget === "select"}
              <Select
                options={(sv.value_list ?? []).map((label, i) => ({ value: String(i), label }))}
                value={currentValue(sv) != null ? String(currentValue(sv)) : ""}
                onValueChange={(v) => setDraft(sv, Number(v))}
              />
            {:else if widget === "number"}
              <Input
                type="number"
                step={sysvarNumberStep(sv.value_type)}
                value={currentValue(sv) as number | null}
                oninput={(e) => {
                  const n = Number((e.target as HTMLInputElement).value);
                  if (Number.isFinite(n)) setDraft(sv, n);
                }}
              />
            {:else}
              <Input
                type="text"
                value={(currentValue(sv) ?? "") as string}
                oninput={(e) => setDraft(sv, (e.target as HTMLInputElement).value)}
              />
            {/if}
          {:else if col.key === "actions"}
            <span class="inline-flex items-center justify-end gap-1.5">
              <Button type="button" size="sm" variant="outline" onclick={() => discardDraft(sv)} disabled={!isDirty(sv) || savingName === sv.name}>×</Button>
              <Button type="button" size="sm" onclick={() => void save(sv)} disabled={!isDirty(sv) || savingName === sv.name}>
                {savingName === sv.name ? "…" : t("common.save")}
              </Button>
              <Button type="button" size="sm" variant="outline" onclick={() => startEdit(sv)} disabled={savingName === sv.name} title={t("sysvars.edit.tooltip")}>⚙</Button>
              <Button type="button" size="sm" variant="destructive" onclick={() => void deleteSv(sv)} disabled={savingName === sv.name} title={t("sysvars.remove.tooltip")}>×</Button>
            </span>
          {/if}
        {/snippet}
      </DataTable>
    </Card>
  {/if}
</section>

<svelte:window onkeydown={onDialogKey} />

{#if editing}
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-[color-mix(in_srgb,var(--color-slate-900)_50%,transparent)] p-4"
    role="dialog"
    aria-modal="true"
    aria-label={t("sysvars.edit.title")}
    tabindex="-1"
    onclick={(e) => {
      if (e.target === e.currentTarget) editing = null;
    }}
    onkeydown={(e) => {
      // Backdrop-local pair for the click dismissal above. Escape from
      // anywhere is handled on the window, because focus can sit outside
      // this subtree — on the ⚙ button that opened the dialog.
      if (e.key === "Escape") editing = null;
    }}
  >
    <div
      bind:this={dialogEl}
      class="w-full max-w-md rounded-lg bg-white p-5 shadow-xl dark:bg-slate-900"
    >
      <header class="mb-3 flex items-baseline justify-between gap-2">
        <h2 class="text-lg font-semibold">
          {t("sysvars.edit.title")}
          <span class="font-mono text-sm text-slate-500 dark:text-slate-400">{editing.name}</span>
        </h2>
        <Badge variant="muted">{editing.value_type}</Badge>
      </header>
      <p class="mb-3 text-xs text-slate-500 dark:text-slate-400">{t("sysvars.edit.note")}</p>
      <div class="space-y-2">
        <label class="block text-sm">
          <span class="block text-xs text-slate-500 dark:text-slate-400">{t("sysvars.edit.name")}</span>
          <Input bind:value={editForm.name} placeholder={editing.name} />
        </label>
        <label class="block text-sm">
          <span class="block text-xs text-slate-500 dark:text-slate-400">{t("sysvars.edit.description")}</span>
          <Input bind:value={editForm.description} />
        </label>
        <label class="block text-sm">
          <span class="block text-xs text-slate-500 dark:text-slate-400">{t("sysvars.create.unit")}</span>
          <Input bind:value={editForm.unit} />
        </label>
        {#if isNumberSysvar(editing.value_type)}
          <div class="grid grid-cols-2 gap-2">
            <label class="text-sm">
              <span class="block text-xs text-slate-500 dark:text-slate-400">{t("common.min")}</span>
              <Input bind:value={editForm.min} />
            </label>
            <label class="text-sm">
              <span class="block text-xs text-slate-500 dark:text-slate-400">{t("common.max")}</span>
              <Input bind:value={editForm.max} />
            </label>
          </div>
        {/if}
        {#if isListSysvar(editing.value_type)}
          <label class="block text-sm">
            <span class="block text-xs text-slate-500 dark:text-slate-400">{t("sysvars.create.values")}</span>
            <Input bind:value={editForm.value_list} />
          </label>
        {/if}
        {#if isBoolSysvar(editing.value_type)}
          <fieldset class="rounded-md border border-slate-200 p-2 dark:border-slate-700">
            <legend class="px-1 text-xs text-slate-500 dark:text-slate-400">{t("sysvars.labels.title")}</legend>
            <div class="grid grid-cols-2 gap-2">
              <label class="text-sm">
                <span class="block text-xs text-slate-500 dark:text-slate-400">{t("sysvars.labels.value0")}</span>
                <Input bind:value={editForm.value_name_0} />
              </label>
              <label class="text-sm">
                <span class="block text-xs text-slate-500 dark:text-slate-400">{t("sysvars.labels.value1")}</span>
                <Input bind:value={editForm.value_name_1} />
              </label>
            </div>
            <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">{t("sysvars.labels.hint")}</p>
          </fieldset>
        {/if}
        <label class="flex items-center justify-between gap-2 text-sm">
          <span class="text-xs text-slate-500 dark:text-slate-400">{t("sysvars.flags.visible")}</span>
          <Switch checked={editForm.is_visible} onCheckedChange={(v) => (editForm.is_visible = v)} />
        </label>
        <label class="flex items-center justify-between gap-2 text-sm">
          <span class="text-xs text-slate-500 dark:text-slate-400">{t("sysvars.flags.logged")}</span>
          <Switch checked={editForm.is_logged} onCheckedChange={(v) => (editForm.is_logged = v)} />
        </label>
        <div class="block text-sm">
          <span class="block text-xs text-slate-500 dark:text-slate-400">{t("sysvars.channel.label")}</span>
          <SysvarChannelPicker
            value={editChannel}
            central={editing.central || undefined}
            onChange={(v) => {
              editChannel = v;
              editChannelDirty = true;
            }}
          />
          <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">{t("sysvars.channel.hint")}</p>
        </div>
      </div>
      <div class="mt-4 flex justify-end gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onclick={() => (editing = null)}
        >
          {t("common.cancel")}
        </Button>
        <Button
          type="button"
          size="sm"
          onclick={() => void saveEdit()}
          disabled={!editing || savingName === editing.name}
        >
          {t("common.save")}
        </Button>
      </div>
    </div>
  </div>
{/if}
