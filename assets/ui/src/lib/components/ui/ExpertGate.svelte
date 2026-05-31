<script lang="ts">
  // ExpertGate hides its children behind the global expertMode
  // toggle. Used to wrap Settings sections, sub-sections, or
  // individual fields that should only surface when the operator
  // opts into expert mode. Renders nothing in basic mode.
  import type { Snippet } from "svelte";
  import { prefs } from "$lib/stores/preferences.svelte";

  type Props = { children?: Snippet };
  let { children }: Props = $props();
</script>

{#if prefs.expertMode}
  {@render children?.()}
{/if}
