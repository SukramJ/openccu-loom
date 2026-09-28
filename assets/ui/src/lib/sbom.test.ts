import { describe, it, expect } from "vitest";
import { parseSBOM, searchRows } from "./sbom";

describe("parseSBOM", () => {
  it("parses a realistic small CycloneDX document into rows", () => {
    const doc = {
      bomFormat: "CycloneDX",
      specVersion: "1.6",
      components: [
        {
          "bom-ref": "pkg:npm/vite@5.4.0",
          type: "library",
          name: "vite",
          version: "5.4.0",
          author: "Evan You",
          purl: "pkg:npm/vite@5.4.0",
          licenses: [{ license: { id: "MIT" } }],
          externalReferences: [
            { type: "website", url: "https://vitejs.dev" },
            { type: "vcs", url: "https://github.com/vitejs/vite.git" },
            {
              type: "distribution",
              url: "https://registry.npmjs.org/vite/-/vite-5.4.0.tgz",
            },
          ],
        },
        {
          "bom-ref": "pkg:golang/github.com/SukramJ/go-fabric@v0.0.0",
          type: "library",
          name: "github.com/SukramJ/go-fabric",
          version: "v0.0.0",
          purl: "pkg:golang/github.com/SukramJ/go-fabric@v0.0.0?type=module",
          externalReferences: [
            { type: "vcs", url: "https://github.com/SukramJ/go-fabric" },
          ],
        },
      ],
    };

    const rows = parseSBOM(doc);
    expect(rows).toHaveLength(2);

    expect(rows[0]).toEqual({
      key: "pkg:npm/vite@5.4.0",
      name: "vite",
      version: "5.4.0",
      license: "MIT",
      purl: "pkg:npm/vite@5.4.0",
      author: "Evan You",
      links: [
        { type: "website", url: "https://vitejs.dev" },
        { type: "vcs", url: "https://github.com/vitejs/vite.git" },
        {
          type: "distribution",
          url: "https://registry.npmjs.org/vite/-/vite-5.4.0.tgz",
        },
      ],
    });

    // Go module: no license entry, no author — must degrade gracefully
    // rather than throw or fabricate a value.
    expect(rows[1]).toEqual({
      key: "pkg:golang/github.com/SukramJ/go-fabric@v0.0.0",
      name: "github.com/SukramJ/go-fabric",
      version: "v0.0.0",
      license: "",
      purl: "pkg:golang/github.com/SukramJ/go-fabric@v0.0.0?type=module",
      author: "",
      links: [{ type: "vcs", url: "https://github.com/SukramJ/go-fabric" }],
    });
  });

  it("handles a component that only has a name", () => {
    const rows = parseSBOM({ components: [{ name: "bare-package" }] });
    expect(rows).toEqual([
      {
        key: "bare-package@",
        name: "bare-package",
        version: "",
        license: "",
        purl: "",
        author: "",
        links: [],
      },
    ]);
  });

  it("joins an SPDX expression-style license verbatim", () => {
    const rows = parseSBOM({
      components: [
        {
          name: "type-fest",
          version: "4.41.0",
          licenses: [{ expression: "(MIT OR CC0-1.0)" }],
        },
      ],
    });
    expect(rows[0].license).toBe("(MIT OR CC0-1.0)");
  });

  it("joins multiple license entries with a comma", () => {
    const rows = parseSBOM({
      components: [
        {
          name: "dual-licensed",
          licenses: [{ license: { id: "MIT" } }, { license: { name: "Apache-2.0" } }],
        },
      ],
    });
    expect(rows[0].license).toBe("MIT, Apache-2.0");
  });

  it("falls back to publisher when author is absent", () => {
    const rows = parseSBOM({
      components: [{ name: "pkg", publisher: "Some Org" }],
    });
    expect(rows[0].author).toBe("Some Org");
  });

  it("drops externalReferences outside website/vcs/distribution", () => {
    const rows = parseSBOM({
      components: [
        {
          name: "pkg",
          externalReferences: [
            { type: "issue-tracker", url: "https://example.com/issues" },
            { type: "vcs", url: "https://example.com/repo" },
          ],
        },
      ],
    });
    expect(rows[0].links).toEqual([{ type: "vcs", url: "https://example.com/repo" }]);
  });

  it("returns an empty list for a document with no components array", () => {
    expect(parseSBOM({})).toEqual([]);
    expect(parseSBOM(null)).toEqual([]);
    expect(parseSBOM("not an object")).toEqual([]);
    expect(parseSBOM({ components: "not an array" })).toEqual([]);
  });

  it("uses a positional fallback key when name and purl are both absent", () => {
    const rows = parseSBOM({ components: [{}, {}] });
    expect(rows.map((r) => r.key)).toEqual(["component-0", "component-1"]);
  });
});

describe("searchRows", () => {
  const rows = parseSBOM({
    components: [
      {
        name: "vite",
        version: "5.4.0",
        author: "Evan You",
        licenses: [{ license: { id: "MIT" } }],
      },
      {
        name: "github.com/SukramJ/go-fabric",
        version: "v0.0.0",
      },
    ],
  });

  it("returns every row for an empty query", () => {
    expect(searchRows(rows, "")).toHaveLength(2);
    expect(searchRows(rows, "   ")).toHaveLength(2);
  });

  it("matches by name case-insensitively", () => {
    expect(searchRows(rows, "VITE")).toEqual([rows[0]]);
  });

  it("matches by license", () => {
    expect(searchRows(rows, "mit")).toEqual([rows[0]]);
  });

  it("matches by author", () => {
    expect(searchRows(rows, "evan")).toEqual([rows[0]]);
  });

  it("matches by version", () => {
    expect(searchRows(rows, "0.0.0")).toEqual([rows[1]]);
  });

  it("returns no rows when nothing matches", () => {
    expect(searchRows(rows, "does-not-exist")).toEqual([]);
  });
});
