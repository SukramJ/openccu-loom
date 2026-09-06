// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The chiptool workflow runs the only real-commissioner guard this project
// has, and a label is a thing a human has to remember. Its `changes` job
// therefore starts the suite automatically when a pull request touches code
// the suite covers, deciding that with a list of path patterns.
//
// A path list rots silently: the patterns keep matching nothing after a
// rename, the job keeps reporting "no matter paths touched", and every
// Matter change from then on ships without the commissioner ever running.
// That is not hypothetical here — the Matter stack used to live under
// internal/north/matter/, which is exactly the path the roadmap still names
// and which no longer exists.
//
// So this pins two things the workflow cannot check about itself: that every
// pattern still addresses something real, and that the patterns actually
// classify a representative set of files the way they claim to.
const chiptoolWorkflow = ".github/workflows/chiptool.yml"

// chiptoolPathPatterns extracts the `-e '<regexp>'` arguments from the
// changes job's grep invocation. Reading them out of the workflow rather
// than restating them is the point: a copy here would pass while the
// workflow said something else.
func chiptoolPathPatterns(t *testing.T) []string {
	t.Helper()

	root := repoRootFromTestFile(t)
	raw, err := os.ReadFile(filepath.Join(root, chiptoolWorkflow))
	if err != nil {
		t.Fatalf("read %s: %v", chiptoolWorkflow, err)
	}
	pats := regexp.MustCompile(`-e '([^']+)'`).FindAllStringSubmatch(string(raw), -1)
	if len(pats) == 0 {
		t.Fatalf("%s carries no `-e '<pattern>'` arguments — either the changes job lost its "+
			"path filter, or it was rewritten in a shape this guard can no longer read. Both are "+
			"reasons to look, because a filter that matches nothing silently stops running the "+
			"commissioner suite", chiptoolWorkflow)
	}
	out := make([]string, 0, len(pats))
	for _, m := range pats {
		out = append(out, m[1])
	}
	return out
}

// TestChiptoolPathFilterClassifiesFilesCorrectly is the behaviour half: the
// patterns must fire for files that can change what a commissioner sees, and
// stay quiet for files that cannot. The quiet cases are the ones that matter
// — a filter that matches everything runs a 30-minute arm64 suite on every
// documentation typo, which gets it disabled, which is the same outcome as
// not having it.
func TestChiptoolPathFilterClassifiesFilesCorrectly(t *testing.T) {
	t.Parallel()
	pats := chiptoolPathPatterns(t)

	matches := func(path string) bool {
		for _, p := range pats {
			if regexp.MustCompile(p).MatchString(path) {
				return true
			}
		}
		return false
	}

	for _, tc := range []struct {
		path string
		want bool
		why  string
	}{
		{"internal/north/matteradapter/assembler.go", true, "the host-side model walk"},
		{"internal/store/matterendpoint/store.go", true, "endpoint identity across restarts"},
		{"internal/model/custom/siren/sound_matter.go", true, "a device's Matter projection"},
		{"internal/model/generic/select_matter.go", true, "a generic data point's projection"},
		{"cmd/openccu-loom/daemon_matter.go", true, "the composition root of the bridge"},
		{"tests/chiptool/harness/parser.go", true, "the suite's own harness"},
		{"compose/matter-smoke.yml", true, "the pinned chip-tool build"},
		{".github/workflows/chiptool.yml", true, "the workflow itself, so a change to it is tested by it"},
		{"go.mod", true, "the Matter stack is an external module now; a bump moves wire behaviour"},
		{"go.sum", true, "same reason as go.mod"},

		{"README.md", false, "documentation cannot change commissioning"},
		{"internal/north/rest/handlers/info.go", false, "the REST surface is not on the Matter path"},
		{"assets/ui/src/lib/i18n.ts", false, "the SPA is not on the Matter path"},
		{"internal/model/custom/light/color.go", false, "a device model file with no Matter projection in it"},
	} {
		if got := matches(tc.path); got != tc.want {
			verb := "did not match"
			if got {
				verb = "matched"
			}
			t.Errorf("%s %s the chiptool path filter, want the opposite (%s)", tc.path, verb, tc.why)
		}
	}
}

