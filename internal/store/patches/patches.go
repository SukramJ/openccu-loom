// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package patches

import (
	"encoding/json"
	"log/slog"
	"strings"
	"sync"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Patch is one idempotent modification applied to a parameter
// descriptor. The [Apply] function mutates the data in place and
// returns true when it actually changed something.
//
// ChannelNo (optional) and Reason fields added to mirror the Python
// reference implementation's channel_no + reason fields.
type Patch struct {
	Model     string // "" = any model
	Parameter hmenum.Parameter
	Paramset  hmenum.ParamsetKey // "" = any paramset
	// ChannelNo restricts the patch to a specific channel number.
	// nil matches all channels (Python: channel_no = None).
	ChannelNo *int
	// Reason is an optional human-readable justification for the patch.
	Reason string
	// Ticket is an optional reference to the upstream issue or PR that motivated
	// the patch (e.g. For audit purposes only; not used at runtime.
	Ticket string
	Apply  func(pd *hmproto.ParameterData) bool
}

// Registry stores the list of active patches.
type Registry struct {
	mu      sync.RWMutex
	patches []Patch
}

// NewRegistry returns a registry pre-populated with the built-ins.
func NewRegistry() *Registry {
	r := &Registry{}
	r.patches = append(r.patches, builtIns()...)
	return r
}

// Register appends a patch.
func (r *Registry) Register(p Patch) {
	r.mu.Lock()
	r.patches = append(r.patches, p)
	r.mu.Unlock()
}

// ApplyTo runs every matching patch against pd for the given channel address.
// The channelAddress is used to extract the channel number for ChannelNo-scoped patches.
// Returns the number of patches that actually modified the descriptor.
//
// channel-no matching added; device_type pre-filter by Model.
func (r *Registry) ApplyTo(model string, paramset hmenum.ParamsetKey, parameter hmenum.Parameter, pd *hmproto.ParameterData) int {
	return r.applyToWithChannel(model, paramset, parameter, pd, -1)
}

// applyToWithChannel is the internal implementation that accepts a channel
// number. channelNo == -1 means "no channel / don't filter by channel".
//
// first-match (most-specific-first) semantics. Exact match: channel_no +
// paramset + parameter 2. Any channel: nil channel_no + paramset + parameter
// 3. Any paramset: channel_no + nil paramset + parameter 4. Any channel &
// paramset: nil + nil + parameter
//
// Each probe returns the first matching patch in registration order; once a
// probe tier finds a match no lower tier is consulted.
func (r *Registry) applyToWithChannel(model string, paramset hmenum.ParamsetKey, parameter hmenum.Parameter, pd *hmproto.ParameterData, channelNo int) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if pd == nil {
		return 0
	}

	// Resolve whether channelNo is known (>= 0).
	chKnown := channelNo >= 0

	// Find first matching patch across the four priority tiers.
	// Tier matching: most-specific first (channel+paramset → no-channel+paramset
	// → channel+no-paramset → no-channel+no-paramset).
	// A patch with Model != "" must also match the model.
	type tierKey struct {
		hasChannel  bool
		hasParamset bool
	}
	tiers := []tierKey{
		{true, true},   // exact: channel + paramset
		{false, true},  // any channel, exact paramset
		{true, false},  // exact channel, any paramset
		{false, false}, // any channel, any paramset
	}

	for _, tier := range tiers {
		for _, p := range r.patches {
			// Model filter.
			if p.Model != "" && !strings.EqualFold(p.Model, model) {
				continue
			}
			// Parameter filter.
			if p.Parameter != "" && p.Parameter != parameter {
				continue
			}
			// Tier: channel dimension.
			if tier.hasChannel {
				// This tier requires an exact channel match.
				if !chKnown || p.ChannelNo == nil || *p.ChannelNo != channelNo {
					continue
				}
			} else {
				// This tier requires no channel restriction (nil ChannelNo).
				if p.ChannelNo != nil {
					continue
				}
			}
			// Tier: paramset dimension.
			if tier.hasParamset {
				// This tier requires an exact paramset match.
				if p.Paramset == "" || p.Paramset != paramset {
					continue
				}
			} else {
				// This tier requires no paramset restriction (empty Paramset).
				if p.Paramset != "" {
					continue
				}
			}
			// Found the most-specific match for this parameter: apply and return.
			if p.Apply != nil && p.Apply(pd) {
				if p.Reason != "" {
					slog.Debug(
						"paramset patch applied",
						"model", model,
						"paramset", paramset,
						"parameter", parameter,
						"reason", p.Reason,
					)
				}
				return 1
			}
			// Patch matched but did not change anything — still counts as
			// "found a match", so we do not fall through to less-specific tiers.
			return 0
		}
	}
	return 0
}

