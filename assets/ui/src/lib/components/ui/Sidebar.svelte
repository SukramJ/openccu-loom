<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import Icon from "./Icon.svelte";
  import BrandMark from "./BrandMark.svelte";
  import type { IconName } from "$lib/icons";
  import { navClusters, type NavItem, type RouteKind } from "$lib/nav";
  import { t } from "$lib/i18n";
  import { installModeStore } from "$lib/stores/installMode.svelte";
  import { messagesStore } from "$lib/stores/messages.svelte";
  import { matterStore } from "$lib/stores/matter.svelte";
  import { infoStore } from "$lib/stores/info.svelte";
  import { authStore } from "$lib/stores/auth.svelte";
  import {
    prefs,
    setNavCollapsed,
    setTheme,
    type Theme,
  } from "$lib/stores/preferences.svelte";

  // Keep a single cluster-level subscription on the install-mode poll
  // so the inbox dot stays live even while the user is on a different
  // route. The Inbox page also subscribes; the store is reference-
  // counted so polling stops once nobody is listening.
  onMount(() => {
    installModeStore.ensurePoll();
    // Pending-message counters for the badge on the Messages entry. Held
    // for the session so the count stays live on every route, not just
    // the message list.
    messagesStore.ensureStream();
    // Probe Matter status once so the sidebar knows whether to show
    // the Bridges cluster. No polling — the store will refresh on
    // navigation to the Matter route.
    void matterStore.loadStatus();
    // Daemon version for the footer line; cached for the session.
    void infoStore.ensure();
  });
  onDestroy(() => {
    installModeStore.release();
    messagesStore.release();
  });

  // HA-style left sidebar with 4 navigation clusters. Mirrors the
  // information-architecture proposal: Übersicht / Inbox /
  // Automatisierung / Status & Diagnose / System. Mobile-first via
  // burger toggle that overlays the main content; on ≥md screens it
  // sits permanently and the main pane shifts to its right.


  type Props = {
    activeKind: RouteKind;
    identitySubject: string | null;
    locale: "de" | "en";
    onLocaleToggle: () => void;
    onLogout: () => void;
    onShortcutHelp: () => void;
    // Mobile off-canvas drawer state, owned by App.svelte. On <md the
    // sidebar is hidden off-screen and slides in as an overlay when
    // mobileOpen is true; onMobileClose closes it (backdrop tap / nav).
    mobileOpen: boolean;
    onMobileClose: () => void;
  };

  let {
    activeKind,
    identitySubject,
    locale,
    onLocaleToggle,
    onLogout,
    onShortcutHelp,
    mobileOpen,
    onMobileClose,
  }: Props = $props();

  // Persist + reactively read the collapsed state. Burger toggles
  // it; the main App.svelte adapts its left padding accordingly.
  const collapsed = $derived(prefs.navCollapsed);

  // Whether the nav renders in its expanded (labelled) form. Always
  // expanded while the mobile drawer is open — the icon-only collapsed
  // form is a desktop space-saver and must not blank the labels in the
  // mobile overlay (which is always full-width). On <md the drawer is
  // either off-screen (content irrelevant) or open (expanded); on >=md
  // it follows the persisted collapse preference.
  const expanded = $derived(mobileOpen || !collapsed);

  function toggle() {
    setNavCollapsed(!collapsed);
  }

  const matterEnabled = $derived(matterStore.status?.enabled === true);
  // The Diagrams view charts measurement history — only surface it when
  // the opt-in history-recording feature is enabled (SV03).
  const historyEnabled = $derived(
    infoStore.info?.capabilities?.includes("history.v1") ?? false,
  );

  const clusters = $derived(
    navClusters({
      matterEnabled,
      historyEnabled,
      isAdmin: authStore.identity?.role === "admin",
    }),
  );

  function isActive(item: NavItem): boolean {
    return item.matches.includes(activeKind);
  }

  // Theme toggle — cycles light → dark → system. Renders a single
  // icon button so the sidebar stays compact when collapsed.
  function nextTheme(): Theme {
    if (prefs.theme === "light") return "dark";
    if (prefs.theme === "dark") return "system";
    return "light";
  }

  function cycleTheme() {
    setTheme(nextTheme());
  }

  const themeIcon: IconName = $derived(
    prefs.theme === "dark"
      ? "mdi:moon"
      : prefs.theme === "light"
        ? "mdi:sun"
        : "mdi:globe",
  );
</script>

<aside
  class="pt-safe pb-safe pl-safe fixed inset-y-0 left-0 z-30 flex w-60 flex-col border-r transition-transform duration-200 md:translate-x-0 {collapsed
    ? 'md:w-16'
    : 'md:w-60'} {mobileOpen ? 'translate-x-0' : '-translate-x-full'}"
  style="background-color: var(--ha-secondary-background-color); border-color: var(--ha-divider-color);"
  aria-label={t("app.menu")}
