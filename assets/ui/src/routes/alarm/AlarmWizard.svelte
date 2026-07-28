<script lang="ts">
  import { onMount, untrack } from "svelte";
  import { api, ApiError, friendlyError } from "$lib/api/client";
  import { alarmPanelStore } from "$lib/stores/alarmPanel.svelte";
  import { deviceStore } from "$lib/stores/devices.svelte";
  import {
    alarmWizardStore,
    ALARM_WIZARD_MAX_TRIGGER_SECONDS,
    ALARM_WIZARD_MODE_ORDER,
    type AlarmWizardMode,
  } from "$lib/stores/alarmWizard.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";
  import {
    buildCandidates,
    guessSensorBinding,
  } from "$lib/alarm/sensorCandidates";
  import type {
    AlarmArea,
    AlarmOutput,
    AlarmOutputCandidate,
    AlarmOutputClass,
    AlarmSensor,
    AlarmSensorType,
    DeviceSummary,
  } from "$lib/api/types";

  import Card from "$lib/components/ui/Card.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";

  // Setup wizard (docs/alarm-concept.md §12.3), skeleton pattern borrowed
  // from routes/Setup.svelte: step dots + Back/Skip/Next footer, one atomic
  // write on the last step. Steps ②/③ used to be bare links into the
  // sensor/output picker tabs — but those tabs require an existing area,
  // and the area itself is only created on Finish, so a first-run operator
  // hit a dead end and looped back to a reset wizard. Both steps now embed
  // a simplified inline picker instead, collecting sensor/output rows in
  // the same DTO shape the full pickers save, applied together with the
  // area on Finish. Step ⑤ (codes/users) stays a pointer at the Codes tab —
  // codes need a real area id to attach to, same as the full picker tabs.
  // All collected state lives in alarmWizardStore so navigating away and
  // back preserves progress instead of resetting on unmount.

  const store = alarmWizardStore;

  const STEP_KEYS = ["areas", "sensors", "outputs", "delays", "codes", "done"] as const;
  const TOTAL = STEP_KEYS.length;

  let submitting = $state(false);

  const numberInputClass =
    "h-9 w-20 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-2 text-right text-sm text-[var(--ha-primary-text-color)] focus-visible:border-[var(--ha-primary-color)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ha-primary-color)]";
  const rowClass =
    "flex w-full items-center gap-3 border-b border-[var(--ha-divider-color)] px-3 py-2 text-left transition last:border-0 hover:bg-[var(--ha-secondary-background-color)] cursor-pointer";

  // --- step 2: sensor picker ---------------------------------------
  // Mirrors AlarmSensors.svelte's confirmAdd defaults (§6.1 presets) —
  // duplicated here because that page's add flow is component-local and
  // out of scope for this file, but the wizard must enroll a sensor with
  // the exact same sensible defaults.
  function presetModes(type: AlarmSensorType): AlarmWizardMode[] {
    switch (type) {
      case "door":
      case "window":
        return ["perimeter", "full", "night", "vacation"];
      case "motion":
        return ["full", "vacation"];
      case "tamper":
        return ["perimeter", "full", "night", "vacation", "custom"];
      case "hazard":
      case "panic":
        return [];
    }
  }
  function presetFlags(type: AlarmSensorType): Record<string, boolean> {
    switch (type) {
      case "door":
        return { use_entry_delay: true, arm_after_closing: true };
      case "motion":
        return { use_exit_delay: true, use_entry_delay: true };
      case "hazard":
      case "panic":
        return { always_on: true };
      default:
        return {};
    }
  }

  let sensorSearch = $state("");
  let sensorShowAll = $state(false);
  const sensorCandidates = $derived(
    buildCandidates(deviceStore.items, {
      query: sensorSearch,
      showAll: sensorShowAll,
      limit: 60,
    }),
  );
  function sensorRowId(device: DeviceSummary, channel: string, parameter: string): string {
    return `${device.central ?? ""}|${channel}|${parameter}`;
  }
  function toggleSensor(device: DeviceSummary, checked: boolean) {
    const binding = guessSensorBinding(device);
    if (!binding) return;
    const id = sensorRowId(device, binding.channel, binding.parameter);
    if (checked) {
      const sensor: AlarmSensor = {
        id,
        central: device.central ?? "",
        interface_id: device.interface_id,
        channel_address: binding.channel,
        parameter: binding.parameter,
        type: binding.type,
        name: device.name || undefined,
        config: {
          modes: presetModes(binding.type),
          ...presetFlags(binding.type),
        },
      };
      store.addSensor(sensor);
    } else {
      store.removeSensor(id);
    }
  }

  // --- step 3: output picker ----------------------------------------
  // Duration/level defaults mirror AlarmOutputs.svelte's CAPS table +
  // defaultConfig() for the device-backed classes the candidates
  // endpoint can return (acoustic/switched/optical siren, smoke sounder,
  // alarm light, chirp) — duplicated for the same reason as the sensor
  // presets above.
  const OUTPUT_DURATION_CLASSES = new Set<AlarmOutputClass>([
    "acoustic_siren",
    "switched_siren",
    "optical_siren",
    "alarm_light",
  ]);
  function defaultOutputConfig(cls: AlarmOutputClass): Record<string, unknown> {
    const base: Record<string, unknown> = { modes: ["full"] };
    if (OUTPUT_DURATION_CLASSES.has(cls)) base.duration_s = 180;
    if (cls === "alarm_light") base.level = 1;
    return base;
  }
  const outputRows = $derived(store.outputCandidates.filter((c) => c.classes.length > 0));
  function outputRowId(c: AlarmOutputCandidate, cls: AlarmOutputClass): string {
    return `${c.central}|${c.channel_address}|${cls}`;
  }
  function toggleOutput(c: AlarmOutputCandidate, checked: boolean) {
    // The wizard enrolls a candidate under its primary (first, canonical-
    // order) class — full per-class control stays on the outputs tab.
    const cls = c.classes[0];
    if (!cls) return;
    const id = outputRowId(c, cls);
    if (checked) {
      const output: AlarmOutput = {
        id,
        class: cls,
        central: c.central,
        channel_address: c.channel_address,
        name: c.channel_name || c.device_name || undefined,
        config: defaultOutputConfig(cls),
      };
      store.addOutput(output);
    } else {
      store.removeOutput(id);
    }
  }

  $effect(() => {
    // Track ONLY the step: the load call reads the store's loading/error
    // flags synchronously, and tracking those would re-trigger the effect
    // on every fetch settle (an infinite refetch on failure). Recovery
    // from a failed fetch is the ErrorState's explicit retry.
    if (store.step === 3) untrack(() => void store.loadOutputCandidates());
  });

  // --- step 6: summary ------------------------------------------------
  const delaySummaryLines = $derived(
    ALARM_WIZARD_MODE_ORDER.map((mode) =>
      t("alarm.wizard.summary.delay_line", {
        mode: t(`alarm.mode.${mode}`),
        exit: store.delays[mode].exit,
        entry: store.delays[mode].entry,
        trigger: store.delays[mode].trigger,
      }),
    ),
  );

  async function finish() {
    if (submitting) return;
    submitting = true;
    try {
      const name = store.areaName.trim() || t("alarm.wizard.area.default_name");
      const modes: Record<
        string,
        { exit_delay_s: number; entry_delay_s: number; trigger_time_s: number }
      > = {};
      for (const m of ALARM_WIZARD_MODE_ORDER) {
        modes[m] = {
          exit_delay_s: store.delays[m].exit,
          entry_delay_s: store.delays[m].entry,
          trigger_time_s: store.delays[m].trigger,
        };
      }
      const area: AlarmArea = { id: store.createdAreaId ?? "", name, config: { modes } };
      // A retry after a partial failure must never create a second area —
      // once createdAreaId is set, every subsequent attempt PUTs it. If
      // that area vanished in the meantime (operator deleted it on the
      // overview between attempts), the PUT 404s: fall back to creating
      // a fresh one instead of dead-ending on the stale id forever.
      let id = store.createdAreaId;
      if (id) {
        try {
          await api.putAlarmArea(id, area);
        } catch (err) {
          if (!(err instanceof ApiError && err.status === 404)) throw err;
          store.setCreatedAreaId(null);
          id = null;
        }
      }
      if (!id) {
        const created = await api.createAlarmArea({ ...area, id: "" });
        id = created.id;
        store.setCreatedAreaId(id);
      }
      // Always send both bulk PUTs — an empty array is a valid full-set
      // replace, and a retry must be able to reconcile away rows a prior
      // attempt already wrote before the operator deselected them. The
      // local ids are channel-derived selection keys, not row identities
      // — send them empty so the server mints per-area UUIDs (the same
      // channel enrolled in a second area must never collide with the
      // first area's row).
      await api.putAlarmAreaSensors(
        id,
        store.selectedSensors.map((s) => ({ ...s, id: "" })),
      );
      await api.putAlarmAreaOutputs(
        id,
        store.selectedOutputs.map((o) => ({ ...o, id: "" })),
      );
      await alarmPanelStore.refresh();
      toastStore.success(t("alarm.toast.saved"), name);
      store.reset();
      location.hash = "#/alarm";
    } catch (err) {
      // Keep every collected step (including createdAreaId) so the
      // operator can just hit Finish again once the underlying issue is
      // fixed — never silently drop their work on a failed save.
      toastStore.error(t("alarm.toast.save_failed"), friendlyError(err, t));
    } finally {
      submitting = false;
    }
  }

  onMount(() => {
    // Same load pattern as AlarmSensors.svelte — the sensor step needs
    // the device inventory ready by the time the operator reaches it.
    deviceStore.refresh();
    deviceStore.ensureStream();
  });
