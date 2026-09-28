// Pure parsing helpers for the Licenses page (#/licenses). Turns the
// embedded CycloneDX 1.6 document (GET /api/v1/sbom) into flat rows the
// DataTable component can render. CycloneDX makes almost every field on a
// component optional — a Go module carries no `licenses` entry unless a
// license scanner ran, and only npm packages tend to have `author` — so
// every accessor here degrades to an empty string / empty array instead of
// throwing on a missing field.

/** One license entry as CycloneDX 1.6 allows it: either a resolved SPDX
 *  id/name pair, or a free-form SPDX expression. */
type CycloneDXLicenseChoice =
  | { license?: { id?: string; name?: string } }
  | { expression?: string };

type CycloneDXExternalRef = { type?: string; url?: string };

type CycloneDXComponent = {
  "bom-ref"?: string;
  name?: string;
  version?: string;
  licenses?: CycloneDXLicenseChoice[];
  purl?: string;
  author?: string;
  publisher?: string;
  externalReferences?: CycloneDXExternalRef[];
};

type CycloneDXDocument = {
  components?: CycloneDXComponent[];
};

/** A link the Licenses page renders out — narrowed to the three
 *  external-reference types worth surfacing on a per-component basis. */
export type SbomLinkType = "website" | "vcs" | "distribution";

export type SbomLink = { type: SbomLinkType; url: string };

/** One row of the Licenses table, derived from a CycloneDX component. */
export type SbomRow = {
  /** Stable row identity: the component's bom-ref, falling back to its
   *  purl, then to `name@version`. */
  key: string;
  name: string;
  version: string;
  /** Joined, text-free license label — e.g. "MIT" or "(MIT OR CC0-1.0)".
   *  Empty when the component carries no license entry at all. */
  license: string;
  purl: string;
  /** The component's author, falling back to its publisher. Empty when
   *  neither is present (common for Go modules). */
  author: string;
  links: SbomLink[];
};

const LINK_TYPES: ReadonlySet<string> = new Set<SbomLinkType>([
  "website",
  "vcs",
  "distribution",
]);

function isLinkType(value: string): value is SbomLinkType {
  return LINK_TYPES.has(value);
}

function licenseLabel(licenses: CycloneDXLicenseChoice[] | undefined): string {
  if (!Array.isArray(licenses) || licenses.length === 0) return "";
  const parts = licenses
    .map((entry) => {
      if ("expression" in entry && typeof entry.expression === "string") {
        return entry.expression;
      }
      if ("license" in entry && entry.license) {
        return entry.license.id ?? entry.license.name ?? "";
      }
      return "";
    })
    .filter((s) => s.length > 0);
  return parts.join(", ");
}

function externalLinks(refs: CycloneDXExternalRef[] | undefined): SbomLink[] {
  if (!Array.isArray(refs)) return [];
  const links: SbomLink[] = [];
  for (const ref of refs) {
    if (typeof ref?.type !== "string" || typeof ref?.url !== "string") continue;
    if (!isLinkType(ref.type)) continue;
    links.push({ type: ref.type, url: ref.url });
  }
  return links;
}

/**
 * Parses a CycloneDX 1.6 document (as served by GET /api/v1/sbom) into
 * rows for the Licenses page. Any shape that is not "an object with a
 * `components` array" yields an empty row list rather than throwing — the
 * daemon's response is arbitrary JSON as far as the OpenAPI contract is
 * concerned (`additionalProperties: true`), so malformed input is a
 * rendering concern (an empty table), not a crash.
 */
export function parseSBOM(doc: unknown): SbomRow[] {
  if (typeof doc !== "object" || doc === null) return [];
  const components = (doc as CycloneDXDocument).components;
  if (!Array.isArray(components)) return [];

  return components.map((c, index) => {
    const name = typeof c.name === "string" ? c.name : "";
    const version = typeof c.version === "string" ? c.version : "";
    const purl = typeof c.purl === "string" ? c.purl : "";
    const bomRef = typeof c["bom-ref"] === "string" ? c["bom-ref"] : "";
    const key = bomRef || purl || (name ? `${name}@${version}` : `component-${index}`);
    const author =
      typeof c.author === "string" && c.author
        ? c.author
        : typeof c.publisher === "string"
          ? c.publisher
          : "";

    return {
      key,
      name,
      version,
      license: licenseLabel(c.licenses),
      purl,
      author,
      links: externalLinks(c.externalReferences),
    };
  });
}

/**
 * Case-insensitive substring match over name / version / license / author —
 * the columns the Licenses table renders. Matches
 * `DataTable`'s own search semantics (see `makeTextMatcher` in `./utils`)
 * without depending on it, so this stays a pure function the page's search
 * box (or DataTable's built-in one) can both rely on.
 */
export function searchRows(rows: SbomRow[], query: string): SbomRow[] {
  const q = query.trim().toLowerCase();
  if (!q) return rows;
  return rows.filter(
    (r) =>
      r.name.toLowerCase().includes(q) ||
      r.version.toLowerCase().includes(q) ||
      r.license.toLowerCase().includes(q) ||
      r.author.toLowerCase().includes(q),
  );
}
