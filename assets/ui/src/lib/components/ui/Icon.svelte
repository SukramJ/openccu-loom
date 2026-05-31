<script lang="ts">
  import type { IconName } from "$lib/icons";
  import { resolveIcon } from "$lib/icons";

  // Thin wrapper around the icon registry so call-sites stay short.
  // Renders a Lucide component sized via Tailwind classes; the icon
  // chosen comes from the HA-style `mdi:<name>` vocabulary defined in
  // `lib/icons.ts`.

  type Props = {
    name: IconName;
    size?: number;
    class?: string;
    "aria-label"?: string;
    title?: string;
  };

  let {
    name,
    size = 18,
    class: className = "",
    "aria-label": ariaLabel,
    title,
  }: Props = $props();

  const Cmp = $derived(resolveIcon(name));
</script>

<Cmp
  size={size}
  class={className}
  aria-label={ariaLabel}
  aria-hidden={ariaLabel ? undefined : "true"}
  {title}
/>
