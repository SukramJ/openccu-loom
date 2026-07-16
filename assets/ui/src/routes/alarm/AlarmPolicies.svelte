<script lang="ts">
  import { api, friendlyError } from "$lib/api/client";
  import { alarmPanelStore } from "$lib/stores/alarmPanel.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";
  import type { AlarmArea, AlarmAreaConfig } from "$lib/api/types";
  import Icon from "$lib/components/ui/Icon.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import Switch from "$lib/components/ui/Switch.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";

  // Policy editor (docs/alarm-concept.md §11 users/codes, §15 rows 19/21/22
  // schedules/pre-alarm/auto-rearm). Edits the engine-owned free-form
  // AlarmAreaConfig document directly by JSON path — same "local
  // dirty-tracked working copy, Save PUTs the whole area back" shape as
  // AlarmSensors/AlarmOutputs, just against api.getAlarmArea/putAlarmArea
  // instead of the sensors/outputs sub-collections. Field paths, defaults,
  // and the code-policy/output-policy semantics are transcribed verbatim
  // from internal/alarm/engine/config.go (CodePolicy, OutputPolicy,
  // ModeConfig.PreAlarmSeconds, AreaConfig.AutoRearmSeconds/PostTrigger,
  // AlarmSchedule) — this view never invents new engine semantics.

  const MODES = ["perimeter", "full", "night", "vacation", "custom"] as const;
  type Mode = (typeof MODES)[number];

  // Anonymous surfaces a per-source silence-code requirement can gate
  // (CodePolicy.RequireSilence). Operator sessions (rest-operator /
  // ws-operator / hmcli) always bypass every code check — the break-glass
  // degradation in docs/alarm-concept.md §11 — so they are not offered
  // here; toggling them would have no engine-side effect.
  const SILENCE_SOURCES = ["mqtt", "keypad", "remote"] as const;

  // Go's time.Weekday numbering (0=Sunday..6=Saturday), matching
  // AlarmSchedule.Days. Reuses the existing weekday.short.* catalogue
  // (week-profile schedules) instead of minting parallel day-name keys.
  const WEEKDAY_NAMES = [
    "SUNDAY",
    "MONDAY",
    "TUESDAY",
    "WEDNESDAY",
    "THURSDAY",
    "FRIDAY",
    "SATURDAY",
  ] as const;

  type ScheduleRow = {
    time?: string;
    days?: number[];
    mode?: string;
    auto_arm?: boolean;
  };

  // --- area state ----------------------------------------------------
  const areas = $derived(alarmPanelStore.areasConfig);
  let areaId = $state("");

  let areaMeta = $state<{ id: string; name: string; position?: number } | null>(null);
  let config = $state<Record<string, unknown>>({});
  let loading = $state(false);
  let loadError = $state<string | null>(null);
  let saving = $state(false);
  let dirty = $state(false);

  // --- generic JSON-path helpers (config is a free document, §14) ----
  function getPath(path: (string | number)[]): unknown {
    let cur: unknown = config;
    for (const key of path) {
      if (cur !== null && typeof cur === "object") {
        cur = (cur as Record<string, unknown>)[String(key)];
      } else {
        return undefined;
      }
    }
    return cur;
  }
  function setPath(path: (string | number)[], value: unknown) {
    function rec(node: unknown, i: number): unknown {
      if (i === path.length) return value;
      const key = path[i];
      if (typeof key === "number") {
        const arr = Array.isArray(node) ? [...node] : [];
        arr[key] = rec(arr[key], i + 1);
        return arr;
      }
      const obj =
        node !== null && typeof node === "object" && !Array.isArray(node)
          ? { ...(node as Record<string, unknown>) }
          : {};
      obj[key] = rec(obj[key], i + 1);
      return obj;
    }
    config = rec(config, 0) as Record<string, unknown>;
    dirty = true;
  }
  function getBool(path: (string | number)[]): boolean {
    return getPath(path) === true;
  }
  function getNum(path: (string | number)[]): number | undefined {
    const v = getPath(path);
    return typeof v === "number" ? v : undefined;
  }
  function getStr(path: (string | number)[]): string {
    const v = getPath(path);
    return typeof v === "string" ? v : "";
  }
  function setNum(path: (string | number)[], raw: string) {
    const trimmed = raw.trim();
    const n = trimmed === "" ? undefined : Number(trimmed);
    setPath(path, n === undefined || Number.isNaN(n) ? undefined : n);
  }
  function setBoolOrUndefined(path: (string | number)[], v: boolean) {
    // omitempty fields: writing false back as `undefined` keeps the
    // persisted document minimal, matching the engine's zero-value
    // defaults instead of pinning an explicit false forever.
    setPath(path, v ? true : undefined);
  }

  // --- code policy (§11) ----------------------------------------------
  // RequireDisarm is a nullable bool in the engine (unset = "required
  // whenever the area has an enabled code"); modeled as a tri-state
  // select so the default stays representable and distinct from an
  // explicit "never".
  function requireDisarmValue(): string {
    const v = getPath(["code_policy", "require_disarm"]);
    if (v === true) return "always";
    if (v === false) return "never";
    return "default";
  }
  function setRequireDisarm(v: string) {
    if (v === "always") setPath(["code_policy", "require_disarm"], true);
    else if (v === "never") setPath(["code_policy", "require_disarm"], false);
    else setPath(["code_policy", "require_disarm"], undefined);
  }
  const requireDisarmOptions = [
    { value: "default", label: t("alarm.policies.code.require_disarm.default") },
    { value: "always", label: t("alarm.policies.code.require_disarm.always") },
    { value: "never", label: t("alarm.policies.code.require_disarm.never") },
  ];

  // --- output policy (hazard / panic, §6.1 / §7) -----------------------
  // Only the fields meaningful for an always-on 24/7 output policy are
  // exposed; ArmDisarmChirps/CountdownTicks belong to the mode-based
  // arm/disarm chirp sequence (ModeConfig.Outputs) and have no effect on
  // the always-on hazard/panic path (engine.go alwaysOnFire/PanicTrigger
  // only read Silent/ExcludeOutdoor/SmokeSounders from this policy).
  function policyRow(kind: "hazard_outputs" | "panic_outputs") {
    return {
      silent: getBool([kind, "silent"]),
      excludeOutdoor: getBool([kind, "exclude_outdoor"]),
      smokeSounders: getBool([kind, "smoke_sounders"]),
    };
  }

  // --- pre-alarm (per configured mode, §15 row 21) ---------------------
  // PreAlarmSeconds lives on ModeConfig, not AreaConfig — only modes
  // already present in config.modes are armable at all, so editing is
  // scoped to those. There is no general-purpose mode editor outside the
  // Setup wizard's one-time delay step today; an area with no modes yet
  // configures them there first.
  function configuredModes(): Mode[] {
    const modes = getPath(["modes"]);
    if (!modes || typeof modes !== "object") return [];
    return MODES.filter((m) => Object.prototype.hasOwnProperty.call(modes, m));
  }

  // --- schedules (§15 row 19) ------------------------------------------
  function schedules(): ScheduleRow[] {
    const v = getPath(["schedules"]);
    return Array.isArray(v) ? (v as ScheduleRow[]) : [];
  }
  function addSchedule() {
    setPath(["schedules"], [
      ...schedules(),
      { time: "07:00", days: [], mode: "full", auto_arm: false },
    ]);
  }
  function removeSchedule(i: number) {
    setPath(
      ["schedules"],
      schedules().filter((_, idx) => idx !== i),
    );
  }
  function scheduleDays(row: ScheduleRow): number[] {
    return Array.isArray(row.days) ? row.days : [];
  }
  function toggleScheduleDay(i: number, day: number) {
    const set = new Set(scheduleDays(schedules()[i]));
    if (set.has(day)) set.delete(day);
    else set.add(day);
    setPath(
      ["schedules", i, "days"],
      Array.from(set).sort((a, b) => a - b),
    );
  }

  // --- data loading ------------------------------------------------
  async function loadPolicies() {
    if (!areaId) {
      areaMeta = null;
      config = {};
      return;
    }
    loading = true;
    loadError = null;
    try {
      const area = await api.getAlarmArea(areaId);
      areaMeta = { id: area.id, name: area.name, position: area.position };
      config = { ...((area.config as Record<string, unknown> | undefined) ?? {}) };
      dirty = false;
    } catch (err) {
      loadError = friendlyError(err, t);
    } finally {
      loading = false;
    }
  }

  async function save() {
    if (!areaId || !areaMeta) return;
    saving = true;
    try {
      const area: AlarmArea = {
        id: areaMeta.id,
        name: areaMeta.name,
        position: areaMeta.position,
        config: config as AlarmAreaConfig,
      };
      await api.putAlarmArea(areaId, area);
      toastStore.success(t("alarm.toast.saved"));
      dirty = false;
      await alarmPanelStore.refresh();
      await loadPolicies();
    } catch (err) {
      toastStore.error(t("alarm.toast.save_failed"), friendlyError(err, t));
    } finally {
      saving = false;
    }
  }

  const areaOptions = $derived(areas.map((a) => ({ value: a.id, label: a.name })));

  $effect(() => {
    if (areas.length > 0 && !areas.some((a) => a.id === areaId)) {
      areaId = areas[0].id;
    }
  });

  let loadedFor = $state("");
  $effect(() => {
    if (areaId && areaId !== loadedFor) {
      loadedFor = areaId;
      void loadPolicies();
    }
  });

  const numberInputClass =
    "h-9 w-24 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-2 text-right text-sm text-[var(--ha-primary-text-color)]";
  const sectionTitleClass = "text-sm font-semibold text-[var(--ha-primary-text-color)]";
  const hintClass = "text-xs text-[var(--ha-secondary-text-color)]";
  const rowClass = "flex items-center justify-between gap-2 text-sm text-[var(--ha-primary-text-color)]";
