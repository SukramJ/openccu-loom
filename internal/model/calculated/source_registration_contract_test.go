// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package calculated

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// sourceRegistering is the seam this contract measures: every calculated
// sensor embeds [sourceSink], and [sourceSink.snapshotSources] reports what
// its Subscribe actually resolved off the channel.
//
// The measurement goes to the registered set rather than to
// [sourceSink.SourcesValid], because the two failure modes are different and
// only one of them is this contract's subject: SourcesValid is false both for
// "no source was ever registered" and for "a registered source carries an
// unusable reading". Reading the set separates them.
type sourceRegistering interface {
	snapshotSources() []SourceDP
}

// sourceRig is one channel constellation the factory produces sensors for.
// Each rig names the sensor types it is there to cover so a rig that stops
// producing them (a moved relevance predicate, a renamed parameter) fails
// here instead of silently shrinking the guard's reach.
type sourceRig struct {
	name  string
	model string
	build func(t *testing.T) *device.Channel
	// wantTypes are the sensor type names the rig must produce, beyond the
	// ones every temperature/humidity channel yields.
	wantTypes []string
}

// climateSensorTypes are the four sensors any channel exposing temperature
// and humidity yields (factory.go: IsTemperatureHumiditySensorRelevant).
var climateSensorTypes = []string{
	"DewPointSensor",
	"DewPointSpreadSensor",
	"VaporConcentrationSensor",
	"EnthalpySensor",
}

// sourceRigs covers every branch of [CreateCalculatedDataPoints]. The models
// are the ones the relevance tables actually name — HmIP-SWO for apparent
// temperature, HmIP-STHO for frost point, HmIP-SRH for the WINDOW_OPEN
// derived-binary mapping, HM-Sec-RHS for a battery entry — so a table that
// moves takes this guard with it rather than leaving it green on a model the
// factory no longer serves.
func sourceRigs() []sourceRig {
	return []sourceRig{
		{
			name:  "temperature+humidity",
			model: "HmIP-STH",
			build: func(t *testing.T) *device.Channel {
				t.Helper()
				ch := sourceRigChannel(t, "STH0001", "HmIP-STH", 1, "HEATING_CLIMATECONTROL_TRANSCEIVER")
				sourceRigPutFloat(ch, hmenum.ParameterActualTemperature)
				sourceRigPutFloat(ch, hmenum.ParameterHumidity)
				return ch
			},
			wantTypes: climateSensorTypes,
		},
		{
			name:  "frost point model",
			model: "HmIP-STHO",
			build: func(t *testing.T) *device.Channel {
				t.Helper()
				ch := sourceRigChannel(t, "STHO0001", "HmIP-STHO", 1, "WEATHER_TRANSCEIVER")
				sourceRigPutFloat(ch, hmenum.ParameterActualTemperature)
				sourceRigPutFloat(ch, hmenum.ParameterHumidity)
				return ch
			},
			wantTypes: append([]string{"FrostPointSensor"}, climateSensorTypes...),
		},
		{
			name:  "weather station with wind",
			model: "HmIP-SWO",
			build: func(t *testing.T) *device.Channel {
				t.Helper()
				ch := sourceRigChannel(t, "SWO0001", "HmIP-SWO", 1, "WEATHER_TRANSCEIVER")
				sourceRigPutFloat(ch, hmenum.ParameterActualTemperature)
				sourceRigPutFloat(ch, hmenum.ParameterHumidity)
				sourceRigPutFloat(ch, hmenum.ParameterWindSpeed)
				return ch
			},
			wantTypes: append([]string{"ApparentTemperatureSensor", "FrostPointSensor"}, climateSensorTypes...),
		},
		{
			name:  "battery maintenance channel",
			model: "HM-Sec-RHS",
			build: func(t *testing.T) *device.Channel {
				t.Helper()
				// Channel 0 on purpose: the WINDOW_OPEN mapping this model
				// also carries is bound to channel 1, so the rig measures the
				// voltage branch alone.
				ch := sourceRigChannel(t, "RHS0001", "HM-Sec-RHS", 0, "MAINTENANCE")
				sourceRigPutFloat(ch, hmenum.ParameterOperatingVoltage)
				ch.PutMaster(generic.NewFloatSensor(generic.Spec{
					Key: hmtypes.DataPointKey{
						ChannelAddress: ch.Address,
						ParamsetKey:    hmenum.ParamsetKeyMaster,
						Parameter:      string(hmenum.ParameterLowBatLimit),
					},
					Descriptor: hmproto.ParameterData{
						Type:       hmenum.ParameterTypeFloat,
						Operations: hmenum.OperationsRead,
					},
				}))
				return ch
			},
			wantTypes: []string{"OperatingVoltageLevelSensor"},
		},
		{
			name:  "derived binary mapping",
			model: "HmIP-SRH",
			build: func(t *testing.T) *device.Channel {
				t.Helper()
				ch := sourceRigChannel(t, "SRH0001", "HmIP-SRH", 1, "SHUTTER_CONTACT")
				ch.Put(generic.NewStringSensor(generic.Spec{
					Key: hmtypes.DataPointKey{
						ChannelAddress: ch.Address,
						Parameter:      string(hmenum.ParameterState),
					},
					Descriptor: hmproto.ParameterData{
						Type:       hmenum.ParameterTypeEnum,
						Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
					},
				}))
				return ch
			},
			wantTypes: []string{"DerivedBinarySensor"},
		},
	}
}

