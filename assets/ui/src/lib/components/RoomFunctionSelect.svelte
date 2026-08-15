<script lang="ts">
  // Reusable multi-select combobox for room / function assignment. Selected
  // entries render as removable chips; a search input filters the existing
  // catalogue in an INLINE dropdown (deliberately not a body portal — plain
  // <button> tap targets are reliable on iPad/iOS Safari, unlike a portaled
  // custom Select). When the typed text matches no existing entry a
  // "create" affordance appears so a new room / function can be added on the
  // spot. Fully controlled: it never mutates the CCU itself — the parent
  // persists via onChange (assignment) and onCreate (new catalogue entry).
  import { t } from "$lib/i18n";
  import { makeTextMatcher } from "$lib/utils";
  import { X, Plus, Check } from "@lucide/svelte";

  type Props = {
    selected: string[];
    options: string[];
    onChange: (next: string[]) => void;
    /** Create a new catalogue entry (parent runs createRoom/createFunction +
     * refreshes options). Omit to hide the create affordance. */
    onCreate?: (name: string) => Promise<void>;
    placeholder?: string;
    /** Label for the "+ create" row, e.g. (v) => `+ „${v}" anlegen`. */
    createLabel?: (value: string) => string;
    /** aria-label for a chip's remove button, e.g. (n) => `${n} entfernen`. */
    removeLabel?: (name: string) => string;
    disabled?: boolean;
    id?: string;
    ariaLabel?: string;
  };

  let {
    selected,
    options,
    onChange,
    onCreate,
    placeholder,
    createLabel,
    removeLabel,
    disabled = false,
    id,
    ariaLabel,
  }: Props = $props();

  let search = $state("");
  let open = $state(false);
  let creating = $state(false);
  let dropUp = $state(false);
  let inputEl = $state<HTMLInputElement | null>(null);
  let blurTimer: ReturnType<typeof setTimeout> | null = null;

  // Reserve ~240px for the menu; flip it above the input when the viewport
  // has more room there (e.g. the last field inside a scrolling modal).
  const MENU_SPACE = 240;

  const matcher = $derived(makeTextMatcher(search));
  const norm = (s: string) => s.trim().toLocaleLowerCase();

  // Existing entries not yet assigned, filtered by the search text.
  const filtered = $derived(
    options
      .filter((o) => !selected.includes(o))
      .filter((o) => (search.trim() ? matcher(o) : true))
      .slice(0, 50),
  );

  // Offer "create" only when the typed text is new (no catalogue entry and
  // not already assigned, case-insensitively) and a creator is wired.
  const canCreate = $derived(
    !!onCreate &&
      search.trim().length > 0 &&
      !options.some((o) => norm(o) === norm(search)) &&
      !selected.some((s) => norm(s) === norm(search)),
  );

  function openMenu() {
    if (blurTimer) clearTimeout(blurTimer);
    if (disabled) return;
    const rect = inputEl?.getBoundingClientRect();
    if (rect && typeof window !== "undefined") {
      dropUp =
        window.innerHeight - rect.bottom < MENU_SPACE && rect.top > MENU_SPACE;
    }
    open = true;
  }
  function scheduleClose() {
    // Delay so a click on an option/create button registers before blur closes.
    if (blurTimer) clearTimeout(blurTimer);
    blurTimer = setTimeout(() => (open = false), 120);
  }

  function add(name: string) {
    if (blurTimer) clearTimeout(blurTimer);
    if (!selected.includes(name)) onChange([...selected, name]);
    search = "";
    open = true;
    inputEl?.focus();
  }
  function remove(name: string) {
    onChange(selected.filter((s) => s !== name));
  }
  async function create() {
    const name = search.trim();
    if (!name || !onCreate || creating) return;
    if (blurTimer) clearTimeout(blurTimer);
    creating = true;
    try {
      await onCreate(name);
      if (!selected.includes(name)) onChange([...selected, name]);
      search = "";
      inputEl?.focus();
    } catch {
      // Contract: onCreate owns the error surface (it knows which write
      // failed and against which CCU) and reports it. Swallow here so the
      // rejection does not escape as an unhandled promise — both call sites
      // discard the promise — while the chip stays unselected and the typed
      // text stays in the input, so nothing claims a room that was never
      // created.
    } finally {
      creating = false;
    }
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === "Escape") {
      open = false;
      return;
    }
    if (e.key === "Enter") {
      e.preventDefault();
      // Prefer an exact existing match, then the first filtered entry,
      // then fall back to creating the typed value.
      const exact = filtered.find((o) => norm(o) === norm(search));
      if (exact) add(exact);
      else if (filtered.length > 0 && search.trim()) add(filtered[0]);
      else if (canCreate) void create();
    }
  }
