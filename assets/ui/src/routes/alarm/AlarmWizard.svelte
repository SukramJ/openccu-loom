<script lang="ts">
  import { api, friendlyError } from "$lib/api/client";
  import { alarmPanelStore } from "$lib/stores/alarmPanel.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";

  import Card from "$lib/components/ui/Card.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";

  // Setup wizard (docs/alarm-concept.md §12.3), skeleton pattern borrowed
  // from routes/Setup.svelte: step dots + Back/Skip/Next footer, one atomic
  // write on the last step. Steps ①–④ collect just enough to create a
  // useful area (name + per-mode delays); ⑤ codes/users is a disabled
  // placeholder — PIN codes ship in a later slice (docs/alarm-concept.md
  // §11) and there is nothing to configure yet; ⑥ finish creates the area
  // via api.createAlarmArea and hands off to the Overview. Sensors and
  // outputs are deliberately NOT collected here: both need a real area id
  // to attach to (the picker and output setup are per-area), so this
  // wizard only points at those tabs — assignment happens after Finish,
  // and the wizard is re-runnable per area (§12.3) for anyone who wants to
  // come back and adjust delays later.

  const MODE_ORDER = ["perimeter", "full", "night", "vacation", "custom"] as const;
  type Mode = (typeof MODE_ORDER)[number];
  type DelayField = "exit" | "entry" | "trigger";
  type Delays = Record<Mode, Record<DelayField, number>>;

  // Defaults mirror internal/alarm/engine/config.go's ModeConfig field
  // names (exit_delay_s / entry_delay_s / trigger_time_s) — 180s is also
  // that package's DefaultTriggerSeconds.
  const DEFAULT_DELAYS = { exit: 30, entry: 15, trigger: 180 } as const;

  function defaultDelays(): Delays {
    const out = {} as Delays;
    for (const m of MODE_ORDER) out[m] = { ...DEFAULT_DELAYS };
    return out;
  }

  const STEP_KEYS = ["areas", "sensors", "outputs", "delays", "codes", "done"] as const;
  const TOTAL = STEP_KEYS.length;

  let step = $state(1);
  let submitting = $state(false);

  let areaName = $state("");
  let delays = $state<Delays>(defaultDelays());

  function back() {
    if (step > 1) step -= 1;
  }
  function next() {
    if (step < TOTAL) step += 1;
  }
  // Skip resets whatever the current step would have collected back to its
  // safe default before advancing — every step is skippable (§12.3) and
  // "skip" means "keep the sensible default", not "leave it half-filled".
  function skip() {
    if (step === 1) areaName = "";
    if (step === 4) delays = defaultDelays();
    next();
  }

  function setDelay(mode: Mode, field: DelayField, raw: string) {
    const n = Math.max(0, Math.round(Number(raw)));
    if (Number.isNaN(n)) return;
    delays = { ...delays, [mode]: { ...delays[mode], [field]: n } };
  }

  const numberInputClass =
    "h-9 w-20 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-2 text-right text-sm text-[var(--ha-primary-text-color)] focus-visible:border-[var(--ha-primary-color)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ha-primary-color)]";

  async function finish() {
    if (submitting) return;
    submitting = true;
    try {
      const name = areaName.trim() || t("alarm.wizard.area.default_name");
      const modes: Record<
        string,
        { exit_delay_s: number; entry_delay_s: number; trigger_time_s: number }
      > = {};
      for (const m of MODE_ORDER) {
        modes[m] = {
          exit_delay_s: delays[m].exit,
          entry_delay_s: delays[m].entry,
          trigger_time_s: delays[m].trigger,
        };
      }
      const created = await api.createAlarmArea({
        id: "",
        name,
        config: { modes },
      });
      await alarmPanelStore.refresh();
      toastStore.success(t("alarm.toast.saved"), created.name);
      location.hash = "#/alarm";
    } catch (err) {
      toastStore.error(t("alarm.toast.save_failed"), friendlyError(err, t));
    } finally {
      submitting = false;
    }
  }
</script>

