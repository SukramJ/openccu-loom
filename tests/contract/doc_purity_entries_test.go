// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// entryCitation is a `<path>.md <ENTRY-ID>` pair found in a code comment:
// a durable document plus the entry inside it the comment leans on.
type entryCitation struct {
	Ref   string
	Entry string
}

// mdEntryRefRE matches a markdown path followed by a catalogue-entry id.
//
// The pattern is deliberately narrow, because the value of this check is
// in what it does not flag. A markdown path in a comment is followed by
// all kinds of prose, and by section markers (`SPECIFICATION.md §11`)
// whose existence needs a parser to confirm. What is checkable without
// one is the entry shape this repo actually uses: a capitalised token
// carrying at least one hyphenated segment, like `BD-A3-CombinedUnused`.
var mdEntryRefRE = regexp.MustCompile(
	`([A-Za-z0-9_./-]+\.md)\s+([A-Z][A-Za-z0-9]*(?:-[A-Za-z0-9]+)+)`,
)

// markdownEntryCitations extracts every entry citation from one comment
// line.
func markdownEntryCitations(line string) []entryCitation {
	matches := mdEntryRefRE.FindAllStringSubmatch(line, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]entryCitation, 0, len(matches))
	for _, m := range matches {
		out = append(out, entryCitation{Ref: m[1], Entry: m[2]})
	}
	return out
}

// TestMarkdownEntryCitationsAreExtractedNarrowly pins what counts as a
// citation of an entry, because the value of the check is entirely in
// what it does NOT flag.
//
// A markdown path in a comment is followed by all kinds of prose, and by
// section markers (`SPECIFICATION.md §11`) whose existence needs a
// parser. Only the catalogue-entry shape this repo uses is checkable
// without one.
func TestMarkdownEntryCitationsAreExtractedNarrowly(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		line string
		want []entryCitation
	}{
		{
			name: "catalogue entry",
			line: "// See notes/parity/by_design.md BD-A3-CombinedUnused.",
			want: []entryCitation{{Ref: "notes/parity/by_design.md", Entry: "BD-A3-CombinedUnused"}},
		},
		{
			name: "section marker is not an entry",
			line: "// Mirrors SPECIFICATION.md §11 for the callback ports.",
			want: nil,
		},
		{
			name: "plain prose after the path",
			line: "// See notes/parity/by_design.md for the divergence catalogue.",
			want: nil,
		},
		{
			name: "a bare capitalised word is not an entry",
			line: "// See notes/parity/by_design.md Matter divergences.",
			want: nil,
		},
		{
			name: "two citations on one line",
			line: "// by_design.md BD-One-Thing and by_design.md BD-Other-Thing",
			want: []entryCitation{
				{Ref: "by_design.md", Entry: "BD-One-Thing"},
				{Ref: "by_design.md", Entry: "BD-Other-Thing"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := markdownEntryCitations(tc.line)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("citation %d = %v, want %v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestDocPurity_CitedMarkdownEntriesExist closes the half of the
// reference check the file-existence half cannot see.
//
// A comment citing `notes/parity/by_design.md BD-A3-CombinedUnused` reads
// as evidence: someone weighed the divergence and wrote it down. The
// existing guard confirms the file is there and stops, so a citation of
// an entry nobody ever wrote passes. Three comments carried exactly that
// id; the pre-release sweep found them by hand, and the guard was green
// throughout.
func TestDocPurity_CitedMarkdownEntriesExist(t *testing.T) {
	t.Parallel()

	repoRoot := filepath.Join("..", "..")
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		t.Fatalf("abs repo root: %v", err)
	}

	type missing struct {
		file, entry, ref string
		lineNo           int
	}
	var out []missing
	docs := map[string]string{}

	for _, root := range []string{"internal", "pkg", "cmd"} {
		_ = filepath.Walk(filepath.Join(repoRoot, root), func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
				return walkErr
			}
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for lineNo, rawLine := range strings.Split(string(content), "\n") {
				line := strings.TrimSpace(rawLine)
				if !strings.HasPrefix(line, "//") {
					continue
				}
				for _, c := range markdownEntryCitations(line) {
					body, ok := docs[c.Ref]
					if !ok {
						raw, statErr := os.ReadFile(filepath.Join(absRoot, c.Ref))
						if statErr != nil {
							// The file half is the other guard's job.
							docs[c.Ref] = ""
							continue
						}
						body = string(raw)
						docs[c.Ref] = body
					}
					if body == "" || strings.Contains(body, c.Entry) {
						continue
					}
					out = append(out, missing{file: path, lineNo: lineNo + 1, ref: c.Ref, entry: c.Entry})
				}
			}
			return nil
		})
	}

	if len(out) == 0 {
		return
	}
	var sb strings.Builder
	sb.WriteString("doc_purity: comment(s) cite an entry that the referenced document does not contain:\n\n")
	for _, m := range out {
		sb.WriteString("  " + m.file + ":" + strconv.Itoa(m.lineNo) + " cites " + m.entry + " in " + m.ref + "\n")
	}
	sb.WriteString("\nEither write the entry, or drop the citation. A citation of an entry\n")
	sb.WriteString("nobody wrote reads as a decision somebody made, which is worse than\n")
	sb.WriteString("no citation at all.\n")
	t.Fatal(sb.String())
}
