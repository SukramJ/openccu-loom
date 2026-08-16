<script lang="ts">
  // Test-only stand-in for $lib/components/schedule/ScheduleTab.svelte.
  // The real panel fetches and renders a full week profile; a sub-tab
  // test only needs to observe whether the panel is mounted at all, which
  // is what separates "the tab is offered" from "the tab is selected".
  type Props = { address?: string };
  let { address = "" }: Props = $props();
</script>

<div data-testid="schedule-tab" data-address={address}></div>
