<script lang="ts">
  import type {
    ClimateSchedule,
    ClimateWeekday,
    ClimatePeriod,
  } from "$lib/api/types";
  import { api, ApiError } from "$lib/api/client";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import ClimateScheduleVisualization from "./ClimateScheduleVisualization.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";

  type Props = {
    address: string;
  };

  let { address }: Props = $props();

  // Local working copy; never mutated by the server fetch directly so
  // we keep a clean "server snapshot" to diff against (dirty flag) and
  // to roll back on "Zurücksetzen".
  let serverSchedule = $state<ClimateSchedule | null>(null);
  let schedule = $state<ClimateSchedule | null>(null);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let notSupported = $state(false);
  let saving = $state(false);
  let activeProfile = $state<string>("P1");

  // Clipboard for "copy day to other days" operation.
  let clipboard = $state<ClimateWeekday | null>(null);

  const weekdays = [
    "MONDAY",
    "TUESDAY",
    "WEDNESDAY",
    "THURSDAY",
    "FRIDAY",
    "SATURDAY",
    "SUNDAY",
  ] as const;

  function wd(day: string): string {
    return t(`weekday.long.${day}`);
  }

  async function load() {
    loading = true;
    loadError = null;
    notSupported = false;
    try {
      const s = await api.getDeviceSchedule(address);
      serverSchedule = deepClone(s);
      schedule = deepClone(s);
      if (!s.profiles) s.profiles = {};
      if (s.active_profile) activeProfile = s.active_profile;
      else if (Object.keys(s.profiles).length > 0) {
        activeProfile = Object.keys(s.profiles).sort()[0];
      }
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        notSupported = true;
      } else {
        loadError = err instanceof Error ? err.message : String(err);
      }
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    void load();
  });

  const profileIds = $derived(
    schedule ? Object.keys(schedule.profiles ?? {}).sort() : [],
  );

  const isDirty = $derived.by(() => {
    if (!schedule || !serverSchedule) return false;
    return (
      JSON.stringify(schedule.profiles) !==
      JSON.stringify(serverSchedule.profiles)
    );
  });

  // Detail line for a failure toast. The status separates a device/CCU
  // rejection (502) from a lock conflict (423), which is the first thing an
  // operator needs when a schedule did not reach the thermostat.
  function failureDetail(err: unknown): string {
    if (err instanceof ApiError) return `${err.status}: ${err.message}`;
    return err instanceof Error ? err.message : String(err);
  }

  async function save() {
    if (!schedule || !isDirty) return;
    saving = true;
    try {
      await api.putDeviceSchedule(address, schedule);
      toastStore.success(t("schedule.saved_toast"));
      await load();
    } catch (err) {
      toastStore.error(t("schedule.save_failed"), failureDetail(err));
    } finally {
      saving = false;
    }
  }

  function reset() {
    if (serverSchedule) schedule = deepClone(serverSchedule);
  }

  async function switchActiveProfile(pid: string) {
    if (!schedule || pid === schedule.active_profile) {
      activeProfile = pid;
      return;
    }
    saving = true;
    try {
      await api.setDeviceActiveProfile(address, pid);
      activeProfile = pid;
      if (schedule) schedule.active_profile = pid;
      if (serverSchedule) serverSchedule.active_profile = pid;
      toastStore.success(t("schedule.profile_active", { profile: pid }));
    } catch (err) {
      toastStore.error(t("climate.set_active_failed"), failureDetail(err));
    } finally {
      saving = false;
    }
  }

  // --- weekday mutation helpers ----------------------------------
  function getWeekday(day: string): ClimateWeekday | null {
    if (!schedule || !schedule.profiles) return null;
    const prof = schedule.profiles[activeProfile];
    if (!prof) return null;
    return prof.weekdays[day] ?? null;
  }

  function ensureWeekday(day: string): ClimateWeekday {
    if (!schedule) throw new Error("no schedule");
    if (!schedule.profiles) schedule.profiles = {};
    if (!schedule.profiles[activeProfile]) {
      schedule.profiles[activeProfile] = { weekdays: {} };
    }
    const prof = schedule.profiles[activeProfile];
    if (!prof.weekdays[day]) {
      prof.weekdays[day] = { base_temperature: 19.0, periods: [] };
    }
    return prof.weekdays[day];
  }

  function setBase(day: string, value: number) {
    const wd = ensureWeekday(day);
    wd.base_temperature = value;
    // Trigger reactivity on the profile map.
    schedule = schedule;
  }

  function updatePeriod(
    day: string,
    index: number,
    patch: Partial<ClimatePeriod>,
  ) {
    const wd = ensureWeekday(day);
    if (!wd.periods[index]) return;
    wd.periods[index] = { ...wd.periods[index], ...patch };
    schedule = schedule;
  }

  function addPeriod(day: string) {
    const wd = ensureWeekday(day);
    // Find a sensible start after the last period.
    const last = wd.periods[wd.periods.length - 1];
    const start = last ? last.end_time : "06:00";
    const end = addHour(start);
    wd.periods.push({
      start_time: start,
      end_time: end,
      temperature: 21.0,
    });
    schedule = schedule;
  }

  function removePeriod(day: string, index: number) {
    const wd = getWeekday(day);
    if (!wd) return;
    wd.periods.splice(index, 1);
    schedule = schedule;
  }

  function copyDay(day: string) {
    const wd = getWeekday(day);
    if (!wd) return;
    clipboard = deepClone(wd);
    toastStore.success(t("climate.day_copied", { count: wd.periods.length }));
  }

  function pasteDay(day: string) {
    if (!clipboard || !schedule || !schedule.profiles) return;
    const prof = schedule.profiles[activeProfile];
    if (!prof) return;
    prof.weekdays[day] = deepClone(clipboard);
    schedule = schedule;
    toastStore.success(t("climate.day_pasted", { day: wd(day) }));
  }

  function fillAllDays() {
    const wd = getWeekday("MONDAY");
    if (!wd || !schedule || !schedule.profiles) return;
    const prof = schedule.profiles[activeProfile];
    if (!prof) return;
    for (const day of weekdays) {
      if (day === "MONDAY") continue;
      prof.weekdays[day] = deepClone(wd);
    }
    schedule = schedule;
    toastStore.success(t("climate.fill_all_done"));
  }

  function addHour(hhmm: string): string {
    const [h, m] = hhmm.split(":").map(Number);
    const total = Math.min(h * 60 + m + 60, 24 * 60);
    return `${String(Math.floor(total / 60)).padStart(2, "0")}:${String(total % 60).padStart(2, "0")}`;
  }

  function deepClone<T>(v: T): T {
    return JSON.parse(JSON.stringify(v)) as T;
  }

  // Scroll the editor list to the weekday section that the
  // visualization just clicked on. The section element receives an
  // `id="climate-day-<DAY>"` so the lookup is purely DOM-based.
  function focusWeekday(day: string): void {
    const el = document.getElementById(`climate-day-${day}`);
    if (el) {
      el.scrollIntoView({ behavior: "smooth", block: "start" });
      el.classList.add("ring-2", "ring-brand-400");
      window.setTimeout(() => {
        el.classList.remove("ring-2", "ring-brand-400");
      }, 1200);
    }
  }
