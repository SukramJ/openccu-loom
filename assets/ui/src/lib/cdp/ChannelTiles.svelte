<!--
  ChannelTiles — the flat CDP-tile + AutoTile-fallback grid, factored
  out of CdpTilesPanel so the fleet-wide Overview route (B8) can render
  the exact same per-device tile set without forking the dispatch
  logic. See ADR 0016 and docs/ui/auto-tile-concept.md.

  Pure presentational component: the caller owns fetching `cdps`
  (`api.listCustomDataPoints`) plus loading/error/empty handling —
  this component only derives which CDPs have a registered widget,
  which channels fall through to AutoTile, and renders the grid.
-->
<script lang="ts">
  import type { CustomDPSummary, DeviceDetail } from "$lib/api/types";
  import { isOverviewExcluded } from "$lib/quickcontrol/domain";
  import AutoTile from "$lib/sensor-actor/AutoTile.svelte";
  import ChannelPinButton from "./ChannelPinButton.svelte";
  import { cdpWidgetFor, hasCdpWidget } from "./dispatch";

  type Props = {
    detail: DeviceDetail;
    cdps: CustomDPSummary[];
    /** Forwarded to AutoTile: show the pin toggle on each channel tile. */
    pinnable?: boolean;
  };

  let { detail, cdps, pinnable = false }: Props = $props();

  // CDPs with a registered widget — these render as CDP tiles.
  const renderable = $derived(cdps.filter((c) => hasCdpWidget(c.kind)));

  // CDP channel numbers, used to filter the orphan section. A
  // channel is orphan when (a) it has no CustomDP attached, or (b)
  // its attached CDP has no CDP-side widget shipping yet (so the
  // user still needs a way to reach it).
  const cdpChannelNumbers = $derived(new Set(renderable.map((c) => c.channel_no)));

  const orphanChannels = $derived(
    detail.channels.filter(
      (c) =>
        !isOverviewExcluded(c) &&
        c.address.includes(":") &&
        !cdpChannelNumbers.has(c.number) &&
        (c.data_points_count ?? 0) > 0,
    ),
  );

  // AutoTile coverage (docs/ui/auto-tile-concept.md). Each orphan
  // channel that ships at least one DP renders as its own AutoTile —
  // peers like HmIP-SMO230's four motion channels and HmIP-STE2-PCB's
  // three temperature sensors all deserve equal tiles.
  const autoTileChannels = $derived(orphanChannels);

  // Adaptive column count: a device with one or two tiles (e.g. a wall
  // thermostat or a contact) used only a third of the width on a 3-col
  // grid. Drop to two columns when there is little to show so the tiles
  // fill more of the row.
  //
  // `items-start` keeps every tile at its own content height instead of
  // CSS Grid's default stretch-to-row-height: a tall ClimateTile (many
  // feature rows) and a short LockTile (two buttons) can land in the
  // same row without the short tile's card being stretched into a
  // near-empty box. Row order stays DOM/row-major (unlike a CSS
  // multi-column masonry layout, which would reorder tiles into
  // column-major reading order) — only the vertical alignment changes.
  const tileGridClass = $derived(
    renderable.length + autoTileChannels.length <= 2
      ? "grid grid-cols-1 items-start gap-3 sm:grid-cols-2"
      : "grid grid-cols-1 items-start gap-3 md:grid-cols-2 xl:grid-cols-3",
  );
</script>

{#if renderable.length > 0 || autoTileChannels.length > 0}
  <div class={tileGridClass}>
    {#each renderable as cdp (`${cdp.name}:${cdp.channel_no}`)}
      {@const Widget = cdpWidgetFor(cdp.kind)}
      {@const ch = detail.channels.find((c) => c.number === cdp.channel_no)}
      {@const title = ch?.name || ch?.type_label || `${detail.address}:${cdp.channel_no}`}
      {#if Widget}
        <!-- The pin sits in an overlay rather than inside the widget:
             every CDP tile renders through the same ControlTile frame,
             and threading a pin prop through all eight widgets would
             couple each of them to the favourites feature for one
             corner affordance. The tile's top-right is free — its header
             row is icon + left-aligned text. -->
        {#if pinnable}
          <div class="relative">
            <Widget address={detail.address} {cdp} {title} />
            <ChannelPinButton
              channelAddress={`${detail.address}:${cdp.channel_no}`}
              label={title}
              class="absolute right-1.5 top-1.5"
            />
          </div>
        {:else}
          <Widget address={detail.address} {cdp} {title} />
        {/if}
      {/if}
    {/each}
    {#each autoTileChannels as ch (ch.address)}
      <AutoTile address={detail.address} channel={ch} {pinnable} />
    {/each}
  </div>
{/if}
