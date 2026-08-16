<script lang="ts">
  // Test-only composition that mirrors App.svelte: the shared confirm
  // dialog is mounted once, ahead of the routed view, so its window
  // keydown listener registers first and the routed view's own handlers
  // run after it — the ordering the focus behaviour depends on.
  import ConfirmDialog from "$lib/components/ui/ConfirmDialog.svelte";
  import Inbox from "../Inbox.svelte";
</script>

<ConfirmDialog />
<Inbox />
