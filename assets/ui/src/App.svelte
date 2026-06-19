<script lang="ts">
  import { onMount } from "svelte";
  import { authStore } from "$lib/stores/auth.svelte";
  import {
    prefs,
    setLocale,
    applyTheme,
    bindSystemTheme,
  } from "$lib/stores/preferences.svelte";
  import DeviceList from "./routes/DeviceList.svelte";
  import DeviceDetail from "./routes/DeviceDetail.svelte";
  import Login from "./routes/Login.svelte";
  import BackupList from "./routes/BackupList.svelte";
  import SysvarList from "./routes/SysvarList.svelte";
  import ProgramList from "./routes/ProgramList.svelte";
  import MessageList from "./routes/MessageList.svelte";
  import Settings from "./routes/Settings.svelte";
  import Inbox from "./routes/Inbox.svelte";
  import FirmwareList from "./routes/FirmwareList.svelte";
  import UnIgnoreList from "./routes/UnIgnoreList.svelte";
  // Imported statically (not code-split): DeviceDetail's history tab
  // already pulls AuditLog into the main chunk, so a dynamic import here
  // would be ineffective (and warns at build time).
  import AuditLog from "./routes/AuditLog.svelte";
  // Matter is the heaviest route subtree and is opt-in (the bridge
  // defaults to off), so it is code-split via dynamic import — most
  // installs never load its JS. See the {#await} in the router below.
  const loadMatter = () => import("./routes/matter/Matter.svelte");
  // Diagnostics + Logs are infrequently visited and not part of the core
  // device-control surface, so they are code-split.
  const loadDiagnostics = () => import("./routes/Diagnostics.svelte");
  const loadLogs = () => import("./routes/Logs.svelte");
  import Toaster from "$lib/components/ui/Toaster.svelte";
  import ShortcutHelp from "$lib/components/ui/ShortcutHelp.svelte";
  import ConnectionBadge from "$lib/components/ui/ConnectionBadge.svelte";
  import ConfirmDialog from "$lib/components/ui/ConfirmDialog.svelte";
  import Sidebar from "$lib/components/ui/Sidebar.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import { dirty } from "$lib/stores/dirty.svelte";
  import { t } from "$lib/i18n";
  import RestartBanner from "$lib/components/RestartBanner.svelte";
  import { refreshRestartPending } from "$lib/stores/restartPending.svelte";

  // Minimal hash-based router. The Go handler serves the SPA under
  // /app/ and rewrites unknown paths to index.html, so client-side
  // routing is safe without full SSR.
  let path = $state<string>(
    location.hash.replace(/^#/, "") || "/devices",
  );

  function onHash() {
    path = location.hash.replace(/^#/, "") || "/devices";
    void refreshRestartPending();
  }
  window.addEventListener("hashchange", onHash);

  onMount(() => {
    authStore.probe();
    void refreshRestartPending();
    // Apply persisted/system theme on first paint and bind to OS-
    // preference changes for "system" mode.
    applyTheme();
    const unbindTheme = bindSystemTheme();
    // Browser-level guard against losing unsaved edits. Any editor
    // that touches `dirty.set(id, true)` will participate.
    const beforeUnload = (e: BeforeUnloadEvent) => {
      if (dirty.any()) {
        e.preventDefault();
        e.returnValue = "";
      }
    };
    window.addEventListener("beforeunload", beforeUnload);
    // Global "?" toggles the shortcut help modal — but only when the
    // user is not typing into an input/textarea/contenteditable.
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "?" || e.ctrlKey || e.metaKey || e.altKey) return;
      const t = e.target as HTMLElement | null;
      if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.isContentEditable)) {
        return;
      }
      e.preventDefault();
      helpOpen = !helpOpen;
    };
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("beforeunload", beforeUnload);
      window.removeEventListener("keydown", onKey);
      unbindTheme();
    };
  });

  async function onLogout() {
    await authStore.logout();
    location.hash = "";
  }

  const locale = $derived(prefs.locale);

  let helpOpen = $state(false);
  // Mobile off-canvas nav drawer (<md only). Opened by the header
  // burger, closed by the backdrop tap or a nav-item tap.
  let mobileNavOpen = $state(false);

  type Route =
    | { kind: "list" }
    | { kind: "detail"; address: string; channel?: number }
    | { kind: "backups" }
    | { kind: "sysvars" }
    | { kind: "programs" }
    | { kind: "messages" }
    | { kind: "audit" }
    | { kind: "diagnostics" }
    | { kind: "logs" }
    | { kind: "settings" }
    | { kind: "inbox" }
    | { kind: "firmware" }
    | { kind: "matter"; subpath: string }
    | { kind: "visibility" }
    | { kind: "unknown" };

  const route = $derived.by<Route>(() => {
    if (!path || path === "/" || path === "/devices") return { kind: "list" };
    if (path === "/backups") return { kind: "backups" };
    if (path === "/sysvars") return { kind: "sysvars" };
    if (path === "/programs") return { kind: "programs" };
    if (path === "/messages") return { kind: "messages" };
    if (path === "/audit") return { kind: "audit" };
    if (path === "/diagnostics") return { kind: "diagnostics" };
    if (path === "/logs") return { kind: "logs" };
    if (path === "/settings") return { kind: "settings" };
    if (path === "/inbox") return { kind: "inbox" };
    if (path === "/firmware") return { kind: "firmware" };
    if (path === "/visibility") return { kind: "visibility" };
    if (path === "/matter" || path.startsWith("/matter/")) {
      return { kind: "matter", subpath: path.slice("/matter".length) || "" };
    }
    const m = path.match(
      /^\/devices\/([^/]+)(?:\/channels\/(\d+))?\/?$/,
    );
    if (!m) return { kind: "unknown" };
    return {
      kind: "detail",
      address: decodeURIComponent(m[1]),
      channel: m[2] ? Number(m[2]) : undefined,
    };
  });

  // Reserve the sidebar's width only from md upward, where the bar is
  // permanently docked. On <md the bar is an overlay, so the content
  // pane is full-width (no left padding).
  const sidebarPad = $derived(prefs.navCollapsed ? "md:pl-[64px]" : "md:pl-[240px]");

  // Sidebar activeKind treats "firmware" and "matter" as their own leaf.
  const activeKindForSidebar = $derived(
    route.kind === "detail" ? "detail" : route.kind,
  );