</script>

{#if loading}
  <Card class="p-6 text-center text-sm text-[var(--ha-secondary-text-color)]">
    {t("schedule.loading")}
  </Card>
{:else if notSupported}
  <Card class="p-6 text-center text-sm text-[var(--ha-secondary-text-color)]">
    {t("schedule.unsupported_channel")}
  </Card>
{:else if loadError}
  <Card class="p-3">
    <p class="text-sm text-red-600 dark:text-red-400">
      {t("common.error")} {loadError}
    </p>
  </Card>
{:else if schedule}
  <Card class="p-4">
    <header class="mb-4 flex flex-wrap items-center justify-between gap-3">
      <div class="flex items-center gap-2">
        {#each profileIds as pid (pid)}
          {@const active = pid === activeProfile}
          {@const running = pid === schedule.active_profile}
          <button
            type="button"
            class="rounded-md border px-3 py-1.5 text-sm transition {active
              ? 'border-brand-500 bg-brand-500 text-white'
              : 'border-slate-300 text-slate-700 dark:border-slate-700 dark:text-slate-200'}"
            onclick={() => (activeProfile = pid)}
          >
            {pid}
            {#if running}
              <Badge variant="default">{t("climate.profile_active_badge")}</Badge>
            {/if}
          </button>
        {/each}
        {#if schedule.active_profile !== activeProfile && profileIds.includes(activeProfile)}
          <Button
            type="button"
            variant="outline"
            size="sm"
            onclick={() => void switchActiveProfile(activeProfile)}
            disabled={saving}
          >
            {t("climate.set_active")}
          </Button>
        {/if}
      </div>
      <div class="flex items-center gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onclick={fillAllDays}
          disabled={saving}
          title={t("climate.fill_all.tooltip")}
        >
          {t("climate.fill_all")}
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

    <div class="mb-4">
      <ClimateScheduleVisualization
        profile={schedule.profiles?.[activeProfile]}
        onWeekdayClick={focusWeekday}
      />
    </div>

    <div class="grid grid-cols-1 gap-3">
      {#each weekdays as day (day)}
        {@const weekday =
          schedule.profiles?.[activeProfile]?.weekdays?.[day] ?? {
            base_temperature: 19,
            periods: [],
          }}
        <section
          id="climate-day-{day}"
          class="scroll-mt-20 rounded-md border border-slate-200 p-3 transition dark:border-slate-800"
        >
          <header class="mb-2 flex flex-wrap items-center justify-between gap-2">
            <h3 class="text-sm font-semibold">{wd(day)}</h3>
            <div class="flex flex-wrap items-center gap-2">
              <label class="flex items-center gap-2 text-xs text-slate-600 dark:text-slate-400">
                {t("climate.base_label")}
                <div class="w-20">
                  <Input
                    type="number"
                    step="0.5"
                    value={weekday.base_temperature}
                    oninput={(e) => {
                      const n = Number((e.target as HTMLInputElement).value);
                      if (Number.isFinite(n)) setBase(day, n);
                    }}
                  />
                </div>
                <span class="text-[var(--ha-secondary-text-color)]">°C</span>
              </label>
              <Button type="button" variant="outline" size="sm" onclick={() => copyDay(day)}>
                {t("common.copy")}
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onclick={() => pasteDay(day)}
                disabled={!clipboard}
              >
                {t("common.paste")}
              </Button>
              <Button type="button" size="sm" onclick={() => addPeriod(day)}>
                {t("climate.add_period")}
              </Button>
            </div>
          </header>

          {#if weekday.periods.length === 0}
            <p class="text-xs italic text-[var(--ha-secondary-text-color)]">
              {t("climate.all_day", { temp: weekday.base_temperature.toFixed(1) })}
            </p>
          {:else}
            <ul class="space-y-2">
              {#each weekday.periods as period, idx (idx)}
                <!-- Two cohesive groups (time-pair · temp+delete) so the
                     row wraps between them on a phone instead of orphaning
                     the → arrow. -->
                <li class="flex flex-wrap items-center gap-x-3 gap-y-2 text-sm">
                  <div class="flex items-center gap-2">
                    <div class="w-28">
                      <Input
                        type="time"
                        value={period.start_time}
                        onchange={(e) =>
                          updatePeriod(day, idx, {
                            start_time: (e.target as HTMLInputElement).value,
                          })}
                      />
                    </div>
                    <span class="text-[var(--ha-secondary-text-color)]">→</span>
                    <div class="w-28">
                      <Input
                        type="time"
                        value={period.end_time === "24:00"
                          ? "00:00"
                          : period.end_time}
                        onchange={(e) => {
                          const v = (e.target as HTMLInputElement).value;
                          updatePeriod(day, idx, {
                            end_time: v === "00:00" ? "24:00" : v,
                          });
                        }}
                      />
                    </div>
                  </div>
                  <div class="flex items-center gap-2">
                    <div class="w-20">
                      <Input
                        type="number"
                        step="0.5"
                        value={period.temperature}
                        oninput={(e) => {
                          const n = Number((e.target as HTMLInputElement).value);
                          if (Number.isFinite(n))
                            updatePeriod(day, idx, { temperature: n });
                        }}
                      />
                    </div>
                    <span class="text-[var(--ha-secondary-text-color)]">°C</span>
                    <Button
                      type="button"
                      variant="destructive"
                      size="sm"
                      onclick={() => removePeriod(day, idx)}
                    >
                      ×
                    </Button>
                  </div>
                </li>
              {/each}
            </ul>
          {/if}
        </section>
      {/each}
    </div>
  </Card>
{/if}
