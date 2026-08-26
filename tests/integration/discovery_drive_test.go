// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build integration || integration_live

// Shared MQTT-Discovery drive helpers. Both the godevccu-backed
// snapshot tests (integration tag) and the live-CCU smoke tests
// (integration_live tag) push every data point of a channel through
// the bridge's HA-Discovery path; keeping a single implementation
// here prevents the two suites from drifting apart on how a
// production-shaped [mqtt.Event] is assembled.

package integration

import (
	"context"
	"encoding/json"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// driveChannelDPs builds an [mqtt.Event] for every Generic-DP on the
// channel (both VALUES and MASTER paramsets) and pushes it through the
// bridge's HA-Discovery path. The caller's recorder captures the
// resulting publishes; per-topic deduplication in the bridge ensures we
// get one entity per unique discovery topic regardless of how many DPs
// share it. centralName must match the bridge's configured central so
// topic construction and hub-info lookups stay consistent.
func driveChannelDPs(ctx context.Context, bridge *mqtt.Bridge, centralName string, d *device.Device, ch *device.Channel) {
	model := d.Model
	iface := string(d.Interface)
	addr := d.Address

	// Mirror the EventBridge: surface the channel's CustomDataPoint as
	// `ev.Source` so the channel-aware aggregator (climate / cover /
	// light / lock / siren / valve) can fire. Without this every
	// custom-domain channel falls through to the per-parameter path
	// and the capture misses every aggregate entity.
	var source payload.Source
	if cdp := ch.CustomDataPoint(); cdp != nil {
		if src, ok := cdp.(payload.Source); ok && src != nil {
			source = src
		}
	}

	common := mqtt.Event{
		Central:        centralName,
		Interface:      iface,
		DeviceAddress:  addr,
		DeviceName:     d.Name(),
		Model:          model,
		ChannelNo:      ch.Number,
		ChannelAddress: ch.Address,
		ChannelType:    ch.Type,
		Channel:        ch,
		Device:         d,
		Source:         source,
	}

	// VALUES paramset — primary surface. Mirror the EventBridge's
	// visibility gate: a DP whose runtime Usage was forced to
	// NO_CREATE (e.g. SuppressUndefinedGenericDataPoints on linked
	// virtual channels) is invisible and must not produce a Discovery
	// payload — without this gate the capture reports thousands of
	// over-emitted entities relative to the production EventBridge.
	for _, dp := range ch.DataPoints() {
		if !visibleForDiscovery(dp) {
			continue
		}
		ev := buildEvent(common, hmenum.ParamsetKeyValues, dp)
		_ = bridge.PublishState(ctx, ev)
	}
	// MASTER paramset — config-category surface.
	for _, dp := range ch.MasterDataPoints() {
		if !visibleForDiscovery(dp) {
			continue
		}
		ev := buildEvent(common, hmenum.ParamsetKeyMaster, dp)
		_ = bridge.PublishState(ctx, ev)
	}
}

// visibleForDiscovery mirrors the EventBridge visibility gate
// (`internal/central/adapter/eventbridge.go:444-446`). DPs whose
// `Visible()` returns false (typically because the runtime usage
// pipeline forced them to NO_CREATE) never reach the production
// MQTT bridge; the drive helper must filter them out to mirror
// production behaviour.
func visibleForDiscovery(dp device.ParameterDataPoint) bool {
	if v, ok := dp.(interface{ Visible() bool }); ok {
		return v.Visible()
	}
	return true
}

// buildEvent fills the descriptor-derived fields on a base Event so
// the discovery builder has min / max / default / value_list /
// writability / category — mirroring what the EventBridge populates
// at runtime. The Category propagation enables the model-driven
// component resolution; without it the discovery builder skips the
// event entirely (ADR 0011).
func buildEvent(base mqtt.Event, paramset hmenum.ParamsetKey, dp device.ParameterDataPoint) mqtt.Event {
	desc := dp.ParameterData()
	ev := base
	ev.Parameter = string(dp.Parameter())
	ev.Writable = desc.IsWritable()
	if cd, ok := dp.(interface {
		Category() hmenum.DataPointCategory
	}); ok {
		ev.Category = cd.Category()
	}
	gc := &payload.GenericConfig{
		Paramset:  paramset,
		Unit:      generic.CleanupUnit(dp.Parameter(), desc.Unit),
		ValueList: append([]string(nil), desc.ValueList...),
		Type:      desc.Type,
	}
	if v, ok := decodeFloat(desc.Min); ok {
		gc.Min = &v
	}
	if v, ok := decodeFloat(desc.Max); ok {
		gc.Max = &v
	}
	if v, ok := decodeFloat(desc.Default); ok {
		gc.Default = &v
	}
	ev.Descriptor = gc
	return ev
}

func decodeFloat(rm json.RawMessage) (float64, bool) {
	if len(rm) == 0 {
		return 0, false
	}
	// Wire descriptors carry MIN/MAX/DEFAULT as raw JSON before
	// type-dispatch; decode into `any` first, then narrow.
	var v any
	if err := json.Unmarshal(rm, &v); err != nil {
		return 0, false
	}
	switch x := v.(type) {
	case float64:
		return x, true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		if x == "" {
			return 0, false
		}
		var f float64
		if err := json.Unmarshal([]byte(x), &f); err == nil {
			return f, true
		}
	}
	return 0, false
}
