// openccu-lite shell embedding. On a lite box the shell shows the SPA in a
// same-origin iframe and hands it the shell's look three ways (the shell's
// "Embedding contract for addon pages", occulited docs/system-api.md):
//
//   1. `theme=system|light|dark` and `lang=de|en` on the frame URL, for the
//      first paint;
//   2. a postMessage `{type: "openccu-lite:theme", theme, lang}` from the
//      shell (its own origin as target), on load and on every change;
//   3. the cookies `ol-theme` (`light`|`dark`, `system` already resolved)
//      and `ol-lang`, for a page that sees neither of the above.
//
// While one of those has arrived the shell's look is in control: the theme
// and language below override the operator's own preference for display,
// without ever being written into it (a direct tab keeps the own setting).
// Any iframe is NOT taken for the shell: the URL and cookie signals count
// only under a same-origin parent, and a message only when it comes from
// the parent window, from this origin, in the contract's shape. A shell
// signal that arrives after the first paint (a message into an iframe that
// started without one) switches the SPA to following from then on.

export type ShellTheme = "system" | "light" | "dark";
export type ShellLang = "de" | "en";
export type ShellLook = { theme: ShellTheme | null; lang: ShellLang | null };

export const SHELL_MESSAGE_TYPE = "openccu-lite:theme";

// Reactive so the Settings controls and the sidebar toggle can show that
// the shell is in control. `theme` / `lang` stay null until the shell has
// named them; a field the shell never sent stays the operator's own.
export const liteShell = $state<{
  active: boolean;
  theme: ShellTheme | null;
  lang: ShellLang | null;
}>({ active: false, theme: null, lang: null });

export function parseShellTheme(v: unknown): ShellTheme | null {
  return v === "system" || v === "light" || v === "dark" ? v : null;
}

// The shell sends its <html lang>, `de` or `en`; a region suffix is tolerated.
export function parseShellLang(v: unknown): ShellLang | null {
  if (typeof v !== "string") return null;
  const l = v.trim().toLowerCase();
  if (l === "de" || l.startsWith("de-")) return "de";
  if (l === "en" || l.startsWith("en-")) return "en";
  return null;
}

export function readUrlLook(search: string): ShellLook {
  const q = new URLSearchParams(search);
  return { theme: parseShellTheme(q.get("theme")), lang: parseShellLang(q.get("lang")) };
}

export function readCookieLook(cookie: string): ShellLook {
  let theme: ShellTheme | null = null;
  let lang: ShellLang | null = null;
  for (const part of cookie.split(";")) {
    const eq = part.indexOf("=");
    if (eq < 0) continue;
    const name = part.slice(0, eq).trim();
    let value = part.slice(eq + 1).trim();
    try {
      value = decodeURIComponent(value);
    } catch {
      continue;
    }
    // ol-theme is resolved by the shell: only light or dark is valid.
    if (name === "ol-theme" && (value === "light" || value === "dark")) theme = value;
    else if (name === "ol-lang") lang = parseShellLang(value);
  }
  return { theme, lang };
}

// The contract's message, or null for anything else (another type, a
// missing or unknown theme). The language is optional.
export function parseShellMessage(data: unknown): ShellLook | null {
  if (!data || typeof data !== "object") return null;
  const d = data as { type?: unknown; theme?: unknown; lang?: unknown };
  if (d.type !== SHELL_MESSAGE_TYPE) return null;
  const theme = parseShellTheme(d.theme);
  if (!theme) return null;
  return { theme, lang: parseShellLang(d.lang) };
}

function framed(): boolean {
  try {
    return window.parent !== window;
  } catch {
    return true;
  }
}

// The shell embeds same-origin; a cross-origin parent (or one we cannot
// read) is never the shell.
function sameOriginParent(): boolean {
  if (typeof window === "undefined" || !framed()) return false;
  try {
    return window.parent.location.origin === window.location.origin;
  } catch {
    return false;
  }
}

// The first-paint look: URL first, the cookies for whatever the URL does
// not name. Null when not framed by a same-origin parent or no signal.
export function detectInitialLook(): ShellLook | null {
  if (!sameOriginParent()) return null;
  const url = readUrlLook(window.location.search);
  let cookie: ShellLook = { theme: null, lang: null };
  try {
    cookie = readCookieLook(document.cookie);
  } catch {
    // cookies unavailable — URL only
  }
  const theme = url.theme ?? cookie.theme;
  const lang = url.lang ?? cookie.lang;
  return theme || lang ? { theme, lang } : null;
}

export function adoptShellLook(look: ShellLook): void {
  liteShell.active = true;
  if (look.theme) liteShell.theme = look.theme;
  if (look.lang) liteShell.lang = look.lang;
}

// Listen for the shell's theme message. Acts only on a message from the
// parent window, from this origin, in the contract's shape.
export function listenForShellLook(onLook: (look: ShellLook) => void): () => void {
  if (typeof window === "undefined") return () => {};
  const handler = (e: MessageEvent) => {
    if (e.origin !== window.location.origin) return;
    if (!framed() || e.source !== window.parent) return;
    const look = parseShellMessage(e.data);
    if (look) onLook(look);
  };
  window.addEventListener("message", handler);
  return () => window.removeEventListener("message", handler);
}