</script>

<div class="relative">
  <!-- Selected chips -->
  {#if selected.length > 0}
    <div class="mb-1.5 flex flex-wrap gap-1.5">
      {#each selected as name (name)}
        <span
          class="inline-flex items-center gap-1 rounded-full bg-brand-50 py-0.5 pl-2.5 pr-1 text-xs font-medium text-brand-700 dark:bg-brand-950 dark:text-brand-300"
        >
          {name}
          <button
            type="button"
            {disabled}
            aria-label={removeLabel ? removeLabel(name) : t("roomfn.remove")}
            class="inline-flex h-5 w-5 items-center justify-center rounded-full hover:bg-brand-100 disabled:opacity-50 dark:hover:bg-brand-900"
            onclick={() => remove(name)}
          >
            <X class="h-3.5 w-3.5" />
          </button>
        </span>
      {/each}
    </div>
  {/if}

  <!-- Search / add input + inline dropdown (no portal → iPad-safe) -->
  <div class="relative">
    <input
      bind:this={inputEl}
      {id}
      type="text"
      role="combobox"
      aria-expanded={open}
      aria-controls={id ? `${id}-list` : undefined}
      aria-label={ariaLabel}
      autocomplete="off"
      autocapitalize="none"
      autocorrect="off"
      spellcheck="false"
      {disabled}
      {placeholder}
      bind:value={search}
      onfocus={openMenu}
      onclick={openMenu}
      oninput={openMenu}
      onblur={scheduleClose}
      onkeydown={onKeydown}
      class="w-full rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-2.5 py-2 text-sm text-[var(--ha-primary-text-color)] shadow-sm focus-visible:border-[var(--ha-primary-color)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ha-primary-color)] disabled:cursor-not-allowed disabled:opacity-50"
    />

    {#if open && (filtered.length > 0 || canCreate || search.trim())}
      <div
        id={id ? `${id}-list` : undefined}
        role="listbox"
        class="absolute z-20 max-h-56 w-full overflow-y-auto rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] py-1 shadow-lg {dropUp
          ? 'bottom-full mb-1'
          : 'top-full mt-1'}"
      >
      {#each filtered as opt (opt)}
        <button
          type="button"
          role="option"
          aria-selected="false"
          class="flex min-h-[40px] w-full items-center gap-2 px-3 py-1.5 text-left text-sm text-[var(--ha-primary-text-color)] hover:bg-[var(--ha-secondary-background-color)]"
          onmousedown={(e) => e.preventDefault()}
          onclick={() => add(opt)}
        >
          <Plus class="h-4 w-4 shrink-0 opacity-60" />
          <span class="truncate">{opt}</span>
        </button>
      {/each}

      {#if canCreate}
        <button
          type="button"
          class="flex min-h-[40px] w-full items-center gap-2 border-t border-[var(--ha-divider-color)] px-3 py-1.5 text-left text-sm font-medium text-brand-700 hover:bg-[var(--ha-secondary-background-color)] disabled:opacity-50 dark:text-brand-300"
          disabled={creating}
          onmousedown={(e) => e.preventDefault()}
          onclick={() => void create()}
        >
          {#if creating}
            <Check class="h-4 w-4 shrink-0 animate-pulse opacity-60" />
          {:else}
            <Plus class="h-4 w-4 shrink-0" />
          {/if}
          <span class="truncate">
            {createLabel ? createLabel(search.trim()) : t("roomfn.create", { name: search.trim() })}
          </span>
        </button>
      {:else if filtered.length === 0 && search.trim()}
        <p class="px-3 py-2 text-sm text-[var(--ha-secondary-text-color)]">
          {t("roomfn.no_matches")}
        </p>
      {/if}
      </div>
    {/if}
  </div>
</div>
