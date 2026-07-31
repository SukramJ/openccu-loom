<!--
  ChannelPinButton — the favourites star for one channel.

  Extracted so the CDP tiles and the AutoTile fallback pin identically.
  They render through different frames (ControlTile vs. AutoTile's own
  header), and before this the star existed only on the fallback — which
  meant the channels an operator most wants quick access to, the actuators
  backed by a custom data point, were the ones that could not be pinned.
-->
<script lang="ts">
  import Icon from "$lib/components/ui/Icon.svelte";
  import { favoritesStore } from "$lib/stores/favorites.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";

  type Props = {
    /** Channel address, e.g. ABC0000001:4 — the favourite's natural key. */
    channelAddress: string;
    /** Display name cached with the favourite so the list can render it. */
    label: string;
    class?: string;
  };

  let { channelAddress, label, class: klass = "" }: Props = $props();

  const pinned = $derived(favoritesStore.isPinned("channel", channelAddress));

  async function toggle() {
    try {
      const nowPinned = await favoritesStore.toggle({
        type: "channel",
        id: channelAddress,
        label,
      });
      toastStore.success(
        nowPinned
          ? t("favorites.added", { label })
          : t("favorites.removed", { label }),
      );
    } catch (err) {
      toastStore.error(err instanceof Error ? err.message : String(err));
    }
  }
</script>

<button
  type="button"
  class="inline-flex min-h-8 min-w-8 items-center justify-center rounded text-[var(--ha-secondary-text-color)] hover:text-[var(--ha-primary-color)] {klass}"
  onclick={() => void toggle()}
  title={pinned ? t("favorites.unpin_channel") : t("favorites.pin_channel")}
  aria-label={pinned ? t("favorites.unpin_channel") : t("favorites.pin_channel")}
  aria-pressed={pinned}
>
  <Icon name={pinned ? "mdi:star" : "mdi:star-outline"} size={14} />
</button>
