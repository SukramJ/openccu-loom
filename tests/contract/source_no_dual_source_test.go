// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"reflect"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/calculated"
	"github.com/SukramJ/openccu-loom/internal/model/custom/climate"
	"github.com/SukramJ/openccu-loom/internal/model/custom/cover"
	"github.com/SukramJ/openccu-loom/internal/model/custom/light"
	"github.com/SukramJ/openccu-loom/internal/model/custom/lock"
	"github.com/SukramJ/openccu-loom/internal/model/custom/siren"
	switchdev "github.com/SukramJ/openccu-loom/internal/model/custom/switch"
	"github.com/SukramJ/openccu-loom/internal/model/custom/textdisplay"
	"github.com/SukramJ/openccu-loom/internal/model/custom/valve"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
)

// TestSourceNoDualStatePayload pins ADR 0007's mitigation rule:
// a type that defines an explicit `State()` method MUST NOT
// also tag struct fields with `payload:"state"`. Mixing both means
// the explicit method shadows the struct-tag sweep, leaving silent
// drift between the tagged fields and what the bridge sees.
//
// Same rule applies to `Info` / `Config`.
//
// This test is the lightweight Go equivalent of the custom
// golangci-lint analyser called for in ADR 0007 mitigations §C.
func TestSourceNoDualStatePayload(t *testing.T) {
	t.Parallel()

	// Same set as TestSourceCompletenessAcrossModelLayers — the
	// canonical roster of Source implementors.
	types := []reflect.Type{
		reflect.TypeFor[device.Channel](),

		reflect.TypeFor[generic.Switch](),
		reflect.TypeFor[generic.BinarySensor](),
		reflect.TypeFor[generic.Float](),
		reflect.TypeFor[generic.Integer](),
		reflect.TypeFor[generic.Select](),
		reflect.TypeFor[generic.Button](),
		reflect.TypeFor[generic.Text](),

		reflect.TypeFor[calculated.DewPointSensor](),
		reflect.TypeFor[calculated.DewPointSpreadSensor](),
		reflect.TypeFor[calculated.FrostPointSensor](),
		reflect.TypeFor[calculated.VaporConcentrationSensor](),
		reflect.TypeFor[calculated.EnthalpySensor](),
		reflect.TypeFor[calculated.ApparentTemperatureSensor](),
		reflect.TypeFor[calculated.DerivedBinarySensor](),
		reflect.TypeFor[calculated.OperatingVoltageLevelSensor](),

		reflect.TypeFor[climate.Climate](),
		reflect.TypeFor[cover.Cover](),
		reflect.TypeFor[cover.Blind](),
		reflect.TypeFor[cover.Garage](),
		reflect.TypeFor[lock.Lock](),
		reflect.TypeFor[siren.Siren](),
		reflect.TypeFor[siren.SmokeSiren](),
		reflect.TypeFor[siren.SoundPlayer](),
		reflect.TypeFor[valve.Irrigation](),
		reflect.TypeFor[valve.Modulating](),
		reflect.TypeFor[textdisplay.TextDisplay](),
		reflect.TypeFor[switchdev.Switch](),
		reflect.TypeFor[light.Light](),
		reflect.TypeFor[light.ColorLight](),
		reflect.TypeFor[light.ColorTempLight](),
		reflect.TypeFor[light.FixedColorLight](),
		reflect.TypeFor[light.EffectLight](),
		reflect.TypeFor[light.DRGDaliLight](),

		reflect.TypeFor[hub.Program](),
		reflect.TypeFor[hub.Sysvar](),
		reflect.TypeFor[hub.Update](),
		reflect.TypeFor[hub.AlarmMessages](),
		reflect.TypeFor[hub.ServiceMessages](),
		reflect.TypeFor[hub.InstallMode](),
		reflect.TypeFor[hub.Connectivity](),
		reflect.TypeFor[hub.Inbox](),
		reflect.TypeFor[hub.Metrics](),
		reflect.TypeFor[hub.Hub](),

		reflect.TypeFor[central.Unit](),
		reflect.TypeFor[client.InterfaceClient](),
	}

	checks := []struct {
		method string
		tag    string
	}{
		{method: "Info", tag: "info"},
		{method: "Config", tag: "config"},
		{method: "State", tag: "state"},
	}

	for _, typ := range types {

		ptr := reflect.PointerTo(typ)
		for _, c := range checks {
			if !methodDefinedExplicitly(ptr, c.method) {
				continue
			}
			if fields := taggedFields(typ, c.tag); len(fields) > 0 {
				t.Errorf("type %s defines %s() AND tags fields %v with payload:%q — pick one (ADR 0007 mitigation §C)",
					typ.String(), c.method, fields, c.tag)
			}
		}
	}
}

// methodDefinedExplicitly reports whether the pointer-type has the
// named method declared **on itself**, not promoted from an embedded
// type. The promoted-only case is the legitimate inheritance pattern
// (e.g. light.ColorLight inherits State from *generic.Float
// via *Light); we don't flag those.
func methodDefinedExplicitly(ptr reflect.Type, name string) bool {
	m, ok := ptr.MethodByName(name)
	if !ok {
		return false
	}
	// A method defined on the outer type has Func.Type with the outer
	// type as receiver. A promoted method's receiver path is longer.
	// Cheaper heuristic: compare the receiver type's String() to the
	// outer pointer's String() — when they differ, the method came in
	// via promotion. Mirrors how `go doc` reports promoted methods.
	if m.Func.IsZero() {
		return false
	}
	recv := m.Func.Type().In(0)
	return recv == ptr
}

// taggedFields returns the names of struct fields tagged with
// `payload:"<kind>"` (or `payload:"<kind>,...."`) directly on the
// given struct type. Embedded struct fields are NOT recursed —
// shadowing a tagged field via an inherited type is a separate
// concern.
func taggedFields(typ reflect.Type, kind string) []string {
	if typ.Kind() != reflect.Struct {
		return nil
	}
	var out []string
	for sf := range typ.Fields() {
		tag, ok := sf.Tag.Lookup("payload")
		if !ok || tag == "" {
			continue
		}
		first := tag
		if before, _, ok := strings.Cut(tag, ","); ok {
			first = before
		}
		if first == kind {
			out = append(out, sf.Name)
		}
	}
	return out
}
