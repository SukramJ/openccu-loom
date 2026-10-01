// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// legacyChannelLine and legacyDataPointLine are the NDJSON line shapes as
// they were first published: summaries embedded by value.
type legacyChannelLine struct {
	DeviceAddress string `json:"device_address"`
	ChannelSummary
}

type legacyDataPointLine struct {
	ChannelAddress string `json:"channel_address"`
	DataPointSummary
}

// legacySnapshotNDJSON encodes env the way the stream was first written:
// every line a map[string]any, whose keys encoding/json sorts, so "data"
// precedes "kind". It is the byte-level oracle for writeSnapshotNDJSON.
func legacySnapshotNDJSON(env SnapshotEnvelope) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	emit := func(kind string, data any) {
		_ = enc.Encode(map[string]any{"kind": kind, "data": data})
	}
	emit("meta", map[string]any{"generated_at": env.GeneratedAt})
	for i := range env.Interfaces {
		emit("interface", env.Interfaces[i])
	}
	for i := range env.Devices {
		emit("device", env.Devices[i])
	}
	for i := range env.DeviceChannels {
		dc := &env.DeviceChannels[i]
		for j := range dc.Channels {
			ch := &dc.Channels[j]
			emit("channel", legacyChannelLine{DeviceAddress: dc.DeviceAddress, ChannelSummary: ch.ChannelSummary})
			for k := range ch.DataPoints {
				emit("data_point", legacyDataPointLine{ChannelAddress: ch.Address, DataPointSummary: ch.DataPoints[k]})
			}
		}
	}
	for i := range env.Rooms {
		emit("room", env.Rooms[i])
	}
	for i := range env.Functions {
		emit("function", env.Functions[i])
	}
	for i := range env.Programs {
		emit("program", env.Programs[i])
	}
	for i := range env.Sysvars {
		emit("sysvar", env.Sysvars[i])
	}
	return buf.Bytes()
}

// TestSnapshotNDJSON_BytesMatchTheLegacyLineEncoding pins the NDJSON stream
// byte for byte: a consumer may key on the line text (or a cache on its
// hash), so a change of key order or of how an embedded summary renders is
// a wire change even when every parser still reads the same values.
func TestSnapshotNDJSON_BytesMatchTheLegacyLineEncoding(t *testing.T) {
	t.Parallel()
	env := SnapshotEnvelope{
		GeneratedAt: "2026-10-01T12:00:00Z",
		Interfaces:  []InterfaceState{{}},
		Devices: []DeviceSummary{
			{Address: "0001ABCD", Interface: "HmIP-RF", InterfaceID: "HmIP-RF@ccu01"},
			{Address: "0002EFGH", Central: "ccu02", Interface: "BidCos-RF", InterfaceID: "BidCos-RF@ccu02"},
		},
		DeviceChannels: []SnapshotDeviceChannels{{
			DeviceAddress: "0001ABCD",
			Channels: []SnapshotChannelEntry{
				{
					ChannelSummary: ChannelSummary{Address: "0001ABCD:1", Number: 1, Type: "SWITCH_VIRTUAL_RECEIVER"},
					DataPoints: []DataPointSummary{
						{Parameter: "STATE", Value: true},
						{Parameter: "LEVEL", Value: 0.5},
					},
				},
				{ChannelSummary: ChannelSummary{Address: "0001ABCD:0", Number: 0}},
			},
		}},
		Rooms:     []RoomEntry{{Name: "Office", DeviceCount: 1}},
		Functions: []FunctionEntry{{Name: "Lighting", DeviceCount: 1}},
		Programs:  []ProgramSummary{{Central: "ccu01"}},
		Sysvars:   []SysvarSummary{{Central: "ccu01"}},
	}

	w := httptest.NewRecorder()
	writeSnapshotNDJSON(w, env)

	want := legacySnapshotNDJSON(env)
	if got := w.Body.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("NDJSON bytes changed\n--- got\n%s--- want\n%s", got, want)
	}
	// meta, interface, 2 devices, 2 channels, 2 data points, room,
	// function, program, sysvar.
	if lines := bytes.Count(want, []byte("\n")); lines != 12 {
		t.Fatalf("fixture should exercise every line kind: %d lines, want 12", lines)
	}
}
