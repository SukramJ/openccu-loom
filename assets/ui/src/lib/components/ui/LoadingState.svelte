<script lang="ts">
  import { cn } from "$lib/utils";
  import { t } from "$lib/i18n";
  import Spinner from "./Spinner.svelte";

  // The one shared "loading…" placeholder. Replaces the ~15 bare
  // `<p>{t("common.loading")}</p>` reimplementations so every view shows the
  // same spinner + label and the same vertical rhythm.
  type Props = {
    label?: string;
    class?: string;
  };

  let { label, class: className }: Props = $props();
</script>

<div
  class={cn(
    "flex items-center justify-center gap-2 py-8 text-sm text-slate-500 dark:text-slate-400",
    className,
  )}
  role="status"
  aria-live="polite"
>
  <Spinner size={18} />
  <span>{label ?? t("common.loading")}</span>
</div>
