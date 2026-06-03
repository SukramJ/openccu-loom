// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
		reflect.TypeOf((*device.Channel)(nil)).Elem(),

		reflect.TypeOf((*generic.Switch)(nil)).Elem(),
		reflect.TypeOf((*generic.BinarySensor)(nil)).Elem(),
		reflect.TypeOf((*generic.Float)(nil)).Elem(),
		reflect.TypeOf((*generic.Integer)(nil)).Elem(),
		reflect.TypeOf((*generic.Select)(nil)).Elem(),
		reflect.TypeOf((*generic.Button)(nil)).Elem(),
		reflect.TypeOf((*generic.Text)(nil)).Elem(),

		reflect.TypeOf((*calculated.DewPointSensor)(nil)).Elem(),
		reflect.TypeOf((*calculated.DewPointSpreadSensor)(nil)).Elem(),
		reflect.TypeOf((*calculated.FrostPointSensor)(nil)).Elem(),
		reflect.TypeOf((*calculated.VaporConcentrationSensor)(nil)).Elem(),
		reflect.TypeOf((*calculated.EnthalpySensor)(nil)).Elem(),
		reflect.TypeOf((*calculated.ApparentTemperatureSensor)(nil)).Elem(),
		reflect.TypeOf((*calculated.DerivedBinarySensor)(nil)).Elem(),
		reflect.TypeOf((*calculated.OperatingVoltageLevelSensor)(nil)).Elem(),

		reflect.TypeOf((*climate.Climate)(nil)).Elem(),
		reflect.TypeOf((*cover.Cover)(nil)).Elem(),
		reflect.TypeOf((*cover.Blind)(nil)).Elem(),
		reflect.TypeOf((*cover.Garage)(nil)).Elem(),
		reflect.TypeOf((*lock.Lock)(nil)).Elem(),
		reflect.TypeOf((*siren.Siren)(nil)).Elem(),
		reflect.TypeOf((*siren.SmokeSiren)(nil)).Elem(),
		reflect.TypeOf((*siren.SoundPlayer)(nil)).Elem(),
		reflect.TypeOf((*valve.Irrigation)(nil)).Elem(),
		reflect.TypeOf((*valve.Modulating)(nil)).Elem(),
		reflect.TypeOf((*textdisplay.TextDisplay)(nil)).Elem(),
		reflect.TypeOf((*switchdev.Switch)(nil)).Elem(),
		reflect.TypeOf((*light.Light)(nil)).Elem(),
		reflect.TypeOf((*light.ColorLight)(nil)).Elem(),
		reflect.TypeOf((*light.ColorTempLight)(nil)).Elem(),
		reflect.TypeOf((*light.FixedColorLight)(nil)).Elem(),
		reflect.TypeOf((*light.EffectLight)(nil)).Elem(),
		reflect.TypeOf((*light.DRGDaliLight)(nil)).Elem(),

		reflect.TypeOf((*hub.Program)(nil)).Elem(),
		reflect.TypeOf((*hub.Sysvar)(nil)).Elem(),
		reflect.TypeOf((*hub.Update)(nil)).Elem(),
		reflect.TypeOf((*hub.AlarmMessages)(nil)).Elem(),
		reflect.TypeOf((*hub.ServiceMessages)(nil)).Elem(),
		reflect.TypeOf((*hub.InstallMode)(nil)).Elem(),
		reflect.TypeOf((*hub.Connectivity)(nil)).Elem(),
		reflect.TypeOf((*hub.Inbox)(nil)).Elem(),
		reflect.TypeOf((*hub.Metrics)(nil)).Elem(),
		reflect.TypeOf((*hub.Hub)(nil)).Elem(),

		reflect.TypeOf((*central.Unit)(nil)).Elem(),
		reflect.TypeOf((*client.InterfaceClient)(nil)).Elem(),
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
		typ := typ
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
	for i := 0; i < typ.NumField(); i++ {
		sf := typ.Field(i)
		tag, ok := sf.Tag.Lookup("payload")
		if !ok || tag == "" {
			continue
		}
		first := tag
		if i := strings.IndexByte(tag, ','); i >= 0 {
			first = tag[:i]
		}
		if first == kind {
			out = append(out, sf.Name)
		}
	}
	return out
}
