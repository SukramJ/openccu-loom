<script lang="ts">
  import type { UISchemaSubsetGroup } from "$lib/api/types";
  import Select from "$lib/components/ui/Select.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import { t } from "$lib/i18n";

  type Props = {
    group: UISchemaSubsetGroup;
    onApply: (
      patch: Record<string, unknown>,
      meta: { fixed: string[]; editable: string[] },
    ) => void;
  };

  let { group, onApply }: Props = $props();

  let selected = $state<string>("");
  let userTouched = $state(false);
  $effect(() => {
    // Re-sync the picker whenever a fresh schema arrives (e.g. after
    // save). A user's pending selection is preserved.
    if (!userTouched) {
      selected =
        group.current_option_id != null ? String(group.current_option_id) : "";
    }
  });

  const options = $derived(
    group.options.map((o) => ({
      value: String(o.id),
      label: o.label,
    })),
  );

  function apply() {
    const opt = group.options.find((o) => String(o.id) === selected);
    if (!opt) return;
    // All members are forced to the option's exact value — same
    // semantics as a fixed-value profile apply, so we surface them as
    // `fixed` to lock the fields after apply.
    onApply({ ...opt.values }, {
      fixed: Object.keys(opt.values),
      editable: [],
    });
  }
</script>

<div class="rounded-md border border-slate-200 bg-slate-50 p-3 dark:border-slate-800 dark:bg-[color-mix(in_srgb,var(--color-slate-900)_50%,transparent)]">
  <p class="mb-2 text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
    {group.label}
    {#if group.current_option_id != null}
      <Badge variant="default">{t("subset.active")}</Badge>
    {/if}
  </p>
  <div class="flex flex-wrap items-center gap-3">
    <div class="min-w-[12rem] flex-1">
      <Select
        {options}
        value={selected}
        placeholder={t("subset.placeholder")}
        onValueChange={(v) => {
          userTouched = true;
          selected = v;
        }}
      />
    </div>
    <Button type="button" variant="outline" size="sm" disabled={!selected} onclick={apply}>
      {t("profile.apply")}
    </Button>
  </div>
  <p class="mt-1 break-words text-[10px] font-mono text-[var(--ha-secondary-text-color)]">
    {group.member_params.join(", ")}
  </p>
</div>
