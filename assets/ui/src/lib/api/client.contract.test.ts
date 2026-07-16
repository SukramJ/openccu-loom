import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

// Contract guard: every URL the REST client constructs must match an
// operation in the OpenAPI contract (via types.generated.ts, which
// `npm run gen:types` derives from assets/openapi.yaml). A client call
// against a path the daemon never mounts fails at runtime with a 404
// that is invisible to typecheck and unit tests — the LINK-paramset
// editor shipped calling `/link-paramsets/` while the contract serves
// `/link-ps/`, and every save failed. This test pins the whole surface
// so the next renamed or misspelled path fails CI instead of the user.

const here = dirname(fileURLToPath(import.meta.url));
const clientSrc = readFileSync(join(here, "client.ts"), "utf8");
const generatedSrc = readFileSync(join(here, "types.generated.ts"), "utf8");

type Op = { method: string; path: string };

/**
 * Spec operations parsed from the generated OpenAPI types. Path keys
 * sit at 4-space indent inside `interface paths`; each defined method
 * sits at 8-space indent and is either an `operations["id"]` reference
 * (operations with an operationId) or an inline `{ … }` block
 * (operations without one). Undefined methods render as `get?: never`
 * and must not count — the optional `?` keeps them off this regex.
 */
function specOperations(): Op[] {
  const ops: Op[] = [];
  let currentPath = "";
  for (const line of generatedSrc.split("\n")) {
    const p = line.match(/^\s{4}"(\/[^"]*)":\s*\{/);
    if (p) {
      currentPath = p[1];
      continue;
    }
    const m = line.match(/^\s{8}(get|put|post|delete|patch|head|options):\s*(?:operations\[|\{)/);
    if (m && currentPath) {
      ops.push({ method: m[1].toUpperCase(), path: normalize(currentPath) });
    }
  }
  return ops;
}

/**
 * Normalizes a path template: `${qs…}` interpolations carry a query
 * string (client convention) and drop entirely; every other `${…}`
 * interpolation becomes the wildcard segment `{p}`; OpenAPI `{param}`
 * placeholders become `{p}` too; the query string is stripped.
 */
function normalize(raw: string): string {
  let p = raw
    .replace(/\$\{(?:qs|q|query)[^}]*\}|\$\{[^}]*toString\(\)[^}]*\}/g, "")
    .replace(/\$\{[^}]*\}/g, "{p}")
    .replace(/\{[^}]+\}/g, "{p}");
  const q = p.indexOf("?");
  if (q >= 0) p = p.slice(0, q);
  if (p.length > 1 && p.endsWith("/")) p = p.slice(0, -1);
  return p;
}

/**
 * Client calls parsed from client.ts source: every `request(...)`
 * first-argument template plus the `method:` of its init object
 * (default GET). The init-object scan is bounded by the next request(
 * occurrence so a nested `headers: {}` cannot leak a method from the
 * following call.
 */
function clientCalls(): (Op & { snippet: string })[] {
  const calls: (Op & { snippet: string })[] = [];
  const re = /\brequest(?:<[^(]*?>)?\(\s*(`[^`]*`|"[^"]*"|'[^']*')/g;
  const matches = [...clientSrc.matchAll(re)];
  for (const [i, m] of matches.entries()) {
    const rawTemplate = m[1].slice(1, -1);
    const start = (m.index ?? 0) + m[0].length;
    const next = matches[i + 1]?.index ?? clientSrc.length;
    const window = clientSrc.slice(start, Math.min(next, start + 400));
    const methodMatch = window.match(/method:\s*"(GET|PUT|POST|DELETE|PATCH|HEAD)"/);
    // A helper call or variable as the init argument (e.g.
    // `alarmVerbInit(code)`) hides its method from this source-level
    // scan — match those against ANY contract method on the path, which
    // still pins the path itself.
    const helperInit = !methodMatch && /^\s*,\s*[A-Za-z_$][\w$]*\s*\(/.test(window);
    calls.push({
      method: methodMatch ? methodMatch[1] : helperInit ? "ANY" : "GET",
      path: normalize(rawTemplate),
      snippet: rawTemplate,
    });
  }
  return calls;
}

/** Wildcard-aware path equality: `{p}` matches any single segment. */
function pathsMatch(a: string, b: string): boolean {
  const sa = a.split("/").filter(Boolean);
  const sb = b.split("/").filter(Boolean);
  if (sa.length !== sb.length) return false;
  return sa.every((s, i) => s === "{p}" || sb[i] === "{p}" || s === sb[i]);
}

describe("REST client ↔ OpenAPI contract", () => {
  const ops = specOperations();

  it("parses a plausible amount of both surfaces", () => {
    // Guards the parsers themselves: if a refactor changes the shape of
    // client.ts or types.generated.ts, this fails loudly instead of the
    // main assertion silently comparing empty sets.
    expect(ops.length).toBeGreaterThan(100);
    expect(clientCalls().length).toBeGreaterThan(100);
  });

  it("every client call matches a contract operation", () => {
    const misses = clientCalls().filter(
      (c) =>
        !ops.some(
          (o) => (c.method === "ANY" || o.method === c.method) && pathsMatch(o.path, c.path),
        ),
    );
    const report = misses.map((c) => `${c.method} ${c.path}   (from \`${c.snippet}\`)`);
    expect(report).toEqual([]);
  });
});
