<script lang="ts">
  // Test-only stand-in for ChannelPanel on the link page; the page's job
  // is orchestrating two panels, not the panel internals. State lives in
  // channel-panel-stub.ts so a test can reach it.
  import { untrack } from "svelte";
  import { stubSides, type StubSide } from "./channel-panel-stub";

  type Props = {
    address: string;
    channel: number;
    peer?: string;
    linkRole?: "sender" | "receiver";
    onDirtyChange?: (count: number) => void;
    onLoaded?: (info: { count: number; error: boolean }) => void;
  };
  let { address, channel, peer, linkRole = "receiver", onDirtyChange, onLoaded }: Props = $props();

  // The role is fixed for the life of a panel on the link page.
  const side: StubSide = untrack(
    () =>
      (stubSides[linkRole] ??= {
        saveResult: true,
        saves: 0,
        discards: 0,
        paramCount: 3,
      }),
  );
  side.markDirty = (n) => onDirtyChange?.(n);

  $effect(() => {
    onLoaded?.({ count: side.paramCount, error: false });
  });

  export async function save(): Promise<boolean> {
    side.saves++;
    if (side.saveResult) onDirtyChange?.(0);
    return side.saveResult;
  }

  export function discard(): void {
    side.discards++;
    onDirtyChange?.(0);
  }
</script>

<div data-testid={`panel-${linkRole}`}>{address}:{channel} ← {peer}</div>
