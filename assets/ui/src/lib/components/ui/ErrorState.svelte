<script lang="ts">
  import { cn } from "$lib/utils";
  import { t } from "$lib/i18n";
  import Card from "./Card.svelte";
  import Button from "./Button.svelte";
  import Icon from "./Icon.svelte";

  // The one shared inline error surface. Replaces the divergent error markup
  // (some wrapped in a Card, some a bare <p>, some with the "Error:" prefix,
  // some with a retry button, some without) with a single consistent shape:
  // Card + alert icon + localized "Error: <message>" + optional retry.
  type Props = {
    message: string;
    onRetry?: () => void;
    class?: string;
  };

  let { message, onRetry, class: className }: Props = $props();
</script>

<Card class={cn("flex items-start gap-3 p-3", className)}>
  <Icon
    name="mdi:alert-circle"
    size={18}
    class="mt-0.5 shrink-0 text-red-600 dark:text-red-400"
    aria-label=""
  />
  <div class="min-w-0 flex-1">
    <p class="text-sm text-red-600 dark:text-red-400">{t("common.error")} {message}</p>
    {#if onRetry}
      <Button variant="outline" size="sm" class="mt-2" onclick={onRetry}>
        {t("common.reload")}
      </Button>
    {/if}
  </div>
</Card>
