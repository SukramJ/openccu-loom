// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package matteradapter

import (
	"fmt"
	"log/slog"

	"github.com/SukramJ/go-fabric/eligibility"
	"github.com/SukramJ/go-fabric/store"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Candidate identifies one row in the allowlist UI list. The
// 5-tuple matches [store.EndpointKey].
//
// `ChannelType` carries the OCCU channel-type token (e.g.
// `"HEATING_CLIMATECONTROL_TRANSCEIVER"`). The REST handler hands it
// to a [ccudata.Translations]-backed labeler so the SPA can render
// channel-specific parameter labels (`POWER` on
// `ENERGIE_METER_TRANSMITTER` resolves differently from the bare
// parameter table).
//
// loom:reachable:reason="returned by CollectCandidates and consumed by the REST /matter/exposable handler; a method-less data struct that the reachability analyzer's type heuristic (which marks a type reachable only via its methods) cannot see used"
type Candidate struct {
	Key         store.EndpointKey
	DisplayName string
	ChannelType string
	Verdict     eligibility.Verdict
}

// CollectCandidates walks every channel of every device on a central
// and returns one Candidate per (channel, dp_kind, dp_key) the model
// layer carries. Used by `/api/v1/matter/exposable`.
//
// The walk lives here rather than beside the classifier because it is
// entirely host vocabulary — devices, channels, usage marks — while the
// verdict it records is Matter vocabulary. [eligibility.Classify] owns
// the second half and knows nothing about this model.
//
// When exposeSecondary is false (the default), a custom-DP entity's
// non-primary CONSTITUENTS are dropped so a multi-channel HmIP actor
// (switch / dimmer / cover / lock / siren / valve) surfaces a single
// primary endpoint instead of several duplicate accessories:
//
//   - its SECONDARY virtual-receiver actor channels — the same custom-DP
//     secondary classification HA-Discovery marks enabled-by-default false
//     ([device.Channel.IsCustomDPSecondaryChannel]); and
//   - its ce_state status DP — the group-state transmitter a custom
//     entity spans off its primary (e.g. the WATER_SWITCH_TRANSMITTER
//     STATE feeding a valve), classified [hmenum.DataPointUsageCDPState].
//
// It also drops the ignored service params and the no_create raw
// constituents an aggregating parent consumes (see [hideFromMatter]), so the
// candidate set matches the entity-creation gate MQTT / HA / REST apply.
// Genuinely standalone DPs — buttons (event), measurements / battery
// (data_point), a channel-0 maintenance sensor — are always collected. This
// is Matter-only; every other north-bound surface still enumerates all channels.
func CollectCandidates(centralName string, devices []*device.Device, exposeSecondary bool) []Candidate {
	var out []Candidate
	for _, dev := range devices {
		if dev == nil {
			continue
		}
		for _, ch := range dev.Channels() {
			if ch == nil {
				continue
			}
			if !exposeSecondary && ch.IsCustomDPSecondaryChannel() {
				continue
			}
			collectChannelCandidates(centralName, dev, ch, exposeSecondary, &out)
		}
	}
	return out
}

func collectChannelCandidates(centralName string, dev *device.Device, ch *device.Channel, exposeSecondary bool, out *[]Candidate) {
	// Operator-hidden channels (G12) are dropped from Matter exposure
	// entirely, mirroring the operation-list hide on the REST/SPA surface.
	if ch.IsHidden() {
		return
	}
	displayName := dev.Name()
	if displayName == "" {
		displayName = dev.Address
	}
	channelHasCustom := ch.CustomDataPoint() != nil

	emit := func(kind store.DPKind, source any) {
		if source == nil {
			return
		}
		// Align the Matter candidate set with the entity-creation gate the
		// other north-bound surfaces apply: drop ignored service params, the
		// raw no_create constituents of an aggregating parent, and (unless the
		// operator opted in) the ce_secondary / ce_state secondary channels.
		// Only generic DPs are gated — a custom / calculated / combined DP is
		// the aggregating entity itself (and a custom wrapper promotes its
		// embedded constituent's no_create usage, so it must not be filtered
		// by that usage). The same helper gates the assembled topology, so the
		// candidate list and the endpoints it produces cannot disagree.
		if kind == store.DPKindGeneric && hideFromMatter(source, channelHasCustom, exposeSecondary) {
			return
		}
		// A structurally-incomplete device — e.g. a custom light whose LEVEL
		// data point never materialised, leaving a nil embedded pointer that
		// a promoted accessor (Name / MatterClusterServers) dereferences —
		// must not crash the whole exposable enumeration. Isolate each
		// candidate: recover, log, and skip the broken one so every healthy
		// device still surfaces on GET /matter/exposable and in the bridge.
		defer func() {
			if r := recover(); r != nil {
				slog.Warn("matter.eligibility.candidate_skipped",
					slog.String("device", dev.Address),
					slog.Int("channel", ch.Number),
					slog.String("dp_kind", string(kind)),
					slog.Any("recovered", r))
			}
		}()
		key := candidateDPKey(source)
		v := eligibility.Classify(source)
		// Skip entries that are unmappable AND have no Matter projection
		// hint (i.e. truly opaque sources). Sources with a real reason
		// surface as ⛔ rows in the UI so the operator understands why.
		if v.State == eligibility.StateUnmappable && v.Reason == eligibility.ReasonNoProjection {
			return
		}
		*out = append(*out, Candidate{
			Key: store.EndpointKey{
				CentralName:   centralName,
				DeviceAddress: dev.Address,
				ChannelNo:     ch.Number,
				DPKind:        kind,
				DPKey:         key,
			},
			DisplayName: displayName,
			ChannelType: ch.Type,
			Verdict:     v,
		})
	}

	// Custom DP (max one per channel).
	if cdp := ch.CustomDataPoint(); cdp != nil {
		emit(store.DPKindCustom, cdp)
	}

	// Calculated DPs (derived sensors).
	for _, calc := range ch.CalculatedDataPoints() {
		emit(store.DPKindCalculated, calc)
	}

	// Combined DPs (fan-out aggregations).
	for _, comb := range ch.CombinedDataPoints() {
		emit(store.DPKindCombined, comb)
	}

	// Generic DPs from VALUES paramset. MASTER DPs are config-only and
	// never bridged to Matter; the eligibility classifier returns
	// Unmappable for them anyway, but skipping the iteration keeps
	// the candidate list lean.
	for _, dp := range ch.DataPoints() {
		emit(store.DPKindGeneric, dp)
	}
}

// The capability probes [candidateDPKey] performs on a candidate source. They
// are named rather than written inline so each rung can be asserted against the
// data-point types the model actually materialises: a probe no production
// type satisfies is a permanently dead rung whose key derivation silently
// never happens.
type (
	dataPointKeyed interface{ DataPointKey() hmtypes.DataPointKey }
	dataPointNamed interface{ Name() string }
)

// candidateDPKey resolves the per-DP component of the candidate key tuple
// `(central, device, channel, kind, dp_key)`. The 5-tuple must be
// unique within a channel — so the heuristic prefers
// [hmtypes.DataPointKey.Parameter] (uniquely identifies the parameter
// slot a DP occupies on a paramset), then `Name()`. The "unknown"
// fallback is only reached for sources that expose neither — which is a
// bug elsewhere; we still want a stable string so the candidate list
// renders, but we tag it with the Go type so the operator sees *which*
// type to investigate rather than a pile of identical "unknown" rows.
//
// It is deliberately more forgiving than [dpKey], which the topology walk
// uses: the walk only ever sees [device.AttachableDataPoint], while the
// candidate list also has to render sources that are structurally broken.
func candidateDPKey(src any) string {
	if k, ok := src.(dataPointKeyed); ok {
		if p := k.DataPointKey().Parameter; p != "" {
			return p
		}
	}
	if named, ok := src.(dataPointNamed); ok {
		if n := named.Name(); n != "" {
			return n
		}
	}
	if src == nil {
		return "unknown"
	}
	return fmt.Sprintf("unknown(%T)", src)
}
