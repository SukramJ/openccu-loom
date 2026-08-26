// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The repository keeps two document trees, and the boundary between them is
// the directory, not a list:
//
//   - `docs/` is the published site. Everything in it is built by MkDocs and
//     served from GitHub Pages, so every page needs a nav entry and every
//     relative link has to stay inside the tree.
//   - `notes/` is the engineering working set — audits, concepts, parity
//     fixtures, plans, contributor procedures. MkDocs never sees it.
//
// The split used to live in `mkdocs.yml`'s `exclude_docs` list, which rotted
// exactly the way an allowlist does: pages were added to `docs/` without being
// added to the list, and published pages accumulated links into excluded
// trees. Those links resolve on disk — so [TestMarkdownLinksValid] stays green
// — but 404 on the built site, because MkDocs only copies `docs_dir`.
//
// `mkdocs build --strict` in `.github/workflows/docs.yml` is the authoritative
// guard. The two tests below reproduce its two most valuable checks in Go so a
// developer gets the failure from `make test`, without the Python toolchain
// installed.

// docsDirLinkRE matches the `[text](target)` shape. Reference-style links
// (`[text][ref]`) are not used in this repo's published pages.
var docsDirLinkRE = regexp.MustCompile(`\[(?:[^\]]*)\]\(([^)\s]+)\)`)

