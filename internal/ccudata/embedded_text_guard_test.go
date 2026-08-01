// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ccudata

import (
	"bytes"
	"encoding/json"
	"regexp"
	"testing"
)

// htmlReferencePattern matches a named or numeric HTML character reference.
// Deliberately narrow so a bare ampersand in prose ("on/off & louder") does
// not register — only a complete reference is a defect.
var htmlReferencePattern = regexp.MustCompile(`&(?:[a-zA-Z][a-zA-Z0-9]{1,10}|#\d{1,5});`)

// TestEmbeddedLoadersDeliverPlainText is the standing guard behind the
// per-store decoding: whatever a loader hands to the daemon must be plain
// text, not the CCU WebUI's HTML fragments.
//
// It works on each loader's *result*, re-serialised to JSON. That is the
// exact set of fields the Go structures decode, so an archive that carries
// references in a field nothing reads stays green — while a future archive
// refresh (or a new field added to a struct) that pulls references into the
// daemon fails here instead of surfacing in an operator's dropdown.
func TestEmbeddedLoadersDeliverPlainText(t *testing.T) {
	t.Parallel()

	profiles, err := LoadProfilesEmbedded()
	if err != nil {
		t.Fatalf("LoadProfilesEmbedded: %v", err)
	}
	translations, err := LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("LoadTranslationsEmbedded: %v", err)
	}
	easymode, err := LoadEasymodeEmbedded()
	if err != nil {
		t.Fatalf("LoadEasymodeEmbedded: %v", err)
	}

	for _, tc := range []struct {
		loader string
		result any
	}{
		{"LoadProfilesEmbedded", profiles},
		{"LoadTranslationsEmbedded", translations},
		{"LoadEasymodeEmbedded", easymode},
	} {
		t.Run(tc.loader, func(t *testing.T) {
			t.Parallel()
			// SetEscapeHTML(false) is load-bearing: the default encoder
			// rewrites '&' to &, which would hide every reference
			// from the pattern below and make this guard pass on anything.
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.SetEscapeHTML(false)
			if err := enc.Encode(tc.result); err != nil {
				t.Fatalf("re-serialise %s result: %v", tc.loader, err)
			}
			found := htmlReferencePattern.FindAllString(buf.String(), 6)
			if len(found) > 0 {
				t.Errorf("%s delivers %d HTML reference(s), e.g. %q — decode them at the load site",
					tc.loader, len(found), found)
			}
		})
	}
}