// sourceRigChannel builds a device + channel pair for one rig.
func sourceRigChannel(t *testing.T, address, model string, chNo int, chType string) *device.Channel {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: address, Model: model})
	return d.AddChannel(address+":"+strconv.Itoa(chNo), chNo, chType, hmenum.ParamsetKeyValues)
}

// sourceRigPutFloat attaches a readable float sensor for one VALUES parameter.
func sourceRigPutFloat(ch *device.Channel, p hmenum.Parameter) {
	ch.Put(generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: string(p)},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	}))
}

// TestEveryCalculatedSensorRegistersItsSourcesAtAttach pins that attaching a
// calculated sensor to a channel resolves and registers its declared sources
// there and then — not on the first read of its value.
//
// A sensor that registers nothing is not merely uninformed: [SourcesValid]
// returns false for an empty source set, which gates the sensor's north-bound
// availability off (see [installSourceValidityGate]). A consumer that reads
// availability before it reads the value — Home Assistant does — then never
// performs the read that lazy resolution would have been waiting for, so the
// entity stays frozen on its restored state forever. The per-source update
// subscription is created in the same pass, so no event reaches the sensor
// either: the two halves fail together and neither is visible from the
// outside.
func TestEveryCalculatedSensorRegistersItsSourcesAtAttach(t *testing.T) {
	instantiated := map[string]struct{}{}

	for _, rig := range sourceRigs() {
		t.Run(rig.name, func(t *testing.T) {
			ch := rig.build(t)
			sensors := CreateCalculatedDataPoints(ch, rig.model)
			if len(sensors) == 0 {
				t.Fatalf("rig %q produced no calculated data points for model %q — "+
					"the relevance predicate or the channel shape moved", rig.name, rig.model)
			}

			got := map[string]struct{}{}
			for _, s := range sensors {
				name := sensorTypeName(s)
				got[name] = struct{}{}
				instantiated[name] = struct{}{}

				reg, ok := s.(sourceRegistering)
				if !ok {
					t.Fatalf("%s does not embed sourceSink — it cannot aggregate "+
						"source state and its availability gate has nothing to read", name)
				}
				if len(reg.snapshotSources()) == 0 {
					t.Errorf("%s registered no source data point at attach: its Subscribe "+
						"resolved nothing off channel %s. SourcesValid() reports false for an "+
						"empty set, so the sensor stays unavailable and never receives an update",
						name, ch.Address)
				}
			}

			for _, want := range rig.wantTypes {
				if _, ok := got[want]; !ok {
					t.Errorf("rig %q no longer produces %s (got %v) — the guard would "+
						"stop covering that sensor", rig.name, want, keysOf(got))
				}
			}
		})
	}

	// Completeness: a sensor type no rig instantiates is a type this contract
	// does not measure. Adding one without a rig must fail here rather than
	// pass silently.
	for _, name := range typesEmbeddingSourceSink(t) {
		if _, ok := instantiated[name]; !ok {
			t.Errorf("%s embeds sourceSink but no rig in sourceRigs() produces it — "+
				"add a channel constellation that does, so its source registration is measured",
				name)
		}
	}
}

// sensorTypeName returns the concrete struct name behind a [Sensor].
func sensorTypeName(s Sensor) string {
	rt := reflect.TypeOf(s)
	for rt != nil && rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	if rt == nil {
		return "<nil>"
	}
	return rt.Name()
}

func keysOf(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// typesEmbeddingSourceSink reads the package's own sources for every struct
// that embeds sourceSink. The list is derived, not maintained: a hand-written
// roster would go stale the moment a sensor is added, which is the case this
// completeness check exists for.
func typesEmbeddingSourceSink(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob package sources: %v", err)
	}
	fset := token.NewFileSet()
	var out []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		parsed, perr := parser.ParseFile(fset, f, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", f, perr)
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				return true
			}
			for _, field := range st.Fields.List {
				if len(field.Names) != 0 {
					continue // named field, not an embedding
				}
				if id, isIdent := field.Type.(*ast.Ident); isIdent && id.Name == "sourceSink" {
					out = append(out, ts.Name.Name)
				}
			}
			return true
		})
	}
	if len(out) == 0 {
		t.Fatal("no struct embedding sourceSink found — the scan lost its subject, " +
			"so the completeness check above proves nothing")
	}
	return out
}