</script>

<div class="mx-auto max-w-2xl">
  <Card class="p-6">
    <div class="mb-4 flex items-center justify-center gap-2" aria-hidden="true">
      {#each Array.from({ length: TOTAL }, (_, i) => i + 1) as n (n)}
        <span
          class="h-2 w-2 rounded-full transition-colors {n === store.step
            ? 'bg-[var(--ha-primary-color)]'
            : n < store.step
              ? 'bg-[color-mix(in_srgb,var(--ha-primary-color)_50%,transparent)]'
              : 'bg-[var(--ha-divider-color)]'}"
        ></span>
      {/each}
    </div>
    <p class="mb-4 text-center text-sm text-[var(--ha-secondary-text-color)]">
      {t("setup.step.progress", { current: store.step, total: TOTAL })}
    </p>

    {#if store.step === 1}
      <h2 class="mb-1 text-lg font-semibold">{t("alarm.wizard.step.areas")}</h2>
      <p class="mb-4 text-sm text-[var(--ha-secondary-text-color)]">
        {t("alarm.wizard.area.hint")}
      </p>
      <label class="block">
        <span class="mb-1 block text-sm font-medium">{t("alarm.area.name")}</span>
        <Input
          type="text"
          placeholder={t("alarm.wizard.area.default_name")}
          value={store.areaName}
          oninput={(e) => store.setAreaName(e.currentTarget.value)}
        />
      </label>
    {:else if store.step === 2}
      <h2 class="mb-1 text-lg font-semibold">{t("alarm.wizard.step.sensors")}</h2>
      <p class="mb-4 text-sm text-[var(--ha-secondary-text-color)]">
        {t("alarm.wizard.sensors.hint")}
      </p>
      <div class="mb-2 flex flex-wrap items-center justify-between gap-2">
        <div class="min-w-48 flex-1">
          <Input type="search" placeholder={t("common.search")} bind:value={sensorSearch} />
        </div>
        <label class="flex items-center gap-1.5 text-xs text-[var(--ha-secondary-text-color)]">
          <input type="checkbox" bind:checked={sensorShowAll} />
          {t("alarm.sensors.add.show_all")}
        </label>
      </div>
      <p class="mb-2 text-xs text-[var(--ha-secondary-text-color)]">
        {t("alarm.sensors.selected", { count: store.selectedSensors.length })}
      </p>
      {#if sensorCandidates.length === 0}
        <EmptyState
          icon="mdi:door"
          message={t("alarm.wizard.sensors.empty")}
          description={t("alarm.wizard.sensors.empty.description")}
        />
      {:else}
        <Card class="max-h-80 overflow-y-auto p-0">
          {#each sensorCandidates as d (d.address)}
            {@const binding = guessSensorBinding(d)}
            {@const id = binding ? sensorRowId(d, binding.channel, binding.parameter) : ""}
            <label class={rowClass}>
              <input
                type="checkbox"
                class="h-4 w-4 shrink-0 cursor-pointer accent-[var(--ha-primary-color)]"
                checked={id !== "" && store.hasSensor(id)}
                disabled={!binding}
                onchange={(e) => toggleSensor(d, e.currentTarget.checked)}
              />
              <div class="min-w-0 flex-1">
                <p class="truncate text-sm text-[var(--ha-primary-text-color)]">
                  {d.name || d.address}
                </p>
                <p class="truncate font-mono text-xs text-[var(--ha-secondary-text-color)]">
                  {d.address}{binding ? ` · ${binding.channel}:${binding.parameter}` : ""}
                </p>
              </div>
              {#if binding}
                <Badge>{t(`alarm.sensor_type.${binding.type}`)}</Badge>
              {/if}
            </label>
          {/each}
        </Card>
      {/if}
    {:else if store.step === 3}
      <h2 class="mb-1 text-lg font-semibold">{t("alarm.wizard.step.outputs")}</h2>
      <p class="mb-4 text-sm text-[var(--ha-secondary-text-color)]">
        {t("alarm.wizard.outputs.hint")}
      </p>
      {#if store.candidatesLoading && store.outputCandidates.length === 0}
        <LoadingState message={t("common.loading")} />
      {:else if store.candidatesError}
        <ErrorState
          message={store.candidatesError}
          onRetry={() => store.retryOutputCandidates()}
        />
      {:else}
        <p class="mb-2 text-xs text-[var(--ha-secondary-text-color)]">
          {t("alarm.sensors.selected", { count: store.selectedOutputs.length })}
        </p>
        {#if outputRows.length === 0}
          <EmptyState
            icon="mdi:volume"
            message={t("alarm.wizard.outputs.empty")}
            description={t("alarm.wizard.outputs.empty.description")}
          />
        {:else}
          <Card class="max-h-80 overflow-y-auto p-0">
            {#each outputRows as c (`${c.central}|${c.channel_address}`)}
              {@const cls = c.classes[0]}
              {@const id = outputRowId(c, cls)}
              <label class={rowClass}>
                <input
                  type="checkbox"
                  class="h-4 w-4 shrink-0 cursor-pointer accent-[var(--ha-primary-color)]"
                  checked={store.hasOutput(id)}
                  onchange={(e) => toggleOutput(c, e.currentTarget.checked)}
                />
                <div class="min-w-0 flex-1">
                  <p class="truncate text-sm text-[var(--ha-primary-text-color)]">
                    {c.channel_name || c.device_name || c.channel_address}
                  </p>
                  <p class="truncate font-mono text-xs text-[var(--ha-secondary-text-color)]">
                    {c.model} · {c.channel_address}
                  </p>
                </div>
                <Badge>{t(`alarm.output_class.${cls}`)}</Badge>
              </label>
            {/each}
          </Card>
        {/if}
      {/if}
    {:else if store.step === 4}
      <h2 class="mb-1 text-lg font-semibold">{t("alarm.wizard.step.delays")}</h2>
      <p class="mb-4 text-sm text-[var(--ha-secondary-text-color)]">
        {t("alarm.wizard.delays.hint")}
      </p>
      <Card class="overflow-x-auto">
        <table class="w-full border-collapse text-sm">
          <thead class="bg-[var(--ha-card-background-color)]">
            <tr class="border-b border-[var(--ha-divider-color)] text-left">
              <th class="p-2 font-medium">{t("alarm.sensors.modes")}</th>
              <th class="p-2 font-medium">{t("alarm.wizard.delay.exit")}</th>
              <th class="p-2 font-medium">{t("alarm.wizard.delay.entry")}</th>
              <th class="p-2 font-medium">{t("alarm.wizard.delay.trigger")}</th>
            </tr>
          </thead>
          <tbody>
            {#each ALARM_WIZARD_MODE_ORDER as mode (mode)}
              <tr class="border-b border-[var(--ha-divider-color)] last:border-0">
                <td class="p-2 font-medium">{t(`alarm.mode.${mode}`)}</td>
                <td class="p-2">
                  <input
                    type="number"
                    min="0"
                    class={numberInputClass}
                    value={store.delays[mode].exit}
                    oninput={(e) => store.setDelay(mode, "exit", e.currentTarget.value)}
                  />
                </td>
                <td class="p-2">
                  <input
                    type="number"
                    min="0"
                    class={numberInputClass}
                    value={store.delays[mode].entry}
                    oninput={(e) => store.setDelay(mode, "entry", e.currentTarget.value)}
                  />
                </td>
                <td class="p-2">
                  <input
                    type="number"
                    min="0"
                    max={ALARM_WIZARD_MAX_TRIGGER_SECONDS}
                    class={numberInputClass}
                    value={store.delays[mode].trigger}
                    oninput={(e) => store.setDelay(mode, "trigger", e.currentTarget.value)}
                  />
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </Card>
    {:else if store.step === 5}
      <h2 class="mb-1 text-lg font-semibold">{t("alarm.wizard.step.codes")}</h2>
      <div
        class="flex items-start gap-3 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-secondary-background-color)] p-4"
      >
        <Icon
          name="mdi:information-outline"
          size={20}
          class="mt-0.5 shrink-0 text-[var(--ha-secondary-text-color)]"
          aria-label=""
        />
        <p class="text-sm text-[var(--ha-secondary-text-color)]">
          {t("alarm.wizard.codes_later")}
        </p>
      </div>
    {:else if store.step === 6}
      <h2 class="mb-1 text-lg font-semibold">{t("alarm.wizard.step.done")}</h2>
      <p class="mb-4 text-sm text-[var(--ha-secondary-text-color)]">
        {t("alarm.wizard.finish.hint")}
      </p>
      <Card class="p-4 text-sm">
        <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
          <dt class="text-[var(--ha-secondary-text-color)]">{t("alarm.area.name")}</dt>
          <dd class="font-medium">
            {store.areaName.trim() || t("alarm.wizard.area.default_name")}
          </dd>
          <dt class="text-[var(--ha-secondary-text-color)]">{t("alarm.wizard.step.sensors")}</dt>
          <dd class="font-medium">{store.selectedSensors.length}</dd>
          <dt class="text-[var(--ha-secondary-text-color)]">{t("alarm.wizard.step.outputs")}</dt>
          <dd class="font-medium">{store.selectedOutputs.length}</dd>
          <dt class="text-[var(--ha-secondary-text-color)]">{t("alarm.wizard.summary.delays")}</dt>
          <dd class="text-xs text-[var(--ha-primary-text-color)]">
            {delaySummaryLines.join(" · ")}
          </dd>
        </dl>
      </Card>
    {/if}

    <div class="mt-6 flex items-center justify-between gap-2">
      <Button variant="ghost" onclick={() => store.back()} disabled={store.step === 1 || submitting}>
        {t("alarm.wizard.back")}
      </Button>
      {#if store.step < TOTAL}
        <div class="flex items-center gap-2">
          <Button variant="ghost" onclick={() => store.skip()} disabled={submitting}>
            {t("alarm.wizard.skip")}
          </Button>
          <Button onclick={() => store.next()} disabled={submitting}>
            {t("alarm.wizard.next")}
          </Button>
        </div>
      {:else}
        <Button onclick={finish} disabled={submitting}>
          {submitting ? t("setup.finishing") : t("alarm.wizard.finish")}
        </Button>
      {/if}
    </div>
  </Card>
</div>
