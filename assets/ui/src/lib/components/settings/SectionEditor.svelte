<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { ConfigSchemaField, ConfigFieldSource } from "$lib/api/client";
  import Button from "$lib/components/ui/Button.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import SourceBadge from "$lib/components/ui/SourceBadge.svelte";
  import ExpertGate from "$lib/components/ui/ExpertGate.svelte";
  import { t } from "$lib/i18n";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";

  type Props = {
    section: string;
    schemaFields: ConfigSchemaField[];
    sources: Record<string, ConfigFieldSource>;
    /**
     * Every section the daemon knows about. We use this to make
     * sure a parent section ("north.rest") does not also claim
     * the fields of a more specific child section
     * ("north.rest.auth.oidc"). Without the longest-prefix
     * guard the OIDC fields would render under BOTH tabs.
     */
    allSections?: string[];
    /**
     * Effective config returned by GET /config/effective, used as
     * the source of truth for default placeholders. Keys are
     * dotted paths matching the schema; values are whatever the
     * daemon currently uses for that field. The SectionEditor
     * walks this dictionary to surface a greyed-out hint inside
     * the input when the current DB-stored value is empty.
     */
    effectiveConfig?: Record<string, unknown>;
  };

  let { section, schemaFields, sources, allSections, effectiveConfig }: Props = $props();

  const sectionFields = $derived(
    schemaFields.filter((f) => {
      if (f.path !== section && !f.path.startsWith(section + ".")) return false;
      // Longest-prefix wins: if a more specific section claims
      // this field, hide it here.
      for (const other of allSections ?? []) {
        if (
          other !== section &&
          other.length > section.length &&
          (f.path === other || f.path.startsWith(other + "."))
        ) {
          return false;
        }
      }
      return true;
    }),
  );

  // Section-level intro: i18n key `settings.section.intro.<section>`.
  // Empty when not defined so the renderer can suppress the row.
  const sectionIntro = $derived.by(() => {
    const key = "settings.section.intro." + section;
    const translated = t(key);
    return translated === key ? "" : translated;
  });

  // Resolve a dotted path against the effective-config tree so we
  // can surface the daemon's default value as a placeholder in
  // each input. Returns undefined when the path is not in the
  // tree.
  function defaultAt(path: string): unknown {
    if (!effectiveConfig) return undefined;
    let cur: unknown = effectiveConfig;
    for (const part of path.split(".")) {
      if (cur == null || typeof cur !== "object") return undefined;
      cur = (cur as Record<string, unknown>)[part];
    }
    return cur;
  }

  // resolveDefault returns the daemon's effective default for a
  // field. Priority:
  //   1. The schema's explicit `default` (covers consumer-side
  //      fallbacks like ValuesCache.Enabled = true even though
  //      the Go struct's zero value is nil).
  //   2. The effective-config snapshot (covers everything that
  //      flows through applyDefaults).
  // Returns undefined when neither produces a value.
  function resolveDefault(f: ConfigSchemaField): unknown {
    if (f.default !== undefined && f.default !== null) return f.default;
    return defaultAt(f.path);
  }

  // String representation suitable for a placeholder attribute.
  // Returns "" when the default itself is empty/null so the
  // browser falls back to the field's own placeholder hint.
  function defaultPlaceholder(f: ConfigSchemaField): string {
    const v = resolveDefault(f);
    if (v == null || v === "") return "";
    if (f.go_type === "time.Duration" && typeof v === "number") {
      return formatDuration(v);
    }
    // Booleans render directly via the checkbox; no placeholder
    // string makes sense for them. parseValue() already pre-
    // populates the working state with the default so the
    // checkbox visually reflects the default state on a fresh
    // DB.
    if (f.go_type === "*bool" || f.go_type === "bool") return "";
    if (f.go_type === "[]string" && Array.isArray(v)) {
      return v.join(", ");
    }
    if (typeof v === "object") return JSON.stringify(v);
    return String(v);
  }

  let original = $state<Record<string, unknown>>({});
  let working = $state<Record<string, unknown>>({});
  let jsonErrors = $state<Record<string, string>>({});
  let loading = $state(true);
  let saving = $state(false);
  let deleting = $state(false);
  let usingDefaults = $state(false);
  let loadError = $state<string | null>(null);
  let showRestartModal = $state(false);

  // humanize turns a snake_case identifier into a readable label.
  // `broker_url` → "Broker URL", `tls_insecure_skip_verify` → "TLS
  // insecure skip verify". Acronyms are upper-cased on word
  // boundaries when they live in a tiny built-in list — everything
  // else just capitalises the first letter so the daemon does not
  // need to ship a translation row per config field.
  function humanize(k: string): string {
    const acronyms = new Set(["url", "tls", "mqtt", "oidc", "ws", "udp", "tcp", "uid", "rf", "rpc", "ip", "api"]);
    return k.split("_")
      .map((part) =>
        acronyms.has(part.toLowerCase())
          ? part.toUpperCase()
          : part.charAt(0).toUpperCase() + part.slice(1),
      )
      .join(" ");
  }

  // fieldLabel resolves a config-schema path to its rendered label,
  // preferring an explicit i18n key (`config.field.<path>`) and
  // falling back to a humanised tail-key. The fallback path means
  // operators see something readable for every field even without
  // a per-field translation row.
  function fieldLabel(path: string): string {
    const key = "config.field." + path;
    const translated = t(key);
    if (translated !== key) return translated;
    return humanize(tailKey(path));
  }

  // fieldHelp returns an optional inline-help string for the field.
  // Looked up as `config.help.<path>` in the i18n catalogue;
  // returns "" when no help is defined so the renderer can
  // suppress the hint row entirely.
  function fieldHelp(path: string): string {
    const key = "config.help." + path;
    const translated = t(key);
    return translated === key ? "" : translated;
  }

  // secretEnvName maps a secret-class field path to the canonical
  // environment-variable name the daemon resolves at runtime.
  // Returns "" when no env override is supported for the field;
  // mirrors internal/configstore/store.go::resolveEnvSecrets and
  // the per-central password_env path on centrals[].
  function secretEnvName(path: string): string {
    switch (path) {
      case "north.mqtt.password":
        return "OPENCCU_LOOM_MQTT_PASSWORD";
      case "north.rest.auth.oidc.client_secret":
        return "OPENCCU_LOOM_OIDC_CLIENT_SECRET";
      default:
        return "";
    }
  }

  // deepClone is a JSON-roundtrip safe-cloner. structuredClone()
  // chokes on Svelte 5 $state proxies (they are not part of the
  // structured-clone whitelist) — the symptom is a DataCloneError
  // when the working object holds anything that originated in a
  // $state-backed parent. JSON-serialise + parse strips proxies and
  // returns a plain tree, which is what we want for the in-memory
  // form state anyway.
  function deepClone<T>(v: T): T {
    return JSON.parse(JSON.stringify(v)) as T;
  }

  function tailKey(path: string): string {
    const parts = path.split(".");
    return parts[parts.length - 1];
  }

  // relativePath returns the field's path relative to the current
  // section so the SectionEditor can store working values in a
  // tree that mirrors the Go section struct. e.g. for
  // section="north.rest" and path="north.rest.rate_limit.enabled"
  // it returns "rate_limit.enabled". Without this two distinct
  // schema fields with the same tail key (north.rest.enabled and
  // north.rest.rate_limit.enabled) would collide in the flat
  // working map and the later one would overwrite the earlier.
  function relativePath(path: string): string {
    if (path === section) return "";
    if (path.startsWith(section + ".")) return path.slice(section.length + 1);
    return path;
  }

  // getDeep walks a dotted path inside a nested record; undefined
  // for empty path or missing nodes.
  function getDeep(obj: Record<string, unknown> | undefined, dotted: string): unknown {
    if (!obj) return undefined;
    if (dotted === "") return obj;
    let cur: unknown = obj;
    for (const part of dotted.split(".")) {
      if (cur == null || typeof cur !== "object") return undefined;
      cur = (cur as Record<string, unknown>)[part];
    }
    return cur;
  }

  // setDeep assigns value at the dotted path, creating intermediate
  // objects on the way. Mutates obj.
  function setDeep(obj: Record<string, unknown>, dotted: string, value: unknown): void {
    if (dotted === "") return;
    const parts = dotted.split(".");
    let cur: Record<string, unknown> = obj;
    for (let i = 0; i < parts.length - 1; i++) {
      const p = parts[i];
      const next = cur[p];
      if (typeof next !== "object" || next === null) {
        cur[p] = {};
      }
      cur = cur[p] as Record<string, unknown>;
    }
    cur[parts[parts.length - 1]] = value;
  }

  // setIn clones the working state and assigns into it via setDeep.
  // Returns the new root so the caller can reassign `working` and
  // trigger Svelte reactivity.
  function setIn(obj: Record<string, unknown>, dotted: string, value: unknown): Record<string, unknown> {
    const next = deepClone(obj);
    setDeep(next, dotted, value);
    return next;
  }

  function parseValue(raw: unknown, f: ConfigSchemaField): unknown {
    const def = f.default;
    // Fallback chain: explicit DB value > schema-curated default >
    // Go zero. The middle step is crucial for fields whose Go
    // zero value is *not* the daemon's actual default (e.g.
    // *bool ValuesCache.Enabled, which is nil in the struct but
    // defaults to true at runtime). Without it the operator
    // would see an empty checkbox and assume "disabled".
    if (f.go_type === "bool" || f.go_type === "*bool") {
      if (typeof raw === "boolean") return raw;
      if (typeof def === "boolean") return def;
      return false;
    }
    if (
      f.go_type.startsWith("int") ||
      f.go_type.startsWith("uint") ||
      f.go_type.startsWith("float")
    ) {
      if (typeof raw === "number") return raw;
      if (typeof def === "number") return def;
      return 0;
    }
    if (f.go_type === "string") {
      if (typeof raw === "string") return raw;
      if (typeof def === "string") return def;
      return "";
    }
    if (f.go_type === "[]string") {
      if (Array.isArray(raw)) return raw;
      if (Array.isArray(def)) return def;
      return [];
    }
    if (f.go_type === "time.Duration") {
      if (typeof raw === "number") return raw;
      if (typeof def === "number") return def;
      return 0;
    }
    if (raw !== undefined && raw !== null) return raw;
    if (def !== undefined && def !== null) return def;
    return "";
  }

  // Go duration helpers — mirror time.ParseDuration / String().
  // Accepts ns, us, µs, ms, s, m, h. Returns int (nanoseconds);
  // throws on parse failure so the caller can surface the error.
  function parseDurationString(s: string): number {
    const trimmed = s.trim();
    if (trimmed === "" || trimmed === "0") return 0;
    const re = /(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)/g;
    const units: Record<string, number> = {
      ns: 1, us: 1_000, "µs": 1_000, ms: 1_000_000,
      s: 1_000_000_000, m: 60_000_000_000, h: 3_600_000_000_000,
    };
    let total = 0;
    let consumed = 0;
    let m: RegExpExecArray | null;
    while ((m = re.exec(trimmed)) !== null) {
      total += parseFloat(m[1]) * units[m[2]];
      consumed += m[0].length;
    }
    if (consumed === 0 || consumed !== trimmed.length) {
      throw new Error("invalid duration: " + s);
    }
    return Math.round(total);
  }

  // Inverse of parseDurationString — produces the most compact
  // human form, mirroring Go's Duration.String().
  function formatDuration(ns: number): string {
    if (!ns || !Number.isFinite(ns)) return "";
    let n = Math.abs(ns);
    const sign = ns < 0 ? "-" : "";
    if (n % 3_600_000_000_000 === 0) return sign + n / 3_600_000_000_000 + "h";
    if (n % 60_000_000_000 === 0) return sign + n / 60_000_000_000 + "m";
    if (n % 1_000_000_000 === 0) return sign + n / 1_000_000_000 + "s";
    if (n % 1_000_000 === 0) return sign + n / 1_000_000 + "ms";
    if (n % 1_000 === 0) return sign + n / 1_000 + "us";
    return sign + n + "ns";
  }

  function isComplex(goType: string): boolean {
    return (
      !["bool", "*bool", "string", "[]string", "time.Duration"].includes(goType) &&
      !goType.startsWith("int") &&
      !goType.startsWith("uint") &&
      !goType.startsWith("float")
    );
  }

  async function load() {
    loading = true;
    loadError = null;
    usingDefaults = false;
    try {
      const data = await api.getConfigSection<Record<string, unknown>>(section);
      const init: Record<string, unknown> = {};
      for (const f of sectionFields) {
        const rel = relativePath(f.path);
        setDeep(init, rel, parseValue(getDeep(data, rel), f));
      }
      original = deepClone(init);
      working = deepClone(init);
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        usingDefaults = true;
        const init: Record<string, unknown> = {};
        for (const f of sectionFields) {
          setDeep(init, relativePath(f.path), parseValue(undefined, f));
        }
        original = deepClone(init);
        working = deepClone(init);
      } else {
        loadError = err instanceof ApiError ? err.message : String(err);
      }
    } finally {
      loading = false;
    }
  }

  onMount(() => void load());

  const isDirty = $derived(JSON.stringify(working) !== JSON.stringify(original));
  const hasRestartField = $derived(sectionFields.some((f) => f.restart_required));

  function resetWorking() {
    working = deepClone(original);
    jsonErrors = {};
  }

  function onDurationInput(rel: string, val: string) {
    try {
      const ns = parseDurationString(val);
      working = setIn(working, rel, ns);
      const errs = { ...jsonErrors };
      delete errs[rel];
      jsonErrors = errs;
    } catch {
      jsonErrors = { ...jsonErrors, [rel]: t("settings.duration_parse_error") };
    }
  }

  function onTextareaInput(rel: string, val: string, goType: string) {
    working = setIn(working, rel, val);
    if (isComplex(goType)) {
      try {
        JSON.parse(val);
        const errs = { ...jsonErrors };
        delete errs[rel];
        jsonErrors = errs;
      } catch {
        jsonErrors = { ...jsonErrors, [rel]: t("settings.json_parse_error") };
      }
    }
  }

  async function save() {
    // Pre-validate: complex types stored as textarea strings must
    // be parseable JSON before we commit.
    for (const f of sectionFields) {
      const rel = relativePath(f.path);
      const v = getDeep(working, rel);
      if (isComplex(f.go_type) && typeof v === "string") {
        try {
          JSON.parse(v);
        } catch {
          jsonErrors = { ...jsonErrors, [rel]: t("settings.json_parse_error") };
          return;
        }
      }
    }

    saving = true;
    try {
      const payload = deepClone(working);
      for (const f of sectionFields) {
        const rel = relativePath(f.path);
        const v = getDeep(payload, rel);
        if (f.go_type === "[]string" && typeof v === "string") {
          setDeep(payload, rel, v.split("\n").map((s) => s.trim()).filter(Boolean));
        } else if (isComplex(f.go_type) && typeof v === "string") {
          setDeep(payload, rel, JSON.parse(v));
        }
      }
      const result = await api.putConfigSection(section, payload);
      original = deepClone(working);
      toastStore.success(t("settings.saved"));
      usingDefaults = false;
      if (result.restart_required || hasRestartField) {
        showRestartModal = true;
      }
    } catch (err) {
      toastStore.error(
        t("settings.save_failed", { err: err instanceof ApiError ? err.message : String(err) }),
      );
    } finally {
      saving = false;
    }
  }

  async function resetSection() {
    const ok = await confirmStore.ask({
      title: t("settings.reset"),
      body: t("settings.reset_confirm"),
      confirmLabel: t("settings.reset"),
      destructive: true,
    });
    if (!ok) return;
    deleting = true;
    try {
      await api.deleteConfigSection(section);
      toastStore.success(t("settings.reset_done"));
      await load();
    } catch (err) {
      toastStore.error(
        t("settings.save_failed", { err: err instanceof ApiError ? err.message : String(err) }),
      );
    } finally {
      deleting = false;
    }
  }

  async function restartNow() {
    showRestartModal = false;
    try {
      await api.restartDaemon();
      toastStore.success(t("settings.restart_signalled"));
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    }
  }

  function textareaValue(v: unknown, goType: string): string {
    if (goType === "[]string") return Array.isArray(v) ? (v as string[]).join("\n") : "";
    if (v == null) return "";
    return JSON.stringify(v, null, 2);
  }
