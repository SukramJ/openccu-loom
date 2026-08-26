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

// TestMarkdownLinksValid walks every Markdown file in the repository
// and fails when a Markdown-syntax link `[text](path.md)` resolves
// to a non-existent file. The check enforces durability of the
// documentation graph — refs into deleted audit-trail docs rot fast
// and leave the survivor with dead links.
//
// What is checked:
//
//   - `[text](relative/or/absolute/path.md)` links inside `.md` files.
//   - `[text](path.md#anchor)` is checked against the file (the anchor
//     fragment is stripped before resolution; we do not validate
//     anchor existence — that would require a Markdown parser).
//   - Relative paths are resolved against the linking file's
//     directory.
//   - Absolute paths (leading `/`) are resolved against the repo root.
//
// What is NOT checked (out of scope by design):
//
//   - Bare path tokens (`see master_tracker.md`) without
//     Markdown-link syntax. Those false-positive on prose.
//   - External URLs (`http://`, `https://`, `mailto:`).
//   - Broken anchor fragments inside an existing file.
//   - Drift-IDs, audit dates, German words. Those rules belong only
//     in production code (see [TestDocPurity]); Markdown docs are the
//     home of audit-tracking metadata.
//
// Exclusions (skipped paths):
//
//   - `node_modules/` and `spa_dist/` — vendored assets.
//   - `.git/` — out of scope.
//   - `.claude/` — agent worktrees are checkouts of other branches; their
//     links are that branch's problem, not this commit's.
//
// Rationale for the broader Markdown rule set being smaller than
// [TestDocPurity]:
//
// `notes/parity/by_design.md` is the audit-trail itself — Drift-IDs,
// audit dates, "parity sweep"
// references all belong there. Banning them in Markdown would break
// the documents they are designed to populate. The one rule that
// transfers cleanly is link integrity: a broken `[text](path.md)`
// link is a stale pointer regardless of what kind of document holds
// it.
func TestMarkdownLinksValid(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("abs repo root: %v", err)
	}

	// Markdown link grammar: `[text](url)` where url is everything
	// up to the first whitespace or closing paren. We strip the
	// trailing `#anchor` after extraction.
	//
	// The regexp is intentionally conservative: it matches only the
	// `[...](...)` shape, not Markdown reference-style links
	// `[text][ref]` (those use `[ref]: url` definitions; checking
	// those would require a two-pass parser and the codebase doesn't
	// use them outside one or two CHANGELOG snippets).
	linkRE := regexp.MustCompile(`\[(?:[^\]]*)\]\(([^)\s]+)\)`)

	type brokenLink struct {
		file    string
		lineNo  int
		target  string
		display string // truncated line for the error report
	}
	var broken []brokenLink

	err = filepath.Walk(repoRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// Directory exclusions.
		if info.IsDir() {
			base := info.Name()
			// `.claude` holds agent worktrees: full checkouts of other
			// branches, whose broken links belong to those branches. Walking
			// into them turns somebody else's work-in-progress into a
			// blocking finding on an unrelated commit, which is exactly what
			// happened when a docs-restructure worktree contributed 60
			// failures to a change that touched none of them.
			if base == "node_modules" || base == "spa_dist" || base == ".git" || base == ".claude" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}

		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		baseDir := filepath.Dir(path)

		for lineNo, rawLine := range strings.Split(string(content), "\n") {
			line := rawLine
			matches := linkRE.FindAllStringSubmatch(line, -1)
			for _, m := range matches {
				target := m[1]

				// Filter out external + scheme links.
				if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") ||
					strings.HasPrefix(target, "mailto:") || strings.HasPrefix(target, "tel:") ||
					strings.HasPrefix(target, "ftp://") {
					continue
				}
				// Pure in-page anchor — no path to resolve.
				if strings.HasPrefix(target, "#") {
					continue
				}
				// Strip a trailing `#anchor` fragment.
				if idx := strings.Index(target, "#"); idx >= 0 {
					target = target[:idx]
				}
				if target == "" {
					continue
				}
				// Resolve.
				var candidate string
				if strings.HasPrefix(target, "/") {
					candidate = filepath.Join(repoRoot, target)
				} else {
					candidate = filepath.Join(baseDir, target)
				}
				// Allow either a file or a directory to satisfy the
				// link target — some Markdowns link to a folder
				// (e.g. `[ADRs](docs/adr/)`).
				if _, statErr := os.Stat(candidate); statErr == nil {
					continue
				}
				broken = append(broken, brokenLink{
					file:    path,
					lineNo:  lineNo + 1,
					target:  target,
					display: strings.TrimSpace(rawLine),
				})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %q: %v", repoRoot, err)
	}

	if len(broken) == 0 {
		return
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "markdown_links: %d broken `[text](path)` link(s) found:\n\n", len(broken))
	for _, b := range broken {
		// Trim very long display lines.
		display := b.display
		if len(display) > 160 {
			display = display[:160] + "…"
		}
		fmt.Fprintf(&sb, "  %s:%d → %s\n    %s\n\n", b.file, b.lineNo, b.target, display)
	}
	sb.WriteString("Either restore the referenced file or remove the link.\n")
	sb.WriteString("Markdown docs may carry audit-tracking metadata that production\n")
	sb.WriteString("code may not (see TestDocPurity), but stale cross-file links rot\n")
	sb.WriteString("the documentation graph for every reader.\n")

	t.Fatal(sb.String())
}
