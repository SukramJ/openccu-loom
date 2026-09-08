<script lang="ts">
  import type { Snippet } from "svelte";
  import { cn } from "$lib/utils";

  /**
   * PageShell — the one page frame every route renders into.
   *
   * Before this existed each route carried its own container, and they had
   * drifted: most used `max-w-6xl px-4 py-6 sm:px-6`, three went full width,
   * two picked `max-w-4xl` / `max-w-5xl`, and eight used `py-8`. Navigating
   * between them shifted the content horizontally and vertically, which reads
   * as the page jumping rather than as a deliberate layout.
   *
   * `width` names the intent instead of leaving it to a literal:
   *  - `default` — the reading-width frame; everything unless stated otherwise.
   *  - `wide`    — no max width, for surfaces that genuinely consume the
   *                viewport: the device table's many columns, the overview
   *                tile grid, the fleet cards.
   *  - `narrow`  — long-form prose (about page).
   *
   * Not for the full-screen centred flows (login, first-run setup): those own
   * their viewport and deliberately have no page frame.
   */
  type Width = "default" | "wide" | "narrow";

  interface Props {
    width?: Width;
    /** Extra classes for the <section> (e.g. `@container`). */
    class?: string;
    children: Snippet;
  }

  let { width = "default", class: cls = "", children }: Props = $props();

  const widths: Record<Width, string> = {
    default: "max-w-6xl",
    wide: "",
    narrow: "max-w-4xl",
  };
</script>

<section class={cn("mx-auto w-full px-4 py-6 sm:px-6", widths[width], cls)}>
  {@render children()}
</section>
