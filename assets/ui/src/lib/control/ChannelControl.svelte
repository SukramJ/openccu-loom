<!--
  Single-channel CONTROL widget host: fetches a channel's data-points,
  resolves the dominant CONTROL family, and renders the matching
  widget from the registry. Renders nothing when:
   - the channel has no CONTROL-tagged data-points, or
   - the resolved family has no widget registered yet.

  Callers branch on the rendered output (via the `resolved` snippet
  prop) to fall back to the existing ParameterField stack.
-->
<script lang="ts">
  import { onMount } from "svelte";
  import { api, friendlyError } from "$lib/api/client";
  import type { DataPointSummary } from "$lib/api/types";
  import { onResync, subscribe } from "$lib/stores/events.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { dpLabel } from "$lib/sensor-actor/classify";
  import { t } from "$lib/i18n";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import { resolveChannel, type ResolvedChannel } from "./resolver";
  import { widgetForResolved } from "./widgets";

  type Props = {
    address: string;
    channel: number;
    title: string;
    secondary?: string;
    /** Fires when the resolver picks no widget — caller renders fallback. */
    onUnresolved?: () => void;
  };

  let { address, channel, title, secondary, onUnresolved }: Props = $props();

  let dataPoints = $state<DataPointSummary[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);

  async function load() {
    loading = true;
    error = null;
    try {
      const list = await api.listDataPoints(address, channel);
      dataPoints = list;
    } catch (err) {
      error = friendlyError(err, t);
    } finally {
      loading = false;
    }
  }

  const channelAddress = $derived(`${address}:${channel}`);

  onMount(() => {
    load();
    // Live updates: patch dataPoints in place when the WS event bus
    // delivers a value change for our channel.
    const unsub = subscribe((ev) => {
      if (ev.type !== "data_point") return;
      const e = ev.payload as {
        channel_address: string;
        parameter: string;
        value: unknown;
      };
      if (e.channel_address !== channelAddress) return;
      const idx = dataPoints.findIndex((dp) => dp.parameter === e.parameter);
      if (idx < 0) return;
      dataPoints[idx] = {
        ...dataPoints[idx],
        value: e.value,
        observed: true,
      };
    });
    // The boot snapshot no longer replays values into the stream; it
    // signals a resync and the channel reloads its own data points.
    const unsubResync = onResync(() => void load());
    return () => {
      unsub();
      unsubResync();
    };
  });

  const resolved = $derived<ResolvedChannel | null>(
    resolveChannel(dataPoints),
  );

  const Widget = $derived(resolved ? widgetForResolved(resolved) : undefined);

  $effect(() => {
    if (!loading && (!resolved || !Widget)) onUnresolved?.();
  });

  async function onSetSlot(slot: string, value: unknown) {
    if (!resolved) return;
    // Multi-family channels (HM-CC-TC simple thermostat:
    // SWITCH.STATE + TEMP.SETPOINT) keep slots from secondary
    // families in `siblings`; search those too so the writer finds
    // the DP no matter which family owns the slot.
    let dp = resolved.slots[slot];
    if (!dp) {
      for (const bucket of Object.values(resolved.siblings)) {
        if (bucket && bucket[slot]) {
          dp = bucket[slot];
          break;
        }
      }
    }
    if (!dp) return;
    try {
      await api.setValue(address, channel, dp.parameter, value);
      // Optimistic update — REST returns 202 Accepted before the CCU
      // confirms; the WS event will reconcile.
      const idx = dataPoints.findIndex((d) => d.parameter === dp.parameter);
      if (idx >= 0) {
        dataPoints[idx] = { ...dataPoints[idx], value };
      }
    } catch (err) {
      // A rejected write is an action result, not a load failure: `error`
      // drives the template branch that replaces the whole widget, so
      // writing it here would leave the operator without the control they
      // just used — and nothing clears it until the next resync.
      toastStore.error(
        t("sensor_actor.action_failed", { name: dpLabel(dp) }),
        friendlyError(err, t),
      );
    }
  }
</script>

{#if loading}
  <div
    class="rounded-xl border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] p-3 text-sm text-[var(--ha-secondary-text-color)]"
  >
    {t("common.loading")}
  </div>
{:else if error}
  <ErrorState message={error} onRetry={load} />
{:else if resolved && Widget}
  <Widget {resolved} {title} {secondary} {onSetSlot} />
{/if}
