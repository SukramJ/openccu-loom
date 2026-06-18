<script lang="ts">
  import { api, ApiError } from "$lib/api/client";
  import type { ConfigSchemaField, ConfigFieldSource } from "$lib/api/client";
  import Button from "$lib/components/ui/Button.svelte";
  import SourceBadge from "$lib/components/ui/SourceBadge.svelte";
  import { t } from "$lib/i18n";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { refreshRestartPending } from "$lib/stores/restartPending.svelte";

  type Props = {
    schemaFields: ConfigSchemaField[];
    sources: Record<string, ConfigFieldSource>;
    effectiveConfig: Record<string, unknown>;
    allSections: string[];
    onChanged?: () => void;
  };

  let { schemaFields, sources, effectiveConfig, allSections, onChanged }: Props = $props();

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

  // Resolve the type-based zero value for a Go type.
  function typeZero(goType: string): unknown {
    if (goType === "bool" || goType === "*bool") return false;
    if (
      goType.startsWith("int") ||
      goType.startsWith("uint") ||
      goType.startsWith("float") ||
      goType === "time.Duration"
    )
      return 0;
    if (goType === "string") return "";
    if (goType === "[]string") return [];
    return undefined;
  }

  // Returns the schema default for a field, falling back to type zero.
  function schemaDefault(f: ConfigSchemaField): unknown {
    if (f.default !== undefined && f.default !== null) return f.default;
    return typeZero(f.go_type);
  }

  // Compare effective value against default; treat arrays by JSON equality.
  function differsFromDefault(f: ConfigSchemaField): boolean {
    const eff = getEffective(f.path);
    const def = schemaDefault(f);
    if (def === undefined) return true; // conservative: show when no default known
    return JSON.stringify(eff) !== JSON.stringify(def);
  }

  // A field is "changed" when it is stored in the DB AND its effective
  // value differs from the schema default. The sources map is keyed at
  // section granularity (e.g. "north.mcp" rather than
  // "north.mcp.enabled"), so check the owning section too — otherwise
  // every nested field would be missed.
  function isDbOverride(f: ConfigSchemaField): boolean {
    return sources[f.path] === "db" || sources[owningSection(f.path)] === "db";
  }

  const changedFields = $derived(
    schemaFields.filter((f) => isDbOverride(f) && differsFromDefault(f)),
  );

  // Group by "owning section" — longest allSections prefix of f.path.
  function owningSection(path: string): string {
    let best = "";
    for (const s of allSections) {
      if (
        (path === s || path.startsWith(s + ".")) &&
        s.length > best.length
      ) {
        best = s;
      }
    }
    return best || "(other)";
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
          <span class="rounded-full bg-slate-100 px-1.5 py-0.5 text-xs text-slate-600 dark:bg-slate-800 dark:text-slate-400">
            {group.fields.length}
          </span>
        </div>
        <div>
          {#each group.fields as field (field.path)}
            <div class="flex flex-wrap items-center justify-between gap-3 border-b border-slate-100 py-2 last:border-0 dark:border-slate-800">
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-1">
                  <span class="text-sm font-medium">{fieldLabel(field.path)}</span>
                  <span class="font-mono text-xs text-[var(--ha-secondary-text-color)]">{field.path}</span>
                  <SourceBadge source={sources[field.path] ?? "db"} />
                </div>
                <span class="mt-0.5 block font-mono text-xs text-slate-600 dark:text-slate-300">
                  {displayValue(field)}
                </span>
              </div>
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={!!reverting[field.path]}
                onclick={() => void revertField(field.path)}
              >
                {reverting[field.path] ? "…" : t("changes.revert")}
              </Button>
            </div>
          {/each}
        </div>
      </div>
    {/each}
  {/if}
</div>
