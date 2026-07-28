<script lang="ts">
  // Alarm Overview — the panel (docs/alarm-concept.md §12.1). One Card per
  // zone from the shared alarmPanelStore: a state badge + per-mode arm
  // buttons (each with an inline readiness dot and a title tooltip listing
  // its blockers), an exit/entry countdown ring on the mode row, and — when
  // a zone is triggered — a high-contrast alarm surface with a giant
  // SILENCE and a DISARM button. A silence-all action and an alarm-health
  // traffic light sit in the local toolbar. The section shell (Alarm.svelte)
  // owns the store lifecycle (WS stream + 1 s ticker + initial refresh); this
  // view only reflects it and offers a retry.

  import { alarmPanelStore } from "$lib/stores/alarmPanel.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { prefs } from "$lib/stores/preferences.svelte";
  import { t } from "$lib/i18n";
  import type { AlarmZoneStatus } from "$lib/api/types";

  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import CountdownRing from "./CountdownRing.svelte";
  import PinPad from "./PinPad.svelte";

  const store = alarmPanelStore;

  // Canonical arm-mode order (docs/alarm-concept.md §4). Matches
  // AlarmArmRequest["mode"] exactly, so `{ mode }` requests type-check.
  const MODE_ORDER = ["perimeter", "full", "night", "vacation", "custom"] as const;
  type ArmMode = (typeof MODE_ORDER)[number];

  // Inline bypass sheet: open for exactly one (zone, mode) at a time. `checked`
  // maps each blocking sensor id → whether it will be bypassed. Pre-ticked so
  // "Force arm" defaults to arming past every blocker — but the full list stays
  // visible and each tick is togglable, so nothing is bypassed silently (§12.1).
  let bypassSheet = $state<{
    zoneId: string;
    mode: ArmMode;
    checked: Record<string, boolean>;
  } | null>(null);

  // PIN-pad transaction: open for exactly one (zone, verb) at a time. The
  // zone is snapshotted so a live status update mid-entry doesn't retarget
  // the pad. Silence is deliberately never routed here (S3).
  let pinPad = $state<{
    zone: AlarmZoneStatus;
    verb: "arm" | "disarm";
    mode?: ArmMode;
    busy: boolean;
  } | null>(null);

  const anyTriggered = $derived(store.zones.some((a) => a.state === "triggered"));

  // codeRequired reads the zone's engine-owned code policy from the config
  // document (docs/alarm-concept.md §11). The live status snapshot carries
  // no policy, so this falls back to the zone config as the task specifies.
  // The SPA prompts only when the operator has EXPLICITLY opted a verb in
  // (require_arm / require_disarm === true): the null-default "require a
  // disarm code only when codes exist" cannot be resolved client-side
  // without the operator-only code list, and the REST verb runs as an
  // operator source the engine exempts anyway — so an over-prompt would
  // gate a surface the backend never gates. An explicit true is the clear
  // signal to collect the PIN here (for duress detection + attribution).
  function codeRequired(zoneId: string, verb: "arm" | "disarm"): boolean {
    const cfg = (store.zonesConfig ?? []).find((a) => a.id === zoneId)?.config;
    if (!cfg || typeof cfg !== "object") return false;
    const policy = (cfg as Record<string, unknown>).code_policy;
    if (!policy || typeof policy !== "object") return false;
    const key = verb === "arm" ? "require_arm" : "require_disarm";
    return (policy as Record<string, unknown>)[key] === true;
  }

  function fmtTime(iso: string): string {
    return new Date(iso).toLocaleTimeString(
      prefs.locale === "de" ? "de-DE" : "en-US",
      { hour: "2-digit", minute: "2-digit" },
    );
  }

  // Arm-mode buttons a zone offers: the modes the engine computed readiness
  // for, in canonical order. Falls back to the two built-in modes before any
  // readiness snapshot has arrived so the panel is always usable.
  function armModes(zoneId: string): ArmMode[] {
    const r = store.readiness[zoneId];
    const present = MODE_ORDER.filter((m) => r?.[m] !== undefined);
    return present.length > 0 ? present : ["perimeter", "full"];
  }

  type Tone = "ready" | "warn" | "blocked";
  function readinessTone(zoneId: string, mode: ArmMode): Tone {
    const r = store.readiness[zoneId]?.[mode];
    if (!r) return "ready"; // unknown → treat as ready so the button still works
    if (!r.ready) return "blocked";
    if (r.warnings && r.warnings.length > 0) return "warn";
    return "ready";
  }

  function toneColor(tone: Tone): string {
    if (tone === "blocked") return "var(--ha-error-color)";
    if (tone === "warn") return "var(--ha-warning-color)";
    return "var(--ha-success-color)";
  }

  // Tooltip text for a mode button: lists the blocking / warning sensor ids
  // (the overview store carries ids, not names — the Sensors view resolves
  // names). Falls back to a plain "ready".
  function readinessTitle(zoneId: string, mode: ArmMode): string {
    const r = store.readiness[zoneId]?.[mode];
    if (!r || (r.ready && !(r.warnings && r.warnings.length))) {
      return t("alarm.readiness.ready");
    }
    const parts: string[] = [];
    if (r.blockers && r.blockers.length) {
      parts.push(`${t("alarm.readiness.blockers_title")}: ${r.blockers.join(", ")}`);
    }
    if (r.warnings && r.warnings.length) {
      parts.push(`${t("alarm.readiness.warnings_title")}: ${r.warnings.join(", ")}`);
    }
    return parts.join(" · ");
  }

  function isActiveMode(zone: AlarmZoneStatus, mode: ArmMode): boolean {
    return (
      zone.mode === mode &&
      (zone.state === "armed" || zone.state === "arming" || zone.state === "pending")
    );
  }

  type BadgeVariant = "default" | "success" | "warning" | "danger" | "muted";
  function stateVariant(state: AlarmZoneStatus["state"]): BadgeVariant {
    switch (state) {
      case "armed":
        return "success";
      case "arming":
      case "pending":
        return "warning";
      case "triggered":
        return "danger";
      default:
        return "muted";
    }
  }

  function modeLabel(mode: AlarmZoneStatus["mode"]): string | null {
    if (!mode || mode === "disarmed") return null;
    return t(`alarm.mode.${mode}`);
  }

  // "since <time>, by <user>" line, reconstructed from the newest arm-class
  // journal entry (the zone status snapshot carries no armed-at timestamp).
  // Absent when there is no recent arm entry — degrade to no line.
  function armInfo(zoneId: string): string | null {
    const e = store.journal.find((j) => j.zone_id === zoneId && j.class === "arm");
    if (!e) return null;
    const time = fmtTime(e.when);
    return e.actor
      ? t("alarm.overview.armed_by", { time, user: e.actor })
      : t("alarm.overview.armed_at", { time });
  }

  function detailStr(details: unknown, ...keys: string[]): string | undefined {
    if (!details || typeof details !== "object") return undefined;
    const rec = details as Record<string, unknown>;
    for (const k of keys) {
      const v = rec[k];
      if (typeof v === "string" && v) return v;
    }
    return undefined;
  }

  // Cause + time line for a triggered card, drawn from the trigger-class
  // journal entry linked to the open incident.
  function causeLine(zone: AlarmZoneStatus): string {
    // The live triggered broadcast carries the sensor name directly;
    // the journal lookup below covers snapshot-loaded incidents.
    const live = zone.incident as (typeof zone.incident & { sensor_name?: string }) | undefined;
    if (live?.sensor_name) {
      return t("alarm.triggered.cause_short", { sensor: live.sensor_name });
    }
    const iid = zone.incident?.id;
    const e = iid
      ? store.journal.find(
          (j) =>
            j.class === "trigger" &&
            j.incident_id != null &&
            String(j.incident_id) === String(iid),
        )
      : undefined;
    const sensor = detailStr(e?.details, "sensor_name", "sensor");
    const room = detailStr(e?.details, "room");
    const time = e ? fmtTime(e.when) : undefined;
    if (sensor && room && time) return t("alarm.triggered.cause", { sensor, room, time });
    if (sensor && time) return `${t("alarm.triggered.cause_short", { sensor })} · ${time}`;
    if (sensor) return t("alarm.triggered.cause_short", { sensor });
    if (time) return t("alarm.triggered.since", { time });
    return "";
  }

  // --- Control verbs. Success feedback via toast; the store already turns
  // failures into an error toast and never blocks the UI. ---

  function armToast(zone: AlarmZoneStatus, mode: ArmMode, state: "arming" | "armed") {
    if (state === "arming") {
      toastStore.success(t("alarm.toast.arming", { zone: zone.name }));
    } else {
      toastStore.success(
        t("alarm.toast.armed", { zone: zone.name, mode: t(`alarm.mode.${mode}`) }),
      );
    }
  }

  function onModeClick(zone: AlarmZoneStatus, mode: ArmMode) {
    const r = store.readiness[zone.id]?.[mode];
    if (r && !r.ready) {
      // Blocked → never arm silently; open the bypass sheet instead.
      const checked: Record<string, boolean> = {};
      for (const id of r.blockers ?? []) checked[id] = true;
      bypassSheet = { zoneId: zone.id, mode, checked };
      return;
    }
    void armDirect(zone, mode);
  }

  async function armDirect(zone: AlarmZoneStatus, mode: ArmMode) {
    if (codeRequired(zone.id, "arm")) {
      pinPad = { zone, verb: "arm", mode, busy: false };
      return;
    }
    const accepted = await store.arm(zone.id, { mode });
    if (accepted) armToast(zone, mode, accepted.state);
  }

  async function forceArm(zone: AlarmZoneStatus) {
    if (!bypassSheet) return;
    const mode = bypassSheet.mode;
    const bypass = Object.keys(bypassSheet.checked).filter((id) => bypassSheet!.checked[id]);
    bypassSheet = null;
    const accepted = await store.arm(zone.id, { mode, force: true, bypass });
    if (accepted) armToast(zone, mode, accepted.state);
  }

  // Safety invariant S3/S6 (docs/alarm-concept.md §2): silence and disarm act
  // on the FIRST tap with NO confirm dialog. This is the one deliberate
  // exception to the SPA rule that destructive actions route through
  // confirmStore — a screaming siren must be a single tap away, and disarm
  // must never be trapped behind a modal.
  async function disarm(zone: AlarmZoneStatus) {
    if (codeRequired(zone.id, "disarm")) {
      // Route through the PIN pad — this covers both the mode-row disarm
      // button and the triggered-surface DISARM (same handler), so the pad
      // works on the high-contrast alarm surface too.
      pinPad = { zone, verb: "disarm", busy: false };
      return;
    }
    const ok = await store.disarm(zone.id);
    if (ok) toastStore.success(t("alarm.toast.disarmed", { zone: zone.name }));
  }

  // PIN-pad submit: run the pending verb WITH the entered code. The store
  // verbs already toast success/failure and never throw, so on failure the
  // pad simply closes and the toast explains. Because REST verbs run as an
  // operator source (§11 break-glass), the code here authenticates duress
  // and populates changed-by rather than gating the action.
  async function submitPin(code: string) {
    if (!pinPad || pinPad.busy) return;
    const { zone, verb, mode } = pinPad;
    pinPad = { ...pinPad, busy: true };
    try {
      if (verb === "arm" && mode) {
        const accepted = await store.arm(zone.id, { mode, code });
        if (accepted) armToast(zone, mode, accepted.state);
      } else {
        const ok = await store.disarm(zone.id, code);
        if (ok) toastStore.success(t("alarm.toast.disarmed", { zone: zone.name }));
      }
    } finally {
      pinPad = null;
    }
  }

  async function silence(zone: AlarmZoneStatus) {
    const ok = await store.silence(zone.id);
    if (ok) toastStore.success(t("alarm.toast.silenced"));
  }

  async function silenceAll() {
    const ok = await store.silenceAll();
    if (ok) toastStore.success(t("alarm.toast.silenced"));
  }

  async function acknowledge(zone: AlarmZoneStatus) {
    const ok = await store.acknowledge(zone.id);
    if (ok) toastStore.success(t("alarm.toast.acknowledged"));
  }