</script>

<svelte:head>
  <title>{
    route.kind === "diagnostics" ? t("page.title.diagnostics") :
    route.kind === "logs" ? t("page.title.logs") :
    route.kind === "list" || route.kind === "detail" ? t("page.title.devices") :
    route.kind === "settings" ? t("page.title.settings") :
    t("page.title.default")
  }</title>
</svelte:head>

<a
  href="#main"
  class="sr-only focus:not-sr-only focus:fixed focus:left-4 focus:top-4 focus:z-50 focus:rounded-md focus:bg-white focus:px-4 focus:py-2 focus:text-sm focus:font-medium focus:shadow-lg focus:ring-2 focus:ring-brand-500 dark:focus:bg-slate-900 dark:focus:text-white"
>
  {t("app.skip_to_content")}
</a>

<Toaster />
<ConfirmDialog />
<ShortcutHelp open={helpOpen} onClose={() => (helpOpen = false)} />

{#if authStore.checking}
  <section
    class="flex min-h-screen items-center justify-center"
    style="color: var(--ha-secondary-text-color); background-color: var(--ha-card-background-color);"
  >
    {t("common.loading")}
  </section>
{:else if !authStore.authenticated}
  <Login />
{:else}
  <div
    class="min-h-screen"
    style="background-color: var(--ha-card-background-color); color: var(--ha-primary-text-color);"
  >
    <Sidebar
      activeKind={activeKindForSidebar}
      identitySubject={authStore.identity?.subject ?? null}
      {locale}
      onLocaleToggle={() => setLocale(locale === "de" ? "en" : "de")}
      onLogout={() => void onLogout()}
      onShortcutHelp={() => (helpOpen = true)}
      mobileOpen={mobileNavOpen}
      onMobileClose={() => (mobileNavOpen = false)}
    />

    {#if mobileNavOpen}
      <button
        type="button"
        class="fixed inset-0 z-20 bg-black/40 md:hidden"
        aria-label={t("app.close_menu")}
        onclick={() => (mobileNavOpen = false)}
      ></button>
    {/if}

    <div class="{sidebarPad} pt-safe pr-safe transition-[padding] duration-200">
      <header
        class="flex h-14 items-center gap-2 border-b px-4"
        style="border-color: var(--ha-divider-color);"
      >
        <button
          type="button"
          class="-ml-1 flex h-10 w-10 items-center justify-center rounded-md hover:bg-slate-100 md:hidden dark:hover:bg-slate-800"
          aria-label={t("app.menu")}
          onclick={() => (mobileNavOpen = true)}
        >
          <Icon name="mdi:menu" size={22} />
        </button>
        <div class="ml-auto">
          <ConnectionBadge />
        </div>
      </header>
      <RestartBanner />
      <main id="main">
        {#if route.kind === "list"}
          <DeviceList />
        {:else if route.kind === "detail"}
          <DeviceDetail
            address={route.address}
            channel={route.channel}
            {locale}
          />
        {:else if route.kind === "backups"}
          <BackupList />
        {:else if route.kind === "sysvars"}
          <SysvarList />
        {:else if route.kind === "programs"}
          <ProgramList />
        {:else if route.kind === "messages"}
          <MessageList />
        {:else if route.kind === "audit"}
          <AuditLog />
        {:else if route.kind === "diagnostics"}
          {#await loadDiagnostics()}
            <LoadingState />
          {:then { default: Diagnostics }}
            <Diagnostics />
          {/await}
        {:else if route.kind === "logs"}
          {#if authStore.identity?.role === "admin"}
            {#await loadLogs()}
              <LoadingState />
            {:then { default: Logs }}
              <Logs />
            {/await}
          {:else}
            <section class="mx-auto max-w-6xl px-6 py-8">
              <div class="rounded-lg border border-[var(--ha-divider-color)] bg-[var(--ha-secondary-background-color)] px-4 py-3 text-sm text-[var(--ha-secondary-text-color)]">
                {t("logs.forbidden")}
              </div>
            </section>
          {/if}
        {:else if route.kind === "settings"}
          <Settings />
        {:else if route.kind === "inbox"}
          <Inbox />
        {:else if route.kind === "firmware"}
          <FirmwareList />
        {:else if route.kind === "matter"}
          {#await loadMatter()}
            <LoadingState />
          {:then { default: Matter }}
            <Matter subpath={route.subpath} />
          {/await}
        {:else if route.kind === "visibility"}
          <UnIgnoreList />
        {:else}
          <section class="mx-auto max-w-6xl px-6 py-8">
            <h1 class="text-2xl font-semibold">{t("app.not_found")}</h1>
            <p class="mt-2" style="color: var(--ha-secondary-text-color);">
              {t("app.unknown_path")} <code>{path}</code>.
            </p>
          </section>
        {/if}
      </main>
    </div>
  </div>
{/if}
