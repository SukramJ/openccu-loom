<script lang="ts">
  import { onMount } from "svelte";
  import { authStore } from "$lib/stores/auth.svelte";
  import { setupStore } from "$lib/stores/setup.svelte";
  import {
    prefs,
    setLocale,
    applyTheme,
    bindSystemTheme,
  } from "$lib/stores/preferences.svelte";
  import { isEmbedded, startHaBridge } from "$lib/theme/ha-bridge";
  import DeviceList from "./routes/DeviceList.svelte";
  import Overview from "./routes/Overview.svelte";
  import DeviceDetail from "./routes/DeviceDetail.svelte";
  import Favorites from "./routes/Favorites.svelte";
  import Login from "./routes/Login.svelte";
  import Setup from "./routes/Setup.svelte";
  import BackupList from "./routes/BackupList.svelte";
  import SysvarList from "./routes/SysvarList.svelte";
  import ProgramList from "./routes/ProgramList.svelte";
  import GroupList from "./routes/GroupList.svelte";
  import LinkList from "./routes/LinkList.svelte";
  import ScheduleList from "./routes/ScheduleList.svelte";
  import Diagrams from "./routes/Diagrams.svelte";
  import MessageList from "./routes/MessageList.svelte";
  import Settings from "./routes/Settings.svelte";
  import Inbox from "./routes/Inbox.svelte";
  import FirmwareList from "./routes/FirmwareList.svelte";
  import SignalQualityList from "./routes/SignalQualityList.svelte";
  import Energy from "./routes/Energy.svelte";
  import Fleet from "./routes/Fleet.svelte";
  // Imported statically (not code-split): DeviceDetail's history tab
  // already pulls AuditLog into the main chunk, so a dynamic import here
  // would be ineffective (and warns at build time).
  import AuditLog from "./routes/AuditLog.svelte";
  import About from "./routes/About.svelte";
  // Matter is the heaviest route subtree and is opt-in (the bridge
  // defaults to off), so it is code-split via dynamic import — most
  // installs never load its JS. See the {#await} in the router below.
  const loadMatter = () => import("./routes/matter/Matter.svelte");
  // The alarm section is a self-contained subtree (panel + picker +
  // journal + walk test + wizard); code-split so installs that never
  // open it don't pay for its JS.
  const loadAlarm = () => import("./routes/alarm/Alarm.svelte");
  // Security & Safety runs independently of the alarm engine and is
  // rarely opened relative to the device-control surface, so it is
  // code-split the same way.
  const loadSecurity = () => import("./routes/security/Security.svelte");
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
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { t } from "$lib/i18n";
  import RestartBanner from "$lib/components/RestartBanner.svelte";
  import { refreshRestartPending } from "$lib/stores/restartPending.svelte";
  import { startRouteStore } from "$lib/stores/startRoute.svelte";
  import { surfacesStore } from "$lib/stores/surfaces.svelte";
  import { foldedRouteTarget, navSurfaceID } from "$lib/nav";

  // Minimal hash-based router. The Go handler serves the SPA under
  // /app/ and rewrites unknown paths to index.html, so client-side
  // routing is safe without full SSR.

  /**
   * The path the router should render for a hash. A view that was folded
   * into another one resolves to its successor here, so an old bookmark
   * reaches the surface it names instead of the not-found page.
   */
  function pathFromHash(hash: string): string {
    const bare = hash.replace(/^#/, "") || "/devices";
    return foldedRouteTarget(bare) ?? bare;
  }

  let path = $state<string>(pathFromHash(location.hash));

  // The raw hash we last committed to. Kept so a cancelled navigation
  // can roll the URL back — hashchange fires only AFTER location.hash
  // has already moved, so we cannot read the previous value from the
  // event itself.
  let committedHash = location.hash;
  // Set while we programmatically restore the hash after a cancelled
  // navigation, so the resulting hashchange echo is swallowed instead
  // of re-prompting.
  let revertingHash = false;

  async function onHash() {
    const nextHash = location.hash;
    const next = pathFromHash(nextHash);
    if (revertingHash) {
      revertingHash = false;
      committedHash = nextHash;
      return;
    }
    // A folded route: put its successor in the address bar so the URL
    // names the view being shown. The rewrite fires a second hashchange
    // that lands the real route.
    const folded = foldedRouteTarget(nextHash);
    if (folded) {
      location.hash = folded;
      return;
    }
    if (next === path) {
      committedHash = nextHash;
      return;
    }
    // In-app navigation would swap the rendered view. If an editor has
    // unsaved edits, confirm before discarding them; on cancel, restore
    // the URL to the view we were on.
    if (dirty.any()) {
      const ok = await confirmStore.ask({
        title: t("nav.leave_title"),
        body: t("nav.leave_body"),
        confirmLabel: t("nav.leave_confirm"),
        destructive: true,
      });
      if (!ok) {
        revertingHash = true;
        location.hash = committedHash;
        return;
      }
    }
    committedHash = nextHash;
    path = next;
    redirectIfHidden();
    void refreshRestartPending();
  }
  window.addEventListener("hashchange", () => void onHash());

  // The configured start route (O03) is applied exactly once, and only
  // when the user arrived without a deep link: an explicit #/... in the
  // URL - a bookmark, a shared link, a reload of the view being worked on
  // - always wins over the preference.
  let startRouteApplied = false;

  /**
   * Send the operator somewhere they can actually reach when the current
   * route names a view their surface profile hides.
   *
   * This closes deep links *within* the SPA — a bookmark, a shared link,
   * a stored start route. It is a UX guarantee, not a security boundary:
   * the boundary is the API (see notes/concepts/ui-surface-profiles.md §2.8),
   * because a hash fragment never reaches the server in the first place.
   */
  function redirectIfHidden() {
    const bare = location.hash.replace(/^#/, "") || "/devices";
    const view = bare.split(/[/?]/).filter(Boolean)[0];
    if (!view) return;
    // Device detail lives under /devices/<address>; its tabs are gated
    // inside the view, not by the router.
    const id = navSurfaceID(`#/${view}`);
    if (surfacesStore.visible(id)) return;
    location.hash = "#/devices";
  }

  function applyStartRoute() {
    if (startRouteApplied) return;
    startRouteApplied = true;
    const hash = location.hash.replace(/^#/, "");
    if (hash && hash !== "/") return;
    const target = startRouteStore.resolve();
    if (target) location.hash = target;
  }

  onMount(() => {
    // The initial hash named a folded route: `path` was already resolved
    // to its successor above, so this only corrects the address bar.
    const foldedInitial = foldedRouteTarget(location.hash);
    if (foldedInitial) location.hash = foldedInitial;
    authStore.probe();
    // Seeded before the route is applied; a failed load resolves to the
    // default rather than delaying the first paint.
    void startRouteStore.load().then(applyStartRoute);
    // The surface profile. Loaded here rather than lazily in the
    // sidebar because the route guard below needs it too, and a view the
    // operator hid must not render for the moment before it arrives.
    void surfacesStore.load().then(redirectIfHidden);
    // First-run probe: when the daemon has no auth source yet, render the
    // onboarding wizard instead of the login screen.
    void setupStore.probe();
    void refreshRestartPending();
    // Apply persisted/system theme on first paint and bind to OS-
    // preference changes for "system" mode.
    applyTheme();
    const unbindTheme = bindSystemTheme();
    // When embedded in HA (Ingress iframe) mirror the live HA theme:
    // copy HA's CSS vars onto our root and track HA's light/dark. Inert
    // and cleanup is a no-op when standalone or cross-origin.
    const stopHaBridge = startHaBridge();
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
      stopHaBridge();
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
    | { kind: "overview" }
    | { kind: "favorites" }
    | { kind: "detail"; address: string; channel?: number; sub?: string }
    | { kind: "backups" }
    | { kind: "sysvars" }
    | { kind: "programs" }
    | { kind: "groups" }
    | { kind: "links" }
    | { kind: "schedules" }
    | { kind: "diagrams" }
    | { kind: "messages" }
    | { kind: "audit" }
    | { kind: "diagnostics" }
    | { kind: "energy" }
    | { kind: "fleet" }
    | { kind: "logs" }
    | { kind: "settings"; tab?: string }
    | { kind: "inbox" }
    | { kind: "firmware" }
    | { kind: "signal" }
    | { kind: "matter"; subpath: string }
    | { kind: "alarm"; subpath: string }
    | { kind: "security"; subpath: string }
    | { kind: "about" }
    | { kind: "unknown" };

  const route = $derived.by<Route>(() => {
    // Split an optional query string (e.g. `/devices/ADDR?tab=links`) off the
    // path so it never leaks into the address match. The device and the
    // settings route read a query; the other exact matches are query-free.
    const qIdx = path.indexOf("?");
    const query = qIdx >= 0 ? path.slice(qIdx + 1) : "";
    const rawPath = qIdx >= 0 ? path.slice(0, qIdx) : path;
    if (!path || path === "/" || path === "/devices") return { kind: "list" };
    if (path === "/overview") return { kind: "overview" };
    if (path === "/favorites") return { kind: "favorites" };
    if (path === "/backups") return { kind: "backups" };
    if (path === "/sysvars") return { kind: "sysvars" };
    if (path === "/programs") return { kind: "programs" };
    if (path === "/groups") return { kind: "groups" };
    if (path === "/links") return { kind: "links" };
    if (path === "/schedules") return { kind: "schedules" };
    if (path === "/diagrams") return { kind: "diagrams" };
    if (path === "/messages") return { kind: "messages" };
    if (path === "/audit") return { kind: "audit" };
    if (path === "/diagnostics") return { kind: "diagnostics" };
    if (path === "/energy") return { kind: "energy" };
    if (path === "/fleet") return { kind: "fleet" };
    if (path === "/logs") return { kind: "logs" };
    if (rawPath === "/settings") {
      return { kind: "settings", tab: new URLSearchParams(query).get("tab") ?? undefined };
    }
    if (path === "/inbox") return { kind: "inbox" };
    if (path === "/firmware") return { kind: "firmware" };
    if (path === "/signal") return { kind: "signal" };
    if (path === "/about") return { kind: "about" };
    if (path === "/matter" || path.startsWith("/matter/")) {
      return { kind: "matter", subpath: path.slice("/matter".length) || "" };
    }
    if (path === "/alarm" || path.startsWith("/alarm/")) {
      return { kind: "alarm", subpath: path.slice("/alarm".length) || "" };
    }
    if (path === "/security" || path.startsWith("/security/")) {
      return { kind: "security", subpath: path.slice("/security".length) || "" };
    }
    const m = rawPath.match(
      /^\/devices\/([^/]+)(?:\/channels\/(\d+))?\/?$/,
    );
    if (!m) return { kind: "unknown" };
    return {
      kind: "detail",
      address: decodeURIComponent(m[1]),
      channel: m[2] ? Number(m[2]) : undefined,
      sub: new URLSearchParams(query).get("tab") ?? undefined,
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
  <!-- Embedded in HA, let HA own the browser-tab title: render none. -->
  {#if !isEmbedded()}
  <title>{
    route.kind === "diagnostics" ? t("page.title.diagnostics") :
    route.kind === "energy" ? t("page.title.energy") :
    route.kind === "fleet" ? t("page.title.fleet") :
    route.kind === "groups" ? t("page.title.groups") :
    route.kind === "links" ? t("page.title.links") :
    route.kind === "schedules" ? t("page.title.schedules") :
    route.kind === "diagrams" ? t("page.title.diagrams") :
    route.kind === "logs" ? t("page.title.logs") :
    route.kind === "list" || route.kind === "detail" ? t("page.title.devices") :
    route.kind === "overview" ? t("page.title.overview") :
    route.kind === "settings" ? t("page.title.settings") :
    route.kind === "signal" ? t("page.title.signal") :
    route.kind === "alarm" ? t("page.title.alarm") :
    route.kind === "security" ? t("page.title.security") :
    route.kind === "about" ? t("page.title.about") :
    t("page.title.default")
  }</title>
  {/if}
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

{#if authStore.checking || setupStore.checking}
  <section
    class="flex min-h-screen items-center justify-center"
    style="color: var(--ha-secondary-text-color); background-color: var(--ha-card-background-color);"
  >
    {t("common.loading")}
  </section>
{:else if setupStore.required && !authStore.authenticated}
  <!-- First-run onboarding only when there is no way in yet. An already-
       authenticated admin (a real session, OIDC, or the HA Ingress
       passthrough of ADR 0044) must never be trapped in the wizard, even
       if no persistent auth source exists in the database. -->
  <Setup />
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
        style="background-color: var(--ha-card-background-color); border-color: var(--ha-divider-color);"
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
        {:else if route.kind === "overview"}
          <Overview />
        {:else if route.kind === "favorites"}
          <Favorites />
        {:else if route.kind === "detail"}
          <DeviceDetail
            address={route.address}
            channel={route.channel}
            sub={route.sub}
            {locale}
          />
        {:else if route.kind === "backups"}
          <BackupList />
        {:else if route.kind === "sysvars"}
          <SysvarList />
        {:else if route.kind === "programs"}
          <ProgramList />
        {:else if route.kind === "groups"}
          <GroupList />
        {:else if route.kind === "links"}
          <LinkList {locale} />
        {:else if route.kind === "schedules"}
          <ScheduleList />
        {:else if route.kind === "diagrams"}
          <Diagrams />
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
        {:else if route.kind === "energy"}
          <Energy />
        {:else if route.kind === "fleet"}
          <Fleet />
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
          <Settings tab={route.tab} />
        {:else if route.kind === "inbox"}
          <Inbox />
        {:else if route.kind === "firmware"}
          <FirmwareList />
        {:else if route.kind === "signal"}
          <SignalQualityList />
        {:else if route.kind === "matter"}
          {#await loadMatter()}
            <LoadingState />
          {:then { default: Matter }}
            <Matter subpath={route.subpath} />
          {/await}
        {:else if route.kind === "alarm"}
          {#await loadAlarm()}
            <LoadingState />
          {:then { default: Alarm }}
            <Alarm subpath={route.subpath} />
          {/await}
        {:else if route.kind === "security"}
          {#await loadSecurity()}
            <LoadingState />
          {:then { default: Security }}
            <Security subpath={route.subpath} />
          {/await}
        {:else if route.kind === "about"}
          <About />
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
