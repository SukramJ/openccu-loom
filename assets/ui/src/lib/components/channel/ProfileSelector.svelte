<script lang="ts">
  import Select from "$lib/components/ui/Select.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import type { UISchemaProfile } from "$lib/api/types";
  import { t } from "$lib/i18n";

  type Props = {
    profile: UISchemaProfile;
    locale: string;
    /**
     * Current values of the channel — used by the dry-run preview to
     * show how many parameters would change before the user clicks
     * apply. Optional for backwards compatibility.
     */
    currentValues?: Record<string, unknown>;
    /**
     * Called when the user hits "Übernehmen". The patch carries the
     * concrete values to write (fixed constraints + range defaults),
     * `fixed` lists the parameters the profile hard-codes (the caller
     * should lock those fields) and `editable` lists the ones the
     * user is still free to tweak. Port of aiohomematic-config's
     * ResolvedProfile.fixed_params / editable_params split.
     */
    onApply: (
      patch: Record<string, unknown>,
      meta: { fixed: string[]; editable: string[] },
    ) => void;
  };

  let { profile, locale, currentValues, onApply }: Props = $props();

  /**
   * openccu-data receiver profile shape:
   *   {
   *     <sender_type>: {
   *       "profiles": [
   *         { id, name: { en, de, … }, description: { en, de }, params: { <p>: {...} } },
   *         …
   *       ]
   *     }
   *   }
   *
   * For LINK paramsets the backend pre-filters the payload down to
   * the sender channel type of the actual link (profile.sender_type)
   * and attaches profile.active_profile_id — the id of the profile
   * whose constraints match the current values. We surface only the
   * relevant sender's variants and pre-select the active one.
   * Outside LINK (MASTER/VALUES currently don't emit a profile) we
   * fall back to showing every sender's variants for diagnostic use.
   */
  type RawProfile = {
    id: number;
    name: Record<string, string>;
    description?: Record<string, string>;
    params?: Record<string, RawParam>;
  };
  type RawParam = {
    constraint_type: string;
    value?: unknown;
    default?: unknown;
    min_value?: unknown;
    max_value?: unknown;
  };

  type Variant = {
    key: string;
    id: number;
    label: string;
    description: string;
    params: Record<string, RawParam>;
  };

  const variants: Variant[] = $derived.by(() => {
    const raw = profile.raw as Record<string, { profiles?: RawProfile[] }> | undefined;
    if (!raw) return [];
    // Prefer the pre-filtered sender subset; fall back to all senders
    // if the backend could not determine the sender type.
    const senderKeys = profile.sender_type && profile.sender_type in raw
      ? [profile.sender_type]
      : Object.keys(raw);
    const singleSender = senderKeys.length === 1;
    const out: Variant[] = [];
    for (const sender of senderKeys) {
      const sub = raw[sender];
      if (!sub) continue;
      for (const p of sub.profiles ?? []) {
        const name =
          p.name?.[locale] ??
          p.name?.en ??
          Object.values(p.name ?? {})[0] ??
          `#${p.id}`;
        out.push({
          key: `${sender}::${p.id}`,
          id: p.id,
          label: singleSender ? name : `${name} (${sender})`,
          description:
            p.description?.[locale] ?? p.description?.en ?? "",
          params: p.params ?? {},
        });
      }
    }
    return out;
  });

  // The backend classifies the link's current values against the
  // profile set and returns the best-matching id via
  // active_profile_id. We initialise the picker to that variant so
  // the user sees "aha, this link is currently in profile X".
  const preselectKey = $derived.by(() => {
    if (!profile.active_profile_id) return "";
    const match = variants.find((v) => v.id === profile.active_profile_id);
    return match?.key ?? "";
  });

  let selected = $state<string>("");
  let userTouched = $state(false);

  $effect(() => {
    if (userTouched) return;
    selected = preselectKey;
  });

  function onSelect(v: string) {
    userTouched = true;
    selected = v;
  }

  const current = $derived(
    variants.find((v) => v.key === selected) ?? null,
  );

  // Dry-run preview: classify each profile parameter against the
  // user's current values without writing anything. Splits into
  //   • would_change — fixed value differs, or range default differs
  //   • already_matches — fixed value already equals current
  //   • out_of_range — list/range constraint that current violates
  // Counts feed the inline preview banner; the lists drive the
  // optional "Details"-expand.
  type PreviewBucket = "change" | "matches" | "violates" | "ignored";
  type PreviewEntry = {
    name: string;
    bucket: PreviewBucket;
    current: unknown;
    next?: unknown;
    detail?: string;
  };
  let previewExpanded = $state(false);

  const preview = $derived.by<PreviewEntry[]>(() => {
    if (!current || !currentValues) return [];
    const out: PreviewEntry[] = [];
    for (const [name, param] of Object.entries(current.params)) {
      const cur = currentValues[name];
      const ct = param.constraint_type;
      if (ct === "fixed" && param.value !== undefined) {
        const matches =
          cur != null && Number(cur) === Number(param.value);
        out.push({
          name,
          bucket: matches ? "matches" : "change",
          current: cur,
          next: param.value,
        });
      } else if (ct === "list") {
        const list = (param as unknown as { values?: number[] }).values ?? [];
        if (list.length === 0) continue;
        const matches =
          cur != null &&
          list.some((v) => Number(cur) === Number(v));
        out.push({
          name,
          bucket: matches ? "matches" : "violates",
          current: cur,
          detail: list.join(" / "),
        });
      } else if (ct === "range") {
        const lo = (param as unknown as { min_value?: number }).min_value;
        const hi = (param as unknown as { max_value?: number }).max_value;
        const n = cur == null ? NaN : Number(cur);
        const inRange =
          Number.isFinite(n) &&
          (lo == null || n >= lo) &&
          (hi == null || n <= hi);
        if (param.default !== undefined && cur !== param.default) {
          out.push({
            name,
            bucket: inRange ? "change" : "violates",
            current: cur,
            next: param.default,
          });
        } else {
          out.push({
            name,
            bucket: inRange ? "matches" : "violates",
            current: cur,
            detail:
              lo != null && hi != null ? `${lo}–${hi}` : undefined,
          });
        }
      }
    }
    return out;
  });

  const previewSummary = $derived.by(() => {
    const buckets = { change: 0, matches: 0, violates: 0, ignored: 0 };
    for (const e of preview) buckets[e.bucket]++;
    return buckets;
  });

  function apply() {
    if (!current) return;
    const patch: Record<string, unknown> = {};
    const fixed: string[] = [];
    const editable: string[] = [];
    for (const [name, param] of Object.entries(current.params)) {
      if (param.constraint_type === "fixed" && param.value !== undefined) {
        patch[name] = param.value;
        fixed.push(name);
      } else if (param.constraint_type === "range") {
        if (param.default !== undefined) patch[name] = param.default;
        editable.push(name);
      } else if (param.constraint_type === "list") {
        // List constraints keep the current value but restrict the
        // allowed options — we surface them as editable; the caller
        // may choose to validate later. Patch untouched.
        editable.push(name);
      }
    }
    onApply(patch, { fixed, editable });
  }