>
  <div
    class="flex h-14 items-center gap-2 border-b px-3"
    style="border-color: var(--ha-divider-color);"
  >
    <button
      type="button"
      class="rounded-md p-1.5 hover:bg-black/5 dark:hover:bg-white/5"
      aria-label={collapsed ? t("app.menu") : t("app.close_menu")}
      title={collapsed ? t("app.menu") : t("app.close_menu")}
      onclick={toggle}
    >
      <Icon name="mdi:menu" size={18} />
    </button>
    {#if expanded}
      <BrandMark mode="wordmark" height={26} href="#/devices" />
    {/if}
  </div>

  <nav class="flex-1 overflow-y-auto py-2">
    {#each clusters as cluster (cluster.label)}
      {#if expanded}
        <h3
          class="px-3 pb-1 pt-3 text-[10px] font-semibold uppercase tracking-wide"
          style="color: var(--ha-disabled-text-color);"
        >
          {cluster.label}
        </h3>
      {/if}
      <ul class="mb-2">
        {#each cluster.items as item (item.href)}
          {@const active = isActive(item)}
          {@const showInstallDot = item.matches.includes("inbox") && installModeStore.active}
          {@const pendingMessages = item.matches.includes("messages") ? messagesStore.total : 0}
          <li>
            <a
              href={item.href}
              onclick={onMobileClose}
              class="relative flex items-center gap-3 px-3 py-2 text-sm font-medium transition"
              style="color: {active ? 'var(--ha-primary-color)' : 'var(--ha-primary-text-color)'}; background-color: {active ? 'rgb(0 0 0 / 0.04)' : 'transparent'};"
              aria-current={active ? "page" : undefined}
              title={!expanded
                ? showInstallDot
                  ? `${item.label} · ${t("sidebar.install_mode_active")}`
                  : pendingMessages > 0
                    ? `${item.label} · ${t("sidebar.pending_messages", { count: String(pendingMessages) })}`
                    : item.label
                : undefined}
            >
              <span class="relative inline-flex">
                <Icon name={item.icon} size={18} />
                {#if showInstallDot}
                  <span
                    class="absolute -right-1 -top-1 inline-block h-2 w-2 animate-pulse rounded-full"
                    style="background-color: var(--ha-primary-color);"
                    aria-hidden="true"
                  ></span>
                {:else if pendingMessages > 0 && !expanded}
                  <span
                    class="absolute -right-1 -top-1 inline-block h-2 w-2 rounded-full"
                    style="background-color: var(--ha-error-color, #db4437);"
                    aria-hidden="true"
                  ></span>
                {/if}
              </span>
              {#if expanded}
                <span class="flex-1">{item.label}</span>
                {#if showInstallDot}
                  <span class="text-[10px] font-semibold uppercase tracking-wide" style="color: var(--ha-primary-color);">
                    {installModeStore.remainingSeconds ?? "…"}s
                  </span>
                {:else if pendingMessages > 0}
                  <span
                    class="inline-flex min-w-5 items-center justify-center rounded-full px-1.5 py-0.5 text-[10px] font-semibold leading-none text-white"
                    style="background-color: var(--ha-error-color, #db4437);"
                    aria-label={t("sidebar.pending_messages", { count: String(pendingMessages) })}
                  >
                    {pendingMessages > 99 ? "99+" : pendingMessages}
                  </span>
                {/if}
              {/if}
            </a>
          </li>
        {/each}
      </ul>
    {/each}
  </nav>

  <div
    class="border-t p-2"
    style="border-color: var(--ha-divider-color);"
  >
    {#if expanded && identitySubject}
      <p class="mb-2 truncate px-1 text-xs" style="color: var(--ha-secondary-text-color);">
        {identitySubject}
      </p>
    {/if}
    <div class="flex flex-wrap items-center gap-1">
      <button
        type="button"
        class="inline-flex items-center justify-center rounded-md p-2.5 hover:bg-black/5 dark:hover:bg-white/5"
        title={t("app.switch_language")}
        aria-label={t("app.switch_language")}
        onclick={onLocaleToggle}
      >
        <span class="text-xs font-semibold">{locale === "de" ? "EN" : "DE"}</span>
      </button>
      <button
        type="button"
        class="inline-flex items-center justify-center rounded-md p-2.5 hover:bg-black/5 dark:hover:bg-white/5"
        title={t("app.theme.toggle")}
        aria-label={t("app.theme.toggle")}
        onclick={cycleTheme}
      >
        <Icon name={themeIcon} size={16} />
      </button>
      <button
        type="button"
        class="inline-flex items-center justify-center rounded-md p-2.5 hover:bg-black/5 pointer-coarse:hidden dark:hover:bg-white/5"
        title={t("shortcut.title")}
        aria-label={t("shortcut.title")}
        aria-keyshortcuts="?"
        onclick={onShortcutHelp}
      >
        <span class="text-xs font-semibold">?</span>
      </button>
      <button
        type="button"
        class="inline-flex items-center justify-center rounded-md p-2.5 hover:bg-black/5 dark:hover:bg-white/5"
        title={t("nav.logout")}
        aria-label={t("nav.logout")}
        onclick={onLogout}
      >
        <Icon name="mdi:logout" size={16} />
      </button>
    </div>
    {#if expanded && infoStore.info}
      <a
        href="#/about"
        onclick={onMobileClose}
        class="mt-1 block px-1 text-xs hover:underline"
        style="color: var(--ha-secondary-text-color);"
        title={t("nav.about")}
      >
        v{infoStore.info.version}
      </a>
    {/if}
  </div>
</aside>
