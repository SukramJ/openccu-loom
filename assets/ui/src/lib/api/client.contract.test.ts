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

/**
 * A contract operation. `body` is the TypeScript type of its 2xx
 * `application/json` response, or null when the operation has none or
 * declares it as a multi-line inline object the line scan cannot read.
 */
type Op = { method: string; path: string; body: string | null };

/**
 * 2xx JSON response type per operationId, read from the `operations`
 * interface at the bottom of the generated file. Operations with an
 * operationId keep their bodies there and are only referenced from the
 * `paths` interface, so the reference has to be resolved through here.
 */
function operationBodies(): Record<string, string> {
  const bodies: Record<string, string> = {};
  let inOperations = false;
  let currentOp = "";
  let inSuccess = false;
  for (const line of generatedSrc.split("\n")) {
    if (/^export interface operations \{/.test(line)) {
      inOperations = true;
      continue;
    }
    if (inOperations && /^\}/.test(line)) break;
    if (!inOperations) continue;
    const op = line.match(/^\s{4}([A-Za-z_$][\w$]*):\s*\{/);
    if (op) {
      currentOp = op[1];
      inSuccess = false;
      continue;
    }
    if (/^\s{12}20\d:\s*\{/.test(line)) {
      inSuccess = true;
      continue;
    }
    if (inSuccess && /^\s{12}\S/.test(line)) inSuccess = false;
    const json = line.match(/^\s{20}"application\/json":\s*(.*);\s*$/);
    if (json && inSuccess && currentOp && !(currentOp in bodies)) {
      bodies[currentOp] = json[1];
      inSuccess = false;
    }
  }
  return bodies;
}

/**
 * Spec operations parsed from the generated OpenAPI types. Path keys
 * sit at 4-space indent inside `interface paths`; each defined method
 * sits at 8-space indent and is either an `operations["id"]` reference
 * (operations with an operationId) or an inline `{ … }` block
 * (operations without one). Undefined methods render as `get?: never`
 * and must not count — the optional `?` keeps them off this regex.
 */
function specOperations(): Op[] {
  const bodies = operationBodies();
  const ops: Op[] = [];
  let currentPath = "";
  let inSuccess = false;
  for (const line of generatedSrc.split("\n")) {
    const p = line.match(/^\s{4}"(\/[^"]*)":\s*\{/);
    if (p) {
      currentPath = p[1];
      continue;
    }
    if (!currentPath) continue;
    const ref = line.match(
      /^\s{8}(get|put|post|delete|patch|head|options):\s*operations\["([^"]+)"\];/,
    );
    if (ref) {
      ops.push({
        method: ref[1].toUpperCase(),
        path: normalize(currentPath),
        body: bodies[ref[2]] ?? null,
      });
      continue;
    }
    const m = line.match(/^\s{8}(get|put|post|delete|patch|head|options):\s*\{/);
    if (m) {
      ops.push({ method: m[1].toUpperCase(), path: normalize(currentPath), body: null });
      inSuccess = false;
      continue;
    }
    if (/^\s{16}20\d:\s*\{/.test(line)) {
      inSuccess = true;
      continue;
    }
    if (inSuccess && /^\s{16}\S/.test(line)) inSuccess = false;
    const json = line.match(/^\s{24}"application\/json":\s*(.*);\s*$/);
    if (json && inSuccess && ops.length > 0) {
      ops[ops.length - 1].body = json[1];
      inSuccess = false;
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

type Call = { method: string; path: string; snippet: string; generic: string };

/**
 * Client calls parsed from client.ts source: every `request(...)`
 * first-argument template, the explicit type argument it was given, and
 * the `method:` of its init object (default GET). The init-object scan
 * is bounded by the next request( occurrence so a nested `headers: {}`
 * cannot leak a method from the following call.
 */
function clientCalls(): Call[] {
  const calls: Call[] = [];
  const re = /\brequest(?:<([^(]*?)>)?\(\s*(`[^`]*`|"[^"]*"|'[^']*')/g;
  const matches = [...clientSrc.matchAll(re)];
  for (const [i, m] of matches.entries()) {
    const rawTemplate = m[2].slice(1, -1);
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
      generic: (m[1] ?? "").trim(),
    });
  }
  return calls;
}

/**
 * True when a TypeScript type denotes a JSON array. `| null` and
 * `| undefined` members are stripped first: the daemon marshals an
 * empty Go slice as `null`, and several client types say so.
 */
function isArrayType(type: string): boolean {
  return type.replace(/\|\s*(null|undefined)/g, "").trim().endsWith("[]");
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

  // The type argument on request() is an unchecked assertion — nothing
  // validates the parsed body against it — so a client that declares a
  // wrapper around a bare-array response typechecks, passes its unit
  // tests against mocks that copy the same mistake, and reads `.items`
  // off an Array at runtime: `undefined`, every time, silently. That
  // shipped on /devices/{addr}/channels and left both channel pickers
  // empty for every device. Comparing array-ness on both sides is the
  // cheapest check that catches the whole class.
  describe("response shapes", () => {
    /** Calls whose path+method resolves to exactly one typed operation. */
    function comparable(): { call: Call; body: string }[] {
      const out: { call: Call; body: string }[] = [];
      for (const call of clientCalls()) {
        if (!call.generic) continue;
        const matched = ops.filter(
          (o) => o.method === call.method && pathsMatch(o.path, call.path),
        );
        // More than one match means a wildcard segment collided with a
        // literal one (`/centrals/{name}` vs `/centrals/discovered`);
        // a null body means the operation has no JSON response or
        // declares it inline across several lines. Neither is a finding.
        if (matched.length !== 1 || matched[0].body === null) continue;
        out.push({ call, body: matched[0].body });
      }
      return out;
    }

    it("compares a plausible share of the surface", () => {
      expect(comparable().length).toBeGreaterThan(60);
    });

    it("declares an array response as an array and an object response as an object", () => {
      const report = comparable()
        .filter(({ call, body }) => isArrayType(body) !== isArrayType(call.generic))
        .map(
          ({ call, body }) =>
            `${call.method} ${call.path}: client \`${call.generic}\` vs contract \`${body}\``,
        );
      expect(report).toEqual([]);
    });
  });
});
