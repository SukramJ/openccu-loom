import type { TaxonomyAssignment, TaxonomyEnum, TaxonomyNode } from "$lib/api/client";
import type { DeviceSummary } from "$lib/api/types";

/**
 * Helpers over a central's taxonomy trees: flattening a tree into picker
 * rows, naming a node by its whole path ("EG › Küche"), and the node
 * references assignments are written with (`room/eg/kueche`).
 */

/** One node as a flat row: its path, its name, its depth and full label. */
export type FlatNode = {
  path: string;
  name: string;
  depth: number;
  /** Names from the root down, joined for display. */
  label: string;
  parentPath: string;
};

export const PATH_SEPARATOR = " › ";

/** Depth-first rows of a tree, parents before children, in tree order. */
export function flatten(nodes: TaxonomyNode[] | undefined, depth = 0, parents: string[] = [], parentPath = ""): FlatNode[] {
  const out: FlatNode[] = [];
  for (const n of nodes ?? []) {
    const names = [...parents, n.name];
    out.push({ path: n.path, name: n.name, depth, label: names.join(PATH_SEPARATOR), parentPath });
    out.push(...flatten(n.children, depth + 1, names, n.path));
  }
  return out;
}

/** The enum of an id, when the central has it. */
export function enumOf(enums: TaxonomyEnum[] | undefined, id: string): TaxonomyEnum | undefined {
  return enums?.find((e) => e.id === id);
}

/** The full label of a node path, or undefined when the tree lacks it. */
export function labelOf(e: TaxonomyEnum | undefined, path: string): string | undefined {
  return flatten(e?.nodes).find((n) => n.path === path)?.label;
}

/**
 * The paths a node may be moved below: every node outside its own
 * subtree (a node cannot become its own descendant), plus the root ("").
 */
export function moveTargets(e: TaxonomyEnum | undefined, path: string): FlatNode[] {
  const own = path + "/";
  return flatten(e?.nodes).filter((n) => n.path !== path && !n.path.startsWith(own));
}

/** The reference an assignment is written with: `<enum>/<path>`. */
export function nodeRef(enumId: string, path: string): string {
  return `${enumId}/${path}`;
}

/** The paths of one enum an address is directly assigned to. */
export function assignedPaths(assignments: TaxonomyAssignment[] | undefined, enumId: string): string[] {
  return (assignments ?? []).filter((a) => a.enum === enumId).map((a) => a.path);
}

/**
 * The display label of an assignment: the whole path when the node sits
 * below another, the bare name at the root.
 */
export function assignmentLabel(a: TaxonomyAssignment, e: TaxonomyEnum | undefined): string {
  return labelOf(e, a.path) ?? a.name;
}

/** A room or function filter value that names one nested node rather
 *  than every node of a name: `@<central>/<enum>/<path>`. */
export function nodeFilterValue(central: string, enumId: string, path: string): string {
  return `@${central}/${enumId}/${path}`;
}

/**
 * The nested nodes of one enum a device is directly assigned to. A node
 * at the top of its tree, and every node of a flat CCU enum, is still
 * told by its name, so nothing changes for a CCU.
 */
export function nestedAssignments(d: Pick<DeviceSummary, "taxonomy">, enumId: string): TaxonomyAssignment[] {
  return (d.taxonomy ?? []).filter((a) => a.enum === enumId && !!a.parent_path);
}

/**
 * Whether a device passes a room or function filter: a node filter value
 * matches the one nested node, a plain value every node of that name.
 */
export function matchesNodeFilter(
  d: Pick<DeviceSummary, "central" | "rooms" | "functions" | "taxonomy">,
  enumId: "room" | "function",
  value: string,
): boolean {
  if (value.startsWith("@")) {
    return nestedAssignments(d, enumId).some((a) => nodeFilterValue(d.central ?? "", enumId, a.path) === value);
  }
  return ((enumId === "room" ? d.rooms : d.functions) ?? []).includes(value);
}
