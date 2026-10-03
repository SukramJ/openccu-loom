// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build bench

// Package bench — source-backed publish micro-benchmark.
//
// Measures the cost an Event.Source adds to PublishState compared to
// the per-parameter raw publish alone. Release-gate threshold is
// +5 % p50 latency vs. the source-less baseline.
package bench

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

type benchPublisher struct{}

func (benchPublisher) Publish(_ context.Context, _ string, _ []byte, _ mqtt.QoS, _ bool, _ ...mqtt.PublishOption) error {
	return nil
}

type benchSource struct{}

func (benchSource) Info() payload.InfoPayload     { return nil }
func (benchSource) Config() payload.ConfigPayload { return nil }
func (benchSource) State() payload.StatePayload {
	return map[string]any{
		"hvac_mode":           "heat",
		"preset_mode":         "boost",
		"current_temperature": 21.5,
		"target_temperature":  22.0,
		"current_humidity":    45,
		"action":              "heating",
		"available":           true,
	}
}
func (benchSource) ServiceMethodNames() []string { return nil }
func (benchSource) Invoke(_ context.Context, _ string, _ map[string]any, _ hmenum.CommandPriority) error {
	return nil
}

// BenchmarkPublishStateBaseline is the per-parameter raw publish
// without an Event.Source — the source-less cost.
func BenchmarkPublishStateBaseline(b *testing.B) {
	br := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "gh",
		CentralName: "ccu",
		RawEnabled:  true,
	}, benchPublisher{})

	ev := mqtt.Event{
		Interface:     "HmIP-RF",
		DeviceAddress: "BWTH001",
		ChannelNo:     1,
		Parameter:     "ACTUAL_TEMPERATURE",
		Value:         21.5,
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = br.PublishState(ctx, ev)
	}
}

// BenchmarkPublishStateWithSource is PublishState with Event.Source
// set — exercises the per-parameter publish plus the source-derived
// slot work in one call. The ratio vs. the baseline is the release
// gate (target: < +5 % p50).
func BenchmarkPublishStateWithSource(b *testing.B) {
	br := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "gh",
		CentralName: "ccu",
		RawEnabled:  true,
	}, benchPublisher{})

	src := benchSource{}
	ev := mqtt.Event{
		Source:        src,
		Interface:     "HmIP-RF",
		DeviceAddress: "BWTH001",
		ChannelNo:     1,
		Parameter:     "ACTUAL_TEMPERATURE",
		Value:         21.5,
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = br.PublishState(ctx, ev)
	}
}

// BenchmarkPublishSlotState is the per-data-point state publish every value
// event reaches: one PerDPState wrapper encoded and handed to the broker.
func BenchmarkPublishSlotState(b *testing.B) {
	br := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "gh",
		CentralName: "ccu",
		RawEnabled:  true,
	}, benchPublisher{})
	slot := payload.TopicSlot{Address: "000C9709AEF157", Channel: 1, Bucket: payload.BucketValues, Parameter: "ACTUAL_TEMPERATURE"}
	state := payload.PerDPState{Value: 21.5, Available: true, ModifiedAt: 1759350000.123, RefreshedAt: 1759350001.456}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		_ = br.PublishSlotState(ctx, "ccu", "HmIP-RF", slot, state)
	}
}
