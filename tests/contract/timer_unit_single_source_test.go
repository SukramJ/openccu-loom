// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/combined"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// timerUnitDescriptorPath is a paramset description captured from an
// HmIP-MP3P ACOUSTIC_SIGNAL_VIRTUAL_RECEIVER channel. Its DURATION_UNIT
// entry is the only authority in this tree on what the timer unit
// ordinals mean: the wire carries the position in VALUE_LIST, never the
// label.
const timerUnitDescriptorPath = "../../internal/model/custom/siren/testdata/hmip_mp3p_sound_receiver_values.json"

// timerUnitHome is the one file that may declare the timer unit ordinals.
const timerUnitHome = "pkg/hmenum/timer.go"

// timerUnitValueList reads DURATION_UNIT's VALUE_LIST from the captured
// descriptor and returns label → ordinal.
func timerUnitValueList(t *testing.T) map[string]int {
	t.Helper()
	raw, err := os.ReadFile(timerUnitDescriptorPath)
	if err != nil {
		t.Fatalf("read %s: %v", timerUnitDescriptorPath, err)
	}
	var doc map[string]struct {
		Type      string   `json:"TYPE"`
		ValueList []string `json:"VALUE_LIST"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", timerUnitDescriptorPath, err)
	}
	desc, ok := doc["DURATION_UNIT"]
	if !ok {
		t.Fatalf("%s no longer describes DURATION_UNIT — the guard lost its source",
			timerUnitDescriptorPath)
	}
	if desc.Type != "ENUM" || len(desc.ValueList) == 0 {
		t.Fatalf("DURATION_UNIT in %s is TYPE=%q with %d VALUE_LIST entries; "+
			"an ordinal only means something for a non-empty ENUM",
			timerUnitDescriptorPath, desc.Type, len(desc.ValueList))
	}
	out := make(map[string]int, len(desc.ValueList))
	for i, label := range desc.ValueList {
		out[label] = i
	}
	return out
}

// TestTimerUnitOrdinalsComeFromTheDeviceValueList pins both timer encoders
// against the device's own DURATION_UNIT VALUE_LIST.
//
// The CCU takes a duration as a (value, unit) pair, and two encoders in
// this tree produce that pair for the same channel: custom.EncodeTimerDuration
// on the service path and combined.RecalcUnit on the combined-write path. If
// they disagree about which ordinal means minutes, one requested duration
// reaches the device as sixty times another — with no wire error, because both
// ordinals are valid ENUM positions.
//
// Neither side is compared against a literal or against the other. Both are
// compared against the position of the label in the captured VALUE_LIST, so
// the test still bites when both copies drift the same way, and the ordinals
// in pkg/hmenum are checked against the same external source rather than
// against themselves.
//
// The limit: "10MS" sits at ordinal 3 of that VALUE_LIST and neither encoder
// can emit it, so this guard says nothing about sub-second durations.
func TestTimerUnitOrdinalsComeFromTheDeviceValueList(t *testing.T) {
	t.Parallel()
	list := timerUnitValueList(t)

	for _, c := range []struct {
		label string
		got   hmenum.TimerUnit
	}{
		{"S", hmenum.TimerUnitSeconds},
		{"M", hmenum.TimerUnitMinutes},
		{"H", hmenum.TimerUnitHours},
	} {
		want, ok := list[c.label]
		if !ok {
			t.Fatalf("DURATION_UNIT VALUE_LIST in %s carries no %q entry",
				timerUnitDescriptorPath, c.label)
		}
		if int(c.got) != want {
			t.Errorf("hmenum timer unit for %q = %d, but the device's VALUE_LIST "+
				"puts %q at ordinal %d (%s)", c.label, int(c.got), c.label, want,
				timerUnitDescriptorPath)
		}
	}

	for _, c := range []struct {
		name  string
		d     time.Duration
		label string
	}{
		{"below the promotion threshold", 30 * time.Second, "S"},
		{"at the promotion threshold", 16343 * time.Second, "S"},
		{"one second past it", 16344 * time.Second, "M"},
		{"past the minutes threshold", 1_000_000 * time.Second, "H"},
		{"the disabled-timer sentinel", time.Duration(custom.TimerNotUsed) * time.Second, "H"},
	} {
		want := list[c.label]
		_, encoded := custom.EncodeTimerDuration(c.d)
		if int(encoded) != want {
			t.Errorf("%s: custom.EncodeTimerDuration(%v) chose unit ordinal %d; "+
				"the device's VALUE_LIST puts %q at %d",
				c.name, c.d, encoded, c.label, want)
		}
		_, recalced := combined.RecalcUnit(c.d.Seconds())
		if int(recalced) != want {
			t.Errorf("%s: combined.RecalcUnit(%v) chose unit ordinal %d; "+
				"the device's VALUE_LIST puts %q at %d",
				c.name, c.d.Seconds(), int(recalced), c.label, want)
		}
	}
}

// TestTimerUnitOrdinalsAreDeclaredOnlyInHmenum fails when a package under
// internal/ or pkg/ declares timer unit names of its own instead of reading
// them from pkg/hmenum.
//
// internal/model/combined carried its own TimerUnit type and three constants
// while internal/model/custom spelled the same ordinals as bare literals; the
// two were coupled by nothing but both authors having read the same
// VALUE_LIST.
//
// The limit: this finds a redeclared *name*, not a re-spelled ordinal. A bare
// 1 in an encoder escapes it, which is why the sibling test above drives both
// encoders against the descriptor instead of trusting the names.
func TestTimerUnitOrdinalsAreDeclaredOnlyInHmenum(t *testing.T) {
	t.Parallel()
	const root = "../.."
	var found []string

	for _, sub := range []string{"internal", "pkg"} {
		err := filepath.Walk(filepath.Join(root, sub), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
				return err //nolint:wrapcheck // walk error is returned as-is
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr //nolint:wrapcheck // path error is returned as-is
			}
			file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if parseErr != nil {
				t.Errorf("parse %s: %v", rel, parseErr)
				return nil
			}
			ast.Inspect(file, func(n ast.Node) bool {
				var names []*ast.Ident
				switch spec := n.(type) {
				case *ast.TypeSpec:
					names = []*ast.Ident{spec.Name}
				case *ast.ValueSpec:
					names = spec.Names
				default:
					return true
				}
				for _, id := range names {
					if strings.HasPrefix(id.Name, "TimerUnit") {
						found = append(found, filepath.ToSlash(rel)+": "+id.Name)
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", sub, err)
		}
	}

	if len(found) == 0 {
		t.Fatalf("no TimerUnit declaration exists under internal/ or pkg/ — "+
			"the guard lost its subject; it expects them in %s", timerUnitHome)
	}
	sawHome := false
	for _, decl := range found {
		if strings.HasPrefix(decl, timerUnitHome+": ") {
			sawHome = true
			continue
		}
		t.Errorf("%s declares a timer unit name of its own; read it from "+
			"hmenum instead, where it is declared in %s", decl, timerUnitHome)
	}
	if !sawHome {
		t.Fatalf("%s no longer declares the timer units — if their home moved, "+
			"move timerUnitHome with it", timerUnitHome)
	}
}
