// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build bench

// Package bench — ADR 0007 payload-build micro-benchmark.
//
// ADR 0007 §Mitigations states: "Benchmark `tests/bench/payload_build_test.go`:
// the per-type cached reflection path must stay below 500 ns/op for a 20-field
// struct." Until this file existed the ADR asserted a bound that nothing
// measured; the 2026-09-13 amendment recorded that, and this file is what
// closes it.
//
// The operation under the bound is [payload.ForWith] — the tag-driven,
// per-type-cached reflection harvest, whose field walk is memoised in a
// `sync.Map` keyed on `(reflect.Type, Kind)` by the shared
// `go-hamqtt/payload` package. That is the production function itself, not a
// copy of it: [BenchmarkPayloadBuildDeviceInfo] drives exactly the call
// `internal/north/mqtt/discovery.go` makes per device on every HA-Discovery
// build.
//
// Two benchmarks, because the ADR describes the workload two ways and only
// one of them is the daemon:
//
//   - [BenchmarkPayloadBuildDeviceInfo] — the real production input, a
//     `*device.Device` built through `device.New`, harvested for
//     [payload.KindInfo] with `UseAltNames`. This also crosses the
//     [payload.ExtraProperties] merge, which the mutex-guarded `name` field
//     forces and which the reflection walk alone cannot see.
//   - [BenchmarkPayloadBuildTwentyField] — the ADR's literal workload
//     parameter, a 20-field tagged struct, run through the same production
//     function.
//
// `script/bench_gate.sh` is what turns these numbers into a gate; the
// ceilings live there, not here, so that a benchmark run stays a measurement
// and the policy stays in one reviewable place.
package bench

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// benchDevice builds a fully-populated device the way the ingest pipeline
// does, so the harvest has a non-zero value in every tagged field. A
// half-empty device would measure the omit-zero fast path rather than the
// work the daemon actually pays.
func benchDevice() *device.Device {
	d := device.New(device.Config{
		InterfaceID:                  "ccu-HmIP-RF",
		Interface:                    hmenum.InterfaceHmIPRF,
		Address:                      "0001D8A9C3B2F1",
		Model:                        "HmIP-BWTH",
		SubModel:                     "HmIP-BWTH-A",
		Name:                         "Wohnzimmer Thermostat",
		Manufacturer:                 hmenum.ManufacturerEQ3,
		ProductGroup:                 hmenum.ProductGroupHmIP,
		Rooms:                        []string{"Wohnzimmer"},
		Functions:                    []string{"Heizung"},
		RxModes:                      []hmenum.CommandRxMode{hmenum.CommandRxModeBurst},
		RxMode:                       hmenum.RxModeBurst,
		Updatable:                    true,
		IseID:                        4711,
		SchemaVersion:                39,
		IgnoreForCustomDataPoint:     false,
		HasCustomDataPointDefinition: true,
		IgnoreOnInitialLoad:          false,
	})
	d.ModelLabel = "Wandthermostat mit Schaltausgang"
	d.ModelIcon = "mdi:thermostat"
	return d
}

// BenchmarkPayloadBuildDeviceInfo measures the production call site:
// `payload.ForWith(ev.Device, payload.KindInfo, payload.Options{UseAltNames: true})`
// from `internal/north/mqtt/discovery.go`. Steady state — the per-type cache
// is warm after the first iteration, which is the state the ADR's bound is
// about ("the per-type cached reflection path").
func BenchmarkPayloadBuildDeviceInfo(b *testing.B) {
	d := benchDevice()
	opts := payload.Options{UseAltNames: true}
	// Warm the per-type cache outside the timed region: the ADR bounds the
	// cached path, and folding the one-off reflection walk into b.N would
	// measure a cost the daemon pays once per process.
	sink := payload.ForWith(d, payload.KindInfo, opts)
	if len(sink) == 0 {
		b.Fatal("payload harvest returned no keys; the benchmark is measuring nothing")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		sink = payload.ForWith(d, payload.KindInfo, opts)
	}
	b.StopTimer()
	if len(sink) == 0 {
		b.Fatal("payload harvest returned no keys")
	}
}

// benchTwentyField is the ADR's stated workload shape: a 20-field struct,
// every field tagged, a mix of scalar and composite types and of plain and
// `alt=`-renamed keys. It is the *input*, not a reimplementation — the code
// under measurement is still production [payload.ForWith].
type benchTwentyField struct {
	InterfaceID   string            `payload:"info"`
	Address       string            `payload:"info,alt=serial_number"`
	Model         string            `payload:"info"`
	ModelLabel    string            `payload:"info,alt=model_label"`
	ModelIcon     string            `payload:"info,alt=model_icon"`
	SubModel      string            `payload:"info,alt=sub_model"`
	Manufacturer  string            `payload:"info"`
	ProductGroup  string            `payload:"info,alt=product_group"`
	Firmware      string            `payload:"info"`
	Room          string            `payload:"info"`
	Function      string            `payload:"info"`
	IseID         int               `payload:"info,alt=ise_id"`
	SchemaVersion int               `payload:"info,alt=schema_version"`
	ChannelCount  int               `payload:"info,alt=channel_count"`
	Updatable     bool              `payload:"info"`
	Available     bool              `payload:"info"`
	RSSIPeer      int               `payload:"info,alt=rssi_peer"`
	RSSIDevice    int               `payload:"info,alt=rssi_device"`
	Rooms         []string          `payload:"info"`
	Attributes    map[string]string `payload:"info"`
}

