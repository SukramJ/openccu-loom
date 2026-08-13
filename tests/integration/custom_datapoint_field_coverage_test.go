//go:build integration

// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package integration

import (
	"context"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// customFieldsNeverFilled exempts a custom-data-point field that no device
// in the simulated fleet fills because the fleet cannot fill it: the
// parameter is absent from every embedded device description, or another
// field covers the same value for the models the fleet does carry.
//
// The key is "<type>.<field>" as the failure message prints it.
var customFieldsNeverFilled = map[string]string{
	"climate.Climate.humidity": "HmIP thermostats report HUMIDITY as INTEGER (OPERATIONS 5), which the " +
		"resolver projects onto Sensor[int32] — Climate.humidityInt covers every model in the fleet. The " +
		"float accessor exists for wired/BidCos thermostats that report it as FLOAT.",
	"light.FixedColorLight.channelColor": "CHANNEL_COLOR appears in no embedded device description at all, " +
		"so the fleet cannot witness this field either way.",
	"siren.SoundPlayer.direction": "HmIP-MP3P is the only SoundPlayer in the fleet and its description " +
		"carries no DIRECTION parameter.",
}

// customFieldsWithAKnownDefect registers a field the fleet proves is nil
// everywhere *and* whose cause is understood: each entry names the wire
// shape the resolver produces against the shape the accessor asks for.
//
// It is separate from [customFieldsNeverFilled] on purpose. That list says
// "the fleet cannot show this"; this one says "the daemon gets this
// wrong, and here is how". Merging them would let a defect wear the face
// of an exemption. An entry disappears when the field starts being
// filled — the guard reports a listed-but-filled field as an error, so a
// fix cannot leave a stale claim behind.
var customFieldsWithAKnownDefect = map[string]string{
	"cover.Garage.doorCommandDp": "DOOR_COMMAND is ENUM with OPERATIONS 2 (write-only) → the resolver " +
		"yields an ActionSelect, while the field asks for Sensor[string]. A write-only parameter also has " +
		"no readable value, so the field cannot work as a status source at all.",
	"light.RGBWLight.mode": "DEVICE_OPERATION_MODE is a MASTER ENUM with OPERATIONS 3 → Select, while the " +
		"field asks for Sensor[string].",
	"light.SoundPlayerLED.onTimeList": "ON_TIME_LIST_1 is ENUM with OPERATIONS 2 → ActionSelect, while the " +
		"field asks for Sensor[string].",
	"light.SoundPlayerLED.repetitions": "REPETITIONS is ENUM with OPERATIONS 2 → ActionSelect, while the " +
		"field asks for Sensor[string].",
	"siren.Siren.acousticIdx": "ACOUSTIC_ALARM_SELECTION is ENUM with OPERATIONS 2 → ActionSelect, while " +
		"the field asks for Sensor[string]. The siren's acoustic-alarm read-back is therefore absent on " +
		"every device.",
	"siren.Siren.opticalIdx": "OPTICAL_ALARM_SELECTION is ENUM with OPERATIONS 2 → ActionSelect, while the " +
		"field asks for Sensor[string].",
	"siren.SmokeSiren.command": "SMOKE_DETECTOR_COMMAND is ENUM with OPERATIONS 2 → ActionSelect, while " +
		"the field asks for Sensor[string].",
	"siren.SoundPlayer.repetitions": "REPETITIONS is ENUM with OPERATIONS 2 → ActionSelect, while the " +
		"field asks for Sensor[string].",
	"siren.SoundPlayer.soundfile": "HmIP-MP3P carries SOUNDFILE twice — read-only on channel 1 (→ " +
		"Sensor[int32]) and read+write on channel 2 (→ Select). The sound player sits on the writable " +
		"channel, where the Sensor[int32] the field asks for is not what the resolver produced.",
	"lock.Lock.directionDp": "DIRECTION exists only on the HM key-matic family (ENUM, OPERATIONS 5 → " +
		"Sensor[int32] on channel 1), but the field is assigned in the LOCK_STATE branch that HmIP door " +
		"locks take, and those descriptions carry no DIRECTION.",
	"textdisplay.TextDisplay.burstLimitWarningDP": "HmIP-WRCD reports BURST_LIMIT_WARNING on the " +
		"maintenance channel 0, while the lookup runs against the display channel.",
	"light.ColorTempLight.kelvin": "COLOR_TEMPERATURE is INTEGER with OPERATIONS 7 → Integer, which is " +
		"what the field asks for, but it is absent from the channel the colour-temperature light is " +
		"built on for every model in the fleet.",
	"cover.Cover.groupLevel": "assigned through a setter rather than a channel accessor; no production " +
		"path calls that setter for any model in the fleet.",
	"light.Light.groupLevel": "assigned through a setter rather than a channel accessor; no production " +
		"path calls that setter for any model in the fleet.",
	"light.EffectLight.program": "assigned through a setter rather than a channel accessor; no production " +
		"path calls that setter for any model in the fleet.",
}

// TestEveryCustomDataPointFieldIsFilledBySomeDevice drives the real
// hydration pipeline over the whole simulated fleet and asserts that
// every typed data-point field on every custom data point is populated
// for at least one device.
//
// A field that stays nil across the entire fleet is one of two things: a
// wrong accessor, or dead code. Both are invisible at runtime — a
// custom data point with a nil field simply reports the feature as
// unsupported, and every unit test around it passes because it wires the
// field itself. That is how 0.58.0 shipped a motion reset with no effect
// at all: the consumer asked for *generic.Action where the resolver
// produces *generic.Button, so the lookup returned nil on every device.
//
// This is the fleet-wide counterpart to the static reachability guard in
// internal/central/adapter: that one proves a cast *can* never succeed
// from the resolver's rules alone; this one finds the casts that never do
// succeed against real wire descriptors, including the type-driven cases
// (a read-only ENUM read through a string-sensor accessor) that no static
// rule catches.
func TestEveryCustomDataPointFieldIsFilledBySomeDevice(t *testing.T) {
	srv := startMockCCUWithDevices(t, snapshotDevices(t))

	xmlClient := newXMLRPCClient(t, srv.URL())
	backend := backends.NewCcuBackend(&xmlrpcBackendCaller{client: xmlClient}, nil, nil)

	c, err := central.New(central.Config{Name: "field-coverage-ccu"})
	if err != nil {
		t.Fatalf("central: %v", err)
	}
	translations, err := ccudata.LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("translations: %v", err)
	}
	pipeline := adapter.NewDevicePipeline(c).
		WithTranslations(translations, snapshotLocale()).
		WithVisibility(visibility.NewRegistry())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	logger := slog.New(slog.DiscardHandler)
	if err := pipeline.IngestFromBackend(
		ctx, "HmIP-RF", hmenum.InterfaceHmIPRF, backend, nil, nil, logger,
	); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	// filled counts, per "<type>.<field>", how many devices populated it;
	// witness remembers one device per field so a failure names something
	// concrete to look at.
	filled := make(map[string]int)
	witness := make(map[string]string)

	devices := c.ModelRegistry.List()
	if len(devices) == 0 {
		t.Fatal("the fleet hydrated no devices at all — the walk is broken and this test would pass vacuously")
	}
	customDPs := 0
	for _, dev := range devices {
		for _, ch := range dev.Channels() {
			cdp := ch.CustomDataPoint()
			if cdp == nil {
				continue
			}
			customDPs++
			for _, f := range dataPointFields(cdp) {
				key := f.owner + "." + f.name
				if _, seen := filled[key]; !seen {
					filled[key] = 0
					witness[key] = dev.Model
				}
				if !f.nil {
					filled[key]++
					witness[key] = dev.Model
				}
			}
		}
	}
	if customDPs == 0 {
		t.Fatal("no channel in the fleet carries a custom data point — the walk is broken and this test " +
			"would pass vacuously")
	}
	if len(filled) == 0 {
		t.Fatal("no typed data-point fields were discovered by reflection — the walk is broken and this " +
			"test would pass vacuously")
	}
	t.Logf("inspected %d custom data points across %d devices, %d distinct fields",
		customDPs, len(devices), len(filled))

	keys := make([]string, 0, len(filled))
	for k := range filled {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		exemptReason, exempt := customFieldsNeverFilled[key]
		defectReason, knownDefect := customFieldsWithAKnownDefect[key]
		switch {
		case exempt && knownDefect:
			t.Errorf("%s is in both lists — a field is either unwitnessable here or defective, not both", key)
		case filled[key] > 0 && exempt:
			t.Errorf("%s is listed in customFieldsNeverFilled (%q) but %d device(s) fill it (e.g. %s) — "+
				"drop the entry so the list keeps meaning what it says",
				key, exemptReason, filled[key], witness[key])
		case filled[key] > 0 && knownDefect:
			t.Errorf("%s is listed in customFieldsWithAKnownDefect (%q) but %d device(s) now fill it "+
				"(e.g. %s) — the defect is fixed, so drop the entry",
				key, defectReason, filled[key], witness[key])
		case filled[key] == 0 && !exempt && !knownDefect:
			t.Errorf("%s is nil on every device in the fleet (seen on e.g. %s).\n"+
				"  A field that is never populated makes its feature report as unsupported, silently and "+
				"everywhere. Either the accessor asks for a shape the resolver does not produce for that "+
				"parameter, the lookup runs against the wrong channel, or the field is dead. Check which "+
				"shape resolveDataPoint yields for the parameter behind it, then record the verdict: a "+
				"cause you understand goes in customFieldsWithAKnownDefect, a value the fleet simply "+
				"cannot carry in customFieldsNeverFilled.",
				key, witness[key])
		}
	}

	for _, list := range []struct {
		name    string
		entries map[string]string
	}{
		{"customFieldsNeverFilled", customFieldsNeverFilled},
		{"customFieldsWithAKnownDefect", customFieldsWithAKnownDefect},
	} {
		for key := range list.entries {
			if _, known := filled[key]; !known {
				t.Errorf("%s names %q, which is not a field on any custom data point any more — a stale "+
					"entry silently exempts nothing and hides the next real one", list.name, key)
			}
		}
	}
}

