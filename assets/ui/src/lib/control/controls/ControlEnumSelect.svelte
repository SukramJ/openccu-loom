<!--
  Generic enum dropdown for CCU VALUE_LIST parameters. Renders a
  native <select> styled in HA's ha-select idiom — small footprint,
  works on keyboard + screen readers, no third-party combobox.

  Filters CCU sentinel values (OLD_VALUE / DO_NOT_CARE) which the
  paramset uses internally but should never reach a user-facing
  picker.
-->
<script lang="ts">
  type Props = {
    /** Current value as either the label (string) or its index (number). */
    value: string | number | undefined;
    /** Full VALUE_LIST from the descriptor. */
    options: string[];
    /** Optional human labels per option; defaults to the raw label. */
    labels?: (option: string) => string;
    disabled?: boolean;
    label?: string;
    onChange: (next: string) => void;
  };

  let { value, options, labels, disabled = false, label, onChange }: Props = $props();

  const SKIP = new Set(["OLD_VALUE", "DO_NOT_CARE"]);

  const visible = $derived(options.filter((o) => !SKIP.has(o)));

  const currentLabel = $derived<string>(
    typeof value === "number"
      ? (options[value] ?? "")
      : ((value as string | undefined) ?? ""),
  );

  function display(o: string): string {
    if (labels) return labels(o);
    return o
      .toLowerCase()
      .replace(/_/g, " ")
      .replace(/\b\w/g, (c) => c.toUpperCase());
  }
</script>

<label class="flex flex-col gap-1">
  {#if label}
    <span class="text-xs text-[var(--ha-secondary-text-color)]">{label}</span>
  {/if}
  <select
    class="h-9 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-2 text-sm text-[var(--ha-primary-text-color)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-offset-1"
    {disabled}
    value={currentLabel}
    onchange={(e) => onChange((e.target as HTMLSelectElement).value)}
  >
    {#each visible as opt (opt)}
      <option value={opt}>{display(opt)}</option>
    {/each}
  </select>
</label>
