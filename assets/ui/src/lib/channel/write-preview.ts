import type { UISchema, UISchemaParameter } from "$lib/api/types";
import { formatDisplayValue } from "$lib/parameter/display-value";

/**
 * The write preview and the read-back check — the two halves of "what did
 * this save actually do".
 *
 * A MASTER or LINK write is a configuration change the operator cannot undo
 * by looking at the device, and the CCU's two backends disagree on what a
 * refused value does: `rfd` clamps an out-of-range value to MAX and answers
 * `ok`, while `hmipserver` stores the rejected value. Both were measured by
 * the homematic-manager project against CCU firmware 3.89.8
 * (its `docs/config-pending.md`) — an external measurement, not one this
 * project made. Either way a plain success toast can follow a write that did
 * not land as sent, which is what the read-back comparison exists to catch.
 */

export type PreviewEntry = {
  name: string;
  label: string;
  /** Display form of the value currently on the device. */
  from: string;
  /** Display form of the value about to be written. */
  to: string;
  /** Raw wire value, i.e. exactly what goes into the request body. */
  raw: unknown;
};

export type ReadBackEntry = {
  name: string;
  label: string;
  /** Display form of what was sent. */
  sent: string;
  /** Display form of what the device reports afterwards. */
  got: string;
};

function labelOf(param: UISchemaParameter): string {
  return param.label?.trim() || param.name;
}

/**
 * One entry per dirty parameter, in the schema's own order so the preview
 * reads like the form above it rather than in hash order.
 */
export function buildPreview(
  schema: Pick<UISchema, "parameters"> | null | undefined,
  values: Record<string, unknown>,
  serverValues: Record<string, unknown>,
  dirtyNames: string[],
): PreviewEntry[] {
  if (!schema) return [];
  const dirty = new Set(dirtyNames);
  const out: PreviewEntry[] = [];
  for (const param of schema.parameters) {
    if (!dirty.has(param.name)) continue;
    out.push({
      name: param.name,
      label: labelOf(param),
      from: formatDisplayValue(param, serverValues[param.name]),
      to: formatDisplayValue(param, values[param.name]),
      raw: values[param.name],
    });
  }
  return out;
}

/** The request body the preview's entries produce — raw wire values. */
export function previewBody(entries: PreviewEntry[]): Record<string, unknown> {
  const body: Record<string, unknown> = {};
  for (const entry of entries) body[entry.name] = entry.raw;
  return body;
}

/**
 * Parameters whose reloaded value differs from what was sent.
 *
 * Numbers are compared as numbers: a CCU may echo `1` where `1.0` was sent,
 * and reporting that as a divergence would train the operator to ignore the
 * warning — which costs exactly the case it exists for. A parameter the
 * reload does not carry at all is skipped rather than reported as changed,
 * because "absent" is not "different".
 */
export function readBackDiff(
  schema: Pick<UISchema, "parameters"> | null | undefined,
  sent: Record<string, unknown>,
  reloaded: Record<string, unknown>,
): ReadBackEntry[] {
  if (!schema) return [];
  const out: ReadBackEntry[] = [];
  for (const param of schema.parameters) {
    if (!(param.name in sent)) continue;
    if (!(param.name in reloaded)) continue;
    const a = sent[param.name];
    const b = reloaded[param.name];
    if (sameValue(a, b)) continue;
    out.push({
      name: param.name,
      label: labelOf(param),
      sent: formatDisplayValue(param, a),
      got: formatDisplayValue(param, b),
    });
  }
  return out;
}

function sameValue(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  const na = Number(a);
  const nb = Number(b);
  if (
    a != null &&
    b != null &&
    a !== "" &&
    b !== "" &&
    Number.isFinite(na) &&
    Number.isFinite(nb)
  ) {
    return na === nb;
  }
  return JSON.stringify(a) === JSON.stringify(b);
}
