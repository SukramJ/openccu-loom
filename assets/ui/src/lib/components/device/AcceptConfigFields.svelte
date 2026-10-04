<script lang="ts">
  import Input from "$lib/components/ui/Input.svelte";
  import RoomFunctionSelect from "$lib/components/RoomFunctionSelect.svelte";
  import { centralStore } from "$lib/stores/centrals.svelte";
  import { t } from "$lib/i18n";
  import type { AcceptDraft } from "./acceptConfig";
  import type { RoomFunctionCatalog } from "./roomFunctionCatalog.svelte";

  // The first-time configuration of a device being accepted: its name,
  // whether the channels follow the name, and its rooms and functions.
  // Shared by the new-devices view's accept dialog and the add-device
  // dialog, so the two ask for the same things in the same way.
  //
  // Each field follows the accepting central's features: a system (or a
  // token) that cannot rename devices or assign rooms does not get the
  // field at all — hidden, never shown-and-failing — and creating a room
  // or function on the spot needs the right to edit the catalogue.
  type Props = {
    draft: AcceptDraft;
    central: string;
    catalog: RoomFunctionCatalog;
    disabled?: boolean;
    ids: { name: string; rooms: string; functions: string };
  };

  let { draft = $bindable(), central, catalog, disabled = false, ids }: Props = $props();

  const scope = $derived(central || undefined);
  const canRename = $derived(centralStore.offers(scope, "device.rename"));
  const canAssign = $derived(centralStore.offers(scope, "taxonomy.assign"));
  const canCreate = $derived(centralStore.offers(scope, "taxonomy.edit"));

  $effect(() => {
    if (canAssign) void catalog.load();
  });
</script>

{#if canRename}
  <div class="mb-4">
    <label class="mb-1 block text-sm font-medium" for={ids.name}>
      {t("inbox.accept_dialog.name_label")}
    </label>
    <Input
      id={ids.name}
      bind:value={draft.name}
      placeholder={t("inbox.accept_dialog.name_placeholder")}
      {disabled}
    />
    <label
      class="mt-2 flex items-center gap-2 text-sm"
      class:opacity-50={draft.name.trim() === ""}
    >
      <input
        type="checkbox"
        bind:checked={draft.includeChannels}
        disabled={disabled || draft.name.trim() === ""}
        class="h-4 w-4 rounded border-[var(--ha-divider-color)] text-brand-600 focus:ring-brand-500"
      />
      {t("inbox.accept_dialog.include_channels")}
    </label>
  </div>
{/if}

{#if canAssign}
  <div class="mb-4">
    <span class="mb-1 block text-sm font-medium">{t("inbox.accept_dialog.rooms_label")}</span>
    <RoomFunctionSelect
      id={ids.rooms}
      ariaLabel={t("inbox.accept_dialog.rooms_label")}
      selected={draft.rooms}
      options={catalog.rooms}
      onChange={(next) => (draft.rooms = next)}
      onCreate={canCreate ? (name) => catalog.createRoom(name, central) : undefined}
      placeholder={t("roomfn.placeholder.room")}
      createLabel={(v) => t("roomfn.create.room", { name: v })}
      removeLabel={(n) => t("roomfn.remove_named", { name: n })}
      {disabled}
    />
  </div>

  <div class="mb-4">
    <span class="mb-1 block text-sm font-medium">{t("inbox.accept_dialog.functions_label")}</span>
    <RoomFunctionSelect
      id={ids.functions}
      ariaLabel={t("inbox.accept_dialog.functions_label")}
      selected={draft.functions}
      options={catalog.functions}
      onChange={(next) => (draft.functions = next)}
      onCreate={canCreate ? (name) => catalog.createFunction(name, central) : undefined}
      placeholder={t("roomfn.placeholder.function")}
      createLabel={(v) => t("roomfn.create.function", { name: v })}
      removeLabel={(n) => t("roomfn.remove_named", { name: n })}
      {disabled}
    />
  </div>
{/if}
