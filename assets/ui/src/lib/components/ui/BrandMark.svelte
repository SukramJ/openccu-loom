<script lang="ts">
  // Liefert das OpenCCU-Loom-Branding in zwei Varianten:
  //   mode="mark"     — nur Bildmarke (Hexhome), für Favicon-große Kontexte
  //   mode="wordmark" — volle Wortmarke (Bildmarke + Schriftzug)
  // Die SVGs liegen unter /app/ (Vite kopiert assets/ui/public/* dorthin).

  type Props = {
    mode?: "mark" | "wordmark";
    height?: number;
    /**
     * Wenn true, wird die Wortmarke mit dem Hashlink "#/devices" verlinkt
     * (typisch für Sidebar-Brand). Wenn false, wird nur das Bild gerendert.
     */
    href?: string | null;
    ariaLabel?: string;
  };

  let {
    mode = "wordmark",
    height = 28,
    href = null,
    ariaLabel = "OpenCCU-Loom",
  }: Props = $props();

  const src = $derived(mode === "mark" ? "/app/mark-hexhome.svg" : "/app/wordmark.svg");
</script>

{#if href}
  <a {href} class="inline-flex items-center" aria-label={ariaLabel}>
    <img {src} alt={ariaLabel} style="height: {height}px; width: auto; display: block;" />
  </a>
{:else}
  <img {src} alt={ariaLabel} style="height: {height}px; width: auto; display: block;" />
{/if}
