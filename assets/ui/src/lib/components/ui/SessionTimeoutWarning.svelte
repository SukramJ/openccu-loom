<script lang="ts">
  import { onMount } from "svelte";
  import { t } from "$lib/i18n";

  // Session-timeout watchdog. Mirrors homematicip-local-frontend's
  // 5-min config-panel auto-release: any unsaved edit older than
  // ~4 min triggers a soft warning so the user notices before the
  // browser tab idles out without saving. The watchdog is purely
  // client-side — multi-user locking is a separate Phase-11 feature
  // (CCU sessions, conflict resolution).
  //
  // The component is meant to be mounted from any editor that
  // tracks dirty state. It does NOT auto-save; the user keeps full
  // control. After acknowledging the warning the timer resets.

  type Props = {
    /** True when the editor has unsaved changes. */
    dirty: boolean;
    /** Total idle budget in milliseconds. Default 5 min. */
    timeoutMs?: number;
    /** When `timeoutMs - remaining < warnAtMs`, show the warning. */
    warnAtMs?: number;
  };

  let {
    dirty,
    timeoutMs = 5 * 60 * 1000,
    warnAtMs = 30 * 1000,
  }: Props = $props();

  let lastTouch = $state(Date.now());
  let now = $state(Date.now());
  let dismissed = $state(false);

  function touch() {
    lastTouch = Date.now();
    dismissed = false;
  }

  onMount(() => {
    // Tick once a second while dirty so the countdown stays accurate
    // without burning cycles on idle pages.
    const interval = setInterval(() => {
      now = Date.now();
    }, 1000);
    // Reset the idle timer on any user input — the user is still
    // around, no need to nag.
    const reset = () => touch();
    window.addEventListener("keydown", reset);
    window.addEventListener("mousedown", reset);
    window.addEventListener("touchstart", reset);
    return () => {
      clearInterval(interval);
      window.removeEventListener("keydown", reset);
      window.removeEventListener("mousedown", reset);
      window.removeEventListener("touchstart", reset);
    };
  });

  // Restart the timer whenever the dirty flag toggles on so a fresh
  // edit does not inherit a stale countdown.
  let prevDirty = $state(false);
  $effect(() => {
    if (dirty && !prevDirty) touch();
    prevDirty = dirty;
  });

  const remaining = $derived(Math.max(0, timeoutMs - (now - lastTouch)));
  const visible = $derived(dirty && !dismissed && remaining <= warnAtMs);

  function format(ms: number): string {
    const s = Math.ceil(ms / 1000);
    const m = Math.floor(s / 60);
    const sec = s % 60;
    return `${m}:${String(sec).padStart(2, "0")}`;
  }
</script>

{#if visible}
  <div
    class="pin-b-safe pin-r-safe fixed z-40 w-[calc(100%-2rem)] max-w-xs rounded-lg border border-amber-300 bg-amber-50 p-3 text-sm shadow-lg dark:border-amber-700 dark:bg-amber-950"
    role="status"
    aria-live="polite"
  >
    <p class="font-semibold text-amber-900 dark:text-amber-100">
      {t("session.unsaved")}
    </p>
    <p class="mt-1 text-xs text-amber-800 dark:text-amber-200">
      {t("session.idle", { time: format(remaining) })}
    </p>
    <div class="mt-2 flex justify-end gap-2">
      <button
        type="button"
        class="inline-flex min-h-[36px] items-center rounded border border-amber-400 px-3 text-xs text-amber-900 hover:bg-amber-100 dark:border-amber-600 dark:text-amber-100 dark:hover:bg-[color-mix(in_srgb,var(--color-amber-900)_40%,transparent)]"
        onclick={() => {
          dismissed = true;
          touch();
        }}
      >
        {t("session.dismiss")}
      </button>
    </div>
  </div>
{/if}
