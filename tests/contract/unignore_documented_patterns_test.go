// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/store/visibility"
)

// unIgnoreExampleFiles are the files that show an operator what an
// un-ignore pattern looks like.
var unIgnoreExampleFiles = []string{
	"example.config.full.yaml",
	filepath.Join("notes", "concepts", "ui", "unignore-concept.md"),
}

var (
	// unIgnoreKeyLine finds the key whose list items are the examples.
	unIgnoreKeyLine = regexp.MustCompile(`^\s*#?\s*un_ignore:`)
	// unIgnoreListItem matches a YAML list item, commented out or not,
	// and captures whatever is quoted. It deliberately accepts ANY
	// content: an extractor that only matched well-formed patterns could
	// never catch a malformed one, which is the whole point here.
	unIgnoreListItem = regexp.MustCompile(`^\s*#?\s*-\s*"([^"]*)"\s*$`)
	// unIgnoreBlockEnd is a line that is neither a list item nor blank
	// nor a bare comment, and therefore ends the example block.
	unIgnoreBlockEnd = regexp.MustCompile(`^\s*[^\s#-]`)
)

// documentedUnIgnorePatterns pulls every example pattern out of one file
// by finding each `un_ignore:` key and reading the list items under it.
func documentedUnIgnorePatterns(body string) []string {
	var out []string
	lines := strings.Split(body, "\n")
	for i := 0; i < len(lines); i++ {
		if !unIgnoreKeyLine.MatchString(lines[i]) {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if m := unIgnoreListItem.FindStringSubmatch(lines[j]); m != nil {
				out = append(out, strings.TrimSpace(m[1]))
				continue
			}
			if unIgnoreBlockEnd.MatchString(lines[j]) {
				i = j
				break
			}
		}
	}
	return out
}

// TestDocumentedUnIgnorePatternsParse feeds every example pattern the
// documentation shows through the parser that has to accept it.
//
// This exists because they did not. Both the reference config and the
// concept document taught `MODEL:CHANNEL:PARAMETER` — `"*:*:RSSI_PEER"`,
// `"HmIP-eTRV-2:0:LOW_BAT"` — a form the parser rejects outright with
// "':' without '@'". The accepted forms are a bare `PARAMETER` and the
// fully-qualified `PARAMETER:PARAMSET@MODEL:CHANNEL`. Four documents, the
// config field's own comment and the REST contract all carried the wrong
// grammar, and an operator following any of them got a silently empty
// un-ignore list: parse failures are counted and logged, never surfaced
// as a config error.
func TestDocumentedUnIgnorePatternsParse(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	total := 0
	for _, path := range unIgnoreExampleFiles {
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		patterns := documentedUnIgnorePatterns(string(data))
		if len(patterns) == 0 {
			t.Errorf("%s: no example pattern found; the extraction stopped matching "+
				"and this guard is measuring nothing", path)
			continue
		}
		for _, pattern := range patterns {
			total++
			parsed := visibility.ParseUnIgnoreLine(pattern)
			switch {
			case parsed.Err != "":
				t.Errorf("%s: the documented pattern %q is rejected by the parser: %s",
					path, pattern, parsed.Err)
			case parsed.Entry == nil:
				t.Errorf("%s: the documented pattern %q parses to no entry, so it un-ignores nothing",
					path, pattern)
			}
		}
	}
	// Negative control: with no pattern extracted anywhere, every
	// assertion above is vacuous.
	if total == 0 {
		t.Fatal("no documented un-ignore pattern was extracted from any source")
	}
}
