<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError, friendlyError } from "$lib/api/client";
  import { alarmPanelStore } from "$lib/stores/alarmPanel.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { prefs } from "$lib/stores/preferences.svelte";
  import { t } from "$lib/i18n";
  import type { AlarmCode, AlarmCodeKind, AlarmCodeRequest } from "$lib/api/types";
  import type { IconName } from "$lib/icons";
  import Icon from "$lib/components/ui/Icon.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import Switch from "$lib/components/ui/Switch.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";

  // Alarm codes editor (docs/alarm-concept.md §11). Codes are security
  // material: the daemon never returns the argon2id hash or the cleartext
  // PIN, so this view treats the PIN as write-only (blank keeps the stored
  // hash on edit) and surfaces the whole list only to operators — a 403 is
  // an "operator required" state, a 503 means the code subsystem is not
  // wired. The duress toggle carries an explicit warning: a duress PIN
  // disarms normally but fires a silent alarm, so it must never be handed
  // out casually.

  const KINDS: AlarmCodeKind[] = ["pin", "keypad_slot", "remote_key"];

  const KIND_ICON: Record<AlarmCodeKind, IconName> = {
    pin: "mdi:lock",
    keypad_slot: "mdi:gesture-tap-button",
    remote_key: "mdi:key",
  };

  // --- data --------------------------------------------------------
  let codes = $state<AlarmCode[]>([]);
  let loading = $state(false);
  let loadError = $state<string | null>(null);
  // Distinct from a plain error: the alarm-code subsystem answered 503, so
  // the whole feature is inert — show a calm explanation, not a red error.
  let unavailable = $state(false);
  let saving = $state(false);

  const areas = $derived(alarmPanelStore.areasConfig);

  // --- editor draft ------------------------------------------------
  type Draft = {
    id: string | null; // null → create
    name: string;
    kind: AlarmCodeKind;
    pin: string; // write-only; blank keeps the stored hash on edit
    duress: boolean;
    perms: { arm: boolean; disarm: boolean; silence: boolean };
    areas: string[]; // empty → every area
    bindingText: string; // raw JSON for the hardware kinds
    validFrom: string; // datetime-local string
    validUntil: string;
    enabled: boolean;
  };
  let draft = $state<Draft | null>(null);
  let saveError = $state<string | null>(null);

  const editing = $derived(draft?.id != null);

  // --- helpers -----------------------------------------------------
  function areaName(id: string): string {
    return areas.find((a) => a.id === id)?.name ?? id;
  }

  function fmtDate(ms: number): string {
    return new Date(ms).toLocaleString(prefs.locale === "de" ? "de-DE" : "en-US", {
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
    });
  }

  // datetime-local <-> Unix ms. An empty field is an open bound (0).
  function msToInput(ms: number | undefined): string {
    if (!ms) return "";
    const d = new Date(ms);
    if (Number.isNaN(d.getTime())) return "";
    // Local wall time, trimmed to minutes, in the format the input wants.
    const pad = (n: number) => String(n).padStart(2, "0");
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
  }
  function inputToMs(v: string): number {
    if (!v) return 0;
    const ms = new Date(v).getTime();
    return Number.isNaN(ms) ? 0 : ms;
  }

  function permChips(c: AlarmCode): string[] {
    const out: string[] = [];
    if (c.perms.arm) out.push(t("alarm.codes.perm.arm"));
    if (c.perms.disarm) out.push(t("alarm.codes.perm.disarm"));
    if (c.perms.silence) out.push(t("alarm.codes.perm.silence"));
    return out;
  }

  // --- load --------------------------------------------------------
  async function load() {
    loading = true;
    loadError = null;
    unavailable = false;
    try {
      codes = await api.listAlarmCodes();
    } catch (err) {
      if (err instanceof ApiError && err.status === 503) {
        unavailable = true;
      } else {
        loadError = friendlyError(err, t);
      }
    } finally {
      loading = false;
    }
  }

  // --- editor ------------------------------------------------------
  function openCreate() {
    saveError = null;
    draft = {
      id: null,
      name: "",
      kind: "pin",
      pin: "",
      duress: false,
      perms: { arm: false, disarm: true, silence: false },
      areas: [],
      bindingText: "",
      validFrom: "",
      validUntil: "",
      enabled: true,
    };
  }

  function openEdit(c: AlarmCode) {
    saveError = null;
    draft = {
      id: c.id,
      name: c.name,
      kind: c.kind,
      pin: "", // never returned; blank keeps the stored hash
      duress: c.duress === true,
      perms: {
        arm: c.perms.arm,
        disarm: c.perms.disarm,
        silence: c.perms.silence,
      },
      areas: [...(c.areas ?? [])],
      bindingText: c.binding != null ? JSON.stringify(c.binding, null, 2) : "",
      validFrom: msToInput(c.valid_from_ms),
      validUntil: msToInput(c.valid_until_ms),
      enabled: c.enabled,
    };
  }

  function toggleArea(id: string) {
    if (!draft) return;
    const set = new Set(draft.areas);
    if (set.has(id)) set.delete(id);
    else set.add(id);
    draft = { ...draft, areas: areas.map((a) => a.id).filter((x) => set.has(x)) };
  }

  // Build the write body from the draft, or null with saveError set on a
  // validation failure.
  function buildRequest(d: Draft): AlarmCodeRequest | null {
    const name = d.name.trim();
    if (!name) {
      saveError = t("alarm.codes.error.name_required");
      return null;
    }
    if (d.kind === "pin" && d.id == null && d.pin.trim() === "") {
      saveError = t("alarm.codes.error.pin_required");
      return null;
    }
    let binding: unknown;
    if (d.kind !== "pin" && d.bindingText.trim() !== "") {
      try {
        binding = JSON.parse(d.bindingText);
      } catch {
        saveError = t("alarm.codes.error.binding_json");
        return null;
      }
    }
    const req: AlarmCodeRequest = {
      name,
      kind: d.kind,
      perms: { ...d.perms },
      enabled: d.enabled,
    };
    // PIN + duress only carry meaning for the pin kind. Send the PIN only
    // when the operator typed one (blank keeps the stored hash on edit).
    if (d.kind === "pin") {
      if (d.pin.trim() !== "") req.pin = d.pin;
      if (d.duress) req.duress = true;
    } else if (binding !== undefined) {
      req.binding = binding;
    }
    if (d.areas.length > 0) req.areas = d.areas;
    const from = inputToMs(d.validFrom);
    const until = inputToMs(d.validUntil);
    if (from) req.valid_from_ms = from;
    if (until) req.valid_until_ms = until;
    return req;
  }

  async function save() {
    if (!draft) return;
    saveError = null;
    const req = buildRequest(draft);
    if (!req) return;
    saving = true;
    try {
      if (draft.id == null) {
        await api.createAlarmCode(req);
      } else {
        await api.putAlarmCode(draft.id, req);
      }
      toastStore.success(t("alarm.toast.saved"));
      draft = null;
      await load();
    } catch (err) {
      saveError = friendlyError(err, t);
    } finally {
      saving = false;
    }
  }

  async function remove(c: AlarmCode) {
    const ok = await confirmStore.ask({
      title: t("alarm.codes.delete.confirm.title"),
      body: t("alarm.codes.delete.confirm.body", { name: c.name }),
      confirmLabel: t("common.delete"),
      destructive: true,
    });
    if (!ok) return;
    try {
      await api.deleteAlarmCode(c.id);
      toastStore.success(t("alarm.toast.deleted"));
      await load();
    } catch (err) {
      toastStore.error(t("alarm.toast.delete_failed"), friendlyError(err, t));
    }
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === "Escape" && draft) draft = null;
  }

  onMount(() => {
    void load();
  });