</script>

{#if store.loading && store.zones.length === 0}
  <LoadingState />
{:else if store.error}
  <ErrorState message={store.error} onRetry={() => void store.refresh()} />
{:else if store.zones.length === 0}
  <EmptyState
    icon="mdi:shield-home"
    message={t("alarm.overview.empty")}
    description={t("alarm.overview.empty.description")}
  >
    {#snippet action()}
      <a href="#/alarm/wizard">
        <Button variant="default" size="sm">{t("alarm.wizard.launch")}</Button>
      </a>
    {/snippet}
  </EmptyState>
{:else}
  <!-- Local toolbar: alarm-health traffic light + silence-all when triggered. -->
  <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
    <span class="inline-flex" title={store.health.note || undefined}>
      <Badge variant={store.health.healthy ? "success" : "danger"} class="gap-1.5">
        <span
          class="inline-block h-2 w-2 rounded-full"
          style="background: currentColor;"
        ></span>
        {store.health.healthy
          ? t("alarm.health.healthy")
          : t("alarm.health.unhealthy")}
      </Badge>
    </span>

    {#if anyTriggered}
      <Button variant="destructive" size="sm" onclick={silenceAll}>
        <Icon name="mdi:bell-off" size={16} aria-label="" />
        {t("alarm.overview.silence_all")}
      </Button>
    {/if}
  </div>

  <div class="grid gap-4 lg:grid-cols-2">
    {#each store.zones as zone (zone.id)}
      <Card class="overflow-hidden p-4">
        <!-- Header: name + state badge -->
        <div class="flex items-start justify-between gap-3">
          <div class="flex min-w-0 items-center gap-2">
            <Icon
              name="mdi:shield-home"
              size={20}
              class="shrink-0 text-[var(--ha-secondary-text-color)]"
              aria-label=""
            />
            <h3 class="truncate text-base font-semibold">{zone.name}</h3>
          </div>
          <Badge variant={stateVariant(zone.state)}>
            {t(`alarm.state.${zone.state}`)}{modeLabel(zone.mode)
              ? ` · ${modeLabel(zone.mode)}`
              : ""}
          </Badge>
        </div>

        {#if zone.state !== "disarmed" && zone.state !== "triggered"}
          {@const info = armInfo(zone.id)}
          {#if info}
            <p class="mt-1 text-xs text-[var(--ha-secondary-text-color)]">{info}</p>
          {/if}
        {/if}

        {#if zone.state === "triggered"}
          {@const cause = causeLine(zone)}
          <!-- High-contrast alarm surface. Error token as the fill so it
               inverts correctly in every skin×scheme combo; white text/borders
               read on the saturated red in both light and dark. -->
          <div
            class="mt-3 rounded-md p-4 text-white"
            style="background: var(--ha-error-color);"
          >
            <div class="flex items-center gap-2">
              <Icon name="mdi:bell-alert" size={22} aria-label="" />
              <span class="text-lg font-extrabold uppercase tracking-wide">
                {t("alarm.triggered.intrusion")}
              </span>
            </div>

            {#if cause}
              <p class="mt-1 text-sm opacity-95">{cause}</p>
            {/if}
            {#if zone.incident?.silenced}
              <p class="mt-1 text-xs font-medium uppercase opacity-90">
                {t("alarm.triggered.silenced")}
              </p>
            {/if}

            <!-- Giant SILENCE + DISARM. Single tap, no confirm (S3/S6). Hand-
                 rolled so the high-contrast red surface stays legible — the
                 tokenised Button variants are tuned for card backgrounds. -->
            <div class="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-2">
              <button
                type="button"
                onclick={() => void silence(zone)}
                class="flex h-16 items-center justify-center gap-2 rounded-md bg-white text-base font-extrabold uppercase tracking-wide text-[var(--ha-error-color)] shadow-sm transition hover:brightness-95 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-white"
              >
                <Icon name="mdi:bell-off" size={22} aria-label="" />
                {t("alarm.action.silence")}
              </button>
              <button
                type="button"
                onclick={() => void disarm(zone)}
                class="flex h-16 items-center justify-center gap-2 rounded-md border-2 border-white/80 text-base font-bold uppercase tracking-wide text-white transition hover:bg-white hover:text-[var(--ha-error-color)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-white"
              >
                <Icon name="mdi:shield" size={20} aria-label="" />
                {t("alarm.action.disarm")}
              </button>
            </div>

            <button
              type="button"
              onclick={() => void acknowledge(zone)}
              class="mt-3 text-sm font-medium underline decoration-white/50 underline-offset-2 hover:decoration-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-white"
            >
              {t("alarm.action.acknowledge")}
            </button>
          </div>
        {:else}
          {@const cd = store.countdowns[zone.id]}
          <!-- Mode row: disarm + per-mode arm buttons, countdown ring aside. -->
          <div class="mt-4 flex flex-wrap items-center justify-between gap-4">
            <div class="flex flex-wrap items-center gap-2">
              <Button
                variant={zone.state === "disarmed" ? "default" : "outline"}
                size="sm"
                disabled={zone.state === "disarmed"}
                onclick={() => void disarm(zone)}
              >
                {t("alarm.mode.disarmed")}
              </Button>

              {#each armModes(zone.id) as mode (mode)}
                {@const tone = readinessTone(zone.id, mode)}
                <Button
                  variant={isActiveMode(zone, mode) ? "default" : "outline"}
                  size="sm"
                  title={readinessTitle(zone.id, mode)}
                  onclick={() => onModeClick(zone, mode)}
                >
                  <span
                    class="inline-block h-2 w-2 shrink-0 rounded-full"
                    style="background: {toneColor(tone)};"
                    aria-hidden="true"
                  ></span>
                  {t(`alarm.mode.${mode}`)}
                </Button>
              {/each}
            </div>

            {#if cd}
              <div class="flex flex-col items-center gap-1">
                <CountdownRing
                  remaining={cd.remaining_s}
                  total={cd.total_s}
                  tone={cd.kind === "entry_delay" ? "danger" : "neutral"}
                />
                <span class="text-xs text-[var(--ha-secondary-text-color)]">
                  {cd.kind === "entry_delay"
                    ? t("alarm.countdown.entry")
                    : t("alarm.countdown.exit")}
                </span>
              </div>
            {/if}
          </div>

          {#if bypassSheet && bypassSheet.zoneId === zone.id}
            {@const blockers = store.readiness[zone.id]?.[bypassSheet.mode]?.blockers ?? []}
            <div
              class="mt-4 rounded-md border p-3"
              style="border-color: var(--ha-divider-color); background: var(--ha-secondary-background-color);"
            >
              <p class="text-sm font-semibold">{t("alarm.bypass.title")}</p>
              <p class="mt-1 text-xs text-[var(--ha-secondary-text-color)]">
                {t("alarm.bypass.description")}
              </p>

              {#if blockers.length === 0}
                <p class="mt-3 text-xs text-[var(--ha-secondary-text-color)]">
                  {t("alarm.bypass.empty")}
                </p>
              {:else}
                <ul class="mt-3 flex flex-col gap-1">
                  {#each blockers as id (id)}
                    <li>
                      <label class="flex items-center gap-2 text-sm">
                        <input
                          type="checkbox"
                          class="h-4 w-4 accent-[var(--ha-primary-color)]"
                          checked={bypassSheet.checked[id] ?? false}
                          onchange={() =>
                            bypassSheet &&
                            (bypassSheet.checked[id] = !bypassSheet.checked[id])}
                        />
                        <span class="truncate">{id}</span>
                      </label>
                    </li>
                  {/each}
                </ul>
              {/if}

              <div class="mt-4 flex flex-wrap items-center gap-2">
                <Button
                  variant="destructive"
                  size="sm"
                  onclick={() => void forceArm(zone)}
                >
                  {t("alarm.bypass.force_arm")}
                </Button>
                <Button variant="outline" size="sm" onclick={() => (bypassSheet = null)}>
                  {t("common.cancel")}
                </Button>
              </div>
            </div>
          {/if}
        {/if}
      </Card>
    {/each}
  </div>
{/if}

{#if pinPad}
  <PinPad
    title={pinPad.verb === "disarm"
      ? t("alarm.pinpad.disarm_title", { zone: pinPad.zone.name })
      : t("alarm.pinpad.arm_title", { mode: t(`alarm.mode.${pinPad.mode}`) })}
    submitLabel={pinPad.verb === "disarm"
      ? t("alarm.action.disarm")
      : t(`alarm.mode.${pinPad.mode}`)}
    busy={pinPad.busy}
    onSubmit={(code) => void submitPin(code)}
    onCancel={() => (pinPad = null)}
  />
{/if}
