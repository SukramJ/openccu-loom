import type { UISchemaParameter } from "$lib/api/types";

/**
 * Daylight-saving-time parameters (`DST_START_*` / `DST_END_*`)
 * configure the CCU's DST switchover. They always travel in two
 * complementary sets (start + end) and the CCU WebUI groups them
 * under their own sub-sections; every `*_TIME` parameter is encoded
 * as minutes since midnight and rendered as an HH:MM time picker.
 *
 * Port of:
 *   homematicip-local-frontend/packages/config-panel/src/components/
 *     config-form.ts :: _detectDstGroups / _renderDstGroup
 */

export type DstGroupName = "start" | "end";

export type DstGroups = {
  start: UISchemaParameter[];
  end: UISchemaParameter[];
  /** Parameter names already handled by the DST renderer. */
  paired: Set<string>;
};

export function detectDstGroups(params: UISchemaParameter[]): DstGroups {
  const start: UISchemaParameter[] = [];
  const end: UISchemaParameter[] = [];
  const paired = new Set<string>();
  for (const p of params) {
    if (p.name.startsWith("DST_START_")) {
      start.push(p);
      paired.add(p.name);
    } else if (p.name.startsWith("DST_END_")) {
      end.push(p);
      paired.add(p.name);
    }
  }
  return { start, end, paired };
}

export function dstHeader(name: DstGroupName, locale: string): string {
  if (locale === "de") {
    return name === "start" ? "Beginn der Sommerzeit" : "Ende der Sommerzeit";
  }
  return name === "start"
    ? "Start of daylight saving time"
    : "End of daylight saving time";
}

/**
 * The `*_TIME` DST parameters carry minutes past midnight. Decode
 * for the HH:MM input; encode on change.
 */
export function isDstTimeParam(name: string): boolean {
  return (
    (name.startsWith("DST_START_") || name.startsWith("DST_END_")) &&
    name.endsWith("_TIME")
  );
}

export function minutesToHHMM(minutes: number): string {
  const mins = Number.isFinite(minutes) ? minutes : 0;
  const h = Math.floor(mins / 60);
  const m = mins % 60;
  return `${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}`;
}

export function hhmmToMinutes(text: string): number | null {
  const parts = text.split(":").map((s) => Number(s));
  if (parts.length !== 2) return null;
  const [h, m] = parts;
  if (!Number.isFinite(h) || !Number.isFinite(m)) return null;
  if (h < 0 || h > 23 || m < 0 || m > 59) return null;
  return h * 60 + m;
}
