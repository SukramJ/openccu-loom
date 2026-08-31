// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"compress/gzip"
	"encoding/json"
	"io/fs"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/store/linkprofile"
)

// profilesArchiveConstraintTypes is the set of constraint_type values the
// profile archive is allowed to carry. A value outside this set means the
// data source grew a constraint kind no matcher in this repo evaluates, so
// the guard fails rather than letting the new kind decode into a zero value.
var profilesArchiveConstraintTypes = map[string]bool{"fixed": true, "list": true, "range": true}

// profilesArchiveDoc is one decoded archive file: senderChannelType ->
// {"profiles": [ … ]}, with each profile's params left as raw JSON so the
// guard can compare the bytes against what the typed struct models.
type profilesArchiveDoc map[string]struct {
	Profiles []struct {
		ID     int                        `json:"id"`
		Params map[string]json.RawMessage `json:"params"`
	} `json:"profiles"`
}

// TestProfilesArchiveConstraintGrammarIsFullyDecoded pins the typed
// constraint struct against the archive it decodes.
//
// A JSON field the struct does not model does not fail to decode — it
// decodes to the zero value, silently. When that field carries the bounds
// of a range constraint or the members of a list constraint, every matcher
// reading the struct sees an unconstrained parameter and can only conclude
// that no profile applies. The failure is invisible at the decode site and
// surfaces as "no profile active" far away from it.
//
// So the expectation here comes from the archive bytes, never from a
// literal in this file: every key the archive emits on a constraint object
// must survive a round-trip through the typed struct with the same decoded
// value, and every constraint_type must be one the matchers evaluate.
func TestProfilesArchiveConstraintGrammarIsFullyDecoded(t *testing.T) {
	files := profilesArchiveFiles(t)
	if len(files) == 0 {
		t.Fatal("profile archive is empty: the guard would pass vacuously")
	}

	constraints := 0
	seenTypes := map[string]int{}

	for _, name := range files {
		doc := profilesArchiveDecode(t, name)
		for senderType, bucket := range doc {
			for _, prof := range bucket.Profiles {
				for param, raw := range prof.Params {
					constraints++

					var want map[string]any
					if err := json.Unmarshal(raw, &want); err != nil {
						t.Fatalf("%s/%s id=%d %s: decode raw constraint: %v", name, senderType, prof.ID, param, err)
					}
					ct, _ := want["constraint_type"].(string)
					if !profilesArchiveConstraintTypes[ct] {
						t.Errorf("%s/%s id=%d %s: constraint_type %q is not one the matchers evaluate",
							name, senderType, prof.ID, param, ct)
						continue
					}
					seenTypes[ct]++

					var typed linkprofile.ParamConstraint
					if err := json.Unmarshal(raw, &typed); err != nil {
						t.Fatalf("%s/%s id=%d %s: decode into linkprofile.ParamConstraint: %v",
							name, senderType, prof.ID, param, err)
					}
					reencoded, err := json.Marshal(typed)
					if err != nil {
						t.Fatalf("%s/%s id=%d %s: re-encode: %v", name, senderType, prof.ID, param, err)
					}
					var got map[string]any
					if err := json.Unmarshal(reencoded, &got); err != nil {
						t.Fatalf("%s/%s id=%d %s: decode re-encoded constraint: %v",
							name, senderType, prof.ID, param, err)
					}

					if !reflect.DeepEqual(got, want) {
						t.Errorf("%s/%s id=%d %s: linkprofile.ParamConstraint does not model the archive constraint\n"+
							" archive: %s\n typed  : %s\n missing/changed keys: %s",
							name, senderType, prof.ID, param, raw, reencoded,
							profilesArchiveKeyDiff(want, got))
					}
				}
			}
		}
	}

	if constraints == 0 {
		t.Fatal("no constraints found in the profile archive: the guard would pass vacuously")
	}
	for ct := range profilesArchiveConstraintTypes {
		if seenTypes[ct] == 0 {
			t.Errorf("archive carries no %q constraint: the round-trip for that kind is untested", ct)
		}
	}
	t.Logf("checked %d constraints across %d archives (%v)", constraints, len(files), seenTypes)
}

// profilesArchiveFiles lists the .json.gz archive basenames served by the
// shared metadata module, skipping the alias table alongside them.
func profilesArchiveFiles(t *testing.T) []string {
	t.Helper()
	entries, err := fs.ReadDir(ccudata.ProfilesFS(), ".")
	if err != nil {
		t.Fatalf("read profile archive dir: %v", err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json.gz") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// profilesArchiveDecode reads one gzipped archive into its raw form.
func profilesArchiveDecode(t *testing.T, name string) profilesArchiveDoc {
	t.Helper()
	f, err := ccudata.ProfilesFS().Open(name)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gunzip %s: %v", name, err)
	}
	defer func() { _ = gz.Close() }()
	var doc profilesArchiveDoc
	if err := json.NewDecoder(gz).Decode(&doc); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return doc
}

// profilesArchiveKeyDiff names the keys that differ between the archive
// object and the round-tripped one, so a failure says which field is
// missing from the struct rather than only that the two differ.
func profilesArchiveKeyDiff(want, got map[string]any) string {
	var diff []string
	for k, v := range want {
		g, ok := got[k]
		switch {
		case !ok:
			diff = append(diff, k+" (absent after round-trip)")
		case !reflect.DeepEqual(v, g):
			diff = append(diff, k+" (value changed)")
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			diff = append(diff, k+" (added by round-trip)")
		}
	}
	sort.Strings(diff)
	return strings.Join(diff, ", ")
}
