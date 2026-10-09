<script lang="ts">
  import Select from "$lib/components/ui/Select.svelte";
  import {
    EXPERT_PROFILE_ID,
    type ProfileVariant,
  } from "$lib/links/link-profiles";
  import { t } from "$lib/i18n";

  // The link-profile picker of the "Profileinstellung" page. Choosing an
  // entry takes effect at once, the way the CCU WebUI's profile dropdown
  // swaps its easymode form: the caller stages the profile's values as an
  // unsaved edit, and nothing reaches the device before the page's
  // "Übernehmen". The picker itself holds no selection state — the panel
  // owns it, so undo, reset and a reload all move it.
  type Props = {
    variants: ProfileVariant[];
    /** Id of the variant shown as selected. */
    selectedId: number;
    /** Id the daemon matched against the stored values (0 = none). */
    detectedId: number;
    onSelect: (variant: ProfileVariant) => void;
  };

  let { variants, selectedId, detectedId, onSelect }: Props = $props();

  const current = $derived(variants.find((v) => v.id === selectedId) ?? null);
  const expert = $derived(selectedId === EXPERT_PROFILE_ID);

  function choose(key: string) {
    const v = variants.find((x) => x.key === key);
    if (v && v.id !== selectedId) onSelect(v);
  }
</script>

{#if variants.length > 0}
  <div class="space-y-2">
    <p class="text-xs font-medium text-[var(--ha-secondary-text-color)]">
      {t("profile.header")}
    </p>
    <Select
      ariaLabel={t("profile.header")}
      options={variants.map((v) => ({ value: v.key, label: v.label }))}
      value={current?.key ?? ""}
      placeholder={t("profile.placeholder")}
      onValueChange={choose}
    />
    {#if !expert && detectedId !== EXPERT_PROFILE_ID && selectedId === detectedId}
      <p class="text-xs text-[var(--ha-success-color)]">✓ {t("profile.detected")}</p>
    {/if}
    {#if expert}
      <p class="text-xs text-[var(--ha-secondary-text-color)]">{t("profile.expert_hint")}</p>
    {:else if current?.description}
      <p
        class="rounded-md bg-[var(--ha-secondary-background-color)] px-3 py-2 text-sm text-[var(--ha-secondary-text-color)]"
      >
        {current.description}
      </p>
    {/if}
  </div>
{/if}
