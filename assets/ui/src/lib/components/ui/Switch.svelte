<script lang="ts">
  import { Switch as BitsSwitch } from "bits-ui";
  import { cn } from "$lib/utils";

  type Props = {
    checked?: boolean;
    disabled?: boolean;
    onCheckedChange?: (checked: boolean) => void;
    class?: string;
    id?: string;
  };

  let {
    checked = $bindable(false),
    disabled = false,
    onCheckedChange,
    class: className,
    id,
  }: Props = $props();
</script>

<BitsSwitch.Root
  bind:checked
  {disabled}
  {id}
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