// ApplyParamset applies all matching patches to every parameter in ps for the
// given channel address. This is the ingestion-time entry point called by
// ParamsetRegistry.Add. Returns the total number of field changes across all
// parameters.
//
// _address_parameter_cache pre-filtering by device_type is handled here via
// the Model field on each Patch.
func (r *Registry) ApplyParamset(model, channelAddress string, paramset hmenum.ParamsetKey, ps hmproto.Paramset) int {
	if len(ps) == 0 {
		return 0
	}
	// Pre-check: does this model have any patches at all?
	r.mu.RLock()
	hasAny := false
	for _, p := range r.patches {
		if p.Model == "" || strings.EqualFold(p.Model, model) {
			hasAny = true
			break
		}
	}
	r.mu.RUnlock()
	if !hasAny {
		return 0
	}

	_, channelNo, _ := hmtypes.SplitChannelAddress(channelAddress)
	total := 0
	for param := range ps {
		pd := ps[param]
		n := r.applyToWithChannel(model, paramset, hmenum.Parameter(param), &pd, channelNo)
		if n > 0 {
			ps[param] = pd
			total += n
		}
	}
	return total
}

// Len reports the patch count.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.patches)
}

// HasPatches reports whether any patches are registered.
func (r *Registry) HasPatches() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.patches) > 0
}

// builtIns returns the factory-shipped patches. They.
func builtIns() []Patch {
	ch0 := 0
	ch1 := 1
	return []Patch{
		// ENERGY_COUNTER on HM-ES-PMSw1-Pl loses the UNIT annotation on
		// some CCU firmwares — patch it back to "Wh".
		{
			Model:     "HM-ES-PMSw1-Pl",
			Parameter: hmenum.Parameter("ENERGY_COUNTER"),
			Paramset:  hmenum.ParamsetKeyValues,
			Reason:    "CCU omits UNIT for ENERGY_COUNTER on some firmwares",
			Apply: func(pd *hmproto.ParameterData) bool {
				if pd.Unit == "" {
					pd.Unit = "Wh"
					return true
				}
				return false
			},
		},
		// HmIP-RGBW sometimes reports SATURATION without the EVENT bit.
		{
			Model:     "HmIP-RGBW",
			Parameter: hmenum.ParameterSaturation,
			Paramset:  hmenum.ParamsetKeyValues,
			Reason:    "HmIP-RGBW omits EVENT bit on SATURATION",
			Apply: func(pd *hmproto.ParameterData) bool {
				if !pd.Operations.IsEvent() {
					pd.Operations |= hmenum.OperationsEvent
					return true
				}
				return false
			},
		},
		// HM-CC-VG-1 virtual heating group: CCU returns invalid MIN/MAX bounds for
		// SET_TEMPERATURE (0/0 or string).
		{
			Model:     "HM-CC-VG-1",
			Parameter: hmenum.ParameterSetTemperature,
			Paramset:  hmenum.ParamsetKeyValues,
			ChannelNo: &ch1,
			Reason:    "CCU returns invalid MIN/MAX bounds for virtual heating groups",
			Apply: func(pd *hmproto.ParameterData) bool {
				wantMin := json.RawMessage(`4.5`)
				wantMax := json.RawMessage(`30.5`)
				changed := false
				if string(pd.Min) != string(wantMin) {
					pd.Min = wantMin
					changed = true
				}
				if string(pd.Max) != string(wantMax) {
					pd.Max = wantMax
					changed = true
				}
				return changed
			},
		},
		// HmIP-FWI fingerprint reader: the CCU declares MAX=21 for CODE_ID, but the
		// device reports CODE_ID=31 in idle/standby (5-bit field, 31 = no active
		// code). The too-low MAX dropped the idle value, so the entity never
		// returned to 31 after a recognized code. Widen MAX to 31; MIN stays.
		{
			Model:     "HmIP-FWI",
			Parameter: hmenum.ParameterCodeID,
			Paramset:  hmenum.ParamsetKeyValues,
			ChannelNo: &ch0,
			Reason:    "CCU declares MAX=21 but device reports idle CODE_ID=31",
			Ticket:    "#3238",
			Apply: func(pd *hmproto.ParameterData) bool {
				wantMax := json.RawMessage(`31`)
				if string(pd.Max) != string(wantMax) {
					pd.Max = wantMax
					return true
				}
				return false
			},
		},
	}
}