</script>

{#if areas.length === 0}
  <EmptyState
    icon="mdi:shield-home"
    message={t("alarm.overview.empty")}
    description={t("alarm.overview.empty.description")}
  >
    {#snippet action()}
      <a href="#/alarm/wizard">
        <Button variant="outline" size="sm">{t("alarm.wizard.launch")}</Button>
      </a>
    {/snippet}
  </EmptyState>
{:else}
  <!-- Toolbar: area selector -->
  <div class="mb-4 flex flex-wrap items-center gap-3">
    <label class="flex items-center gap-2 text-sm text-[var(--ha-secondary-text-color)]">
      <span>{t("alarm.sensors.area")}</span>
      <div class="min-w-48">
        <Select options={areaOptions} bind:value={areaId} />
      </div>
    </label>
  </div>

  {#if dirty}
    <div
      class="mb-3 flex flex-wrap items-center gap-2 rounded-md border border-[color-mix(in_srgb,var(--ha-warning-color)_40%,transparent)] bg-[color-mix(in_srgb,var(--ha-warning-color)_12%,transparent)] p-2 text-sm"
    >
      <span class="font-medium text-[var(--ha-warning-color)]">{t("common.modified")}</span>
      <div class="ml-auto flex gap-2">
        <Button variant="outline" size="sm" onclick={() => void loadPolicies()} disabled={saving}>
          {t("common.reset")}
        </Button>
        <Button size="sm" onclick={() => void save()} disabled={saving}>
          {saving ? t("common.saving") : t("common.save")}
        </Button>
      </div>
    </div>
  {/if}

  {#if loadError}
    <ErrorState message={loadError} onRetry={() => void loadPolicies()} />
  {:else if loading && !areaMeta}
    <LoadingState message={t("common.loading")} />
  {:else}
    <div class="flex flex-col gap-4">
      <!-- Code policy -->
      <Card class="flex flex-col gap-3 p-4">
        <h3 class={sectionTitleClass}>{t("alarm.policies.section.codes")}</h3>

        <label class={rowClass}>
          <span>{t("alarm.policies.code.require_arm")}</span>
          <Switch
            checked={getBool(["code_policy", "require_arm"])}
            onCheckedChange={(v) => setBoolOrUndefined(["code_policy", "require_arm"], v)}
          />
        </label>

        <label class={rowClass}>
          <span>{t("alarm.policies.code.require_disarm")}</span>
          <div class="min-w-44">
            <Select
              value={requireDisarmValue()}
              onValueChange={setRequireDisarm}
              options={requireDisarmOptions}
            />
          </div>
        </label>

        <div class="flex flex-col gap-2">
          <span class="text-sm text-[var(--ha-primary-text-color)]">{t("alarm.policies.code.require_silence")}</span>
          <p class={hintClass}>{t("alarm.policies.code.require_silence.hint")}</p>
          {#each SILENCE_SOURCES as src (src)}
            <label class={rowClass}>
              <span>{t(`alarm.policies.code.source.${src}`)}</span>
              <Switch
                checked={getBool(["code_policy", "require_silence", src])}
                onCheckedChange={(v) => setBoolOrUndefined(["code_policy", "require_silence", src], v)}
              />
            </label>
          {/each}
        </div>

        <p class={hintClass}>{t("alarm.policies.code.hint")}</p>
      </Card>

      <!-- Hazard outputs -->
      <Card class="flex flex-col gap-3 p-4">
        <h3 class={sectionTitleClass}>{t("alarm.policies.section.hazard")}</h3>
        <p class={hintClass}>{t("alarm.policies.section.hazard.hint")}</p>
        {@const row = policyRow("hazard_outputs")}
        <label class={rowClass}>
          <span>{t("alarm.policies.output.silent")}</span>
          <Switch
            checked={row.silent}
            onCheckedChange={(v) => setBoolOrUndefined(["hazard_outputs", "silent"], v)}
          />
        </label>
        <label class={rowClass}>
          <span>{t("alarm.policies.output.exclude_outdoor")}</span>
          <Switch
            checked={row.excludeOutdoor}
            onCheckedChange={(v) => setBoolOrUndefined(["hazard_outputs", "exclude_outdoor"], v)}
          />
        </label>
        <label class={rowClass}>
          <span>{t("alarm.policies.output.smoke_sounders")}</span>
          <Switch
            checked={row.smokeSounders}
            onCheckedChange={(v) => setBoolOrUndefined(["hazard_outputs", "smoke_sounders"], v)}
          />
        </label>
      </Card>

      <!-- Panic outputs -->
      <Card class="flex flex-col gap-3 p-4">
        <h3 class={sectionTitleClass}>{t("alarm.policies.section.panic")}</h3>
        <p class={hintClass}>{t("alarm.policies.section.panic.hint")}</p>
        {@const row = policyRow("panic_outputs")}
        <label class={rowClass}>
          <span>{t("alarm.policies.output.silent")}</span>
          <Switch
            checked={row.silent}
            onCheckedChange={(v) => setBoolOrUndefined(["panic_outputs", "silent"], v)}
          />
        </label>
        <label class={rowClass}>
          <span>{t("alarm.policies.output.exclude_outdoor")}</span>
          <Switch
            checked={row.excludeOutdoor}
            onCheckedChange={(v) => setBoolOrUndefined(["panic_outputs", "exclude_outdoor"], v)}
          />
        </label>
        <label class={rowClass}>
          <span>{t("alarm.policies.output.smoke_sounders")}</span>
          <Switch
            checked={row.smokeSounders}
            onCheckedChange={(v) => setBoolOrUndefined(["panic_outputs", "smoke_sounders"], v)}
          />
        </label>
      </Card>

      <!-- Pre-alarm (per configured mode) -->
      <Card class="flex flex-col gap-3 p-4">
        <h3 class={sectionTitleClass}>{t("alarm.policies.section.prealarm")}</h3>
        <p class={hintClass}>{t("alarm.policies.prealarm.hint")}</p>
        {#if configuredModes().length === 0}
          <p class={hintClass}>{t("alarm.policies.prealarm.empty")}</p>
        {:else}
          {#each configuredModes() as mode (mode)}
            <label class={rowClass}>
              <span>{t(`alarm.mode.${mode}`)}</span>
              <input
                type="number"
                min="0"
                class={numberInputClass}
                value={getNum(["modes", mode, "pre_alarm_s"]) ?? ""}
                oninput={(e) => setNum(["modes", mode, "pre_alarm_s"], e.currentTarget.value)}
              />
            </label>
          {/each}
        {/if}
      </Card>

      <!-- Post-trigger + auto-rearm -->
      <Card class="flex flex-col gap-3 p-4">
        <h3 class={sectionTitleClass}>{t("alarm.policies.section.rearm")}</h3>
        <label class={rowClass}>
          <span>{t("alarm.policies.posttrigger")}</span>
          <div class="min-w-44">
            <Select
              value={getStr(["post_trigger"]) || "return_to_armed"}
              onValueChange={(v) =>
                setPath(["post_trigger"], v === "return_to_armed" ? undefined : v)}
              options={[
                { value: "return_to_armed", label: t("alarm.policies.posttrigger.return_to_armed") },
                { value: "disarm", label: t("alarm.policies.posttrigger.disarm") },
              ]}
            />
          </div>
        </label>
        <label class={rowClass}>
          <span>{t("alarm.policies.rearm.seconds")}</span>
          <input
            type="number"
            min="0"
            class={numberInputClass}
            value={getNum(["auto_rearm_s"]) ?? ""}
            oninput={(e) => setNum(["auto_rearm_s"], e.currentTarget.value)}
          />
        </label>
        <p class={hintClass}>{t("alarm.policies.rearm.hint")}</p>
      </Card>

      <!-- Schedules -->
      <Card class="flex flex-col gap-3 p-4">
        <div class="flex items-center justify-between">
          <h3 class={sectionTitleClass}>{t("alarm.policies.section.schedules")}</h3>
          <Button size="sm" onclick={addSchedule}>
            <Icon name="mdi:plus" size={16} />
            {t("alarm.policies.schedules.add")}
          </Button>
        </div>

        {#if schedules().length === 0}
          <EmptyState icon="mdi:calendar-clock" message={t("alarm.policies.schedules.empty")} />
        {:else}
          <div class="flex flex-col gap-3">
            {#each schedules() as row, i (i)}
              <div class="flex flex-col gap-2 rounded-md border border-[var(--ha-divider-color)] p-3">
                <div class="flex flex-wrap items-center gap-3">
                  <label class="flex flex-col gap-1 text-xs text-[var(--ha-secondary-text-color)]">
                    {t("alarm.policies.schedules.time")}
                    <input
                      type="time"
                      class="h-9 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-2 text-sm text-[var(--ha-primary-text-color)]"
                      value={row.time ?? ""}
                      oninput={(e) => setPath(["schedules", i, "time"], e.currentTarget.value)}
                    />
                  </label>
                  <label class="flex flex-col gap-1 text-xs text-[var(--ha-secondary-text-color)]">
                    {t("alarm.policies.schedules.mode")}
                    <div class="min-w-36">
                      <Select
                        value={row.mode ?? "full"}
                        onValueChange={(v) => setPath(["schedules", i, "mode"], v)}
                        options={MODES.map((m) => ({ value: m, label: t(`alarm.mode.${m}`) }))}
                      />
                    </div>
                  </label>
                  <label class="ml-auto flex items-center gap-2 text-xs text-[var(--ha-secondary-text-color)]">
                    <span>{t("alarm.policies.schedules.auto_arm")}</span>
                    <Switch
                      checked={row.auto_arm === true}
                      onCheckedChange={(v) => setPath(["schedules", i, "auto_arm"], v || undefined)}
                    />
                  </label>
                  <button
                    type="button"
                    class="text-[var(--ha-secondary-text-color)] hover:text-[var(--ha-error-color)]"
                    title={t("common.remove")}
                    aria-label={t("common.remove")}
                    onclick={() => removeSchedule(i)}
                  >
                    <Icon name="mdi:trash-can" size={16} />
                  </button>
                </div>
                <p class={hintClass}>{t("alarm.policies.schedules.auto_arm.hint")}</p>
                <div class="flex flex-wrap items-center gap-1.5">
                  <span class={hintClass}>{t("alarm.policies.schedules.days")}</span>
                  {#each WEEKDAY_NAMES as name, d (name)}
                    {@const on = scheduleDays(row).includes(d)}
                    <button
                      type="button"
                      aria-pressed={on}
                      class="rounded-full border px-2 py-0.5 text-xs transition {on
                        ? 'border-[var(--ha-primary-color)] bg-[color-mix(in_srgb,var(--ha-primary-color)_15%,transparent)] text-[var(--ha-primary-color)]'
                        : 'border-[var(--ha-divider-color)] text-[var(--ha-secondary-text-color)] hover:border-[var(--ha-secondary-text-color)]'}"
                      onclick={() => toggleScheduleDay(i, d)}
                    >
                      {t(`weekday.short.${name}`)}
                    </button>
                  {/each}
                </div>
              </div>
            {/each}
          </div>
        {/if}
      </Card>
    </div>
  {/if}
{/if}