</script>

{#if variants.length > 0}
  <div class="rounded-md border border-slate-200 bg-slate-50 p-3 dark:border-slate-800 dark:bg-[color-mix(in_srgb,var(--color-slate-900)_50%,transparent)]">
    <p class="mb-2 text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
      {t("profile.header")} · {profile.sender_type
        ? `${profile.sender_type} → ${profile.receiver_type}`
        : profile.receiver_type}
      {#if preselectKey}
        <span class="ml-2 font-normal normal-case text-[var(--ha-secondary-text-color)]">
          · {t("profile.detected")}
        </span>
      {/if}
    </p>
    <div class="flex flex-wrap items-center gap-3">
      <div class="min-w-[12rem] flex-1">
        <Select
          options={variants.map((v) => ({
            value: v.key,
            label: v.label,
          }))}
          value={selected}
          placeholder={t("profile.placeholder")}
          onValueChange={onSelect}
        />
      </div>
      <Button
        type="button"
        variant="outline"
        size="sm"
        disabled={!current}
        onclick={apply}
      >
        {t("profile.apply")}
      </Button>
    </div>
    {#if current?.description}
      <p class="mt-2 text-xs text-[var(--ha-secondary-text-color)]">{current.description}</p>
    {/if}

    {#if current && currentValues && preview.length > 0}
      <div class="mt-3 rounded border border-slate-200 bg-white p-2 text-xs dark:border-slate-700 dark:bg-slate-950">
        <div class="flex flex-wrap items-center gap-3">
          <span class="font-semibold text-slate-700 dark:text-slate-200">
            {t("profile.preview_label")}
          </span>
          <span class="text-emerald-700 dark:text-emerald-300">
            ✓ {previewSummary.matches}
            {t("profile.preview.matching")}
          </span>
          <span class="text-amber-700 dark:text-amber-300">
            ↻ {previewSummary.change}
            {t("profile.preview.will_change")}
          </span>
          {#if previewSummary.violates > 0}
            <span class="text-red-700 dark:text-red-300">
              ⚠ {previewSummary.violates}
              {t("profile.preview.conflict")}
            </span>
          {/if}
          <button
            type="button"
            class="ml-auto text-[var(--ha-secondary-text-color)] hover:text-brand-700"
            aria-expanded={previewExpanded}
            onclick={() => (previewExpanded = !previewExpanded)}
          >
            {previewExpanded ? t("profile.preview.hide") : t("profile.preview.show")}
          </button>
        </div>
        {#if previewExpanded}
          <div class="overflow-x-auto">
          <table class="table-reflow mt-2 w-full text-left text-[11px]">
            <thead class="text-[var(--ha-secondary-text-color)]">
              <tr>
                <th class="py-1 pr-2">{t("profile.col.parameter")}</th>
                <th class="py-1 pr-2">{t("profile.col.current")}</th>
                <th class="py-1 pr-2">{t("profile.col.next")}</th>
                <th class="py-1">{t("profile.col.status")}</th>
              </tr>
            </thead>
            <tbody>
              {#each preview as entry (entry.name)}
                <tr class="border-t border-slate-100 dark:border-slate-800">
                  <td class="reflow-title py-1 pr-2 font-mono">{entry.name}</td>
                  <td class="py-1 pr-2 font-mono text-[var(--ha-secondary-text-color)]" data-label={t("profile.col.current")}>
                    {entry.current ?? "—"}
                  </td>
                  <td class="py-1 pr-2 font-mono" data-label={t("profile.col.next")}>
                    {entry.next ?? entry.detail ?? "—"}
                  </td>
                  <td class="py-1" data-label={t("profile.col.status")}>
                    {#if entry.bucket === "matches"}
                      <span class="text-emerald-700 dark:text-emerald-300">✓</span>
                    {:else if entry.bucket === "change"}
                      <span class="text-amber-700 dark:text-amber-300">↻</span>
                    {:else if entry.bucket === "violates"}
                      <span class="text-red-700 dark:text-red-300">⚠</span>
                    {/if}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
          </div>
        {/if}
      </div>
    {/if}
  </div>
{/if}
