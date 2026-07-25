<script lang="ts">
  import type { UISchemaParameter } from "$lib/api/types";
  import ParameterField from "$lib/components/parameter/ParameterField.svelte";
  import ParameterTimePair from "$lib/components/parameter/ParameterTimePair.svelte";
  import DstSubgroup from "./DstSubgroup.svelte";
  import { detectTimePairs } from "$lib/channel/time-pairs";
  import { detectDstGroups, dstHeader } from "$lib/channel/dst-groups";
  import { disambiguateLabels } from "$lib/channel/disambiguate-labels";
  import type { LabelDisambiguation } from "$lib/channel/disambiguate-labels";
  import {
    formatReading,
    isConditionValueParam,
  } from "$lib/channel/brightness-helper";
  import { t } from "$lib/i18n";
  import type { ParamValues } from "$lib/channel/validate";

  type Props = {
    parameters: UISchemaParameter[];
    values: ParamValues;
    dirty: Set<string>;
    errors: Record<string, string>;
    locale: string;
    /** Parameter names locked by a profile apply; rendered read-only. */
    locked?: Set<string>;
    /**
     * Live brightness reading of the LINK sender channel, when it
     * exposes one. Enables the "take current brightness" helper on the
     * SHORT_/LONG_ COND_VALUE_LO/_HI threshold fields. Null / omitted
     * for VALUES / MASTER and links whose sender has no brightness DP.
     */
    brightnessSource?: { value: number; unit: string | null } | null;
    onParamChange: (name: string, value: unknown) => void;
    onAction?: (name: string) => void;
    /**
     * Determine one parameter's live value from the device. When set, a
     * "Determine" button is rendered for determine-capable parameters.
     * Wired only in the MASTER editor; omitted elsewhere.
     */
    onDetermine?: (name: string) => Promise<void>;
  };

  let {
    parameters,
    values,
    dirty,
    errors,
    locale,
    locked,
    brightnessSource = null,
    onParamChange,
    onAction,
    onDetermine,
  }: Props = $props();

  // Build the per-field brightness helper for a condition-threshold
  // parameter, or undefined when it does not apply (non-condition field,
  // no sender reading). The button patches the field through the normal
  // onParamChange path so dirty tracking / undo keep working.
  function brightnessHelperFor(
    p: UISchemaParameter,
  ): { value: number; display: string } | undefined {
    if (!brightnessSource) return undefined;
    if (!isConditionValueParam(p.name)) return undefined;
    return {
      value: brightnessSource.value,
      display: formatReading(brightnessSource.value, brightnessSource.unit),
    };
  }

  // DST first: `DST_START_*` / `DST_END_*` are pulled out of the flat
  // list and rendered as two titled sub-sections. This matches the
  // CCU WebUI layout and keeps "Sonstige Einstellungen" from being
  // polluted with a dozen DST fields.
  const dst = $derived(detectDstGroups(parameters));

  // Pair detection on what's left after DST — Unit+Value couples
  // collapse into a single row.
  const remaining = $derived(
    parameters.filter((p) => !dst.paired.has(p.name)),
  );
  const pairResult = $derived(detectTimePairs(remaining));
  const pairs = $derived(pairResult.pairs);
  const paired = $derived(pairResult.paired);

  function currentValue(p: UISchemaParameter): unknown {
    return p.name in values ? values[p.name] : p.value;
  }

  // LINK-paramset categories carry semantic context the user can
  // navigate by: jump targets define what happens on key press,
  // conditions gate transitions, action types pick the firmware mode.
  // Group the leftover parameters by category and render lightweight
  // section headers between them. Falls back to a single block when
  // the schema does not classify (VALUES / MASTER / pre-classifier
  // LINK).
  type CategoryGroup = { category: string; label: string; params: UISchemaParameter[] };

  function categoryLabel(c: string, l: string): string {
    const de: Record<string, string> = {
      level: "Pegel",
      time: "Zeit",
      jump_target: "Sprungziel",
      condition: "Bedingung",
      action: "Aktionstyp",
      other: "Sonstige",
    };
    const en: Record<string, string> = {
      level: "Level",
      time: "Time",
      jump_target: "Jump target",
      condition: "Condition",
      action: "Action type",
      other: "Other",
    };
    const map = l === "de" ? de : en;
    return map[c] ?? c;
  }

  const categoryOrder = ["level", "time", "jump_target", "condition", "action", "other"];

  const ungroupedParams = $derived(
    remaining.filter((p) => !paired.has(p.name)),
  );

  const useCategoryGrouping = $derived(
    ungroupedParams.some((p) => !!p.category) &&
      ungroupedParams.length >= 4,
  );

  const categoryGroups = $derived.by<CategoryGroup[]>(() => {
    if (!useCategoryGrouping) return [];
    const buckets = new Map<string, UISchemaParameter[]>();
    for (const p of ungroupedParams) {
      const key = p.category || "other";
      const list = buckets.get(key);
      if (list) list.push(p);
      else buckets.set(key, [p]);
    }
    const out: CategoryGroup[] = [];
    for (const c of categoryOrder) {
      const params = buckets.get(c);
      if (params && params.length) {
        out.push({ category: c, label: categoryLabel(c, locale), params });
        buckets.delete(c);
      }
    }
    for (const [c, params] of buckets) {
      out.push({ category: c, label: categoryLabel(c, locale), params });
    }
    return out;
  });

  // Duplicate-label disambiguation for the flat (non-category) render.
  // Category groups compute their own map per group in the template.
  const ungroupedDisambiguation = $derived(disambiguateLabels(ungroupedParams));

  // Append the localized directional qualifier to a colliding label so
  // two parameters that share a server-provided label (differing only
  // by an upper/lower threshold suffix in their name) can be told
  // apart. Returns the label unchanged when there is nothing to add.
  function decorateLabel(
    p: UISchemaParameter,
    dis: LabelDisambiguation | undefined,
  ): string | undefined {
    if (!dis || dis.direction === null) return p.label;
    const qualifier =
      dis.direction === "upper"
        ? t("parameter.threshold.upper")
        : t("parameter.threshold.lower");
    return `${p.label ?? p.name} (${qualifier})`;
  }
