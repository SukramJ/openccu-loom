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

	walkRepoMarkdown(t, repoRoot, func(path string, content []byte) {
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
	})

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

// walkRepoMarkdown calls visit once per Markdown file in the repository,
// applying the directory exclusions the Markdown guards in this file share.
// The exclusion list lives here rather than in each guard so a second guard
// cannot walk a set the first one deliberately skips — `.claude` above all,
// which holds agent worktrees of other branches (see [TestMarkdownLinksValid]
// for what each exclusion protects).
func walkRepoMarkdown(t *testing.T, repoRoot string, visit func(path string, content []byte)) {
	t.Helper()

	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			base := info.Name()
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
		visit(path, content)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %q: %v", repoRoot, err)
	}
}

// goFabricURLRE matches every https://github.com/SukramJ/go-fabric reference in
// a Markdown file, in link syntax or bare in prose. It is deliberately looser
// than the `[text](url)` grammar [TestMarkdownLinksValid] uses: an extractor
// that only accepts the well-formed shape never sees the malformed one, so this
// one takes everything that starts with the repository URL and lets the parser
// in [TestGoFabricDocLinksResolveInLocalCheckout] decide what is checkable.
var goFabricURLRE = regexp.MustCompile("https://github\\.com/SukramJ/go-fabric(?:/[^\\s)\\]\"'`>|]*)?")

// TestGoFabricDocLinksResolveInLocalCheckout checks the path component of every
// https://github.com/SukramJ/go-fabric/{blob,tree,raw}/main/<path> reference in
// this repository's Markdown against a local go-fabric checkout, and fails when
// such a reference names a path that checkout does not carry.
//
// # Why a guard at all
//
// [TestMarkdownLinksValid] skips absolute URLs. That was harmless while every
// cross-reference was a repository path: a moved file broke the link in the
// same commit that moved it, and the guard caught it there. The Matter stack is
// its own module now and the documentation points into it by URL, so those
// references rot with no commit here — nothing in this repository moves when
// go-fabric does, and the reader finds a 404 long before any test does.
//
// # Why not simply fetch the URL
//
// A fetch measures the real thing — the ref, the path and the anchor at once —
// and would be the honest check if it could run. It cannot: a gate that needs
// the network goes red on a proxy, a rate limit, an offline developer or a
// GitHub outage, and a gate that fails for reasons unrelated to the change
// stops being read. This project would rather have no gate than a flaky one.
//
// # What is therefore measured, and what is not
//
// Measured, offline: the path component. A `/blob/main/<path>` or
// `/tree/main/<path>` URL names a file or directory in go-fabric's tree, and a
// developer working across both repositories has that tree checked out beside
// this one. A file renamed, moved or deleted upstream is caught.
//
// NOT measured, and stated rather than implied:
//
//   - Whether the *ref* exists. Only `main` links are checked at all, and they
//     are checked against a working tree that may sit ahead of or behind
//     origin/main. A link to a tag or a commit SHA is skipped: the working tree
//     is not that ref, so resolving the path against it would answer a
//     different question than the one the URL asks.
//   - Whether an `#anchor` still exists. The fragment is stripped, exactly as
//     [TestMarkdownLinksValid] strips it — validating it needs a Markdown
//     parser and, for a Go file, a symbol resolver.
//   - Non-path URLs (`/issues/…`, `/pull/…`, `/compare/…`, `/releases/…`) and
//     the bare repository URL. Nothing in a local checkout answers for those.
//
// # Why it skips rather than fails when it cannot measure
//
// The checkout is not a build input: `make test` must pass for a developer who
// has never cloned go-fabric, and CI clones only this repository. A missing —
// or wrong — checkout therefore means "this check did not run", not "the links
// are broken". A skip says that in the one place a reader looks; a failure
// would say something the check has no evidence for. The skip is reported with
// the path that was tried so it is visible rather than silent.
func TestGoFabricDocLinksResolveInLocalCheckout(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("abs repo root: %v", err)
	}

	checkout, why := goFabricCheckout(repoRoot)
	if checkout == "" {
		t.Skipf("go-fabric checkout unavailable, link paths not measured: %s", why)
	}

	type badRef struct {
		file   string
		lineNo int
		url    string
		target string
	}
	var bad []badRef
	checked, skipped := 0, 0

	walkRepoMarkdown(t, repoRoot, func(path string, content []byte) {
		for lineNo, rawLine := range strings.Split(string(content), "\n") {
			for _, url := range goFabricURLRE.FindAllString(rawLine, -1) {
				target, ok := goFabricRepoPath(url)
				if !ok {
					skipped++
					continue
				}
				checked++
				if _, statErr := os.Stat(filepath.Join(checkout, filepath.FromSlash(target))); statErr == nil {
					continue
				}
				bad = append(bad, badRef{file: path, lineNo: lineNo + 1, url: url, target: target})
			}
		}
	})

	t.Logf("go-fabric references: %d path-bearing on main (checked against %s), %d not checkable offline", checked, checkout, skipped)

	if len(bad) == 0 {
		return
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "go-fabric doc links: %d reference(s) name a path the local checkout does not have:\n\n", len(bad))
	for _, b := range bad {
		fmt.Fprintf(&sb, "  %s:%d → %s\n    missing in %s: %s\n\n", b.file, b.lineNo, b.url, checkout, b.target)
	}
	sb.WriteString("The file was renamed, moved or deleted in go-fabric, or the link was\n")
	sb.WriteString("mistyped. Point the link at the current path.\n")
	sb.WriteString("If instead the local checkout is stale, update it and re-run — this\n")
	sb.WriteString("check resolves against the working tree, not against origin/main.\n")

	t.Fatal(sb.String())
}