<div class="mx-auto max-w-2xl">
  <Card class="p-6">
    <div class="mb-4 flex items-center justify-center gap-2" aria-hidden="true">
      {#each Array.from({ length: TOTAL }, (_, i) => i + 1) as n (n)}
        <span
          class="h-2 w-2 rounded-full transition-colors {n === step
            ? 'bg-[var(--ha-primary-color)]'
            : n < step
              ? 'bg-[color-mix(in_srgb,var(--ha-primary-color)_50%,transparent)]'
              : 'bg-[var(--ha-divider-color)]'}"
        ></span>
      {/each}
    </div>
    <p class="mb-4 text-center text-sm text-[var(--ha-secondary-text-color)]">
      {t("setup.step.progress", { current: step, total: TOTAL })}
    </p>

    {#if step === 1}
      <h2 class="mb-1 text-lg font-semibold">{t("alarm.wizard.step.areas")}</h2>
      <p class="mb-4 text-sm text-[var(--ha-secondary-text-color)]">
        {t("alarm.wizard.area.hint")}
      </p>
      <label class="block">
        <span class="mb-1 block text-sm font-medium">{t("alarm.area.name")}</span>
        <Input
          type="text"
          placeholder={t("alarm.wizard.area.default_name")}
          bind:value={areaName}
        />
      </label>
    {:else if step === 2}
      <h2 class="mb-1 text-lg font-semibold">{t("alarm.wizard.step.sensors")}</h2>
      <p class="mb-4 text-sm text-[var(--ha-secondary-text-color)]">
        {t("alarm.wizard.sensors.hint")}
      </p>
      <a
        href="#/alarm/picker"
        class="inline-flex items-center gap-1.5 text-sm font-medium text-[var(--ha-primary-color)] hover:underline"
      >
        <Icon name="mdi:link" size={16} aria-label="" />
        {t("alarm.wizard.sensors.cta")}
      </a>
    {:else if step === 3}
      <h2 class="mb-1 text-lg font-semibold">{t("alarm.wizard.step.outputs")}</h2>
      <p class="mb-4 text-sm text-[var(--ha-secondary-text-color)]">
        {t("alarm.wizard.outputs.hint")}
      </p>
      <a
        href="#/alarm/outputs"
        class="inline-flex items-center gap-1.5 text-sm font-medium text-[var(--ha-primary-color)] hover:underline"
      >
        <Icon name="mdi:link" size={16} aria-label="" />
        {t("alarm.wizard.outputs.cta")}
      </a>
    {:else if step === 4}
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
            {#each MODE_ORDER as mode (mode)}
              <tr class="border-b border-[var(--ha-divider-color)] last:border-0">
                <td class="p-2 font-medium">{t(`alarm.mode.${mode}`)}</td>
                <td class="p-2">
                  <input
                    type="number"
                    min="0"
                    class={numberInputClass}
                    value={delays[mode].exit}
                    oninput={(e) => setDelay(mode, "exit", e.currentTarget.value)}
                  />
                </td>
                <td class="p-2">
                  <input
                    type="number"
                    min="0"
                    class={numberInputClass}
                    value={delays[mode].entry}
                    oninput={(e) => setDelay(mode, "entry", e.currentTarget.value)}
                  />
                </td>
                <td class="p-2">
                  <input
                    type="number"
                    min="0"
                    class={numberInputClass}
                    value={delays[mode].trigger}
                    oninput={(e) => setDelay(mode, "trigger", e.currentTarget.value)}
                  />
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </Card>
    {:else if step === 5}
      <h2 class="mb-1 text-lg font-semibold">{t("alarm.wizard.step.codes")}</h2>
      <p class="mb-4 text-sm text-[var(--ha-secondary-text-color)]">
        {t("alarm.wizard.codes_later")}
      </p>
      <!-- Disabled placeholder: codes/users ship with a later slice, so this
           step renders an inert row rather than a form with nothing to
           submit. -->
      <div
        class="pointer-events-none flex items-center gap-3 rounded-md border border-dashed border-[var(--ha-divider-color)] p-4 opacity-50"
        aria-disabled="true"
      >
        <Icon name="mdi:lock" size={20} class="shrink-0" aria-label="" />
        <Input type="text" disabled placeholder={t("alarm.wizard.step.codes")} class="max-w-xs" />
      </div>
    {:else if step === 6}
      <h2 class="mb-1 text-lg font-semibold">{t("alarm.wizard.step.done")}</h2>
      <p class="mb-4 text-sm text-[var(--ha-secondary-text-color)]">
        {t("alarm.wizard.finish.hint")}
      </p>
      <Card class="p-4 text-sm">
        <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
          <dt class="text-[var(--ha-secondary-text-color)]">{t("alarm.area.name")}</dt>
          <dd class="font-medium">{areaName.trim() || t("alarm.wizard.area.default_name")}</dd>
        </dl>
      </Card>
    {/if}

    <div class="mt-6 flex items-center justify-between gap-2">
      <Button variant="ghost" onclick={back} disabled={step === 1 || submitting}>
        {t("alarm.wizard.back")}
      </Button>
      {#if step < TOTAL}
        <div class="flex items-center gap-2">
          <Button variant="ghost" onclick={skip} disabled={submitting}>
            {t("alarm.wizard.skip")}
          </Button>
          <Button onclick={next} disabled={submitting}>
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
