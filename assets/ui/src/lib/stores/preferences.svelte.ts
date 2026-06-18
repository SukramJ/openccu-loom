// User preferences persisted in localStorage. Pre-populated from
// navigator.language / prefers-color-scheme on first load so users
// immediately see something sensible. The store is a Svelte 5 rune
// object so callers can read e.g. `prefs.locale` / `prefs.theme` and
// the UI re-renders on change.

const KEY = "openccu-loom.prefs.v1";
// LEGACY_KEY removed: clean break on rebrand to OpenCCU-Loom

export type Theme = "light" | "dark" | "system";

type Prefs = {
  locale: "de" | "en";
  theme: Theme;
  navCollapsed: boolean;
  // expertMode reveals expert-tier configuration fields in the
  // Settings UI (analog to the existing channel-paramset expert
  // toggle). Persisted alongside the other preferences so the
  // operator's choice survives navigation and reloads.
  expertMode: boolean;
  // deviceView toggles the device-list layout between a multi-column
  // card grid and a single-column list (HA-config-panel style).
  deviceView: "grid" | "list";
};

function detectLocale(): "de" | "en" {
  return (navigator.language ?? "en").toLowerCase().startsWith("de") ? "de" : "en";
}

function load(): Prefs {
  try {
    const raw = localStorage.getItem(KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as Partial<Prefs>;
      const locale =
        parsed.locale === "de" || parsed.locale === "en"
          ? parsed.locale
          : detectLocale();
      const theme: Theme =
        parsed.theme === "light" || parsed.theme === "dark" || parsed.theme === "system"
          ? parsed.theme
          : "system";
      const navCollapsed = parsed.navCollapsed === true;
      const expertMode = parsed.expertMode === true;
      const deviceView: "grid" | "list" = parsed.deviceView === "list" ? "list" : "grid";
      return { locale, theme, navCollapsed, expertMode, deviceView };
    }
  } catch {
    // ignore
  }
  return { locale: detectLocale(), theme: "system", navCollapsed: false, expertMode: false, deviceView: "grid" };
}

function persist(p: Prefs): void {
  try {
    localStorage.setItem(KEY, JSON.stringify(p));
  } catch {
    // ignore (private mode, quota)
  }
}

const initial = load();

export const prefs = $state<Prefs>({
  locale: initial.locale,
  theme: initial.theme,
  navCollapsed: initial.navCollapsed,
  expertMode: initial.expertMode,
  deviceView: initial.deviceView,
});

// Keep <html> in sync with the resolved theme. Listens to
// system-preference changes so "system" mode tracks the OS toggle.
export function applyTheme(): void {
  const root = document.documentElement;
  const dark =
    prefs.theme === "dark" ||
    (prefs.theme === "system" &&
      window.matchMedia?.("(prefers-color-scheme: dark)").matches);
  root.classList.toggle("dark", dark);
  root.style.colorScheme = dark ? "dark" : "light";
}

export function setLocale(loc: "de" | "en"): void {
  prefs.locale = loc;
  persist({ ...prefs });
}

export function setTheme(theme: Theme): void {
  prefs.theme = theme;
  persist({ ...prefs });
  applyTheme();
}

export function setNavCollapsed(collapsed: boolean): void {
  prefs.navCollapsed = collapsed;
  persist({ ...prefs });
}

export function setExpertMode(on: boolean): void {
  prefs.expertMode = on;
  persist({ ...prefs });
}

export function setDeviceView(view: "grid" | "list"): void {
  prefs.deviceView = view;
  persist({ ...prefs });
}

// Wire system-preference change → applyTheme. Safe to call once at
// app start; idempotent.
export function bindSystemTheme(): () => void {
  if (typeof window === "undefined" || !window.matchMedia) return () => {};
  const m = window.matchMedia("(prefers-color-scheme: dark)");
  const handler = () => {
    if (prefs.theme === "system") applyTheme();
  };
  m.addEventListener("change", handler);
  return () => m.removeEventListener("change", handler);
}
