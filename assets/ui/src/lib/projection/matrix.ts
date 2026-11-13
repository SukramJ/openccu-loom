// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

// The projection as a matrix: one row per subject of a channel, one column
// per plane. The daemon answers per plane, each in its own vocabulary of
// kinds and keys; this module is the one place that says which entries of
// different planes are the same subject.
import type { DeviceProjection, ProjectionEntry, ProjectionPlane } from "$lib/api/types";

export type Verdict = "published" | "folded" | "withheld";

// What a row is about. The order here is the order of the rows within a
// channel.
export const ROW_KINDS = [
  "channel",
  "entity",
  "group",
  "parameter",
  "event",
  "schedule",
  "schedule_switch",
  "combined",
  "update",
  "device",
  "companion",
  "master",
  "other",
] as const;
export type RowKind = (typeof ROW_KINDS)[number];

export type Cell = {
  // The strongest verdict of the cell's entries: published over folded over
  // withheld. A plane can carry one subject in several entries (a schedule's
  // two raw topics), and the cell shows whether any of them is there.
  verdict: Verdict;
  entries: ProjectionEntry[];
};

export type MatrixRow = {
  id: string;
  channel: number;
  kind: RowKind;
  // The parameter, switch key or group key; empty for a row that is the
  // channel's own (entity, event, schedule) and for the whole channel.
  key: string;
  cells: Record<string, Cell>;
};

export type ReasonCount = { plane: string; reason: string; count: number };

export type Matrix = {
  // Every plane the daemon answered for, in its order.
  planes: ProjectionPlane[];
  rows: MatrixRow[];
  // Withheld entries per plane and reason, most frequent first.
  withheld: ReasonCount[];
};

const GROUP_KEYS = new Set(["BUTTON", "ELECTRICAL"]);
const MASTER_PREFIX = "MASTER:";

// rowOf names the row an entry belongs to. Kinds and keys are the planes'
// own (see the descriptions of ProjectionEntry in the API); an entry of a
// plane or kind this build does not know gets a row of its own rather than
// being dropped.
export function rowOf(plane: string, e: ProjectionEntry): { kind: RowKind; key: string } {
  const kind = e.kind ?? "";
  const key = e.key ?? "";
  const parameter = (): { kind: RowKind; key: string } =>
    key.startsWith(MASTER_PREFIX)
      ? { kind: "master", key: key.slice(MASTER_PREFIX.length) }
      : { kind: "parameter", key };
  switch (plane) {
    case "matter":
      if (kind === "") return { kind: "channel", key: "" };
      if (kind === "custom") return { kind: "entity", key: "" };
      if (kind === "combined") return { kind: "combined", key };
      if (GROUP_KEYS.has(key)) return { kind: "group", key };
      return { kind: "parameter", key };
    case "measurements":
    case "rest":
      return { kind: "parameter", key };
    case "ha_discovery":
    case "mqtt_raw":
      switch (kind) {
        case "":
          return { kind: "channel", key: "" };
        case "data_point":
          return parameter();
        case "channel_entity":
        case "channel_state":
          return { kind: "entity", key: "" };
        case "event":
          return { kind: "event", key: "" };
        case "schedule":
        case "week_profile":
          return { kind: "schedule", key: "" };
        case "schedule_switch":
          return { kind: "schedule_switch", key };
        case "combined":
          return { kind: "combined", key };
        case "update":
          return { kind: "update", key: "" };
        case "device":
          return key === "update" ? { kind: "update", key: "" } : { kind: "device", key };
        case "companion":
          return { kind: "companion", key };
        default:
          return { kind: "other", key: `${kind}:${key}` };
      }
    default:
      return { kind: "other", key: `${kind}:${key}` };
  }
}

const RANK: Record<Verdict, number> = { published: 2, folded: 1, withheld: 0 };

function verdictOf(e: ProjectionEntry): Verdict {
  return e.verdict === "published" || e.verdict === "folded" ? e.verdict : "withheld";
}

export function buildMatrix(p: DeviceProjection): Matrix {
  const rows = new Map<string, MatrixRow>();
  const withheld = new Map<string, ReasonCount>();
  for (const plane of p.planes) {
    for (const e of plane.entries) {
      const { kind, key } = rowOf(plane.plane, e);
      const id = `${e.channel}|${kind}|${key}`;
      let row = rows.get(id);
      if (!row) {
        row = { id, channel: e.channel, kind, key, cells: {} };
        rows.set(id, row);
      }
      const verdict = verdictOf(e);
      const cell = row.cells[plane.plane];
      if (!cell) {
        row.cells[plane.plane] = { verdict, entries: [e] };
      } else {
        cell.entries.push(e);
        if (RANK[verdict] > RANK[cell.verdict]) cell.verdict = verdict;
      }
      if (verdict === "withheld") {
        const reason = e.reason ?? "";
        const wk = `${plane.plane}|${reason}`;
        const rc = withheld.get(wk);
        if (rc) rc.count++;
        else withheld.set(wk, { plane: plane.plane, reason, count: 1 });
      }
    }
  }
  const order = (k: RowKind) => ROW_KINDS.indexOf(k);
  const sorted = [...rows.values()].sort(
    (a, b) => a.channel - b.channel || order(a.kind) - order(b.kind) || a.key.localeCompare(b.key),
  );
  return {
    planes: p.planes,
    rows: sorted,
    withheld: [...withheld.values()].sort((a, b) => b.count - a.count || a.reason.localeCompare(b.reason)),
  };
}

// appears reports whether a row is carried by at least one plane, as an
// entry of its own or inside another.
export function appears(row: MatrixRow): boolean {
  return Object.values(row.cells).some((c) => c.verdict !== "withheld");
}

// hasReason reports whether a row is withheld on plane for reason.
export function hasReason(row: MatrixRow, plane: string, reason: string): boolean {
  return (row.cells[plane]?.entries ?? []).some((e) => e.verdict === "withheld" && (e.reason ?? "") === reason);
}

// primaryIdentity is the one identifier an operator looks for on a plane:
// the endpoint, the config topic, the topic, the path. The rest of the
// identity stays in the details.
export function primaryIdentity(plane: string, e: ProjectionEntry): { key: string; value: string } | null {
  const id = e.identity ?? {};
  const pick = (...keys: string[]) => {
    for (const k of keys) if (id[k]) return { key: k, value: id[k] };
    return null;
  };
  switch (plane) {
    case "matter":
      return pick("endpoint_id", "source_key");
    case "ha_discovery":
      return pick("discovery_topic", "object_id");
    case "mqtt_raw":
      return pick("topic");
    case "measurements":
      return pick("measurement", "parameter");
    case "rest":
      return pick("path");
    default: {
      const first = Object.keys(id)[0];
      return first ? { key: first, value: id[first] } : null;
    }
  }
}
