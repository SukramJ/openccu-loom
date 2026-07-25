import { describe, it, expect } from "vitest";
import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const HERE = dirname(fileURLToPath(import.meta.url));

// Widgets that only display sensor data — they accept onSetSlot as an
// optional prop but never call it. Everything else must declare and
// dispatch through the unified slot writer or the registry's
// `<Widget {onSetSlot} />` wiring becomes silently a no-op. ButtonEvent
// is intentionally NOT here: its press slots are writable on virtual
// remotes, so it declares and dispatches onSetSlot like any actor.
const READ_ONLY = new Set([
  "BinarySensor.svelte",
  "Powermeter.svelte",
  "Sensor.svelte",
]);

function listWidgetFiles(): string[] {
  return readdirSync(HERE)
    .filter((f) => f.endsWith(".svelte"))
    .sort();
}

describe("widget prop contract", () => {
  it("every writable widget declares onSetSlot in its Props type", () => {
    const offenders: string[] = [];
    for (const f of listWidgetFiles()) {
      if (READ_ONLY.has(f)) continue;
      const src = readFileSync(join(HERE, f), "utf8");
      if (!/onSetSlot:\s*\(\s*slot:\s*string\s*,\s*value:\s*unknown\s*\)\s*=>\s*void/.test(src)) {
        offenders.push(f);
      }
    }
    expect(offenders).toEqual([]);
  });

  it("no writable widget calls a stale onSetLevel / onSetValue callback", () => {
    const offenders: string[] = [];
    for (const f of listWidgetFiles()) {
      if (READ_ONLY.has(f)) continue;
      const src = readFileSync(join(HERE, f), "utf8");
      if (/\bonSet(Level|Value)\s*\(/.test(src)) {
        offenders.push(f);
      }
    }
    expect(offenders).toEqual([]);
  });
});