</script>

{#if dst.start.length > 0 || dst.end.length > 0}
  <div class="mb-4 grid grid-cols-1 gap-3 md:grid-cols-2">
    {#if dst.start.length > 0}
      <DstSubgroup
        title={dstHeader("start")}
        parameters={dst.start}
        {values}
        {dirty}
        {errors}
        {onParamChange}
      />
    {/if}
    {#if dst.end.length > 0}
      <DstSubgroup
        title={dstHeader("end")}
        parameters={dst.end}
        {values}
        {dirty}
        {errors}
        {onParamChange}
      />
    {/if}
  </div>
{/if}

<div class="grid grid-cols-1 gap-4 md:grid-cols-2">
  {#each pairs as pair (pair.prefix)}
    <ParameterTimePair
      {pair}
      {locale}
      unitValue={currentValue(pair.unitParam)}
      valueValue={currentValue(pair.valueParam)}
      unitDirty={dirty.has(pair.unitParam.name)}
      valueDirty={dirty.has(pair.valueParam.name)}
      unitError={errors[pair.unitParam.name] ?? null}
      valueError={errors[pair.valueParam.name] ?? null}
      onChange={(name, v) => onParamChange(name, v)}
    />
  {/each}
  {#if !useCategoryGrouping}
    {#each ungroupedParams as p (p.name)}
      {@const dis = ungroupedDisambiguation.get(p.name)}
      <ParameterField
        parameter={dis ? { ...p, label: decorateLabel(p, dis) } : p}
        nameBadge={dis?.emphasizeName ?? false}
        value={currentValue(p)}
        dirty={dirty.has(p.name)}
        error={errors[p.name] ?? null}
        forceDisabled={locked?.has(p.name) ?? false}
        brightnessHelper={brightnessHelperFor(p)}
        onChange={(v) => onParamChange(p.name, v)}
        onAction={onAction ? () => onAction(p.name) : undefined}
        onDetermine={onDetermine ? () => onDetermine(p.name) : undefined}
      />
    {/each}
  {/if}
</div>

{#if useCategoryGrouping}
  <div class="space-y-4">
    {#each categoryGroups as g (g.category)}
      {@const groupDisambiguation = disambiguateLabels(g.params)}
      <section>
        <h4 class="mb-2 text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
          {g.label}
        </h4>
        <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
          {#each g.params as p (p.name)}
            {@const dis = groupDisambiguation.get(p.name)}
            <ParameterField
              parameter={dis ? { ...p, label: decorateLabel(p, dis) } : p}
              nameBadge={dis?.emphasizeName ?? false}
              value={currentValue(p)}
              dirty={dirty.has(p.name)}
              error={errors[p.name] ?? null}
              forceDisabled={locked?.has(p.name) ?? false}
              brightnessHelper={brightnessHelperFor(p)}
              onChange={(v) => onParamChange(p.name, v)}
              onAction={onAction ? () => onAction(p.name) : undefined}
              onDetermine={onDetermine ? () => onDetermine(p.name) : undefined}
            />
          {/each}
        </div>
      </section>
    {/each}
  </div>
{/if}
