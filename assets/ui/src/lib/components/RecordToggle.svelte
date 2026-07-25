<!--
  Per-datapoint recording toggle (SV10). Shows whether the selected data
  point's live values are persisted to measurement history and lets an
  operator force it on/off; "reset" clears the override back to the
  parameter-name glob policy. Self-hiding when the history feature is off
  (the endpoint 404s → HistoryDisabledError).
-->
<script lang="ts">
  import {
    getRecordingOverride,
    setRecordingOverride,
    HistoryDisabledError,
    ApiError,
    type RecordingState,
  } from "$lib/api/client";
  import Switch from "$lib/components/ui/Switch.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";

  type Props = {
    central: string;
    interfaceId: string;
    channel: string;
    parameter: string;
  };
  let { central, interfaceId, channel, parameter }: Props = $props();

  let recording = $state<RecordingState | null>(null);
  let available = $state(true);
  let busy = $state(false);

  // Reload the effective state whenever the selected data point changes.
  $effect(() => {
    const params = { central, interfaceId, channel, parameter };
    if (!params.central || !params.interfaceId || !params.channel || !params.parameter) {
      recording = null;
      return;
    }
    let cancelled = false;
    getRecordingOverride(params)
      .then((s) => {
        if (!cancelled) {
          recording = s;
          available = true;
        }
      })
      .catch((err) => {
        if (cancelled) return;
        if (err instanceof HistoryDisabledError) {
          available = false;
        } else {
          recording = null;
        }
      });
    return () => {
      cancelled = true;
    };
  });

  async function apply(record: boolean | null) {
    busy = true;
    try {
      recording = await setRecordingOverride({ central, interfaceId, channel, parameter, record });
      toastStore.success(record === null ? t("history.record_reset_done") : t("history.record_saved"));
    } catch (err) {
      const msg = err instanceof ApiError ? err.message : String(err);
      toastStore.error(t("history.record_error", { error: msg }));
    } finally {
      busy = false;
    }
  }
</script>

{#if available && recording}
  <span class="text-xs font-semibold text-slate-500 dark:text-slate-400">
    {t("history.record_label")}
  </span>
  <Switch
    checked={recording.record}
    disabled={busy}
    onCheckedChange={(v) => apply(v)}
  />
  {#if recording.source === "override"}
    <Button variant="ghost" size="sm" disabled={busy} onclick={() => apply(null)}>
      {t("history.record_reset")}
    </Button>
  {/if}
{/if}
