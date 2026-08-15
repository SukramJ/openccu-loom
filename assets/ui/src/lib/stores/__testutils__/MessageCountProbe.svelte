<script lang="ts">
  // Test-only probe for messagesStore. The sidebar renders the pending-message
  // badge straight from these getters, so a counter that mutates without
  // invalidating anything paints once at mount and never again — a defect no
  // direct getter assertion can see, because the getter itself always reads
  // the current value. Rendering the getters is what makes the reactivity
  // observable.
  import { messagesStore } from "$lib/stores/messages.svelte";
</script>

<span data-testid="total">{messagesStore.total}</span>
<span data-testid="service">{messagesStore.serviceCount}</span>
<span data-testid="alarm">{messagesStore.alarmCount}</span>
