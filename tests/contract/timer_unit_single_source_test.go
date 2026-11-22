// SPDX-License-Identifier: Apache-2.0
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
// descriptor and returns it, with label → ordinal.
func timerUnitValueList(t *testing.T) (valueList []string, ordinals map[string]int) {
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
	return desc.ValueList, out
}

// TestTimerUnitOrdinalsComeFromTheDeviceValueList pins the timer encoder
// against the device's own DURATION_UNIT VALUE_LIST.
//
// The CCU takes a duration as a (value, unit) pair, and the unit is a position
// in the parameter's VALUE_LIST. A wrong ordinal is not a wire error — every
// position is a valid ENUM value — so one requested duration reaches the
// device as sixty times another, or a hundredth of it.
//
// Neither the encoder nor pkg/hmenum is compared against a literal. Both are
// compared against the position of the label in the captured VALUE_LIST: the
// encoder built from that list, the encoder of a channel that declares no
// list (which falls back to the hmenum ordinals), and the decoder of each,
// which has to read every ordinal back as the unit the list puts there.
func TestTimerUnitOrdinalsComeFromTheDeviceValueList(t *testing.T) {
	t.Parallel()
	valueList, list := timerUnitValueList(t)

	for _, c := range []struct {
		label string
		got   hmenum.TimerUnit
	}{
		{"S", hmenum.TimerUnitSeconds},
		{"M", hmenum.TimerUnitMinutes},
		{"H", hmenum.TimerUnitHours},
		{"10MS", hmenum.TimerUnit10ms},
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

	declared := custom.DurationUnitsFromList(valueList)
	undeclared := custom.DurationUnits{}
	for _, c := range []struct {
		name            string
		d               time.Duration
		label           string
		undeclaredLabel string
	}{
		{"a sub-second duration", 500 * time.Millisecond, "10MS", "S"},
		{"below the promotion threshold", 30 * time.Second, "S", "S"},
		{"at the promotion threshold", 16343 * time.Second, "S", "S"},
		{"one second past it", 16344 * time.Second, "M", "M"},
		{"past the minutes threshold", 1_000_000 * time.Second, "H", "H"},
		{"the disabled-timer sentinel", time.Duration(custom.TimerNotUsed) * time.Second, "H", "H"},
	} {
		for _, e := range []struct {
			units custom.DurationUnits
			what  string
			label string
		}{
			{declared, "the descriptor's list", c.label},
			{undeclared, "no declared list", c.undeclaredLabel},
		} {
			want := list[e.label]
			value, unit := e.units.Encode(c.d)
			if int(unit) != want {
				t.Errorf("%s with %s: Encode(%v) chose unit ordinal %d; "+
					"the device's VALUE_LIST puts %q at %d",
					c.name, e.what, c.d, unit, e.label, want)
			}
			seconds, ok := e.units.Seconds(float64(value), unit)
			if !ok {
				t.Errorf("%s with %s: Seconds(%d, %d) does not know the ordinal it encoded",
					c.name, e.what, value, unit)
			}
			wantSeconds := map[string]float64{
				"10MS": float64(value) / 100, "S": float64(value),
				"M": float64(value) * 60, "H": float64(value) * 3600,
			}[e.label]
			if seconds != wantSeconds {
				t.Errorf("%s with %s: (%d, %d) reads back as %vs; the list puts %q there",
					c.name, e.what, value, unit, seconds, e.label)
			}
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
// 1 in an encoder escapes it, which is why the sibling test above drives the
// encoder and the decoder against the descriptor instead of trusting the names.
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
