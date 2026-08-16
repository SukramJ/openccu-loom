<script lang="ts">
  import type {
    ClimateSchedule,
    SimpleScheduleEntry,
  } from "$lib/api/types";
  import { api, ApiError } from "$lib/api/client";
  import { toastStore } from "$lib/stores/toast.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Switch from "$lib/components/ui/Switch.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import TargetChannelsPicker from "./TargetChannelsPicker.svelte";
  import ScheduleVisualization from "./ScheduleVisualization.svelte";
  import { t } from "$lib/i18n";

  type Props = {
    address: string;
    schedule: ClimateSchedule;
    onReload: () => void;
  };

  let { address, schedule, onReload }: Props = $props();

  // Domain dispatches widget choice. Mirrors aiohomematic's
  // DataPointCategory mapping in week_profile.create_empty_schedule_group:
  // SWITCH/LIGHT/COVER/LOCK/VALVE — each gets the fields it actually
  // needs and nothing else.
  const domain = $derived(
    (schedule.domain as
      | "switch"
      | "light"
      | "cover"
      | "lock"
      | "valve"
      | "climate"
      | "")
      ?? "",
  );

  let serverEntries = $state<SimpleScheduleEntry[]>([]);
  let entries = $state<SimpleScheduleEntry[]>([]);
  let saving = $state(false);
  // Keyed by slot number, not by list index: the list is keyed by
  // slot_no and removing an entry renumbers every index after it, which
  // would move the open advanced panel to a different slot.
  let expanded = $state<Set<number>>(new Set());

  $effect(() => {
    serverEntries = deepClone(schedule.simple_entries ?? []);
    entries = deepClone(schedule.simple_entries ?? []);
  });

  const weekdayKeys = [
    "MONDAY",
    "TUESDAY",
    "WEDNESDAY",
    "THURSDAY",
    "FRIDAY",
    "SATURDAY",
    "SUNDAY",
  ] as const;

  const conditionKeys = [
    "fixed_time",
    "astro",
    "fixed_if_before_astro",
    "astro_if_before_fixed",
    "fixed_if_after_astro",
    "astro_if_after_fixed",
    "earliest_of_fixed_and_astro",
    "latest_of_fixed_and_astro",
  ] as const;

  function shortDay(d: string): string {
    return t(`weekday.short.${d}`);
  }

  function condLabel(c: string): string {
    return t(`schedule.cond.${c}`);
  }

  const isDirty = $derived(
    JSON.stringify(entries) !== JSON.stringify(serverEntries),
  );

  function nextFreeSlot(): number {
    const used = new Set(entries.map((e) => e.slot_no));
    for (let i = 1; i <= 24; i++) if (!used.has(i)) return i;
    return -1;
  }

  function emptyEntryForDomain(slotNo: number): SimpleScheduleEntry {
    const base: SimpleScheduleEntry = {
      slot_no: slotNo,
      weekdays: ["MONDAY", "TUESDAY", "WEDNESDAY", "THURSDAY", "FRIDAY"],
      time: "07:00",
      condition: "fixed_time",
      level: 1,
    };
    if (domain === "cover") base.level_2 = 0;
    return base;
  }

  function addEntry() {
    const slot = nextFreeSlot();
    if (slot < 0) {
      toastStore.warn(t("schedule.max_reached"));
      return;
    }
    entries = [...entries, emptyEntryForDomain(slot)];
  }

  function removeEntry(idx: number) {
    const gone = entries[idx]?.slot_no;
    entries = entries.filter((_, i) => i !== idx);
    if (gone !== undefined && expanded.has(gone)) {
      const next = new Set(expanded);
      next.delete(gone);
      expanded = next;
    }
  }

  function toggleWeekday(idx: number, day: string) {
    const cur = entries[idx].weekdays;
    const next = cur.includes(day)
      ? cur.filter((d) => d !== day)
      : [...cur, day].sort(
          (a, b) =>
            weekdayKeys.indexOf(a as never) -
            weekdayKeys.indexOf(b as never),
        );
    entries[idx] = { ...entries[idx], weekdays: next };
  }

  // colorLabel renders a read-only category for a universal-light switch
  // point. The packed color_value is opaque in this slice (its 20-bit
  // layout is firmware-specific and unvalidated), so only the type is
  // surfaced — the raw value round-trips verbatim without being decoded.
  function colorLabel(type: number): string {
    if (type === 1) return t("schedule.color.temperature");
    if (type === 2) return t("schedule.color.effect");
    return t("schedule.color.hue_saturation");
  }

  function patch(idx: number, p: Partial<SimpleScheduleEntry>) {
    entries[idx] = { ...entries[idx], ...p };
  }

  function reset() {
    entries = deepClone(serverEntries);
    expanded = new Set();
  }

  function toggleExpanded(slotNo: number) {
    const next = new Set(expanded);
    if (next.has(slotNo)) next.delete(slotNo);
    else next.add(slotNo);
    expanded = next;
  }

  // Called from the visualisation strip when the user clicks a slot
  // marker. Scrolls the corresponding list row into view and expands
  // its advanced section so the user lands on the editable detail.
  function focusSlot(slotNo: number) {
    if (!entries.some((e) => e.slot_no === slotNo)) return;
    if (!expanded.has(slotNo)) {
      const next = new Set(expanded);
      next.add(slotNo);
      expanded = next;
    }
    // requestAnimationFrame so the (now expanded) row has a layout
    // before we scroll it.
    requestAnimationFrame(() => {
      const el = document.getElementById(`schedule-slot-${slotNo}`);
      if (el) el.scrollIntoView({ behavior: "smooth", block: "center" });
    });
  }

  async function save() {
    saving = true;
    try {
      for (const e of entries) {
        if (e.weekdays.length === 0) {
          toastStore.warn(
            t("schedule.weekday_select_one", { n: e.slot_no }),
          );
          saving = false;
          return;
        }
        if (!/^\d{2}:\d{2}$/.test(e.time)) {
          toastStore.warn(
            t("schedule.invalid_time", { n: e.slot_no, time: e.time }),
          );
          saving = false;
          return;
        }
      }
      const payload: ClimateSchedule = {
        channel: schedule.channel,
        kind: "simple",
        simple_entries: entries,
      };
      await api.putDeviceSchedule(address, payload);
      toastStore.success(t("schedule.saved_toast"));
      onReload();
    } catch (err) {
      toastStore.error(
        t("schedule.save_failed"),
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err),
      );
    } finally {
      saving = false;
    }
  }

  function deepClone<T>(v: T): T {
    return JSON.parse(JSON.stringify(v)) as T;
  }

  // Convenience: a slot "involves astro" when its condition is not
  // pure "fixed_time". Used to gate the astro-detail row.
  function involvesAstro(c: string | undefined): boolean {
    return !!c && c !== "fixed_time";
  }

  // Domain-driven options for the level widget. Switches: binary
  // toggle. Lights: slider 0-100. Covers: slider 0-100 plus optional
  // slat (level_2). Locks: dropdown of fixed lock actions. Valves:
  // 0-100 slider.
  function levelLabel(): string {
    return t("schedule.level");
  }
