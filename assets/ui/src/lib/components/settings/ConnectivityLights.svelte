<script lang="ts">
  // Tri-light health indicator strip — one dot each for CCU,
  // MQTT, Matter. Polls /api/v1/health every 10 s (the endpoint
  // is auth-free and cheap) and aggregates the components by
  // service:
  //
  //   CCU    = aggregate of every `<central>-<interface>` row
  //   MQTT   = the standalone `mqtt` row, if present
  //   Matter = the standalone `matter` row, if present
  //
  // Each light:
  //   green   — all matched components healthy
  //   amber   — at least one degraded, none unhealthy
  //   red     — at least one unhealthy
  //   grey    — no matching components reported (service disabled)
  //
  // Hover surfaces the per-component summary via native <title>.
  import { onMount, onDestroy } from "svelte";
  import { api } from "$lib/api/client";
  import type { HealthComponent, HealthSnapshot } from "$lib/api/types";
  import { t } from "$lib/i18n";

  let components = $state<HealthComponent[]>([]);
  let pollTimer: ReturnType<typeof setInterval> | undefined;

  async function load() {
    try {
      const snap: HealthSnapshot = await api.health();
      components = snap.components;
    } catch {
      // Network blip — keep stale data so the dots don't flicker.
    }
  }

  onMount(() => {
    void load();
    pollTimer = setInterval(load, 10_000);
  });
  onDestroy(() => {
    if (pollTimer) clearInterval(pollTimer);
  });

  // A "service light" aggregates a subset of the health-component
  // list into one indicator + tooltip.
  type LightStatus = "green" | "amber" | "red" | "grey";
  type ServiceLight = {
    label: string;
    status: LightStatus;
    detail: string;
  };

  function aggregateInterfaceRows(rows: HealthComponent[]): ServiceLight {
    // CCU rows look like "<central>-<interface>" (e.g.
    // "OttoGo-HmIP-RF"). The single-word "central" row is the
    // daemon-global heartbeat — we treat it as one more component.
    const ifaceRows = rows.filter((c) => c.name.includes("-") || c.name === "central");
    if (ifaceRows.length === 0) {
      return { label: t("connectivity.ccu"), status: "grey", detail: t("connectivity.no_components") };
    }
    return rollUp(t("connectivity.ccu"), ifaceRows);
  }

  function pickRow(rows: HealthComponent[], name: string, label: string): ServiceLight {
    const row = rows.find((c) => c.name === name);
    if (!row) {
      return { label, status: "grey", detail: t("connectivity.no_components") };
    }
    return rollUp(label, [row]);
  }

  function rollUp(label: string, rows: HealthComponent[]): ServiceLight {
    let unhealthy = 0;
    let degraded = 0;
    for (const r of rows) {
      if (r.status === "unhealthy") unhealthy++;
      else if (r.status === "degraded") degraded++;
    }
    const status: LightStatus =
      unhealthy > 0 ? "red" : degraded > 0 ? "amber" : "green";
    const detail = rows
      .map((r) => {
        const status = t(`health.status.${r.status}`);
        const note = r.note_key ? t(r.note_key) : r.note;
        return `${r.name}: ${status}${note ? " — " + note : ""}`;
      })
      .join("\n");
    return { label, status, detail };
  }

  const lights = $derived<ServiceLight[]>([
    aggregateInterfaceRows(components),
    pickRow(components, "mqtt", t("connectivity.mqtt")),
    pickRow(components, "matter", t("connectivity.matter")),
  ]);

  const palette: Record<LightStatus, string> = {
    green: "bg-emerald-500",
    amber: "bg-amber-500",
    red: "bg-red-500",
    grey: "bg-slate-400",
  };

  // $derived, not a plain const: t() only re-evaluates inside a reactive
  // scope, and these words go into the tooltip and the aria-label, which
  // have to follow a language switch like every other caption.
  const labels = $derived<Record<LightStatus, string>>({
    green: t("connectivity.green"),
    amber: t("connectivity.amber"),
    red: t("connectivity.red"),
    grey: t("connectivity.grey"),
  });
</script>

<div class="flex flex-wrap items-center gap-3 text-xs">
  {#each lights as light (light.label)}
    <span
      class="inline-flex items-center gap-1.5"
      title={`${light.label}: ${labels[light.status]}\n\n${light.detail}`}
    >
      <span
        class={`inline-block size-2.5 rounded-full ${palette[light.status]}`}
        aria-label={`${light.label}: ${labels[light.status]}`}
      ></span>
      <span class="text-[var(--ha-secondary-text-color)]">{light.label}</span>
    </span>
  {/each}
</div>
