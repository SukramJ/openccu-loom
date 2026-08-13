// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The daemon is the only entity-naming authority: a north-bound adapter
// renders the name it is handed and composes nothing itself. These guards
// pin the two ways that rule has been broken in practice — a second
// implementation of the multi-channel postfix, and a second writer of the
// translated parameter name.
//
// The defect they prevent is invisible from either side. Both spellings
// look right in their own test, and only a consumer holding both sees
// that one data point arrives in Home Assistant twice under two names:
// FROST_PROTECTION on an HmIP-BWTH (channels 1 and 8, named channels)
// was "Frostschutz" over REST and "Frostschutz ch1" over MQTT discovery,
// because the MQTT copy of the postfix rule tested two of the authority's
// three conditions.

// postfixBuilderExemptions lists files allowed to build the `" chN"`
// parameter postfix, with the reason. The authority is the only entry.
var postfixBuilderExemptions = map[string]string{
	"internal/model/device/namedata.go": "the naming authority — this is where the postfix rule lives",
}

// translatedParameterWriterExemptions lists files allowed to assign
// NameData.TranslatedParameterName, with the reason.
var translatedParameterWriterExemptions = map[string]string{
	"internal/model/naming/namedata.go": "NameData.WithTranslatedParameter — the single composer of translation + postfix",
	"internal/model/device/namedata.go": "BuildCustomDataPointName carries the custom-DP postfix translation, a separate rule with its own channel-group marker (ch/vch)",
}

// TestTheMultiChannelPostfixHasOneImplementation asserts that no file
// outside the naming authority formats the `" chN"` parameter postfix.
//
// A second implementation does not fail loudly; it drifts. The MQTT
// discovery path carried one for several releases with two of the
// authority's three conditions, so it appended the postfix on channels
// the authority had decided needed none.
func TestTheMultiChannelPostfixHasOneImplementation(t *testing.T) {
	t.Parallel()
	checked := 0
	offenders := forEachProductionGoFile(t, func(rel, src string) bool {
		checked++
		if _, exempt := postfixBuilderExemptions[rel]; exempt {
			return false
		}
		// The leading space is what makes it the parameter postfix: the
		// bare "ch%d" form is the custom-DP / press-event channel-group
		// marker, which is a different rule with its own tests.
		return strings.Contains(src, `" ch%d"`) || strings.Contains(src, `" ch" +`)
	})
	if checked == 0 {
		t.Fatal("no production files were scanned — the walk is broken and this test would pass vacuously")
	}
	for _, rel := range offenders {
		t.Errorf("%s builds the \" chN\" parameter postfix, but %s is the naming authority.\n"+
			"  A second implementation of this rule drifts from the first, and the drift is only "+
			"visible to a consumer holding both names. Take the postfix from the NameData the "+
			"authority built (NameData.ChannelPostfix, or NameData.WithTranslatedParameter).",
			rel, "internal/model/device/namedata.go")
	}
	for rel := range postfixBuilderExemptions {
		if _, err := os.Stat(filepath.Join(repoRootForHelpers(t), rel)); err != nil {
			t.Errorf("postfixBuilderExemptions names %q, which no longer exists — a stale exemption "+
				"silently allows the next real copy", rel)
		}
	}
}

// TestOnlyTheAuthorityWritesTheTranslatedParameterName asserts that
// NameData.TranslatedParameterName is assigned only where the naming
// rules live.
//
// Writing the field elsewhere is how a caller re-decides the postfix
// without looking like it: the assignment reads as "apply the
// translation", and the postfix decision rides along in the composed
// string.
func TestOnlyTheAuthorityWritesTheTranslatedParameterName(t *testing.T) {
	t.Parallel()
	checked := 0
	offenders := forEachProductionGoFile(t, func(rel, src string) bool {
		checked++
		if _, exempt := translatedParameterWriterExemptions[rel]; exempt {
			return false
		}
		for _, assignment := range []string{
			"TranslatedParameterName =",
			"TranslatedParameterName:",
		} {
			if strings.Contains(src, assignment) {
				return true
			}
		}
		return false
	})
	if checked == 0 {
		t.Fatal("no production files were scanned — the walk is broken and this test would pass vacuously")
	}
	for _, rel := range offenders {
		t.Errorf("%s assigns NameData.TranslatedParameterName.\n"+
			"  Only the naming package composes a translation with the multi-channel postfix "+
			"(NameData.WithTranslatedParameter). Assigning the field directly re-decides the "+
			"postfix in passing, which is what made MQTT discovery and REST disagree.", rel)
	}
	for rel := range translatedParameterWriterExemptions {
		if _, err := os.Stat(filepath.Join(repoRootForHelpers(t), rel)); err != nil {
			t.Errorf("translatedParameterWriterExemptions names %q, which no longer exists — a stale "+
				"exemption silently allows the next real writer", rel)
		}
	}
}

// forEachProductionGoFile calls match for every non-test Go file under
// internal/ and cmd/ (repo-relative path + source) and returns the paths
// it matched, sorted by walk order.
func forEachProductionGoFile(t *testing.T, match func(rel, src string) bool) []string {
	t.Helper()
	root := repoRootForHelpers(t)
	var hits []string
	for _, tree := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, tree), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case "node_modules", "spa_dist", "testdata":
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			if match(filepath.ToSlash(rel), string(src)) {
				hits = append(hits, filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", tree, err)
		}
	}
	return hits
}
