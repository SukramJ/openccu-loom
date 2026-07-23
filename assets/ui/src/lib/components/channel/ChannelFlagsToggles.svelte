<script lang="ts">
  // Operator per-channel overrides (G12): hidden (drop the channel from the
  // operation surfaces — data-point list / MQTT / Matter) and locked (block
  // control writes). Mirrors SecureTransmission's toggle shape.
  import { untrack } from "svelte";
  import { api, friendlyError } from "$lib/api/client";
  import Switch from "$lib/components/ui/Switch.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";

  type Props = {
    address: string; // device address (channel address = address:channelNo)
    channelNo: number;
    hidden?: boolean;
    locked?: boolean;
    // Notifies the parent so it can update its channel summary in place.
    onChange?: (flags: { hidden: boolean; locked: boolean }) => void;
  };

  let {
    address,
    channelNo,
    hidden = false,
    locked = false,
    onChange,
  }: Props = $props();

  // Initialise from the prop once; the component owns the state afterward.
  let curHidden = $state(untrack(() => hidden));
  let curLocked = $state(untrack(() => locked));
  let busy = $state(false);
  // Force-remount seam: after a failed toggle the desired state is unchanged,
  // so bumping the key pulls the Switch thumb back to the authoritative value.
  let nonce = $state(0);

  async function apply(next: { hidden?: boolean; locked?: boolean }) {
    if (busy) return;
    busy = true;
    try {
      const res = await api.setChannelFlags(address, channelNo, next);
      curHidden = res.hidden;
      curLocked = res.locked;
      onChange?.({ hidden: res.hidden, locked: res.locked });
      toastStore.success(t("channel.flags.saved_toast"));
    } catch (err) {
      toastStore.error(t("channel.flags.failed"), friendlyError(err, t));
    } finally {
      busy = false;
      nonce += 1;
    }
  }
</script>

<div
  class="flex flex-col gap-2 rounded border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] p-3"
>
  <label
    class="flex items-center justify-between gap-3 text-sm font-medium text-[var(--ha-primary-text-color)]"
  >
    <span>{t("channel.flags.hidden.title")}</span>
    {#key nonce}
      <Switch
        checked={curHidden}
        disabled={busy}
        onCheckedChange={(v) => apply({ hidden: v })}
      />
    {/key}
  </label>
  <p class="text-xs text-[var(--ha-secondary-text-color)]">
    {t("channel.flags.hidden.help")}
  </p>
  <label
    class="flex items-center justify-between gap-3 text-sm font-medium text-[var(--ha-primary-text-color)]"
  >
    <span>{t("channel.flags.locked.title")}</span>
    {#key nonce}
      <Switch
        checked={curLocked}
        disabled={busy}
        onCheckedChange={(v) => apply({ locked: v })}
      />
    {/key}
  </label>
  <p class="text-xs text-[var(--ha-secondary-text-color)]">
    {t("channel.flags.locked.help")}
  </p>
</div>
