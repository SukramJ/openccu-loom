<script lang="ts">
  import type { UISchemaParameter } from "$lib/api/types";
  import ParameterField from "$lib/components/parameter/ParameterField.svelte";
  import ParameterTimePair from "$lib/components/parameter/ParameterTimePair.svelte";
  import DstSubgroup from "./DstSubgroup.svelte";
  import { detectTimePairs } from "$lib/channel/time-pairs";
  import { detectDstGroups, dstHeader } from "$lib/channel/dst-groups";
  import type { ParamValues } from "$lib/channel/validate";

  type Props = {
    parameters: UISchemaParameter[];
    values: ParamValues;
    dirty: Set<string>;
    errors: Record<string, string>;
    locale: string;
    /** Parameter names locked by a profile apply; rendered read-only. */
    locked?: Set<string>;
    onParamChange: (name: string, value: unknown) => void;
    onAction?: (name: string) => void;
  };

  let {
    parameters,
    values,
    dirty,
    errors,
    locale,
    locked,
    onParamChange,
    onAction,
  }: Props = $props();

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
</script>

{#if dst.start.length > 0 || dst.end.length > 0}
  <div class="mb-4 grid grid-cols-1 gap-3 md:grid-cols-2">
    {#if dst.start.length > 0}
      <DstSubgroup
        title={dstHeader("start", locale)}
        parameters={dst.start}
        {values}
        {dirty}
        {errors}
        {onParamChange}
      />
    {/if}
    {#if dst.end.length > 0}
      <DstSubgroup
        title={dstHeader("end", locale)}
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
      <ParameterField
        parameter={p}
        value={currentValue(p)}
        dirty={dirty.has(p.name)}
        error={errors[p.name] ?? null}
        forceDisabled={locked?.has(p.name) ?? false}
        onChange={(v) => onParamChange(p.name, v)}
        onAction={onAction ? () => onAction(p.name) : undefined}
      />
    {/each}
  {/if}
</div>

{#if useCategoryGrouping}
  <div class="space-y-4">
    {#each categoryGroups as g (g.category)}
      <section>
        <h4 class="mb-2 text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
          {g.label}
        </h4>
        <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
          {#each g.params as p (p.name)}
            <ParameterField
              parameter={p}
              value={currentValue(p)}
              dirty={dirty.has(p.name)}
              error={errors[p.name] ?? null}
              forceDisabled={locked?.has(p.name) ?? false}
              onChange={(v) => onParamChange(p.name, v)}
              onAction={onAction ? () => onAction(p.name) : undefined}
            />
          {/each}
        </div>
      </section>
    {/each}
  </div>
{/if}
