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
      "flex h-10 w-full items-center justify-between rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-3 py-1 text-base text-[var(--ha-primary-text-color)] shadow-sm sm:text-sm",
      "focus-visible:border-[var(--ha-primary-color)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ha-primary-color)]",
      "disabled:cursor-not-allowed disabled:opacity-50",
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
      class="z-50 max-h-[60vh] min-w-[8rem] overflow-y-auto rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] p-1 text-sm text-[var(--ha-primary-text-color)] shadow-md"
      sideOffset={4}
    >
      {#each options as option (option.value)}
        <BitsSelect.Item
          value={option.value}
          label={option.label}
          class="relative flex min-h-[40px] cursor-default select-none items-center rounded-sm py-1.5 pl-8 pr-2 text-base outline-none data-[highlighted]:bg-[var(--ha-secondary-background-color)] data-[disabled]:opacity-50 sm:min-h-0 sm:text-sm"
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
