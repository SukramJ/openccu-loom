<script lang="ts">
  // PIN pad (docs/alarm-concept.md §12.1). A large, touch-friendly digit
  // grid used by the Overview's disarm/arm flow whenever an area's code
  // policy requires a code — and, by the same safety rule, NEVER for
  // silence (S3: silencing a screaming siren is one tap, never gated).
  //
  // Rendered as a self-contained overlay dialog (own backdrop + card on
  // --ha-* tokens) so it reads correctly on top of any surface — in
  // particular the high-contrast red triggered surface — in all four
  // skin×scheme combos. The entered code is masked; only its length is
  // ever shown. Digits arrive from the on-screen grid or the physical
  // keyboard; Enter submits, Escape cancels, Backspace deletes.

  import { t } from "$lib/i18n";
  import Icon from "$lib/components/ui/Icon.svelte";

  type Props = {
    title?: string;
    submitLabel?: string;
    busy?: boolean;
    error?: string | null;
    /** Minimum digit count before submit is enabled (default 1). */
    minLength?: number;
    onSubmit: (code: string) => void;
    onCancel: () => void;
  };

  let {
    title = t("alarm.pinpad.title"),
    submitLabel = t("alarm.action.disarm"),
    busy = false,
    error = null,
    minLength = 1,
    onSubmit,
    onCancel,
  }: Props = $props();

  let code = $state("");

  const canSubmit = $derived(code.length >= minLength && !busy);

  const DIGITS = ["1", "2", "3", "4", "5", "6", "7", "8", "9"] as const;

  function press(d: string) {
    if (busy) return;
    // Cap length defensively so a wall-tablet key-repeat can't grow the
    // string without bound; PINs are short.
    if (code.length >= 32) return;
    code += d;
  }

  function backspace() {
    if (busy) return;
    code = code.slice(0, -1);
  }

  function clear() {
    if (busy) return;
    code = "";
  }

  function submit() {
    if (!canSubmit) return;
    onSubmit(code);
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key >= "0" && e.key <= "9") {
      e.preventDefault();
      press(e.key);
    } else if (e.key === "Backspace") {
      e.preventDefault();
      backspace();
    } else if (e.key === "Enter") {
      e.preventDefault();
      submit();
    } else if (e.key === "Escape") {
      e.preventDefault();
      onCancel();
    }
  }
</script>

<svelte:window onkeydown={onKeydown} />

<!-- Backdrop -->
<div
  class="fixed inset-0 z-50 bg-black/50"
  role="presentation"
  onclick={() => !busy && onCancel()}
></div>

<!-- Centered pad -->
<div class="pointer-events-none fixed inset-0 z-50 flex items-center justify-center p-4">
  <div
    class="pointer-events-auto flex w-full max-w-xs flex-col gap-4 rounded-lg border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] p-5 shadow-xl"
    role="dialog"
    aria-modal="true"
    aria-label={title}
  >
    <div class="flex items-center gap-2">
      <Icon name="mdi:lock" size={20} class="text-[var(--ha-primary-color)]" aria-label="" />
      <h2 class="flex-1 text-base font-semibold text-[var(--ha-primary-text-color)]">{title}</h2>
      <button
        type="button"
        class="text-[var(--ha-secondary-text-color)] hover:text-[var(--ha-primary-text-color)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ha-primary-color)]"
        aria-label={t("common.close")}
        disabled={busy}
        onclick={() => onCancel()}
      >
        <Icon name="mdi:close" size={20} aria-label="" />
      </button>
    </div>

    <!-- Masked display: one dot per entered digit, length only. -->
    <div
      class="flex h-12 items-center justify-center rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-secondary-background-color)] px-3 text-2xl tracking-[0.4em] text-[var(--ha-primary-text-color)]"
      aria-label={t("alarm.pinpad.entered", { count: code.length })}
      aria-live="polite"
    >
      {#if code.length === 0}
        <span class="text-sm tracking-normal text-[var(--ha-secondary-text-color)]">
          {t("alarm.pinpad.placeholder")}
        </span>
      {:else}
        <span aria-hidden="true">{"•".repeat(code.length)}</span>
      {/if}
    </div>

    {#if error}
      <p class="text-center text-sm font-medium text-[var(--ha-error-color)]" role="alert">
        {error}
      </p>
    {/if}

    <!-- Digit grid: 1-9, then clear / 0 / backspace. -->
    <div class="grid grid-cols-3 gap-2">
      {#each DIGITS as d (d)}
        <button
          type="button"
          class="flex h-14 items-center justify-center rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] text-xl font-semibold text-[var(--ha-primary-text-color)] transition hover:bg-[var(--ha-secondary-background-color)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ha-primary-color)] disabled:opacity-50"
          aria-label={t("alarm.pinpad.digit", { digit: d })}
          disabled={busy}
          onclick={() => press(d)}
        >
          {d}
        </button>
      {/each}

      <button
        type="button"
        class="flex h-14 items-center justify-center rounded-md border border-[var(--ha-divider-color)] text-sm font-medium text-[var(--ha-secondary-text-color)] transition hover:bg-[var(--ha-secondary-background-color)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ha-primary-color)] disabled:opacity-50"
        aria-label={t("alarm.pinpad.clear")}
        disabled={busy || code.length === 0}
        onclick={clear}
      >
        {t("alarm.pinpad.clear")}
      </button>

      <button
        type="button"
        class="flex h-14 items-center justify-center rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] text-xl font-semibold text-[var(--ha-primary-text-color)] transition hover:bg-[var(--ha-secondary-background-color)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ha-primary-color)] disabled:opacity-50"
        aria-label={t("alarm.pinpad.digit", { digit: "0" })}
        disabled={busy}
        onclick={() => press("0")}
      >
        0
      </button>

      <button
        type="button"
        class="flex h-14 items-center justify-center rounded-md border border-[var(--ha-divider-color)] text-xl text-[var(--ha-secondary-text-color)] transition hover:bg-[var(--ha-secondary-background-color)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ha-primary-color)] disabled:opacity-50"
        aria-label={t("alarm.pinpad.backspace")}
        disabled={busy || code.length === 0}
        onclick={backspace}
      >
        <span aria-hidden="true">&#9003;</span>
      </button>
    </div>

    <div class="flex gap-2">
      <button
        type="button"
        class="h-11 flex-1 rounded-md border border-[var(--ha-divider-color)] text-sm font-medium text-[var(--ha-primary-text-color)] transition hover:bg-[var(--ha-secondary-background-color)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ha-primary-color)] disabled:opacity-50"
        disabled={busy}
        onclick={() => onCancel()}
      >
        {t("common.cancel")}
      </button>
      <button
        type="button"
        class="h-11 flex-1 rounded-md bg-[var(--ha-primary-color)] text-sm font-semibold text-white transition hover:brightness-95 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ha-primary-color)] disabled:opacity-50"
        disabled={!canSubmit}
        onclick={submit}
      >
        {submitLabel}
      </button>
    </div>
  </div>
</div>