// goFabricRepoPath extracts the repository-relative path a go-fabric URL names.
// ok is false for every URL whose target a local checkout cannot answer for:
// the bare repository URL, a non-path route (`/issues/…`, `/pull/…`), and any
// `blob`/`tree`/`raw` reference to a ref other than `main` — see
// [TestGoFabricDocLinksResolveInLocalCheckout] for why the ref is not guessed.
func goFabricRepoPath(url string) (string, bool) {
	const prefix = "https://github.com/SukramJ/go-fabric"
	tail := strings.TrimPrefix(url, prefix)
	// A trailing sentence period or comma belongs to the prose, not the URL.
	tail = strings.TrimRight(tail, ".,;:")
	if idx := strings.Index(tail, "#"); idx >= 0 {
		tail = tail[:idx]
	}
	tail = strings.Trim(tail, "/")
	if tail == "" {
		return "", false
	}
	segments := strings.Split(tail, "/")
	if len(segments) < 3 {
		return "", false
	}
	switch segments[0] {
	case "blob", "tree", "raw":
	default:
		return "", false
	}
	if segments[1] != "main" {
		return "", false
	}
	return strings.Join(segments[2:], "/"), true
}

// goFabricCheckout locates the sibling go-fabric working tree. It returns an
// empty path plus the reason when there is none to measure against, so the
// caller can skip with something a reader can act on.
//
// The location is either the GO_FABRIC_DIR environment variable or the sibling
// directory beside this repository — the layout CLAUDE.md's reference table
// documents for every companion checkout. A directory that is not go-fabric
// (wrong sibling, stale name) is rejected by reading its go.mod module line
// rather than trusting the directory name: resolving paths against the wrong
// tree would report confident failures about links that are fine.
func goFabricCheckout(repoRoot string) (dir, why string) {
	candidate := os.Getenv("GO_FABRIC_DIR")
	source := "GO_FABRIC_DIR"
	if candidate == "" {
		candidate = filepath.Join(filepath.Dir(repoRoot), "go-fabric")
		source = "sibling checkout"
	}
	gomod, err := os.ReadFile(filepath.Join(candidate, "go.mod"))
	if err != nil {
		return "", fmt.Sprintf("%s %q has no readable go.mod (%v)", source, candidate, err)
	}
	if !strings.Contains(string(gomod), "module github.com/SukramJ/go-fabric") {
		return "", fmt.Sprintf("%s %q is not the go-fabric module", source, candidate)
	}
	return candidate, ""
}
