// @vitest-environment node
import { describe, it, expect } from "vitest";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

// The page frame, the page title and the tab strip each used to be
// hand-rolled per view, and they drifted: 20 routes constrained the content
// to max-w-6xl while three ran full width and two picked their own; six views
// rendered an <h1> a weight lighter than the shared header; five tab strips
// disagreed on height, alignment and active colour. Sharing the components
// fixed it once — this keeps it fixed, because nothing else fails when the
// next view opens with its own <section class="mx-auto max-w-6xl …">.

const ROUTES = join(__dirname);

// Full-screen centred flows own their viewport and deliberately have no page
// frame: the login card and the first-run wizard.
const NO_FRAME = new Set(["Login.svelte", "Setup.svelte"]);

function read(rel: string): string {
  return readFileSync(join(ROUTES, rel), "utf8");
}

/** Route components that own a page: routes/*.svelte plus the three tab shells. */
function topLevelRoutes(): string[] {
  const flat = readdirSync(ROUTES)
    .filter((f) => f.endsWith(".svelte") && !f.includes(".test."))
    .filter((f) => !NO_FRAME.has(f));
  return [
    ...flat,
    "alarm/Alarm.svelte",
    "matter/Matter.svelte",
    "security/Security.svelte",
  ];
}

/** The sub-views rendered inside a tab shell, which must not add a second frame. */
function subViews(): string[] {
  const out: string[] = [];
  for (const dir of ["alarm", "matter", "security"]) {
    for (const f of readdirSync(join(ROUTES, dir))) {
      if (!f.endsWith(".svelte") || f.includes(".test.")) continue;
      if (["Alarm.svelte", "Matter.svelte", "Security.svelte"].includes(f)) continue;
      out.push(`${dir}/${f}`);
    }
  }
  return out;
}

describe("page frame", () => {
  it("every route renders through PageShell, never its own container", () => {
    const offenders: string[] = [];
    for (const rel of topLevelRoutes()) {
      const src = read(rel);
      if (!src.includes("<PageShell")) offenders.push(`${rel}: no <PageShell>`);
      // A root <section> carrying the frame's own utilities is the shape that
      // drifted; PageShell owns width and padding now.
      const rootFrame = /^<section[^>]*\bclass="[^"]*\b(max-w-\w+|px-\d+|py-\d+)\b/m;
      if (rootFrame.test(src)) offenders.push(`${rel}: hand-rolled root container`);
    }
    expect(offenders).toEqual([]);
  });

  it("a tab sub-view does not add a second frame", () => {
    const offenders = subViews().filter((rel) => read(rel).includes("<PageShell"));
    expect(offenders).toEqual([]);
  });
});

describe("page title", () => {
  it("no view hand-rolls an <h1>; PageHeader owns it", () => {
    const offenders: string[] = [];
    for (const rel of [...topLevelRoutes(), ...subViews()]) {
      if (read(rel).includes("<h1")) offenders.push(rel);
    }
    expect(offenders).toEqual([]);
  });
});

describe("tab strips", () => {
  it("no view hand-rolls a tablist; Tabs owns it", () => {
    const offenders: string[] = [];
    for (const rel of [...topLevelRoutes(), ...subViews()]) {
      if (read(rel).includes('role="tablist"')) offenders.push(rel);
    }
    expect(offenders).toEqual([]);
  });

  it("no view hand-rolls the underline strip either", () => {
    // Checking for role="tablist" alone missed two strips that never carried
    // the role — a <nav> of buttons, each with border-b-2 for the underline.
    // The visual marker is what makes it a tab strip to an operator, so that
    // is what the guard reads.
    const offenders: string[] = [];
    for (const rel of [...topLevelRoutes(), ...subViews()]) {
      if (read(rel).includes("border-b-2")) offenders.push(rel);
    }
    expect(offenders).toEqual([]);
  });
});

describe("form controls", () => {
  it("no view drops to a raw <select>; Select owns it", () => {
    // A raw select is ~38px tall next to a 40px button, carries its own copy
    // of a long class chain, and can only be named by a `title` tooltip.
    // Scoped to routes/: the shared table's filter row is deliberately native
    // and compact, and the device-control widgets under lib/ are their own
    // question.
    const offenders: string[] = [];
    for (const rel of [...topLevelRoutes(), ...subViews()]) {
      if (read(rel).includes("<select")) offenders.push(rel);
    }
    expect(offenders).toEqual([]);
  });
});
