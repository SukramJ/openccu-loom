<script lang="ts">
  import type { Snippet } from "svelte";
  import { cn } from "$lib/utils";
  import Icon from "./Icon.svelte";
  import type { IconName } from "$lib/icons";

  // The one shared empty-state. Replaces the per-list bare `<p>` messages so
  // every "nothing here" surface shares an icon + message + optional action.
  // `description` is an optional second, muted line that explains what the
  // surface will show once it has content (or how to populate it).
  type Props = {
    message: string;
    description?: string;
    icon?: IconName;
    class?: string;
    action?: Snippet;
  };

  let {
    message,
    description,
    icon = "mdi:text-box-search-outline",
    class: className,
    action,
  }: Props = $props();
</script>

<div
  class={cn(
    "flex flex-col items-center justify-center gap-3 py-12 text-center",
    className,
  )}
>
  <Icon name={icon} size={40} class="text-slate-300 dark:text-slate-600" aria-label="" />
  <div class="flex flex-col items-center gap-1">
    <p class="text-sm text-slate-500 dark:text-slate-400">{message}</p>
    {#if description}
      <p class="max-w-sm text-balance text-xs text-slate-400 dark:text-slate-500">
        {description}
      </p>
    {/if}
  </div>
  {#if action}
    {@render action()}
  {/if}
</div>
