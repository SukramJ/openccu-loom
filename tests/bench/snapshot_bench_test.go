// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build bench

// Package bench — nested-snapshot micro-benchmark.
//
// P2 of the external-client drop-in optimisations replaced the N×M
// cold-start (one /snapshot + a GET per device + a GET per channel +
// a GET per channel's data-points) with a single /snapshot call that can
// nest channels and data points via ?include=. The operational win is
// the elimination of round trips, which a CPU micro-benchmark cannot
// measure directly — but it must not come at a pathological server-side
// cost. These benchmarks pin the build cost of each snapshot shape on a
// representative fleet so a regression in the nesting path is caught.
//
// The fleet below is benchFleetDevices × benchFleetChannels ×
// benchFleetDataPoints — the same product a mid-sized CCU exposes. The
// flat shape is the baseline; the nested shapes are what the drop-in
// client requests. The legacy flow it replaces would have issued
// 1 + N + N×C HTTP requests for the same data.
package bench

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

const (
	benchFleetDevices    = 200
	benchFleetChannels   = 6
	benchFleetDataPoints = 8
)

// benchDeviceIndex is a read-only handlers.DeviceIndex over a fixed fleet.
type benchDeviceIndex struct {
	list []*device.Device
	byID map[string]*device.Device
}

func (b *benchDeviceIndex) Devices() []*device.Device { return b.list }

func (b *benchDeviceIndex) Device(address string) (*device.Device, bool) {
	d, ok := b.byID[address]
	return d, ok
}

func (b *benchDeviceIndex) CentralOf(string) string { return "ccu" }

// benchFleet builds a fleet of `devices` devices, each with `channels`
// channels carrying `dps` BinarySensor data points. Kind is set so each
// DP reports a real Category(), exercising the classification lookup the
// nested shapes perform.
func benchFleet(devices, channels, dps int) *benchDeviceIndex {
	idx := &benchDeviceIndex{byID: make(map[string]*device.Device, devices)}
	for d := 0; d < devices; d++ {
		addr := "BENCH" + strconv.Itoa(100000+d)
		dev := device.New(device.Config{
			Address:     addr,
			Model:       "HmIP-BENCH",
			Interface:   hmenum.InterfaceHmIPRF,
			InterfaceID: "HmIP-RF@CCU",
			Name:        "Bench Device",
		})
		for c := 1; c <= channels; c++ {
			chAddr := addr + ":" + strconv.Itoa(c)
			ch := dev.AddChannel(chAddr, c, "SWITCH", hmenum.ParamsetKeyValues)
			for p := 0; p < dps; p++ {
				ch.Put(generic.NewBinarySensor(generic.Spec{
					Key: hmtypes.DataPointKey{
						ChannelAddress: chAddr,
						ParamsetKey:    hmenum.ParamsetKeyValues,
						Parameter:      "STATE" + strconv.Itoa(p),
					},
					Descriptor: hmproto.ParameterData{
						Type:       hmenum.ParameterTypeBool,
						Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
					},
					Kind: generic.KindBinarySensor,
				}))
			}
		}
		idx.list = append(idx.list, dev)
		idx.byID[addr] = dev
	}
	return idx
}

// runSnapshotBench drives the Snapshot handler with the given query and
// Accept header b.N times, reporting allocations.
func runSnapshotBench(b *testing.B, rawQuery, accept string) {
	b.Helper()
	idx := benchFleet(benchFleetDevices, benchFleetChannels, benchFleetDataPoints)
	h := handlers.Snapshot(handlers.SnapshotDeps{Devices: idx})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodGet, "/snapshot?"+rawQuery, http.NoBody)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d", rec.Code)
		}
	}
}

// BenchmarkSnapshotFlat is the baseline: structural device summaries only,
// no nesting (the shape every client got before P2).
func BenchmarkSnapshotFlat(b *testing.B) {
	runSnapshotBench(b, "", "")
}

// BenchmarkSnapshotIncludeChannels nests each device's channels.
func BenchmarkSnapshotIncludeChannels(b *testing.B) {
	runSnapshotBench(b, "include=channels", "")
}

// BenchmarkSnapshotIncludeDataPoints nests channels AND expands each
// channel's data points — the full one-shot bootstrap the drop-in client
// uses in place of the N×M per-channel fetch.
func BenchmarkSnapshotIncludeDataPoints(b *testing.B) {
	runSnapshotBench(b, "include=channels,data_points", "")
}

// BenchmarkSnapshotNDJSONDataPoints is the streaming variant of the full
// nested shape — the recommended surface for large CCUs, since neither
// side has to hold the whole payload in memory.
func BenchmarkSnapshotNDJSONDataPoints(b *testing.B) {
	runSnapshotBench(b, "include=channels,data_points", "application/x-ndjson")
}