// BenchmarkPayloadBuildTwentyField is the ADR's literal wording: the
// per-type cached reflection path over a 20-field struct.
func BenchmarkPayloadBuildTwentyField(b *testing.B) {
	v := &benchTwentyField{
		InterfaceID:   "ccu-HmIP-RF",
		Address:       "0001D8A9C3B2F1",
		Model:         "HmIP-BWTH",
		ModelLabel:    "Wandthermostat mit Schaltausgang",
		ModelIcon:     "mdi:thermostat",
		SubModel:      "HmIP-BWTH-A",
		Manufacturer:  "eQ-3",
		ProductGroup:  "HmIP",
		Firmware:      "2.10.6",
		Room:          "Wohnzimmer",
		Function:      "Heizung",
		IseID:         4711,
		SchemaVersion: 39,
		ChannelCount:  9,
		Updatable:     true,
		Available:     true,
		RSSIPeer:      -64,
		RSSIDevice:    -71,
		Rooms:         []string{"Wohnzimmer"},
		Attributes:    map[string]string{"zone": "heating"},
	}
	opts := payload.Options{UseAltNames: true}
	sink := payload.ForWith(v, payload.KindInfo, opts)
	if len(sink) != 20 {
		b.Fatalf("expected a 20-key harvest for the ADR's 20-field struct, got %d", len(sink))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		sink = payload.ForWith(v, payload.KindInfo, opts)
	}
	b.StopTimer()
	if len(sink) != 20 {
		b.Fatalf("expected a 20-key harvest, got %d", len(sink))
	}
}

// benchDiscoveryEvent is the per-entity discovery input the daemon builds
// for one wire data point on a real device — the `mqtt.Event` shape
// `EventBridge` hands `DiscoveryBuilder.Build` on every HA-Discovery
// republish. `Device` carries the same `*device.Device` the two
// `payload.ForWith` benchmarks above harvest, because that is what the
// production path passes.
func benchDiscoveryEvent() mqtt.Event {
	return mqtt.Event{
		Central:        "ccu",
		Interface:      "HmIP-RF",
		DeviceAddress:  "0001D8A9C3B2F1",
		DeviceName:     "Wohnzimmer Thermostat",
		Model:          "HmIP-BWTH",
		ChannelNo:      1,
		ChannelAddress: "0001D8A9C3B2F1:1",
		Parameter:      "ACTUAL_TEMPERATURE",
		Value:          21.5,
		Category:       hmenum.DataPointCategorySensor,
		Device:         benchDevice(),
	}
}

// BenchmarkDiscoveryBuildPerEntity is the NEIGHBOUR measurement, and it is
// the one that decides whether ADR 0007's 500 ns/op bound is worth anything.
//
// A per-call cost is not a budget until it is a fraction of something. The
// amendment that first measured `payload.ForWith` established what one call
// costs; it did not establish what share of the work that call is part of.
// This benchmark supplies the denominator: the complete per-entity
// HA-Discovery build — `DefaultDiscoveryBuilder.Build`, the exact call
// `Bridge.PublishDiscoveryOnly` makes — which contains exactly one
// `deviceDescriptor` and therefore exactly one `payload.ForWith`, plus topic
// construction, component classification, device-class and state-class
// resolution, i18n lookups, the `hadiscovery.RenderComponent` model render
// and the JSON marshal of the ~1.3 KB payload that goes on the wire.
//
// Read it against `BenchmarkPayloadBuildDeviceInfo`: that benchmark's ns/op
// over this one's is the share of a discovery build that `ForWith` actually
// accounts for. Neither figure means anything on its own.
func BenchmarkDiscoveryBuildPerEntity(b *testing.B) {
	tb := mqtt.NewTopicBuilder("openccu-loom")
	db := mqtt.NewDefaultDiscoveryBuilder(tb, "ccu")
	ev := benchDiscoveryEvent()
	if _, _, _, buf, ok := db.Build(ev); !ok || len(buf) == 0 {
		b.Fatal("discovery build declined the benchmark event; the benchmark is measuring nothing")
	}
	b.ReportAllocs()
	b.ResetTimer()
	var sink []byte
	for range b.N {
		_, _, _, sink, _ = db.Build(ev)
	}
	b.StopTimer()
	if len(sink) == 0 {
		b.Fatal("discovery build produced no payload")
	}
}