// TestPublishedDocsLinksStayInsideDocsDir fails when a page under `docs/`
// links to a file outside `docs/` with a relative path.
//
// MkDocs copies only `docs_dir` into the site, so `../CLAUDE.md` or
// `../notes/parity/by_design.md` renders as a link that 404s for every reader
// of the published site, while looking perfectly valid in the repo. The fix is
// never to move the target into `docs/` reflexively — a working document does
// not become user documentation by being linked. Link it as an absolute repo
// URL instead:
//
//	[`by_design.md`](https://github.com/SukramJ/openccu-loom/blob/main/notes/parity/by_design.md)
//
// which resolves both on the site and when the file is read on GitHub.
func TestPublishedDocsLinksStayInsideDocsDir(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("abs repo root: %v", err)
	}
	docsDir := filepath.Join(repoRoot, "docs")

	type escape struct {
		file   string
		lineNo int
		target string
	}
	var escapes []escape

	err = filepath.Walk(docsDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		baseDir := filepath.Dir(path)

		for lineNo, rawLine := range strings.Split(string(content), "\n") {
			for _, m := range docsDirLinkRE.FindAllStringSubmatch(rawLine, -1) {
				target := m[1]
				switch {
				case strings.HasPrefix(target, "http://"), strings.HasPrefix(target, "https://"),
					strings.HasPrefix(target, "mailto:"), strings.HasPrefix(target, "tel:"),
					strings.HasPrefix(target, "ftp://"), strings.HasPrefix(target, "#"):
					continue
				}
				if idx := strings.Index(target, "#"); idx >= 0 {
					target = target[:idx]
				}
				if target == "" {
					continue
				}
				var resolved string
				if strings.HasPrefix(target, "/") {
					resolved = filepath.Join(repoRoot, target)
				} else {
					resolved = filepath.Join(baseDir, target)
				}
				rel, relErr := filepath.Rel(docsDir, resolved)
				if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					relFile, _ := filepath.Rel(repoRoot, path)
					escapes = append(escapes, escape{file: relFile, lineNo: lineNo + 1, target: m[1]})
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %q: %v", docsDir, err)
	}

	if len(escapes) == 0 {
		return
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "published docs: %d link(s) leave docs_dir and will 404 on the site:\n\n", len(escapes))
	for _, e := range escapes {
		fmt.Fprintf(&sb, "  %s:%d → %s\n", e.file, e.lineNo, e.target)
	}
	sb.WriteString("\nMkDocs publishes only docs/. Replace each target with an absolute\n")
	sb.WriteString("repo URL (https://github.com/SukramJ/openccu-loom/blob/main/<path>),\n")
	sb.WriteString("which works on the site and on GitHub. Do not move a working\n")
	sb.WriteString("document into docs/ just to satisfy this test — see notes/README.md\n")
	sb.WriteString("for which tree a document belongs in.\n")

	t.Fatal(sb.String())
}

// TestEveryPublishedDocIsInTheNav fails when a Markdown file under `docs/` is
// missing from `mkdocs.yml`'s nav.
//
// A page that is not in the nav is still built and still reachable by URL, but
// nothing links to it: it is published without being findable, which is the
// worst of both trees. `mkdocs build --strict` rejects it in CI; this test
// reports the same thing locally by checking that each page's `docs/`-relative
// path appears literally in `mkdocs.yml`.
//
// The match is textual on purpose — parsing the nav would mean pulling a YAML
// dependency into the contract suite for a check whose whole value is that it
// is cheap and runs in `make test`.
func TestEveryPublishedDocIsInTheNav(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("abs repo root: %v", err)
	}
	docsDir := filepath.Join(repoRoot, "docs")

	config, err := os.ReadFile(filepath.Join(repoRoot, "mkdocs.yml"))
	if err != nil {
		t.Fatalf("read mkdocs.yml: %v", err)
	}
	mkdocs := string(config)

	var missing []string
	err = filepath.Walk(docsDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			// `hooks/` holds the MkDocs build hook, not content.
			if info.Name() == "hooks" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		rel, relErr := filepath.Rel(docsDir, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if !strings.Contains(mkdocs, ": "+rel) {
			missing = append(missing, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %q: %v", docsDir, err)
	}

	if len(missing) == 0 {
		return
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "published docs: %d page(s) under docs/ are absent from the mkdocs.yml nav:\n\n", len(missing))
	for _, m := range missing {
		fmt.Fprintf(&sb, "  docs/%s\n", m)
	}
	sb.WriteString("\nAdd a nav entry, or move the document under notes/ if it is a working\n")
	sb.WriteString("document rather than user-facing documentation. Everything left in\n")
	sb.WriteString("docs/ is published — that is the whole contract of the split.\n")

	t.Fatal(sb.String())
}

// The ADR index claims to catalogue every ADR: "The table below catalogues
// every ADR." Nothing checked that, and it drifted three entries behind —
// 0063, 0064 and 0065 were all missing while the sentence stayed. The nav
// guard above does not help: a new ADR reaches the nav because it must, to be
// published at all, and the index is a separate page whose rows nobody has to
// touch.
//
// A reader who trusts the sentence and finds no row concludes the ADR does not
// exist. That is the failure this guards against.
func TestEveryADRIsInTheIndex(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("abs repo root: %v", err)
	}

	indexPath := filepath.Join(repoRoot, "docs", "developer", "adr-index.md")
	index, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read %s: %v", indexPath, err)
	}

	adrs, err := filepath.Glob(filepath.Join(repoRoot, "docs", "adr", "[0-9]*.md"))
	if err != nil {
		t.Fatalf("glob ADRs: %v", err)
	}
	if len(adrs) == 0 {
		t.Fatal("found no ADRs under docs/adr/ — the glob is wrong, not the index")
	}

	// A row looks like: | [0042](../adr/0042-clear-ccu-cache-and-repull.md) | … |
	rowFor := func(number, file string) *regexp.Regexp {
		return regexp.MustCompile(`(?m)^\|\s*\[` + regexp.QuoteMeta(number) +
			`\]\(\.\./adr/` + regexp.QuoteMeta(file) + `\)\s*\|`)
	}

	var missing []string
	for _, path := range adrs {
		file := filepath.Base(path)
		number, _, found := strings.Cut(file, "-")
		if !found {
			t.Fatalf("ADR filename %q does not start with a number followed by '-'", file)
		}
		if !rowFor(number, file).MatchString(string(index)) {
			missing = append(missing, file)
		}
	}

	if len(missing) == 0 {
		return
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "ADR index: %d of %d ADR(s) have no row in docs/developer/adr-index.md:\n\n",
		len(missing), len(adrs))
	for _, m := range missing {
		fmt.Fprintf(&sb, "  docs/adr/%s\n", m)
	}
	sb.WriteString("\nThe page states that it catalogues every ADR, so a missing row reads as\n")
	sb.WriteString("\"this decision was never recorded\". Add a row:\n\n")
	sb.WriteString("  | [NNNN](../adr/NNNN-slug.md) | Title | One sentence on the decision. |\n")

	t.Fatal(sb.String())
}
