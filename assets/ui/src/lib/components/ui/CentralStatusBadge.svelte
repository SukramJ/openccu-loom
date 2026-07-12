<script lang="ts">
  // Shared status badge for one CCU. Maps the pair (available, readiness)
  // onto a Badge variant + localized label so every surface (Fleet,
  // Overview, DeviceList, Matter) renders bring-up state identically.
  // "ready" wins over availability; an unreachable-and-not-ready central
  // reads as "Offline"; the intermediate phases read as "Initializing".
  import Badge from "$lib/components/ui/Badge.svelte";
  import { t } from "$lib/i18n";
  import type { SystemCCUEntry } from "$lib/api/types";

  type Readiness = SystemCCUEntry["readiness"];
  type Variant = "default" | "success" | "warning" | "danger" | "muted";

  let { available, readiness }: { available: boolean; readiness: Readiness } =
    $props();

  const view = $derived.by((): { variant: Variant; label: string } => {
    const r = readiness;
    if (r?.ready || r?.phase === "ready") {
      return { variant: "success", label: t("central.readiness.ready") };
    }
    if (!available) {
      return { variant: "danger", label: t("central.readiness.offline") };
    }
    switch (r?.phase) {
      case "waiting_for_ccu":
        return { variant: "muted", label: t("central.readiness.waiting") };
      case "loading_hub":
        return {
          variant: "warning",
          label: t("central.readiness.loading_hub"),
        };
      case "loading_devices":
        return {
          variant: "warning",
          label: t("central.readiness.loading_devices", {
            loaded: r?.interfaces_loaded ?? 0,
            total: r?.interfaces_total ?? 0,
          }),
        };
      default:
        return { variant: "muted", label: t("central.readiness.unknown") };
    }
  });
</script>

<Badge variant={view.variant}>{view.label}</Badge>