</script>

<svelte:window onkeydown={onKeydown} />

<div class="mb-4 flex flex-wrap items-center gap-3">
  <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("alarm.codes.subtitle")}</p>
  <Button size="sm" class="ml-auto" onclick={openCreate} disabled={unavailable}>
    <Icon name="mdi:plus" size={16} aria-label="" />
    {t("alarm.codes.add")}
  </Button>
</div>

{#if loading && codes.length === 0}
  <LoadingState message={t("common.loading")} />
{:else if unavailable}
  <EmptyState
    icon="mdi:key"
    message={t("alarm.codes.unavailable")}
    description={t("alarm.codes.unavailable.description")}
  />
{:else if loadError}
  <ErrorState message={loadError} onRetry={() => void load()} />
{:else if codes.length === 0}
  <EmptyState
    icon="mdi:key"
    message={t("alarm.codes.empty")}
    description={t("alarm.codes.empty.description")}
  >
    {#snippet action()}
      <Button variant="outline" size="sm" onclick={openCreate}>
        <Icon name="mdi:plus" size={16} aria-label="" />
        {t("alarm.codes.add")}
      </Button>
    {/snippet}
  </EmptyState>
{:else}
  <div class="grid grid-cols-1 gap-3 lg:grid-cols-2">
    {#each codes as c (c.id)}
      <Card class="flex flex-col gap-3 p-4">
        <!-- Header -->
        <div class="flex items-start gap-2">
          <Icon name={KIND_ICON[c.kind]} size={22} class="mt-0.5 text-[var(--ha-primary-color)]" aria-label="" />
          <div class="min-w-0 flex-1">
            <p class="truncate font-medium text-[var(--ha-primary-text-color)]" title={c.name}>
              {c.name}
            </p>
            <p class="truncate text-xs text-[var(--ha-secondary-text-color)]">
              {t(`alarm.codes.kind.${c.kind}`)}
            </p>
          </div>
          <div class="flex shrink-0 items-center gap-1">
            {#if !c.enabled}
              <Badge variant="muted">{t("alarm.codes.disabled")}</Badge>
            {/if}
            {#if c.duress}
              <Badge variant="danger">{t("alarm.codes.duress.badge")}</Badge>
            {/if}
            <button
              type="button"
              class="text-[var(--ha-secondary-text-color)] hover:text-[var(--ha-primary-color)]"
              title={t("common.edit")}
              aria-label={t("common.edit")}
              onclick={() => openEdit(c)}
            >
              <Icon name="mdi:pencil" size={16} aria-label="" />
            </button>
            <button
              type="button"
              class="text-[var(--ha-secondary-text-color)] hover:text-[var(--ha-error-color)]"
              title={t("common.delete")}
              aria-label={t("common.delete")}
              onclick={() => void remove(c)}
            >
              <Icon name="mdi:trash-can" size={16} aria-label="" />
            </button>
          </div>
        </div>

        <!-- Permissions -->
        <div class="flex flex-wrap items-center gap-1.5">
          <span class="text-xs text-[var(--ha-secondary-text-color)]">{t("alarm.codes.perms")}</span>
          {#each permChips(c) as p (p)}
            <Badge variant="default">{p}</Badge>
          {:else}
            <span class="text-xs text-[var(--ha-secondary-text-color)]">{t("common.none")}</span>
          {/each}
        </div>

        <!-- Areas -->
        <div class="flex flex-wrap items-center gap-1.5 text-xs text-[var(--ha-secondary-text-color)]">
          <span>{t("alarm.codes.areas")}</span>
          {#if !c.areas || c.areas.length === 0}
            <span class="text-[var(--ha-primary-text-color)]">{t("alarm.codes.areas.all")}</span>
          {:else}
            <span class="text-[var(--ha-primary-text-color)]">
              {c.areas.map(areaName).join(", ")}
            </span>
          {/if}
        </div>

        <!-- Validity window -->
        {#if c.valid_from_ms || c.valid_until_ms}
          <div class="flex items-center gap-1.5 text-xs text-[var(--ha-secondary-text-color)]">
            <Icon name="mdi:calendar-clock" size={14} aria-label="" />
            <span>
              {c.valid_from_ms ? fmtDate(c.valid_from_ms) : t("alarm.codes.validity.open")}
              &ndash;
              {c.valid_until_ms ? fmtDate(c.valid_until_ms) : t("alarm.codes.validity.open")}
            </span>
          </div>
        {/if}
      </Card>
    {/each}
  </div>
{/if}

<!-- Editor drawer -->
{#if draft}
  <div
    class="fixed inset-0 z-40 bg-black/40"
    role="presentation"
    onclick={() => (draft = null)}
  ></div>
  <div
    class="fixed right-0 top-0 z-50 flex h-full w-full max-w-md flex-col overflow-y-auto border-l border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] shadow-xl"
    role="dialog"
    aria-modal="true"
    aria-label={editing ? t("alarm.codes.edit") : t("alarm.codes.add")}
  >
    <header class="flex items-center gap-2 border-b border-[var(--ha-divider-color)] p-4">
      <Icon name="mdi:key" size={20} class="text-[var(--ha-primary-color)]" aria-label="" />
      <h2 class="flex-1 font-semibold text-[var(--ha-primary-text-color)]">
        {editing ? t("alarm.codes.edit") : t("alarm.codes.add")}
      </h2>
      <button
        type="button"
        class="text-[var(--ha-secondary-text-color)] hover:text-[var(--ha-primary-text-color)]"
        aria-label={t("common.close")}
        onclick={() => (draft = null)}
      >
        <Icon name="mdi:close" size={20} aria-label="" />
      </button>
    </header>

    <div class="flex flex-col gap-4 p-4">
      <!-- Name -->
      <label class="flex flex-col gap-1.5">
        <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.codes.field.name")}</span>
        <Input bind:value={draft.name} placeholder={t("alarm.codes.field.name")} />
      </label>

      <!-- Kind -->
      <div class="flex flex-col gap-1.5">
        <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.codes.field.kind")}</span>
        <Select
          value={draft.kind}
          onValueChange={(v) => draft && (draft = { ...draft, kind: v as AlarmCodeKind })}
          options={KINDS.map((k) => ({ value: k, label: t(`alarm.codes.kind.${k}`) }))}
        />
      </div>

      {#if draft.kind === "pin"}
        <!-- PIN (write-only) -->
        <label class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.codes.field.pin")}</span>
          <Input
            type="password"
            inputmode="numeric"
            autocomplete="off"
            bind:value={draft.pin}
            placeholder={editing ? t("alarm.codes.field.pin.keep") : t("alarm.codes.field.pin.placeholder")}
          />
          <span class="text-xs text-[var(--ha-secondary-text-color)]">{t("alarm.codes.field.pin.help")}</span>
        </label>

        <!-- Duress -->
        <div class="flex flex-col gap-1.5">
          <label class="flex items-center justify-between gap-2 text-sm text-[var(--ha-primary-text-color)]">
            <span>{t("alarm.codes.field.duress")}</span>
            <Switch checked={draft.duress} onCheckedChange={(v) => draft && (draft = { ...draft, duress: v })} />
          </label>
          {#if draft.duress}
            <div
              class="flex gap-2 rounded-md border border-[color-mix(in_srgb,var(--ha-error-color)_45%,transparent)] bg-[color-mix(in_srgb,var(--ha-error-color)_10%,transparent)] p-2 text-xs text-[var(--ha-primary-text-color)]"
            >
              <Icon name="mdi:alert" size={16} class="mt-0.5 shrink-0 text-[var(--ha-error-color)]" aria-label="" />
              <span>{t("alarm.codes.duress.warning")}</span>
            </div>
          {/if}
        </div>
      {:else}
        <!-- Hardware binding (keypad slot / remote key) -->
        <label class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.codes.field.binding")}</span>
          <textarea
            bind:value={draft.bindingText}
            rows="5"
            spellcheck="false"
            class="w-full rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-3 py-2 font-mono text-xs text-[var(--ha-primary-text-color)] shadow-sm focus-visible:border-[var(--ha-primary-color)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ha-primary-color)]"
            placeholder={"{ }"}
          ></textarea>
          <span class="text-xs text-[var(--ha-secondary-text-color)]">{t("alarm.codes.field.binding.help")}</span>
        </label>
      {/if}

      <!-- Permissions -->
      <div class="flex flex-col gap-2">
        <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.codes.perms")}</span>
        <label class="flex items-center justify-between gap-2 text-sm text-[var(--ha-primary-text-color)]">
          <span>{t("alarm.codes.perm.arm")}</span>
          <Switch checked={draft.perms.arm} onCheckedChange={(v) => draft && (draft = { ...draft, perms: { ...draft.perms, arm: v } })} />
        </label>
        <label class="flex items-center justify-between gap-2 text-sm text-[var(--ha-primary-text-color)]">
          <span>{t("alarm.codes.perm.disarm")}</span>
          <Switch checked={draft.perms.disarm} onCheckedChange={(v) => draft && (draft = { ...draft, perms: { ...draft.perms, disarm: v } })} />
        </label>
        <label class="flex items-center justify-between gap-2 text-sm text-[var(--ha-primary-text-color)]">
          <span>{t("alarm.codes.perm.silence")}</span>
          <Switch checked={draft.perms.silence} onCheckedChange={(v) => draft && (draft = { ...draft, perms: { ...draft.perms, silence: v } })} />
        </label>
      </div>

      <!-- Areas -->
      <div class="flex flex-col gap-2">
        <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.codes.field.areas")}</span>
        {#if areas.length === 0}
          <p class="text-xs text-[var(--ha-secondary-text-color)]">{t("alarm.codes.areas.all")}</p>
        {:else}
          <p class="text-xs text-[var(--ha-secondary-text-color)]">{t("alarm.codes.field.areas.help")}</p>
          <div class="flex flex-col gap-1">
            {#each areas as a (a.id)}
              <label class="flex items-center gap-2 text-sm text-[var(--ha-primary-text-color)]">
                <input
                  type="checkbox"
                  class="h-4 w-4 accent-[var(--ha-primary-color)]"
                  checked={draft.areas.includes(a.id)}
                  onchange={() => toggleArea(a.id)}
                />
                <span class="truncate">{a.name}</span>
              </label>
            {/each}
          </div>
        {/if}
      </div>

      <!-- Validity window -->
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <label class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.codes.field.valid_from")}</span>
          <input
            type="datetime-local"
            bind:value={draft.validFrom}
            class="h-10 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-3 text-sm text-[var(--ha-primary-text-color)] shadow-sm focus-visible:border-[var(--ha-primary-color)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ha-primary-color)]"
          />
        </label>
        <label class="flex flex-col gap-1.5">
          <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">{t("alarm.codes.field.valid_until")}</span>
          <input
            type="datetime-local"
            bind:value={draft.validUntil}
            class="h-10 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-3 text-sm text-[var(--ha-primary-text-color)] shadow-sm focus-visible:border-[var(--ha-primary-color)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ha-primary-color)]"
          />
        </label>
      </div>
      <p class="-mt-2 text-xs text-[var(--ha-secondary-text-color)]">{t("alarm.codes.field.validity.help")}</p>

      <!-- Enabled -->
      <label class="flex items-center justify-between gap-2 text-sm text-[var(--ha-primary-text-color)]">
        <span>{t("alarm.codes.field.enabled")}</span>
        <Switch checked={draft.enabled} onCheckedChange={(v) => draft && (draft = { ...draft, enabled: v })} />
      </label>

      {#if saveError}
        <p class="text-sm font-medium text-[var(--ha-error-color)]" role="alert">{saveError}</p>
      {/if}
    </div>

    <footer class="mt-auto flex gap-2 border-t border-[var(--ha-divider-color)] p-4">
      <Button variant="outline" size="sm" onclick={() => (draft = null)}>{t("common.cancel")}</Button>
      <Button size="sm" class="ml-auto" disabled={saving} onclick={() => void save()}>
        {saving ? t("common.saving") : t("common.save")}
      </Button>
    </footer>
  </div>
{/if}
