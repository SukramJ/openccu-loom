// SPDX-License-Identifier: MIT
//
// Widget selection for a system variable's inline value control.
// Pure dispatch over the CCU wire value_type (+ value_list presence) so
// both the sysvar list and the favorites view render the same control
// for the same variable shape. Read/write path only — the edit dialog
// (unit / bounds / labels) picks its own fields.
//
// The CCU exposes boolean sysvars under two wire types that both carry a
// bool value: LOGIC (a plain logic value) and ALARM (an alarm flag). The
// daemon coerces either to a bool, and the write path accepts a bool for
// both, so both render as a switch — same as the BOOL alias the create
// dialog offers. Without this a LOGIC/ALARM variable (the most common
// kind) would fall through to the free-text field.

/** Value types whose observed value is a bool and whose write accepts a bool. */
const BOOL_VALUE_TYPES = new Set(["BOOL", "LOGIC", "ALARM"]);

/** Value types rendered as a numeric input. */
const NUMBER_VALUE_TYPES = new Set(["INTEGER", "FLOAT", "NUMBER"]);

/** The inline control a sysvar value renders as. */
export type SysvarWidget = "switch" | "select" | "number" | "text";

/** Minimal shape the widget dispatch needs — a subset of SysvarEntry. */
export interface SysvarWidgetInput {
  value_type?: string | null;
  value_list?: string[] | null;
}

/**
 * Value types whose value is an index into a label list. The CCU
 * reports these as LIST on the wire; ENUM is the alias the create
 * dialog offers before the variable exists, kept here so a freshly
 * created variable still matches before the first refresh re-reads it.
 */
const LIST_VALUE_TYPES = new Set(["LIST", "ENUM"]);

/** True for a boolean-flavoured sysvar (BOOL / LOGIC / ALARM). */
export function isBoolSysvar(valueType: string | null | undefined): boolean {
  return BOOL_VALUE_TYPES.has((valueType ?? "").toUpperCase());
}

/** True for a label-list sysvar (wire type LIST, or the ENUM alias). */
export function isListSysvar(valueType: string | null | undefined): boolean {
  return LIST_VALUE_TYPES.has((valueType ?? "").toUpperCase());
}

/** True for a numeric sysvar (INTEGER / FLOAT / NUMBER). */
export function isNumberSysvar(valueType: string | null | undefined): boolean {
  return NUMBER_VALUE_TYPES.has((valueType ?? "").toUpperCase());
}

/**
 * Pick the inline control for a sysvar value.
 *
 * Order matters: a boolean type wins over a value_list, because ALARM
 * variables frequently ship a two-entry label list yet should still be
 * flipped with a switch. A labelled list (LIST / ENUM) becomes a
 * dropdown; a numeric type — or a label-less LIST, whose write is a
 * numeric index — becomes a number input; everything else (STRING,
 * unknown) falls back to free text.
 */
export function sysvarWidget(sv: SysvarWidgetInput): SysvarWidget {
  const type = (sv.value_type ?? "").toUpperCase();
  if (BOOL_VALUE_TYPES.has(type)) return "switch";
  if (sv.value_list && sv.value_list.length > 0) return "select";
  if (NUMBER_VALUE_TYPES.has(type) || type === "LIST") return "number";
  return "text";
}

/** True when the number input should accept fractional steps. */
export function sysvarNumberStep(valueType: string | null | undefined): "any" | "1" {
  const type = (valueType ?? "").toUpperCase();
  return type === "FLOAT" || type === "NUMBER" ? "any" : "1";
}
