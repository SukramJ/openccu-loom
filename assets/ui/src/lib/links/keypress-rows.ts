import type { UISchemaParameter } from "$lib/api/types";

/**
 * One row of the short/long keypress table: the parameters that
 * configure the same behaviour for a short and for a long press.
 *
 * A row carries lists rather than single parameters because a time
 * setting is two parameters (`*_TIME_BASE` + `*_TIME_FACTOR`, or
 * `*_UNIT` + `*_VALUE`); both halves land in the same cell so the
 * grid inside it can still collapse them into one time picker.
 */
export type KeypressRow = {
  /** Parameter name with the keypress prefix and time-pair suffix removed. */
  stem: string;
  short: UISchemaParameter[];
  long: UISchemaParameter[];
};

export type KeypressLayout = {
  /** Parameters that belong to neither keypress. */
  common: UISchemaParameter[];
  rows: KeypressRow[];
};

// The daemon classifies a LINK parameter as short / long keypress by
// exactly these name prefixes (internal/central/adapter/
// link_param_metadata.go, stripKeypressPrefix), so splitting on them here
// pairs the same parameters the schema's keypress groups contain.
const SHORT = "SHORT_";
const LONG = "LONG_";

// The companion suffixes of a time setting. Stripping them puts both
// halves of one time pair under the same row key.
const TIME_PAIR_SUFFIX = /_(BASE|FACTOR|UNIT|VALUE)$/;

function rowKey(nameWithoutPrefix: string): string {
  return nameWithoutPrefix.replace(TIME_PAIR_SUFFIX, "");
}

/**
 * Lay out LINK parameters as a short/long comparison: every parameter
 * named `SHORT_<x>` sits in the same row as its `LONG_<x>` twin.
 * Rows keep the order in which their first parameter appears in the
 * input, so the schema's ordering carries through. A parameter with no
 * twin still gets its row, with the other cell empty.
 */
export function keypressRows(parameters: UISchemaParameter[]): KeypressLayout {
  const common: UISchemaParameter[] = [];
  const rows: KeypressRow[] = [];
  const byKey = new Map<string, KeypressRow>();
  for (const p of parameters) {
    const upper = p.name.toUpperCase();
    let side: "short" | "long" | null = null;
    let rest = "";
    if (upper.startsWith(SHORT)) {
      side = "short";
      rest = upper.slice(SHORT.length);
    } else if (upper.startsWith(LONG)) {
      side = "long";
      rest = upper.slice(LONG.length);
    }
    if (side === null) {
      common.push(p);
      continue;
    }
    const key = rowKey(rest);
    let row = byKey.get(key);
    if (!row) {
      row = { stem: key, short: [], long: [] };
      byKey.set(key, row);
      rows.push(row);
    }
    row[side].push(p);
  }
  return { common, rows };
}
