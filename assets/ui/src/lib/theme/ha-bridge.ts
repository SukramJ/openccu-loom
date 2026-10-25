// Home-Assistant theme bridge. When the SPA runs inside HA's Ingress
// iframe (same-origin) it mirrors the user's live HA theme by copying
// HA's CSS custom properties off the parent document onto our own
// :root, and by tracking HA's light/dark choice via the parent
// background luminance. Outside HA (standalone, the openccu-lite shell,
// any other iframe — see isEmbedded) the bridge is inert and
// the static HA-default literals of the fabric-ui tokens.css
// [data-skin="ha"] (imported by app.css) apply.

// The pure helpers (the variable list, colour parsing, luminance) come from
// @sukramj/fabric-ui, which ported them from this file. The frame detection
// and everything that reads it stay here: the package's isEmbedded does not
// know the openccu-lite shell, and its resolveSkin / startHaBridge call that
// isEmbedded internally, so re-exporting them would dress the lite shell as
// Home Assistant.
import { HA_THEME_VARS, parseRgb, relativeLuminance } from "@sukramj/fabric-ui/theme";
import { liteShell } from "./lite-shell.svelte";

// The path segment Home Assistant's Supervisor serves an add-on's Ingress
// under (`/api/hassio_ingress/<token>/…`, see $lib/api/base).
const HA_INGRESS_SEGMENT = "/api/hassio_ingress/";

// True when the SPA runs inside Home Assistant, detected positively — an
// iframe alone is not enough (the openccu-lite shell frames the SPA too,
// and an unknown host must not be dressed as HA):
//   - served under HA's Ingress path, or
//   - a same-origin parent whose document is HA's frontend
//     (`<home-assistant>`).
// The openccu-lite shell's signals (lite-shell.svelte.ts) win over both.
// Anything else framed behaves as standalone.
export function isEmbedded(): boolean {
  if (typeof window === "undefined") return false;
  try {
    if (window.self === window.top) return false;
  } catch {
    // top inaccessible — still framed
  }
  if (liteShell.active) return false;
  if (window.location.pathname.includes(HA_INGRESS_SEGMENT)) return true;
  try {
    return window.parent.document.querySelector("home-assistant") !== null;
  } catch {
    // Cross-origin or unreadable parent — not provably HA.
    return false;
  }
}

// Resolve the effective skin: embedding into HA always forces "ha";
// otherwise the operator's stored choice stands.
export function resolveSkin(stored: "loom" | "ha"): "loom" | "ha" {
  return isEmbedded() ? "ha" : stored;
}

// Start mirroring the live HA theme. No-op (returns a no-op cleanup)
// when standalone or when the parent is cross-origin / otherwise
// inaccessible — any failure falls back to the static HA defaults.
export function startHaBridge(): () => void {
  if (!isEmbedded()) return () => {};

  try {
    const parentWin = window.parent;
    const parentRoot = parentWin.document.documentElement;
    const ourRoot = document.documentElement;

    const apply = () => {
      // The openccu-lite shell took over after start — not HA after all.
      if (liteShell.active) return;
      try {
        const ps = parentWin.getComputedStyle(parentRoot);
        for (const name of HA_THEME_VARS) {
          const value = ps.getPropertyValue(name).trim();
          if (value) ourRoot.style.setProperty(name, value);
        }
        // Track HA's light/dark by the parent background luminance. This
        // OVERRIDES the SPA's own light/dark while embedded.
        const bg =
          ps.getPropertyValue("--primary-background-color").trim() ||
          ps.backgroundColor;
        const rgb = parseRgb(bg);
        if (rgb) {
          const dark = relativeLuminance(rgb) < 0.5;
          ourRoot.classList.toggle("dark", dark);
          ourRoot.style.colorScheme = dark ? "dark" : "light";
        }
      } catch {
        // Parent became inaccessible mid-flight — leave the last good
        // values in place; the static HA fallback still applies.
      }
    };

    apply();

    const observer = new MutationObserver(() => apply());
    observer.observe(parentRoot, {
      attributes: true,
      attributeFilter: ["style", "class"],
    });

    const onFocus = () => apply();
    const onVisibility = () => apply();
    window.addEventListener("focus", onFocus);
    // visibilitychange fires on document, not window.
    document.addEventListener("visibilitychange", onVisibility);

    return () => {
      observer.disconnect();
      window.removeEventListener("focus", onFocus);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  } catch {
    // Cross-origin parent (or any access failure) — static HA defaults
    // from tokens.css [data-skin="ha"] apply; nothing to tear down.
    return () => {};
  }
}
