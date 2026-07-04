// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build bench

// Package bench — aggregated-state publish micro-benchmark.
//
// ADR 0008 follow-up: measure the cost the aggregated-state topic
// adds to PublishState compared to the per-parameter raw publish
// alone. Release-gate threshold is +5 % p50 latency vs. the
// non-aggregated baseline.
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
// without an Event.Source — the pre-ADR-0007 cost.
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
	for i := 0; i < b.N; i++ {
		_ = br.PublishState(ctx, ev)
	}
}

// BenchmarkPublishStateWithSource is PublishState with Event.Source
// set — exercises both the per-parameter publish AND the aggregated
// state publish in one call. The ratio vs. the baseline is the
// release gate (target: < +5 % p50).
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
	for i := 0; i < b.N; i++ {
		_ = br.PublishState(ctx, ev)
	}
}

// BenchmarkPublishSourceStateOnly isolates the aggregated publish
// path so a regression in `PublishSourceState` shows up directly,
// independent of the per-parameter publish cost.
func BenchmarkPublishSourceStateOnly(b *testing.B) {
	br := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "gh",
		CentralName: "ccu",
		RawEnabled:  true,
	}, benchPublisher{})

	src := benchSource{}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = br.PublishSourceState(ctx, "ccu", "HmIP-RF", "BWTH001", 1, src)
	}
}