</script>

{#snippet secretWidget(f: ConfigSchemaField)}
  {@const rel = relativePath(f.path)}
  {@const v = getDeep(working, rel)}
  {@const help = fieldHelp(f.path)}
  {@const envName = secretEnvName(f.path)}
  {@const fromEnv = sources[f.path] === "env"}
  <div class="flex flex-wrap items-start justify-between gap-3 border-b border-slate-100 py-2 last:border-0 dark:border-slate-800">
    <div class="min-w-0 flex-1">
      <div class="flex flex-wrap items-center gap-1">
        <span class="text-sm font-medium">{fieldLabel(f.path)}</span>
        <span class="font-mono text-xs text-[var(--ha-secondary-text-color)]">{f.go_type}</span>
        {#if f.restart_required}
          <Badge variant="warning">{t("settings.restart_required")}</Badge>
        {/if}
      </div>
      {#if help}
        <p class="mt-0.5 text-xs text-[var(--ha-secondary-text-color)]">{help}</p>
      {/if}
      {#if envName}
        <p class="mt-0.5 text-xs text-[var(--ha-secondary-text-color)]">
          {fromEnv
            ? t("settings.secret_from_env", { name: envName })
            : t("settings.secret_env_override", { name: envName })}
        </p>
      {/if}
    </div>
    <div class="flex w-full min-w-0 flex-col items-stretch gap-1 sm:w-auto sm:items-end">
      <SourceBadge source={sources[f.path] ?? (v ? "db" : "default")} />
      <input
        type="password"
        value={String(v ?? "")}
        oninput={(e) => (working = setIn(working, rel, (e.target as HTMLInputElement).value))}
        disabled={fromEnv}
        autocomplete="new-password"
        placeholder={fromEnv ? "•••• (env)" : ""}
        class="h-9 w-full rounded border border-slate-300 bg-white px-2 text-base disabled:opacity-50 sm:w-60 sm:text-sm dark:border-slate-700 dark:bg-slate-900"
      />
    </div>
  </div>
{/snippet}

{#snippet fieldWidget(f: ConfigSchemaField)}
  {@const rel = relativePath(f.path)}
  {@const v = getDeep(working, rel)}
  {@const help = fieldHelp(f.path)}
  <div class="flex flex-wrap items-start justify-between gap-3 border-b border-slate-100 py-2 last:border-0 dark:border-slate-800">
    <div class="min-w-0 flex-1">
      <div class="flex flex-wrap items-center gap-1">
        <span class="text-sm font-medium">{fieldLabel(f.path)}</span>
        <span class="font-mono text-xs text-[var(--ha-secondary-text-color)]">{f.go_type}</span>
        {#if f.restart_required}
          <Badge variant="warning">{t("settings.restart_required")}</Badge>
        {/if}
      </div>
      {#if help}
        <p class="mt-0.5 text-xs text-[var(--ha-secondary-text-color)]">{help}</p>
      {/if}
    </div>
    <div class="flex w-full min-w-0 flex-col items-stretch gap-1 sm:w-auto sm:items-end">
      <SourceBadge source={sources[f.path] ?? "default"} />
      {#if f.go_type === "bool" || f.go_type === "*bool"}
        <input
          type="checkbox"
          checked={!!v}
          onchange={(e) => (working = setIn(working, rel, (e.target as HTMLInputElement).checked))}
          class="h-4 w-4"
        />
      {:else if f.go_type.startsWith("int") || f.go_type.startsWith("uint") || f.go_type.startsWith("float")}
        <input
          type="number"
          value={v === 0 || v === undefined || v === null ? "" : (v as number)}
          oninput={(e) => {
            const raw = (e.target as HTMLInputElement).value;
            working = setIn(working, rel, raw === "" ? 0 : Number(raw));
          }}
          placeholder={defaultPlaceholder(f)}
          class="h-9 w-full rounded border border-slate-300 bg-white px-2 text-base sm:w-36 sm:text-sm dark:border-slate-700 dark:bg-slate-900"
        />
      {:else if f.go_type === "string"}
        <input
          type="text"
          value={String(v ?? "")}
          oninput={(e) => (working = setIn(working, rel, (e.target as HTMLInputElement).value))}
          placeholder={defaultPlaceholder(f)}
          class="h-9 w-full rounded border border-slate-300 bg-white px-2 text-base sm:w-60 sm:text-sm dark:border-slate-700 dark:bg-slate-900"
        />
      {:else if f.go_type === "time.Duration"}
        <input
          type="text"
          value={formatDuration(Number(v ?? 0))}
          oninput={(e) => onDurationInput(rel, (e.target as HTMLInputElement).value)}
          placeholder={defaultPlaceholder(f) || "e.g. 60s, 5m, 250ms"}
          class="h-9 w-full rounded border border-slate-300 bg-white px-2 text-base sm:w-36 sm:text-sm dark:border-slate-700 dark:bg-slate-900"
          class:border-red-500={!!jsonErrors[rel]}
        />
        {#if jsonErrors[rel]}
          <p class="text-xs text-red-600 dark:text-red-400">{jsonErrors[rel]}</p>
        {/if}
      {:else}
        <textarea
          value={textareaValue(v, f.go_type)}
          oninput={(e) => onTextareaInput(rel, (e.target as HTMLTextAreaElement).value, f.go_type)}
          rows={3}
          placeholder={defaultPlaceholder(f)}
          class="w-full rounded border border-slate-300 bg-white px-2 py-1 font-mono text-xs sm:w-60 dark:border-slate-700 dark:bg-slate-900"
          class:border-red-500={!!jsonErrors[rel]}
        ></textarea>
        {#if jsonErrors[rel]}
          <p class="text-xs text-red-600 dark:text-red-400">{jsonErrors[rel]}</p>
        {/if}
      {/if}
    </div>
  </div>
{/snippet}

<div class="space-y-3">
  {#if loading}
    <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("common.loading")}</p>
  {:else if loadError}
    <p class="text-sm text-red-600 dark:text-red-400">{t("common.error")} {loadError}</p>
  {:else}
    {#if sectionIntro}
      <p class="rounded border border-slate-200 bg-slate-50 px-3 py-2 text-sm text-slate-700 dark:border-slate-700 dark:bg-slate-800/40 dark:text-slate-300">
        {sectionIntro}
      </p>
    {/if}
    {#if usingDefaults}
      <p class="rounded border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-300">
        {t("settings.section_unset")}
      </p>
    {/if}

    <div>
      {#each sectionFields as field (field.path)}
        {#if field.class === "secret"}
          {@render secretWidget(field)}
        {:else if field.class === "expert"}
          <ExpertGate>
            {@render fieldWidget(field)}
          </ExpertGate>
        {:else}
          {@render fieldWidget(field)}
        {/if}
      {/each}
    </div>

    <div class="flex flex-wrap items-center gap-2 border-t border-slate-200 pt-3 dark:border-slate-800">
      <Button
        type="button"
        variant="default"
        size="sm"
        disabled={!isDirty || saving || Object.keys(jsonErrors).length > 0}
        onclick={() => void save()}
      >
        {saving ? t("common.saving") : t("common.save")}
      </Button>
      <Button
        type="button"
        variant="outline"
        size="sm"
        disabled={!isDirty || saving}
        onclick={resetWorking}
      >
        {t("common.reset")}
      </Button>
      {#if !usingDefaults}
        <Button
          type="button"
          variant="ghost"
          size="sm"
          disabled={deleting}
          onclick={() => void resetSection()}
          class="ml-auto text-red-600 hover:text-red-700 dark:text-red-400"
        >
          {t("settings.reset")}
        </Button>
      {/if}
    </div>
  {/if}
</div>

{#if showRestartModal}
  <div
    class="fixed inset-0 z-50 flex items-center justify-center p-4"
    style="background-color: rgb(0 0 0 / 0.45);"
    role="dialog"
    aria-modal="true"
  >
    <div class="w-full max-w-sm rounded-lg border border-slate-200 bg-white p-5 shadow-xl dark:border-slate-700 dark:bg-slate-900">
      <h2 class="mb-2 text-base font-semibold">{t("settings.restart_required")}</h2>
      <p class="mb-4 text-sm text-[var(--ha-secondary-text-color)]">
        {t("settings.restart_daemon_help")}
      </p>
      <div class="flex justify-end gap-2">
        <Button type="button" variant="outline" size="sm" onclick={() => (showRestartModal = false)}>
          {t("settings.restart_later")}
        </Button>
        <Button type="button" variant="destructive" size="sm" onclick={() => void restartNow()}>
          {t("settings.restart_daemon")}
        </Button>
      </div>
    </div>
  </div>
{/if}
