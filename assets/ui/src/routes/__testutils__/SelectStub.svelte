<script lang="ts">
  // Test-only stand-in for $lib/components/ui/Select.svelte. The real
  // component wraps bits-ui's floating-portal listbox, which relies on
  // pointer-capture / positioning APIs happy-dom does not implement —
  // driving it through fireEvent never opens the option list. This stub
  // renders every option as a plain always-visible button so a load-race
  // test can pick between them deterministically.
  type Option = { value: string; label: string };
  type Props = {
    options: Option[];
    value?: string;
    onValueChange?: (value: string) => void;
  };
  // `value` is bindable like the real Select's, so a view that drives its
  // state through `bind:value` behaves the same against this stub.
  let { options, value = $bindable(""), onValueChange }: Props = $props();

  function pick(next: string) {
    value = next;
    onValueChange?.(next);
  }
</script>

<div role="listbox">
  {#each options as o (o.value)}
    <button
      type="button"
      role="option"
      aria-selected={o.value === value}
      onclick={() => pick(o.value)}
    >
      {o.label}
    </button>
  {/each}
</div>
