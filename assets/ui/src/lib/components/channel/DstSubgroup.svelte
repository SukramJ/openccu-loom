<script lang="ts">
  import type { UISchemaParameter } from "$lib/api/types";
  import ParameterField from "$lib/components/parameter/ParameterField.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import {
    hhmmToMinutes,
    isDstTimeParam,
    minutesToHHMM,
  } from "$lib/channel/dst-groups";
  import type { ParamValues } from "$lib/channel/validate";
  import { t } from "$lib/i18n";

  type Props = {
    title: string;
    parameters: UISchemaParameter[];
    values: ParamValues;
    dirty: Set<string>;
    errors: Record<string, string>;
    onParamChange: (name: string, value: unknown) => void;
  };

  let {
    title,
    parameters,
    values,
    dirty,
    errors,
    onParamChange,
  }: Props = $props();

  function currentValue(p: UISchemaParameter): unknown {
    return p.name in values ? values[p.name] : p.value;
  }
</script>

<div
  class="rounded-md border border-slate-200 bg-slate-50 p-3 dark:border-slate-800 dark:bg-[color-mix(in_srgb,var(--color-slate-900)_40%,transparent)]"
>
  <div
    class="mb-2 text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]"
  >
    {title}
  </div>
  <div class="grid grid-cols-1 gap-3">
    {#each parameters as p (p.name)}
      {#if isDstTimeParam(p.name)}
        {@const minutes = Number(currentValue(p) ?? 0)}
        <div class="flex items-center justify-between gap-3">
          <div class="flex-1">
            <span class="text-sm font-medium text-slate-700 dark:text-slate-300">
              {p.label || p.name}
            </span>
            {#if dirty.has(p.name)}
              <Badge variant="warning">{t("parameter.modified")}</Badge>
            {/if}
            {#if errors[p.name]}
              <p class="text-xs text-red-600 dark:text-red-400">{errors[p.name]}</p>
            {/if}
          </div>
          <div class="w-28">
            {#if !p.operations.write}
              <!-- Read-only: show formatted time without a disabled
                   input, which tends to look broken mid-form. -->
              <span class="font-mono text-sm text-slate-700 dark:text-slate-200">
                {minutesToHHMM(minutes)}
              </span>
            {:else}
              <Input
                type="time"
                value={minutesToHHMM(minutes)}
                onchange={(e) => {
                  const el = e.target as HTMLInputElement;
                  const mins = hhmmToMinutes(el.value);
                  if (mins !== null) onParamChange(p.name, mins);
                }}
              />
            {/if}
          </div>
        </div>
      {:else}
        <ParameterField
          parameter={p}
          value={currentValue(p)}
          dirty={dirty.has(p.name)}
          error={errors[p.name] ?? null}
          onChange={(v) => onParamChange(p.name, v)}
        />
      {/if}
    {/each}
  </div>
</div>
