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
  import AuditLog from "./routes/AuditLog.svelte";
  import Diagnostics from "./routes/Diagnostics.svelte";
  import Logs from "./routes/Logs.svelte";
  import Settings from "./routes/Settings.svelte";
  import Inbox from "./routes/Inbox.svelte";
  import FirmwareList from "./routes/FirmwareList.svelte";
  import Matter from "./routes/matter/Matter.svelte";
  import UnIgnoreList from "./routes/UnIgnoreList.svelte";
  import Toaster from "$lib/components/ui/Toaster.svelte";
  import ShortcutHelp from "$lib/components/ui/ShortcutHelp.svelte";
  import ConnectionBadge from "$lib/components/ui/ConnectionBadge.svelte";
  import ConfirmDialog from "$lib/components/ui/ConfirmDialog.svelte";
  import Sidebar from "$lib/components/ui/Sidebar.svelte";
  import { dirty } from "$lib/stores/dirty.svelte";
  import { t } from "$lib/i18n";

  // Minimal hash-based router. The Go handler serves the SPA under
  // /app/ and rewrites unknown paths to index.html, so client-side
  // routing is safe without full SSR.
  let path = $state<string>(
    location.hash.replace(/^#/, "") || "/devices",
  );

  function onHash() {
    path = location.hash.replace(/^#/, "") || "/devices";
  }
  window.addEventListener("hashchange", onHash);

  onMount(() => {
    authStore.probe();
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

  const sidebarPad = $derived(prefs.navCollapsed ? "pl-[64px]" : "pl-[240px]");

  // Sidebar activeKind treats "firmware" and "matter" as their own leaf.
  const activeKindForSidebar = $derived(
    route.kind === "detail" ? "detail" : route.kind,
  );
</script>

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
    />

    <div class="{sidebarPad} transition-[padding] duration-200">
      <header
        class="flex h-14 items-center justify-end gap-2 border-b px-4"
        style="border-color: var(--ha-divider-color);"
      >
        <ConnectionBadge />
      </header>
      <main>
        {#if route.kind === "list"}
          <DeviceList />
        {:else if route.kind === "detail"}
          <DeviceDetail
            address={route.address}
            channel={route.channel}
            {locale}
          />
        {:else if route.kind === "backups"}
          <BackupList {locale} />
        {:else if route.kind === "sysvars"}
          <SysvarList />
        {:else if route.kind === "programs"}
          <ProgramList />
        {:else if route.kind === "messages"}
          <MessageList {locale} />
        {:else if route.kind === "audit"}
          <AuditLog {locale} />
        {:else if route.kind === "diagnostics"}
          <Diagnostics {locale} />
        {:else if route.kind === "logs"}
          {#if authStore.identity?.role === "admin"}
            <Logs {locale} />
          {:else}
            <section class="mx-auto max-w-6xl px-6 py-8">
              <p style="color: var(--ha-secondary-text-color);">{t("logs.forbidden")}</p>
            </section>
          {/if}
        {:else if route.kind === "settings"}
          <Settings />
        {:else if route.kind === "inbox"}
          <Inbox {locale} />
        {:else if route.kind === "firmware"}
          <FirmwareList />
        {:else if route.kind === "matter"}
          <Matter subpath={route.subpath} />
        {:else if route.kind === "visibility"}
          <UnIgnoreList />
        {:else}
          <section class="mx-auto max-w-6xl px-6 py-8">
            <h1 class="text-2xl font-semibold">Nicht gefunden</h1>
            <p class="mt-2" style="color: var(--ha-secondary-text-color);">
              Unbekannter Pfad <code>{path}</code>.
            </p>
          </section>
        {/if}
      </main>
    </div>
  </div>
{/if}
