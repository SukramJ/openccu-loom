<script lang="ts">
  import type { PickerSortField } from "$lib/alarm/sensorCandidates";
  import { areasStore } from "$lib/stores/areas.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import { t } from "$lib/i18n";

  /**
   * PickerFilters — the search + room + function + area + sort row above a
   * candidate list.
   *
   * The alarm wizard renders it twice, once for the sensor step and once for
   * the outputs step, and the two copies had to be kept in step by hand: same
   * five controls, same labels, same "all" entry, differing only in which
   * state they drive. The one difference that survived is the sensor step's
   * "show all" toggle, which is why it is opt-in rather than always drawn.
   *
   * The area select reads `areasStore` directly, exactly as both call sites
   * did: it is a fleet-wide store, and a select that vanishes when no areas
   * are defined is the same decision in both places.
   */
  interface Props {
    search: string;
    room: string;
    func: string;
    area: string;
    sort: PickerSortField;
    /** Renders the "show all candidates" toggle and binds it. */
    withShowAll?: boolean;
    showAll?: boolean;
    roomOptions: string[];
    funcOptions: string[];
    searchPlaceholder?: string;
  }

  let {
    search = $bindable(""),
    room = $bindable(""),
    func = $bindable(""),
    area = $bindable(""),
    sort = $bindable<PickerSortField>("name"),
    withShowAll = false,
    showAll = $bindable(false),
    roomOptions,
    funcOptions,
    searchPlaceholder = "",
  }: Props = $props();

  const ALL = $derived({ value: "", label: t("alarm.sensors.filter.all") });
</script>

<div class="mb-2 flex flex-wrap items-center gap-2">
  <div class="min-w-40 flex-1">
    <Input
      type="search"
      placeholder={searchPlaceholder || t("common.search")}
      bind:value={search}
    />
  </div>
  <Select
    class="w-auto"
    bind:value={room}
    ariaLabel={t("alarm.sensors.filter.room")}
    options={[ALL, ...roomOptions.map((r) => ({ value: r, label: r }))]}
  />
  <Select
    class="w-auto"
    bind:value={func}
    ariaLabel={t("alarm.sensors.filter.function")}
    options={[ALL, ...funcOptions.map((f) => ({ value: f, label: f }))]}
  />
  {#if areasStore.areas.length > 0}
    <Select
      class="w-auto"
      bind:value={area}
      ariaLabel={t("alarm.sensors.filter.area")}
      options={[ALL, ...areasStore.areas.map((a) => ({ value: a.id, label: a.name }))]}
    />
  {/if}
  <Select
    class="w-auto"
    value={sort}
    ariaLabel={t("common.sort")}
    onValueChange={(v) => (sort = v as PickerSortField)}
    options={[
      { value: "name", label: t("alarm.wizard.sort.name") },
      { value: "room", label: t("alarm.wizard.sort.room") },
      { value: "model", label: t("alarm.wizard.sort.model") },
    ]}
  />
  {#if withShowAll}
    <label class="flex items-center gap-1.5 text-xs text-[var(--ha-secondary-text-color)]">
      <input type="checkbox" bind:checked={showAll} />
      {t("alarm.sensors.add.show_all")}
    </label>
  {/if}
</div>
