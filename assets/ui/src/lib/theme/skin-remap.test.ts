// @vitest-environment happy-dom
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, it, expect } from "vitest";

// happy-dom's getComputedStyle does not load or cascade the app's actual
// stylesheet (no CSSOM), so it cannot resolve `var(--color-slate-500)` or
// the HA-skin var()-with-fallback chain the way a real browser would. The
// reliable check is therefore against the compiled source of app.css
// itself — this still fails the moment the palette-remap block or the
// refreshed HA fallbacks regress or get deleted. Resolved against the
// Vite/vitest project root (assets/ui/), not import.meta.url, because the
// test file is transformed and does not keep a stable file:// URL.
const appCssPath = resolve(process.cwd(), "src/app.css");
const appCss = readFileSync(appCssPath, "utf-8");

// Isolate the html[data-skin="ha"] palette-remap block (the one containing
// --color-slate-*) from the earlier html[data-skin="ha"] consumption-token
// block, so assertions can't accidentally pass against the wrong block.
function extractSkinHaBlocks(css: string): string[] {
  const blocks: string[] = [];
  const re = /html\[data-skin="ha"\]([^{]*)\{/g;
  let match: RegExpExecArray | null = re.exec(css);
  while (match !== null) {
    const start = match.index;
    const bodyStart = match.index + match[0].length;
    let depth = 1;
    let i = bodyStart;
    while (i < css.length && depth > 0) {
      if (css[i] === "{") depth++;
      else if (css[i] === "}") depth--;
      i++;
    }
    blocks.push(css.slice(start, i));
    match = re.exec(css);
  }
  return blocks;
}

describe("HA skin palette remap (app.css)", () => {
  const haBlocks = extractSkinHaBlocks(appCss);
  const paletteRemapBlock = haBlocks.find((b) => b.includes("--color-slate-500"));
  const tokenBlock = haBlocks.find((b) => b.includes("--ha-primary-color:"));

  it("defines a palette-remap block for html[data-skin=\"ha\"] (no .dark selector)", () => {
    expect(paletteRemapBlock).toBeDefined();
    // The remap block itself must be scoped to plain html[data-skin="ha"],
    // not html[data-skin="ha"].dark — the ramp is monotonic on purpose.
    expect(paletteRemapBlock!.startsWith('html[data-skin="ha"] {')).toBe(true);
  });

  it("remaps the neutral (slate) ramp to the refreshed HA greys", () => {
    expect(paletteRemapBlock).toMatch(/--color-slate-500:\s*#727272;/);
    expect(paletteRemapBlock).toMatch(/--color-slate-900:\s*#1c1c1c;/);
    expect(paletteRemapBlock).toMatch(/--color-slate-950:\s*#141414;/);
  });

  it("remaps error/warning/success/info-adjacent ramps to the latest HA semantic colors", () => {
    expect(paletteRemapBlock).toMatch(/--color-red-500:\s*#db4437;/);
    expect(paletteRemapBlock).toMatch(/--color-amber-500:\s*#ffa600;/);
    expect(paletteRemapBlock).toMatch(/--color-green-500:\s*#43a047;/);
    expect(paletteRemapBlock).toMatch(/--color-emerald-500:\s*#43a047;/);
  });

  it("remaps the brand ramp to HA's primary color and folds violet onto it", () => {
    expect(paletteRemapBlock).toMatch(/--color-brand-500:\s*#009ac7;/);
    expect(paletteRemapBlock).toMatch(/--color-violet-500:\s*#009ac7;/);
  });

  it("does NOT remap --color-white / --color-black (on-primary text stays literal)", () => {
    expect(paletteRemapBlock).not.toMatch(/--color-white/);
    expect(paletteRemapBlock).not.toMatch(/--color-black/);
  });

  it("refreshes --ha-primary-color to HA's latest default (#009ac7) as the var() fallback", () => {
    expect(tokenBlock).toBeDefined();
    expect(tokenBlock).toMatch(/--ha-primary-color:\s*var\(--primary-color,\s*#009ac7\);/);
  });

  it("uses flat (shadow-less) cards to match HA's latest card styling", () => {
    expect(tokenBlock).toMatch(/--ha-elevation-card:\s*var\(--ha-card-box-shadow,\s*none\);/);
  });

  it("does not touch the loom (:root / html.dark) defaults", () => {
    const rootBlockMatch = appCss.match(/:root\s*\{[^}]*\}/);
    expect(rootBlockMatch).not.toBeNull();
    // The loom :root block must not itself define the HA palette-remap
    // vars — those only ever apply under html[data-skin="ha"].
    expect(rootBlockMatch![0]).not.toMatch(/--color-slate-500/);
  });
});
