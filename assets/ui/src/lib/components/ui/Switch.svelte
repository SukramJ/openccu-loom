<script lang="ts">
  import { Switch as BitsSwitch } from "bits-ui";
  import { cn } from "$lib/utils";

  type Props = {
    checked?: boolean;
    disabled?: boolean;
    onCheckedChange?: (checked: boolean) => void;
    class?: string;
    id?: string;
    /**
     * Accessible name, for switches that carry no adjacent <label>.
     * A switch whose only label is a heading three elements away is
     * announced as "switch" and nothing else.
     */
    ariaLabel?: string;
    /** Element that describes the switch, e.g. its help text. */
    ariaDescribedby?: string;
  };

  let {
    checked = $bindable(false),
    disabled = false,
    onCheckedChange,
    class: className,
    id,
    ariaLabel,
    ariaDescribedby,
  }: Props = $props();
</script>

<BitsSwitch.Root
  bind:checked
  {disabled}
  {id}
  aria-label={ariaLabel}
  aria-describedby={ariaDescribedby}
  onCheckedChange={(v) => onCheckedChange?.(v)}
  class={cn(
    "peer inline-flex h-6 w-11 shrink-0 cursor-pointer items-center rounded-full border-2 border-transparent transition-colors",
    "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-500 focus-visible:ring-offset-2",
    "disabled:cursor-not-allowed disabled:opacity-50",
    "data-[state=checked]:bg-brand-500 data-[state=unchecked]:bg-slate-300 dark:data-[state=unchecked]:bg-slate-700",
    className,
  )}
>
  <BitsSwitch.Thumb
    class="pointer-events-none block h-5 w-5 rounded-full bg-white shadow-lg ring-0 transition-transform data-[state=checked]:translate-x-5 data-[state=unchecked]:translate-x-0 dark:bg-slate-200"
  />
</BitsSwitch.Root>
