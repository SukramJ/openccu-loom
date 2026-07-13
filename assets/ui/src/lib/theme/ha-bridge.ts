// Home-Assistant theme bridge. When the SPA runs inside HA's Ingress
// iframe (same-origin) it mirrors the user's live HA theme by copying
// HA's CSS custom properties off the parent document onto our own
// :root, and by tracking HA's light/dark choice via the parent
// background luminance. Standalone (non-iframe) the bridge is inert and
// the static HA-default literals in app.css [data-skin="ha"] apply.

// HA theme variables copied from the parent document onto our root. The
// --ha-* consumption tokens in app.css read these via var(--…, fallback),
// so a live value here overrides the HA-default literal.
const HA_THEME_VARS = [
  "--primary-color",
  "--accent-color",
  "--primary-background-color",
  "--secondary-background-color",
  "--card-background-color",
  "--primary-text-color",
  "--secondary-text-color",
  "--disabled-text-color",
  "--divider-color",
  "--error-color",
  "--warning-color",
  "--success-color",
  "--info-color",
  "--ha-card-border-radius",
  "--ha-card-box-shadow",
  "--app-header-background-color",
  "--app-header-text-color",
  "--sidebar-background-color",
  "--rgb-primary-color",
  "--text-primary-color",
];

// True when the SPA is rendered inside an iframe (HA Ingress). A
// cross-origin parent still satisfies this — the throwing property
// accesses are guarded in startHaBridge.
export function isEmbedded(): boolean {
  return typeof window !== "undefined" && window.self !== window.top;
}

// Resolve the effective skin: embedding into HA always forces "ha";
// otherwise the operator's stored choice stands.
export function resolveSkin(stored: "loom" | "ha"): "loom" | "ha" {
  return isEmbedded() ? "ha" : stored;
}

// Parse a CSS colour string into [r,g,b] (0–255) or null if unrecognised.
// Handles #rgb, #rrggbb and rgb()/rgba().
function parseRgb(value: string): [number, number, number] | null {
  const v = value.trim();
  if (!v) return null;
  if (v[0] === "#") {
    let hex = v.slice(1);
    if (hex.length === 3) {
      hex = hex[0] + hex[0] + hex[1] + hex[1] + hex[2] + hex[2];
    }
    if (hex.length >= 6) {
      const r = parseInt(hex.slice(0, 2), 16);
      const g = parseInt(hex.slice(2, 4), 16);
      const b = parseInt(hex.slice(4, 6), 16);
      if (!Number.isNaN(r) && !Number.isNaN(g) && !Number.isNaN(b)) {
        return [r, g, b];
      }
    }
    return null;
  }
  const m = v.match(/rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)/i);
  if (m) {
    return [Number(m[1]), Number(m[2]), Number(m[3])];
  }
  return null;
}

// WCAG relative luminance (0 = black, 1 = white) for an sRGB triple.
function relativeLuminance([r, g, b]: [number, number, number]): number {
  const lin = (c: number) => {
    const s = c / 255;
    return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  };
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
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
    // from app.css [data-skin="ha"] apply; nothing to tear down.
    return () => {};
  }
}
