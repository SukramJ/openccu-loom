// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package eligibility walks the model and produces the candidate list
// the operator-facing allowlist UI consumes. It does *not* classify
// sources itself — every DP exposes its verdict via
// [interfaces.MatterEligibilitySource]. This package delegates and
// composes; the model is the single source of truth.
//
// Default verdict for sources that don't override
// [interfaces.MatterEligibilitySource]: derived structurally from
// [interfaces.MatterEndpointSource] / [interfaces.MatterMeasurementSource]
// via [DeriveMatterEligibility]. Custom DPs with caveats (Siren tones,
// Effect light effect dispatch, FixedColorLight palette quantisation)
// implement the interface explicitly to surface
// MatterEligibilityPartial + a UX-readable reason.
package eligibility

import (
	"fmt"
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Verdict is a re-export of [interfaces.MatterEligibilityVerdict] for
// callers that only depend on this package.
type Verdict = interfaces.MatterEligibilityVerdict

// State is a re-export of [interfaces.MatterEligibilityState].
type State = interfaces.MatterEligibilityState

// Re-exports of the state constants so callers don't need both
// imports.
const (
	StateUnmappable = interfaces.MatterEligibilityUnmappable
	StateMappable   = interfaces.MatterEligibilityMappable
	StatePartial    = interfaces.MatterEligibilityPartial
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
type Candidate struct {
	Key         store.EndpointKey
	DisplayName string
	ChannelType string
	Verdict     Verdict
}

// Classify returns the verdict for one source. It honours
// [interfaces.MatterEligibilitySource] when present, otherwise falls
// back to [DeriveMatterEligibility] for the structural default.
func Classify(src any) Verdict {
	if src == nil {
		return Verdict{State: StateUnmappable, Reason: "nil source"}
	}
	if eligible, ok := src.(interfaces.MatterEligibilitySource); ok {
		return eligible.MatterEligibility()
	}
	return DeriveMatterEligibility(src)
}

// DeriveMatterEligibility computes the default verdict for a source
// from the structural surface it already implements. Custom DPs that
// implement MatterEndpointSource get a Mappable verdict with the
// declared device type + cluster IDs. Generic / Calculated DPs that
// implement MatterMeasurementSource get a Mappable verdict with the
// measurement-class-derived device type + cluster ID. Anything else
// is Unmappable.
//
// Custom DPs with caveats (Siren tones, Effect dispatch,
// FixedColorLight palette quantisation) implement
// [interfaces.MatterEligibilitySource] directly and override the
// derivation; they typically call DeriveMatterEligibility for the
// base verdict and then patch in `State = Partial` plus a reason.
func DeriveMatterEligibility(src any) Verdict {
	if ep, ok := src.(interfaces.MatterEndpointSource); ok {
		dt := ep.MatterDeviceType()
		clusters := clusterIDs(ep.MatterClusterServers())
		if dt == 0 || len(clusters) == 0 {
			return Verdict{
				State:  StateUnmappable,
				Reason: "source advertises no Matter device type or cluster",
			}
		}
		return Verdict{
			State:      StateMappable,
			DeviceType: dt,
			Clusters:   clusters,
		}
	}
	if ms, ok := src.(interfaces.MatterMeasurementSource); ok {
		class := ms.MatterMeasurementClass()
		if class == interfaces.MatterMeasurementNone {
			return Verdict{State: StateUnmappable, Reason: "measurement class is None"}
		}
		dt := interfaces.MatterMeasurementClassDeviceType(class)
		cl := interfaces.MatterMeasurementClassClusterID(class)
		if cl == 0 {
			return Verdict{State: StateUnmappable, Reason: "measurement class has no cluster equivalent"}
		}
		return Verdict{
			State:      StateMappable,
			DeviceType: dt, // 0 for measurements that ride on a host endpoint
			Clusters:   []uint32{cl},
		}
	}
	return Verdict{State: StateUnmappable, Reason: "no Matter projection on source type"}
}

// CollectCandidates walks every channel of every device on a central
// and returns one Candidate per (channel, dp_kind, dp_key) the model
// layer carries. Used by `/api/v1/matter/exposable`.
func CollectCandidates(centralName string, devices []*device.Device) []Candidate {
	var out []Candidate
	for _, dev := range devices {
		if dev == nil {
			continue
		}
		for _, ch := range dev.Channels() {
			if ch == nil {
				continue
			}
			collectChannelCandidates(centralName, dev, ch, &out)
		}
	}
	return out
}

func collectChannelCandidates(centralName string, dev *device.Device, ch *device.Channel, out *[]Candidate) {
	displayName := dev.Name
	if displayName == "" {
		displayName = dev.Address
	}

	emit := func(kind store.DPKind, source any) {
		if source == nil {
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
		key := dpKey(source)
		v := Classify(source)
		// Skip entries that are unmappable AND have no Matter projection
		// hint (i.e. truly opaque sources). Sources with a real reason
		// surface as ⛔ rows in the UI so the operator understands why.
		if v.State == StateUnmappable && v.Reason == "no Matter projection on source type" {
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

// dpKey resolves the per-DP component of the candidate key tuple
// `(central, device, channel, kind, dp_key)`. The 5-tuple must be
// unique within a channel — so the heuristic prefers
// [hmtypes.DataPointKey.Parameter] (uniquely identifies the parameter
// slot a DP occupies on a paramset), then `Profile()` (custom DPs
// declare their profile name), then `Name()`. The "unknown" fallback
// is only reached for sources that expose none of these — which is a
// bug elsewhere; we still want a stable string so the candidate list
// renders, but we tag it with the Go type so the operator sees *which*
// type to investigate rather than a pile of identical "unknown" rows.
func dpKey(src any) string {
	if k, ok := src.(interface{ DataPointKey() hmtypes.DataPointKey }); ok {
		if p := k.DataPointKey().Parameter; p != "" {
			return p
		}
	}
	if named, ok := src.(interface{ Profile() string }); ok {
		if p := named.Profile(); p != "" {
			return p
		}
	}
	if named, ok := src.(interface{ Name() string }); ok {
		if n := named.Name(); n != "" {
			return n
		}
	}
	if src == nil {
		return "unknown"
	}
	return fmt.Sprintf("unknown(%T)", src)
}

func clusterIDs(servers []interfaces.MatterClusterServer) []uint32 {
	out := make([]uint32, 0, len(servers))
	for _, s := range servers {
		if s == nil {
			continue
		}
		out = append(out, s.MatterClusterID())
	}
	return out
}
