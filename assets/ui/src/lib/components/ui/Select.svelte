<script lang="ts">
  import { Select as BitsSelect } from "bits-ui";
  import { Check, ChevronDown } from "@lucide/svelte";
  import { cn } from "$lib/utils";
  import { t } from "$lib/i18n";

  type Option = { value: string; label: string };

  type Props = {
    options: Option[];
    value?: string;
    placeholder?: string;
    disabled?: boolean;
    onValueChange?: (value: string) => void;
    class?: string;
  };

  let {
    options,
    value = $bindable(""),
    placeholder = t("select.placeholder"),
    disabled = false,
    onValueChange,
    class: className,
  }: Props = $props();

  const label = $derived(
    options.find((o) => o.value === value)?.label ?? "",
  );
</script>

<BitsSelect.Root
  type="single"
  bind:value
  {disabled}
  onValueChange={(v) => typeof v === "string" && onValueChange?.(v)}
>
  <BitsSelect.Trigger
    class={cn(
      "flex h-10 w-full items-center justify-between rounded-md border border-slate-300 bg-white px-3 py-1 text-base shadow-sm sm:text-sm",
      "focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500",
      "disabled:cursor-not-allowed disabled:opacity-50",
      "dark:border-slate-700 dark:bg-slate-900",
      className,
    )}
  >
    <span class={cn(!value && "text-[var(--ha-secondary-text-color)]")}>
      {label || placeholder}
    </span>
    <ChevronDown class="ml-2 h-4 w-4 opacity-60" />
  </BitsSelect.Trigger>

  <BitsSelect.Portal>
    <BitsSelect.Content
      class="z-50 max-h-[60vh] min-w-[8rem] overflow-y-auto rounded-md border border-slate-200 bg-white p-1 text-sm shadow-md dark:border-slate-800 dark:bg-slate-900"
      sideOffset={4}
    >
      {#each options as option (option.value)}
        <BitsSelect.Item
          value={option.value}
          label={option.label}
          class="relative flex min-h-[40px] cursor-default select-none items-center rounded-sm py-1.5 pl-8 pr-2 text-base outline-none data-[highlighted]:bg-slate-100 data-[disabled]:opacity-50 sm:min-h-0 sm:text-sm dark:data-[highlighted]:bg-slate-800"
        >
          {#snippet children({ selected }: { selected: boolean })}
            <span
              class="absolute left-2 flex h-4 w-4 items-center justify-center"
            >
              {#if selected}<Check class="h-4 w-4" />{/if}
            </span>
            {option.label}
          {/snippet}
        </BitsSelect.Item>
      {/each}
    </BitsSelect.Content>
  </BitsSelect.Portal>
</BitsSelect.Root>