</script>

<Card class="p-4">
  <header class="mb-4 flex flex-wrap items-center justify-between gap-3">
    <div>
      <h2 class="text-lg font-semibold">{t("schedule.simple_title")}</h2>
      <p class="flex items-center gap-2 text-xs text-[var(--ha-secondary-text-color)]">
        <span>{t("schedule.slots_count", { count: entries.length })}</span>
        {#if domain}
          <Badge variant="muted">{domain}</Badge>
        {/if}
      </p>
    </div>
    <div class="flex items-center gap-2">
      <Button type="button" variant="outline" size="sm" onclick={addEntry}>
        {t("schedule.add_slot")}
      </Button>
      <Button
        type="button"
        variant="outline"
        size="sm"
        onclick={reset}
        disabled={!isDirty || saving}
      >
        {t("common.reset")}
      </Button>
      <Button
        type="button"
        size="sm"
        onclick={() => void save()}
        disabled={!isDirty || saving}
      >
        {saving ? t("common.saving") : t("common.save")}
      </Button>
    </div>
  </header>

  {#if entries.length === 0}
    <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("schedule.empty_slots")}</p>
  {:else}
    <!-- Weekly visualisation: at-a-glance preview of when each
         slot fires. Clicking a marker scrolls the matching editor
         row into view and expands its advanced details. -->
    <div class="mb-4">
      <ScheduleVisualization
        {entries}
        {domain}
        onSlotClick={focusSlot}
      />
    </div>
    <ul class="space-y-2">
      {#each entries as entry, idx (entry.slot_no)}
        {@const isExpanded = expanded.has(entry.slot_no)}
        {@const astro = involvesAstro(entry.condition)}
        <li
          id="schedule-slot-{entry.slot_no}"
          class="rounded-md border border-slate-200 bg-white p-3 shadow-sm dark:border-slate-800 dark:bg-slate-900"
        >
          <!-- Primary row — always visible. The widget mix changes
               with the device domain so the user only sees the
               controls that are meaningful for their device. -->
          <div class="flex flex-wrap items-center gap-3">
            <Badge variant="muted">#{entry.slot_no}</Badge>

            <div class="flex items-center gap-1" role="group" aria-label={t("schedule.aria.weekdays")}>
              {#each weekdayKeys as day (day)}
                {@const active = entry.weekdays.includes(day)}
                <button
                  type="button"
                  class="h-9 w-9 rounded-full border text-xs font-medium transition {active
                    ? 'border-brand-500 bg-brand-500 text-white'
                    : 'border-slate-300 text-slate-600 hover:border-brand-500 dark:border-slate-700 dark:text-slate-300'}"
                  aria-pressed={active}
                  onclick={() => toggleWeekday(idx, day)}
                >
                  {shortDay(day)}
                </button>
              {/each}
            </div>

            <div class="w-24">
              <Input
                type="time"
                value={entry.time}
                onchange={(e) =>
                  patch(idx, {
                    time: (e.target as HTMLInputElement).value,
                  })}
              />
            </div>

            <!-- Domain-specific level widget. Switch only needs an
                 on/off toggle; everything else exposes the numeric
                 level. Cover additionally edits level_2 (slat). -->
            {#if domain === "switch"}
              <label class="flex items-center gap-2 text-sm text-slate-600 dark:text-slate-300">
                <Switch
                  checked={entry.level >= 0.5}
                  onCheckedChange={(v) => patch(idx, { level: v ? 1 : 0 })}
                />
                <span>{entry.level >= 0.5 ? t("quick.on") : t("quick.off")}</span>
              </label>
            {:else if domain === "cover"}
              <label class="flex items-center gap-2 text-xs text-slate-600 dark:text-slate-400">
                {t("schedule.cover.position")}
                <input
                  type="range"
                  min="0"
                  max="100"
                  step="5"
                  value={Math.round(entry.level * 100)}
                  oninput={(e) =>
                    patch(idx, {
                      level: Number((e.target as HTMLInputElement).value) / 100,
                    })}
                  class="h-3 w-32 cursor-pointer accent-brand-500"
                />
                <span class="w-10 text-right font-mono">
                  {Math.round(entry.level * 100)}%
                </span>
              </label>
              <label class="flex items-center gap-2 text-xs text-slate-600 dark:text-slate-400">
                {t("schedule.cover.slat")}
                <input
                  type="range"
                  min="0"
                  max="100"
                  step="5"
                  value={Math.round((entry.level_2 ?? 0) * 100)}
                  oninput={(e) =>
                    patch(idx, {
                      level_2:
                        Number((e.target as HTMLInputElement).value) / 100,
                    })}
                  class="h-3 w-24 cursor-pointer accent-brand-500"
                />
                <span class="w-10 text-right font-mono">
                  {Math.round((entry.level_2 ?? 0) * 100)}%
                </span>
              </label>
            {:else if domain === "lock"}
              <!-- Lock devices: show mode + (action|permission). The
                   underlying level/duration/target_channels are
                   recomputed by the backend on save (port of
                   aiohomematic's _LOCK_ACTION_TO_RAW). -->
              <label class="flex flex-wrap items-center gap-2 text-xs">
                <span class="text-slate-600 dark:text-slate-400">
                  {t("schedule.lock.mode")}
                </span>
                <div class="w-44">
                  <Select
                    options={[
                      {
                        value: "door_lock",
                        label: t("schedule.lock.door_lock"),
                      },
                      {
                        value: "user_permission",
                        label: t("schedule.lock.user_permission"),
                      },
                    ]}
                    value={entry.lock_mode ?? "door_lock"}
                    onValueChange={(v) =>
                      patch(idx, {
                        lock_mode: v as "door_lock" | "user_permission",
                        // Reset the orthogonal field so we don't ship
                        // stale conflicting state.
                        lock_action: v === "door_lock" ? entry.lock_action ?? "lock_autorelock_end" : undefined,
                        permission: v === "user_permission" ? entry.permission ?? "granted" : undefined,
                      })}
                  />
                </div>
              </label>
              {#if (entry.lock_mode ?? "door_lock") === "door_lock"}
                <label class="flex flex-wrap items-center gap-2 text-xs">
                  <span class="text-slate-600 dark:text-slate-400">
                    {t("schedule.lock.action")}
                  </span>
                  <div class="w-56">
                    <Select
                      options={[
                        {
                          value: "lock_autorelock_end",
                          label: t("schedule.lock.action.lock_autorelock_end"),
                        },
                        {
                          value: "lock_autorelock_start",
                          label: t("schedule.lock.action.lock_autorelock_start"),
                        },
                        {
                          value: "unlock_autorelock_end",
                          label: t("schedule.lock.action.unlock_autorelock_end"),
                        },
                        {
                          value: "autorelock_end",
                          label: t("schedule.lock.action.autorelock_end"),
                        },
                      ]}
                      value={entry.lock_action ?? "lock_autorelock_end"}
                      onValueChange={(v) =>
                        patch(idx, { lock_action: v })}
                    />
                  </div>
                </label>
              {:else}
                <label class="flex flex-wrap items-center gap-2 text-xs">
                  <span class="text-slate-600 dark:text-slate-400">
                    {t("schedule.lock.permission")}
                  </span>
                  <div class="w-44">
                    <Select
                      options={[
                        {
                          value: "granted",
                          label: t("schedule.lock.granted"),
                        },
                        {
                          value: "not_granted",
                          label: t("schedule.lock.not_granted"),
                        },
                      ]}
                      value={entry.permission ?? "granted"}
                      onValueChange={(v) =>
                        patch(idx, { permission: v })}
                    />
                  </div>
                </label>
              {/if}
            {:else if domain === "light" || domain === "valve"}
              <label class="flex items-center gap-2 text-xs text-slate-600 dark:text-slate-400">
                {levelLabel()}
                <input
                  type="range"
                  min="0"
                  max="100"
                  step="1"
                  value={Math.round(entry.level * 100)}
                  oninput={(e) =>
                    patch(idx, {
                      level: Number((e.target as HTMLInputElement).value) / 100,
                    })}
                  class="h-3 w-32 cursor-pointer accent-brand-500"
                />
                <span class="w-10 text-right font-mono">
                  {Math.round(entry.level * 100)}%
                </span>
              </label>
            {:else}
              <!-- Generic fallback — bare numeric input. -->
              <div class="w-20">
                <Input
                  type="number"
                  step="0.1"
                  min="0"
                  max="1"
                  value={entry.level}
                  oninput={(e) => {
                    const n = Number((e.target as HTMLInputElement).value);
                    if (Number.isFinite(n)) patch(idx, { level: n });
                  }}
                />
              </div>
            {/if}

            {#if astro}
              <Badge variant="default">{condLabel(entry.condition!)}</Badge>
            {/if}

            <button
              type="button"
              class="ml-auto text-xs text-[var(--ha-secondary-text-color)] hover:text-brand-700"
              aria-expanded={isExpanded}
              onclick={() => toggleExpanded(entry.slot_no)}
              title={t("schedule.advanced")}
            >
              {isExpanded ? "▾" : "▸"} {t("schedule.advanced")}
            </button>

            <Button
              type="button"
              variant="destructive"
              size="sm"
              onclick={() => removeEntry(idx)}
              title={t("common.remove")}
            >
              ×
            </Button>
          </div>

          {#if isExpanded}
            <!-- Advanced row: condition, astro, target channels,
                 duration / ramp_time. Hidden by default because
                 the average user never touches these. -->
            <div class="mt-3 grid grid-cols-1 gap-3 border-t border-slate-200 pt-3 text-xs dark:border-slate-700 md:grid-cols-2">
              <label class="flex items-center gap-2">
                <span class="text-slate-600 dark:text-slate-400">
                  {t("schedule.condition")}
                </span>
                <div class="flex-1">
                  <Select
                    options={conditionKeys.map((c) => ({
                      value: c,
                      label: condLabel(c),
                    }))}
                    value={entry.condition ?? "fixed_time"}
                    onValueChange={(v) => patch(idx, { condition: v })}
                  />
                </div>
              </label>

              {#if astro}
                <label class="flex flex-wrap items-center gap-2">
                  <span class="text-slate-600 dark:text-slate-400">
                    {t("schedule.astro")}
                  </span>
                  <div class="w-32">
                    <Select
                      options={[
                        {
                          value: "sunrise",
                          label: t("schedule.astro.sunrise"),
                        },
                        {
                          value: "sunset",
                          label: t("schedule.astro.sunset"),
                        },
                      ]}
                      value={entry.astro_type ?? "sunrise"}
                      onValueChange={(v) =>
                        patch(idx, { astro_type: v as "sunrise" | "sunset" })}
                    />
                  </div>
                  <span class="text-slate-600 dark:text-slate-400">
                    {t("schedule.astro.offset")}
                  </span>
                  <div class="w-20">
                    <Input
                      type="number"
                      step="1"
                      min="-720"
                      max="720"
                      value={entry.astro_offset_minutes ?? 0}
                      oninput={(e) => {
                        const n = Number(
                          (e.target as HTMLInputElement).value,
                        );
                        if (Number.isFinite(n))
                          patch(idx, { astro_offset_minutes: n });
                      }}
                    />
                  </div>
                  <span class="text-[var(--ha-secondary-text-color)]">min</span>
                </label>
              {/if}

              {#if domain !== "cover" && domain !== "lock"}
                <label class="flex items-center gap-2">
                  <span class="text-slate-600 dark:text-slate-400">
                    {t("schedule.duration")}
                  </span>
                  <div class="w-32">
                    <Input
                      type="text"
                      placeholder={t("schedule.duration_placeholder")}
                      value={entry.duration ?? ""}
                      oninput={(e) =>
                        patch(idx, {
                          duration: (e.target as HTMLInputElement).value || undefined,
                        })}
                    />
                  </div>
                </label>
              {/if}

              {#if domain === "light"}
                <label class="flex items-center gap-2">
                  <span class="text-slate-600 dark:text-slate-400">
                    {t("schedule.ramp_time")}
                  </span>
                  <div class="w-32">
                    <Input
                      type="text"
                      placeholder={t("schedule.ramp_placeholder")}
                      value={entry.ramp_time ?? ""}
                      oninput={(e) =>
                        patch(idx, {
                          ramp_time: (e.target as HTMLInputElement).value || undefined,
                        })}
                    />
                  </div>
                </label>
              {/if}

              {#if schedule.color_capable && entry.color_type != null}
                <label class="flex items-center gap-2">
                  <span class="text-slate-600 dark:text-slate-400">
                    {t("schedule.color")}
                  </span>
                  <Badge variant="muted">{colorLabel(entry.color_type)}</Badge>
                </label>
              {/if}

              <div class="md:col-span-2">
                <p class="mb-1 text-slate-600 dark:text-slate-400">
                  {t("schedule.target_channels")}
                </p>
                <TargetChannelsPicker
                  selected={entry.target_channels ?? []}
                  onChange={(v) => patch(idx, { target_channels: v })}
                />
              </div>
            </div>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</Card>
