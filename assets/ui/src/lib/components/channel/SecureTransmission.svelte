<script lang="ts">
  import { api, friendlyError } from "$lib/api/client";
  import Switch from "$lib/components/ui/Switch.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { t } from "$lib/i18n";

  type Props = {
    channelAddress: string;
    // Edit-lock token held by the parent MASTER panel. AES_ACTIVE is a
    // MASTER paramset write, gated behind the per-resource edit lock;
    // presenting the parent's token keeps the write inside the same
    // session. Omitting it on a gated mount yields 423 on the write.
    editToken?: string;
    // Parent-level guard: the switch is disabled while another operator
    // holds the lock or the parent's own lock was lost mid-life.
    disabled?: boolean;
  };

  let { channelAddress, editToken, disabled = false }: Props = $props();

  // present stays false until the raw MASTER paramset is probed and
  // actually carries AES_ACTIVE — the row renders nothing otherwise.
  // The read deliberately bypasses the UISchema/visibility path:
  // AES_ACTIVE carries the `internal` ui-flag and never reaches the
  // schema builder, so it has to be read straight from the paramset.
  let present = $state(false);
  let active = $state(false);
  let busy = $state(false);
  // Force-remount seam: after a cancelled or failed toggle the desired
  // state is unchanged, so re-rendering `checked={active}` alone would
  // not necessarily pull the Switch's thumb back to the truth. Bumping
  // the key remounts the Switch against the authoritative `active`.
  let switchNonce = $state(0);

  function truthy(v: unknown): boolean {
    return v === true || v === 1 || v === "1" || v === "true";
  }

  $effect(() => {
    const addr = channelAddress;
    let cancelled = false;
    (async () => {
      try {
        const ps = await api.getParamset(addr, "MASTER");
        if (cancelled) return;
        if (ps && Object.prototype.hasOwnProperty.call(ps, "AES_ACTIVE")) {
          present = true;
          active = truthy(ps.AES_ACTIVE);
        } else {
          present = false;
        }
      } catch {
        // A raw-paramset read failure just hides the row; the main panel
        // surfaces its own load error. AES state stays untouched.
        if (!cancelled) present = false;
      }
    })();
    return () => {
      cancelled = true;
    };
  });

  async function onToggle(next: boolean) {
    if (busy) return;
    // Enabling secured transmission adds an acknowledgement round-trip
    // to every command and raises the channel's radio load; confirm
    // before turning it on. Disabling lowers the load and needs none.
    if (next) {
      const ok = await confirmStore.ask({
        title: t("channel.secure_transmission.confirm_title"),
        body: t("channel.secure_transmission.confirm_body"),
        confirmLabel: t("channel.secure_transmission.enable"),
      });
      if (!ok) {
        switchNonce += 1;
        return;
      }
    }
    busy = true;
    try {
      await api.putParamset(
        channelAddress,
        "MASTER",
        { AES_ACTIVE: next },
        editToken,
      );
      active = next;
      toastStore.success(
        next
          ? t("channel.secure_transmission.enabled_toast")
          : t("channel.secure_transmission.disabled_toast"),
      );
    } catch (err) {
      toastStore.error(
        t("channel.secure_transmission.failed"),
        friendlyError(err, t),
      );
    } finally {
      busy = false;
      switchNonce += 1;
    }
  }
</script>

{#if present}
  <div
    class="mb-4 flex flex-col gap-1 rounded border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] p-3"
  >
    <label
      class="flex items-center justify-between gap-3 text-sm font-medium text-[var(--ha-primary-text-color)]"
    >
      <span>{t("channel.secure_transmission.title")}</span>
      {#key switchNonce}
        <Switch
          checked={active}
          disabled={busy || disabled}
          onCheckedChange={onToggle}
        />
      {/key}
    </label>
    <p class="text-xs text-[var(--ha-secondary-text-color)]">
      {t("channel.secure_transmission.help")}
    </p>
  </div>
{/if}
