<script lang="ts">
  // Test-only stand-in for ChannelPanel on pages that host several panels
  // (the link page, the device parameter page); those pages orchestrate the
  // panels, so their tests need the panel's contract, not its internals.
  // A LINK panel registers under its link role, a MASTER panel under
  // `ch<number>`. State lives in channel-panel-stub.ts so a test can reach it.
  import { untrack } from "svelte";
  import { stubSides, type StubSide } from "./channel-panel-stub";

  type Props = {
    address: string;
    channel: number;
    paramset?: "VALUES" | "MASTER" | "LINK";
    peer?: string;
    linkRole?: "sender" | "receiver";
    onDirtyChange?: (count: number) => void;
    onLoaded?: (info: { count: number; error: boolean }) => void;
  };
  let {
    address,
    channel,
    paramset = "LINK",
    peer,
    linkRole = "receiver",
    onDirtyChange,
    onLoaded,
  }: Props = $props();

  // The identity is fixed for the life of a panel on these pages.
  const key = untrack(() => (paramset === "LINK" ? linkRole : `ch${channel}`));
  const side: StubSide = untrack(
    () =>
      (stubSides[key] ??= {
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

<div data-testid={`panel-${key}`}>{address}:{channel} ← {peer}</div>
