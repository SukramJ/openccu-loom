// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// changelogFiles are the three release-note surfaces that reach users
// verbatim: the repository history, and the two add-on changelogs Home
// Assistant renders in its add-on store and Update view.
var changelogFiles = []string{
	"CHANGELOG.md",
	filepath.Join("packaging", "ha-addon", "openccu-loom", "CHANGELOG.md"),
	filepath.Join("packaging", "ha-addon", "openccu-loom-remote", "CHANGELOG.md"),
}

// germanFunctionWords are whole words that carry no meaning of their own
// and therefore only appear when a sentence is written in German. A term
// borrowed into English prose ("a direct link (Direktverknüpfung)") brings
// none of them along, which is what keeps this list from firing on the
// device names, CCU strings and quoted daemon output the changelogs are
// full of.
//
// Words that are also English words (die, hat, man, war, so, in, an) are
// deliberately absent — their presence says nothing about the language of
// the sentence around them.
var germanFunctionWords = []string{
	"aber", "alle", "als", "auch", "beide", "beim", "damit", "dann",
	"das", "dass", "dem", "den", "der", "des", "diese", "diesem",
	"dieser", "dieses", "durch", "ein", "eine", "einem", "einen",
	"einer", "eines", "für", "hier", "ihre", "ihrer", "immer", "ist",
	"jetzt", "kann", "kein", "keine", "keinen", "können", "mehr", "mit",
	"muss", "müssen", "nach", "nicht", "noch", "nur", "oder", "schon",
	"sich", "sie", "sind", "soll", "sowie", "statt", "über", "und",
	"vom", "von", "weil", "wenn", "werden", "wieder", "wird", "wurde",
	"wurden", "zum", "zur",
}

// germanWordPattern matches any word from germanFunctionWords, case-insensitively.
var germanWordPattern = regexp.MustCompile(`(?i)\b(` + strings.Join(germanFunctionWords, "|") + `)\b`)

// quotedOrCodeSpan matches the parts of a line that legitimately hold
// non-English text: inline code spans and quoted strings in the three
// quote styles the changelogs use. Whatever a CCU, a device or Home
// Assistant literally says gets reproduced as-is, and that is not a
// language defect.
var quotedOrCodeSpan = regexp.MustCompile("`[^`]*`" + `|"[^"]*"|„[^“”"]*[“”"]|“[^”]*”`)

// germanProseThreshold is how many distinct function words a block must
// carry before it counts as a German sentence rather than an English one
// with a borrowed term in it. Two is enough: German prose reaches it in
// its first clause, while a borrowed noun phrase never does.
const germanProseThreshold = 2

// TestChangelogsAreEnglish fails when a changelog block is written in
// German. All three changelogs are English by convention; the add-on ones
// drifted into German for two releases (0.58.2, 0.58.3) because nothing
// checked them — the release workflow only ever reads the repository
// CHANGELOG.md, and no test read any of them.
//
// Scanning is per block (one bullet or paragraph), not per line, so a
// wrapped sentence is judged as a whole. Fenced code, block quotes,
// inline code spans and quoted strings are excluded: they reproduce what
// some other system said and stay in whatever language that system used.
func TestChangelogsAreEnglish(t *testing.T) {
	t.Parallel()

	repoRoot := filepath.Join("..", "..")

	for _, rel := range changelogFiles {
		t.Run(rel, func(t *testing.T) {
			t.Parallel()

			raw, err := os.ReadFile(filepath.Join(repoRoot, rel))
			if err != nil {
				t.Fatalf("read %s: %v", rel, err)
			}

			for _, b := range germanBlocks(string(raw)) {
				t.Errorf(
					"%s:%d: block under %q reads as German (%s)\n%s\n\n"+
						"Changelogs are English. Quote a CCU or Home Assistant string "+
						"verbatim if you need one, but write the sentence around it in English.",
					rel, b.lineNo, b.section, strings.Join(b.words, ", "), b.text,
				)
			}
		})
	}
}

// changelogBlock is one bullet or paragraph that reads as German prose.
type changelogBlock struct {
	section string   // the version heading it sits under
	lineNo  int      // 1-based line number of the block's first line
	text    string   // the block as written, for the failure message
	words   []string // the distinct function words that identified it
}

// germanBlocks splits a changelog into blocks and returns those whose
// prose — everything outside code, block quotes and quoted strings —
// carries at least germanProseThreshold distinct German function words.
func germanBlocks(content string) []changelogBlock {
	var (
		blocks    []changelogBlock
		section   = "(no version heading)"
		inFence   bool
		lines     = strings.Split(content, "\n")
		blockFrom int
		blockRaw  []string
		blockPros []string
	)

	flush := func() {
		defer func() { blockRaw, blockPros = nil, nil }()
		if len(blockRaw) == 0 {
			return
		}
		words := distinctGermanWords(strings.Join(blockPros, " "))
		if len(words) < germanProseThreshold {
			return
		}
		blocks = append(blocks, changelogBlock{
			section: section,
			lineNo:  blockFrom,
			text:    strings.Join(blockRaw, "\n"),
			words:   words,
		})
	}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			flush()
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}

		// A blank line, a heading or a new bullet ends the previous block.
		isBullet := strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || isBullet {
			flush()
		}
		if trimmed == "" {
			continue
		}
		if h := strings.TrimLeft(trimmed, "#"); len(h) != len(trimmed) {
			section = strings.TrimSpace(h)
			continue
		}
		// Block quotes reproduce daemon or CCU output verbatim.
		if strings.HasPrefix(trimmed, ">") {
			continue
		}

		if len(blockRaw) == 0 {
			blockFrom = i + 1
		}
		blockRaw = append(blockRaw, line)
		blockPros = append(blockPros, quotedOrCodeSpan.ReplaceAllString(line, " "))
	}
	flush()

	return blocks
}

// distinctGermanWords returns the set of German function words in prose,
// lower-cased and deduplicated, in the order they first appear.
func distinctGermanWords(prose string) []string {
	var (
		seen  = map[string]bool{}
		words []string
	)
	for _, m := range germanWordPattern.FindAllString(prose, -1) {
		w := strings.ToLower(m)
		if seen[w] {
			continue
		}
		seen[w] = true
		words = append(words, w)
	}
	return words
}