// dataPointFieldState is one typed data-point field of a custom data
// point, with whether it is nil on this instance.
type dataPointFieldState struct {
	owner string
	name  string
	nil   bool
}

// dataPointFields reflects over a custom data point and reports every
// field that holds a data point — a pointer to a generic shape, or an
// interface satisfied by one. Unexported fields are included: they are
// where custom data points keep their constituents, and IsNil works on
// them even though Interface does not.
func dataPointFields(cdp any) []dataPointFieldState {
	v := reflect.ValueOf(cdp)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	owner := v.Type().String()

	var out []dataPointFieldState
	for i := range v.NumField() {
		field := v.Type().Field(i)
		fv := v.Field(i)
		switch fv.Kind() {
		case reflect.Pointer:
			// Only the generic shapes — a pointer to a config struct or a
			// registry is not a data point.
			if !strings.HasPrefix(field.Type.String(), "*generic.") {
				continue
			}
		case reflect.Interface:
			// Capability interfaces (generic.ActionTrigger and friends)
			// are the recommended shape for a consumer, so they count too.
			if !strings.HasPrefix(field.Type.String(), "generic.") {
				continue
			}
		default:
			continue
		}
		out = append(out, dataPointFieldState{owner: owner, name: field.Name, nil: fv.IsNil()})
	}
	return out
}
