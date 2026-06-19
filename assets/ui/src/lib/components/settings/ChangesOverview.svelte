<script lang="ts">
  import { api, ApiError } from "$lib/api/client";
  import type { ConfigSchemaField } from "$lib/api/client";
  import Button from "$lib/components/ui/Button.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import { t } from "$lib/i18n";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { refreshRestartPending } from "$lib/stores/restartPending.svelte";

  type Props = {
    // changedPaths is the authoritative set from the backend (config
    // fields that differ from the running boot config) — i.e. what the
    // operator changed this session, not what differs from the default.
    changedPaths: string[];
    schemaFields: ConfigSchemaField[];
    effectiveConfig: Record<string, unknown>;
    allSections: string[];
    onChanged?: () => void;
    // Switch the settings view to another tab (used to route a non-section
    // field like "centrals" to the CCUs tab where it is managed).
    onNavigate?: (tab: string) => void;
  };

  let { changedPaths, schemaFields, effectiveConfig, allSections, onChanged, onNavigate }: Props = $props();

  // Humanize helper (mirrors SectionEditor).
  function humanize(k: string): string {
    const acronyms = new Set(["url", "tls", "mqtt", "oidc", "ws", "udp", "tcp", "uid", "rf", "rpc", "ip", "api"]);
    return k
      .split("_")
      .map((part) =>
        acronyms.has(part.toLowerCase())
          ? part.toUpperCase()
          : part.charAt(0).toUpperCase() + part.slice(1),
      )
      .join(" ");
  }

  function fieldLabel(path: string): string {
    const key = "config.field." + path;
    const translated = t(key);
    if (translated !== key) return translated;
    const parts = path.split(".");
    return humanize(parts[parts.length - 1]);
  }

  // Walk effectiveConfig by dotted path.
  function getEffective(path: string): unknown {
    let cur: unknown = effectiveConfig;
    for (const part of path.split(".")) {
      if (cur == null || typeof cur !== "object") return undefined;
      cur = (cur as Record<string, unknown>)[part];
    }
    return cur;
  }

  // The changed fields are exactly those the backend reports as differing
  // from the boot config; intersect with the schema so we have a label,
  // type and current value for each.
  const changedSet = $derived(new Set(changedPaths));
  const changedFields = $derived(schemaFields.filter((f) => changedSet.has(f.path)));

  // Longest allSections prefix of a path — empty when no config section
  // owns it (e.g. "centrals", which lives in its own store and is managed
  // on the CCUs tab, not via the per-field config reset).
  function sectionPrefix(path: string): string {
    let best = "";
    for (const s of allSections) {
      if ((path === s || path.startsWith(s + ".")) && s.length > best.length) {
        best = s;
      }
    }
    return best;
  }

  function owningSection(path: string): string {
    return sectionPrefix(path) || "(other)";
  }

  // A field is revertible via DELETE /config/fields/{path} only when a
  // config section owns it. Non-section fields (centrals) can't be reset
  // that way — DELETE returns 400 — so we route the operator to where
  // they actually manage them instead.
  function revertible(path: string): boolean {
    return sectionPrefix(path) !== "";
  }

  const groupedChanged = $derived.by(() => {
    const order: string[] = [];
    const buckets = new Map<string, ConfigSchemaField[]>();
    for (const f of changedFields) {
      const sec = owningSection(f.path);
      if (!buckets.has(sec)) {
        buckets.set(sec, []);
        order.push(sec);
      }
      buckets.get(sec)!.push(f);
    }
    return order.map((sec) => ({ sec, fields: buckets.get(sec)! }));
  });

  // Compact display value for a field.
  function displayValue(f: ConfigSchemaField): string {
    if (f.class === "secret") return "••••";
    const v = getEffective(f.path);
    if (v == null) return "—";
    if (f.go_type === "time.Duration" && typeof v === "number") {
      return formatDuration(v);
    }
    if (Array.isArray(v)) {
      // Arrays of primitives join cleanly; arrays of objects (e.g.
      // centrals) would render as "[object Object]" — show a count.
      if (v.every((x) => x === null || typeof x !== "object")) {
        return v.join(", ") || "[]";
      }
      return t("changes.n_entries", { count: String(v.length) });
    }
    if (typeof v === "object") return JSON.stringify(v);
    return String(v);
  }

  function formatDuration(ns: number): string {
    if (!ns || !Number.isFinite(ns)) return "0";
    const n = Math.abs(ns);
    const sign = ns < 0 ? "-" : "";
    if (n % 3_600_000_000_000 === 0) return sign + n / 3_600_000_000_000 + "h";
    if (n % 60_000_000_000 === 0) return sign + n / 60_000_000_000 + "m";
    if (n % 1_000_000_000 === 0) return sign + n / 1_000_000_000 + "s";
    if (n % 1_000_000 === 0) return sign + n / 1_000_000 + "ms";
    if (n % 1_000 === 0) return sign + n / 1_000 + "us";
    return sign + n + "ns";
  }

  let reverting = $state<Record<string, boolean>>({});

  async function revertField(path: string) {
    reverting = { ...reverting, [path]: true };
    try {
      await api.resetConfigField(path);
      toastStore.success(t("changes.reverted"));
      onChanged?.();
      void refreshRestartPending();
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    } finally {
      const next = { ...reverting };
      delete next[path];
      reverting = next;
    }
  }
</script>

<div class="space-y-4">
  {#if changedFields.length === 0}
    <p class="rounded border border-slate-200 bg-slate-50 px-4 py-6 text-center text-sm text-[var(--ha-secondary-text-color)] dark:border-slate-700 dark:bg-slate-800/40">
      {t("changes.empty")}
    </p>
  {:else}
    <p class="rounded border border-slate-200 bg-slate-50 px-3 py-2 text-sm text-slate-700 dark:border-slate-700 dark:bg-slate-800/40 dark:text-slate-300">
      {t("changes.intro")}
    </p>

    {#each groupedChanged as group (group.sec)}
      <div>
        <div class="mb-1 flex items-center gap-2 border-b border-slate-200 pb-1 dark:border-slate-700">
          <h3 class="text-sm font-semibold text-slate-700 dark:text-slate-200">
            {group.sec}
          </h3>
          <Badge variant="muted">{group.fields.length}</Badge>
        </div>
        <div>
          {#each group.fields as field (field.path)}
            <div class="flex flex-wrap items-center justify-between gap-3 border-b border-slate-100 py-2 last:border-0 dark:border-slate-800">
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-1">
                  <span class="text-sm font-medium">{fieldLabel(field.path)}</span>
                  <span class="font-mono text-xs text-[var(--ha-secondary-text-color)]">{field.path}</span>
                </div>
                <span class="mt-0.5 block font-mono text-xs text-slate-600 dark:text-slate-300">
                  {displayValue(field)}
                </span>
              </div>
              {#if revertible(field.path)}
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={!!reverting[field.path]}
                  onclick={() => void revertField(field.path)}
                >
                  {reverting[field.path] ? "…" : t("changes.revert")}
                </Button>
              {:else if field.path === "centrals"}
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onclick={() => onNavigate?.("ccus")}
                >
                  {t("changes.manage_ccus")}
                </Button>
              {:else}
                <span class="text-xs text-[var(--ha-secondary-text-color)]">
                  {t("changes.not_revertible")}
                </span>
              {/if}
            </div>
          {/each}
        </div>
      </div>
    {/each}
  {/if}
</div>