// TestChiptoolPathFilterPatternsAddressSomethingReal is the rot half. Every
// pattern is anchored on a repo path; if that path no longer exists, the
// pattern is dead and the filter has quietly narrowed.
//
// It checks the literal prefix of each pattern rather than trying to
// interpret the regexp: a prefix is what a rename breaks.
func TestChiptoolPathFilterPatternsAddressSomethingReal(t *testing.T) {
	t.Parallel()
	root := repoRootFromTestFile(t)

	for _, pat := range chiptoolPathPatterns(t) {
		prefix := literalPrefix(pat)
		if prefix == "" {
			t.Errorf("pattern %q has no literal prefix to check — it cannot be verified against the tree", pat)
			continue
		}
		// A prefix ending in "/" names a directory, and that directory has to
		// exist: falling back to its parent is what made an earlier cut of
		// this test miss the case it exists for — "internal/north/matter/"
		// passed because "internal/north" was there. A prefix that does not
		// end in "/" is the leading part of a filename, so its directory is
		// the most that can be checked.
		target := filepath.Join(root, prefix)
		if _, err := os.Stat(target); err == nil {
			continue
		}
		if !strings.HasSuffix(prefix, "/") {
			if _, err := os.Stat(filepath.Dir(target)); err == nil {
				continue
			}
		}
		t.Errorf("chiptool path filter pattern %q addresses %q, which does not exist. A pattern that "+
			"matches nothing does not fail the workflow — it makes it report 'no matter paths touched' "+
			"forever, and the commissioner suite stops running on Matter changes", pat, prefix)
	}
}

// literalPrefix returns the leading part of an anchored regexp that contains
// no metacharacters — the part a rename would invalidate.
func literalPrefix(pat string) string {
	p := strings.TrimPrefix(pat, "^")
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		// An escape is a literal, not a metacharacter: `\.github` is the
		// directory .github, and treating the backslash as the end of the
		// prefix would leave that pattern unverifiable.
		if p[i] == '\\' && i+1 < len(p) {
			i++
			b.WriteByte(p[i])
			continue
		}
		if strings.IndexByte(`.*+?()[]{}|$`, p[i]) >= 0 {
			break
		}
		b.WriteByte(p[i])
	}
	// Trim back to the last path separator so "internal/model/" survives a
	// pattern like "^internal/model/.*matter.*\.go$" while "internal/mod"
	// from a hypothetical truncation does not masquerade as a real path.
	out := b.String()
	if i := strings.LastIndex(out, "/"); i >= 0 {
		return out[:i+1]
	}
	return out
}

// TestChiptoolTriggerFiresOnAFreshPullRequest pins the event list, which
// neither test above can see: they check what the `changes` job decides,
// and that job only runs if the workflow started at all.
//
// A pull request fires `opened` and nothing else when it is created. Without
// that type the workflow does not start on the one event every pull request
// begins with — the changes job never runs, the suite never starts, and no
// path pattern inside it matters. `synchronize` covers it on the next push,
// but "the next push" means the first review happens with no commissioner run
// behind it, and a pull request that is merged as opened gets none at all.
//
// This is not hypothetical twice over. The automatic trigger shipped without
// `opened` in the sibling Matter module and failed to trigger itself; the
// same list was ported here and did the same thing on the release pull
// request that touched the Matter assembler.
//
// Read textually rather than through a YAML parser: the repository has no
// YAML dependency in its test path, and adding one to read four words would
// cost more than it explains.
func TestChiptoolTriggerFiresOnAFreshPullRequest(t *testing.T) {
	t.Parallel()

	root := repoRootFromTestFile(t)
	raw, err := os.ReadFile(filepath.Join(root, chiptoolWorkflow))
	if err != nil {
		t.Fatalf("read %s: %v", chiptoolWorkflow, err)
	}
	m := regexp.MustCompile(`(?m)^\s*types:\s*\[([^\]]*)\]`).FindSubmatch(raw)
	if m == nil {
		t.Fatalf("%s carries no `types: [...]` list under pull_request — either it stopped "+
			"triggering on pull requests entirely, or it was rewritten in a shape this guard "+
			"cannot read. Both stop the commissioner from running on Matter changes",
			chiptoolWorkflow)
	}
	have := strings.Split(strings.ReplaceAll(string(m[1]), " ", ""), ",")

	for _, want := range []string{"opened", "labeled", "synchronize", "reopened"} {
		found := false
		for _, h := range have {
			if h == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("pull_request types %v is missing %q — without it the workflow does not start "+
				"on that event, and every path pattern inside it is moot", have, want)
		}
	}
}
