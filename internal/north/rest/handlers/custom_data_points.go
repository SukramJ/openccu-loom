// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/cdpkind"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// CustomDPWriter is the mutating surface Phase C handlers use to
// invoke operations on custom data points. The daemon wires a concrete
// implementation that translates the abstract (device address, name,
// operation, params) tuple into the appropriate model method call and
// ultimately pushes a wire command.
//
// Implementations are responsible for audit-log entries — the handler
// layer passes Source ("rest:custom-dp:PUT") so the entry has provenance.
type CustomDPWriter interface {
	// InvokeCustomDP dispatches `operation` with `params` on the custom
	// data point identified by `deviceAddress` and `name`. Returns
	// ErrUnknownOperation when the operation string is not in the
	// dispatch table for the DP's category, and ErrBadParam when a
	// required param is missing or out of range.
	InvokeCustomDP(
		ctx context.Context,
		deviceAddress string,
		name string,
		operation string,
		params map[string]any,
		priority hmenum.CommandPriority,
		source string,
	) error
}

// --- sentinel errors (used by CustomDPWriter implementations) ---

// ErrUnknownOperation is returned by InvokeCustomDP when `operation`
// is not in the dispatch table for the data point's category.
var ErrUnknownOperation = errors.New("custom_dp: unknown operation")

// ErrBadParam is returned when a required param is missing or out of range.
var ErrBadParam = errors.New("custom_dp: bad parameter")

// --- DTOs ---

// CustomDPSummary is one entry in GET .../cdps.
type CustomDPSummary struct {
	Name                string   `json:"name"`
	Category            string   `json:"category"`
	ChannelNo           int      `json:"channel_no"`
	SupportedOperations []string `json:"supported_operations"`
	// Kind is a stable widget hint (`light`, `light_color`,
	// `light_color_temp`, `cover_blind`, `cover_garage`,
	// `climate_hmip`, `climate_rf`, `climate_simple`, `lock`,
	// `siren`, `switch`, `text_display`, …) derived from the
	// concrete Custom-DP type. Drives the SPA's CDP-aware widget
	// selection — see ADR 0016. Empty when the kind classifier
	// does not recognise the Custom-DP.
	Kind string `json:"kind,omitempty"`
	// Channels lists every CCU channel this Custom-DP composes
	// (primary channel + group siblings via
	// RebasedChannelGroupConfig). Lets the SPA's CDP-first view
	// hide channels that are already represented by a CDP tile.
	// Always contains at least the primary channel.
	Channels []int `json:"channels,omitempty"`
	// Capabilities is a flat string→bool map naming the optional
	// features the device exposes (e.g. `dimmable`, `color`,
	// `color_temp`, `tilt`, `boost`, `away`). Mirrors the
	// per-category Capability struct from
	// `internal/model/custom/mixins.go`; the flat shape keeps the
	// REST DTO category-agnostic.
	Capabilities map[string]bool `json:"capabilities,omitempty"`
	// Config is the static configuration block from the Custom-DP
	// — temperature bounds, available HVAC modes, available preset /
	// week-program slots, etc. Populated from the DP's Config()
	// when it implements [payload.Source].
	// Lets the SPA render kind-specific choice lists (climate
	// preset_modes, hvac_modes; cover capability hints) without a
	// second round-trip.
	Config payload.ConfigPayload `json:"config,omitempty"`
	// State is the live state snapshot from the Custom-DP — the same
	// semantic keys the WS `custom_data_point.state_changed` event
	// carries (`is_locked`, `hvac_mode`, `brightness`, …). Including
	// it in the list lets clients seed entity state at bootstrap
	// without one extra round-trip per CDP.
	State payload.StatePayload `json:"state,omitempty"`
}

// CustomDPDetail is returned by GET .../cdps/{name}.
type CustomDPDetail struct {
	Name      string `json:"name"`
	Category  string `json:"category"`
	ChannelNo int    `json:"channel_no"`
	State     any    `json:"state"`
}

// CustomDPOperationRequest is the body for POST .../cdps/{name}/{operation}.
// The operation is now in the URL path; only params and priority remain in the body.
type CustomDPOperationRequest struct {
	Params   map[string]any `json:"params,omitempty"`
	Priority string         `json:"priority,omitempty"`
}

// --- helpers ---

// customDPName returns a stable string name for an AttachableDataPoint.
// The name is derived from the DataPointKey's Parameter field, which is
// the canonical identifier used across the model.
func customDPName(dp device.AttachableDataPoint) string {
	return dp.DataPointKey().Parameter
}

// supportedOperationsFor returns the list of valid operation strings for
// the given data-point category. Mirrors the per-category dispatch tables
// in the custom_dispatch_*.go files.
func supportedOperationsFor(cat hmenum.DataPointCategory) []string { //nolint:exhaustive // non-custom categories return an empty list
	switch cat {
	case hmenum.DataPointCategoryLight:
		return []string{"turn_on", "turn_off", "set_brightness", "set_color", "set_color_temperature", "set_effect"}
	case hmenum.DataPointCategoryClimate:
		return []string{"set_temperature", "enable_boost", "disable_boost", "set_mode", "set_profile", "enable_away", "disable_away"}
	case hmenum.DataPointCategoryCover:
		return []string{"open", "close", "set_position", "stop", "set_tilt"}
	case hmenum.DataPointCategoryLock:
		return []string{"lock", "unlock", "open"}
	case hmenum.DataPointCategorySiren:
		return []string{"turn_on", "turn_off"}
	case hmenum.DataPointCategoryTextDisplay:
		return []string{"write", "clear"}
	case hmenum.DataPointCategoryValve:
		return []string{"open", "close", "set_level"}
	case hmenum.DataPointCategorySwitch:
		return []string{"turn_on", "turn_off", "turn_on_for", "toggle"}
	default:
		// Never nil: the wire schema declares supported_operations as a
		// required array, and clients (the HA drop-in) reject a null.
		return []string{}
	}
}

// customDPConfig returns the Custom-DP's static configuration block
// (temperature bounds, available preset / hvac modes, …). Reads the
// typed [payload.ConfigPayload] via the [payload.Source] interface
// and returns it directly — encoding/json marshals the concrete typed
// struct into the same key/value pairs as the previous map conversion.
// Returns nil when the DP exposes no config; the field then becomes
// omitempty in the wire JSON.
func customDPConfig(dp device.AttachableDataPoint) payload.ConfigPayload {
	src, ok := dp.(payload.Source)
	if !ok || src == nil {
		return nil
	}
	return src.Config()
}

// customDPState returns a JSON-serialisable state snapshot for a
// custom data point. The concrete type is resolved via interface
// assertions so the handler layer stays decoupled from the model
// packages.
//
// Resolution order:
//  1. [payload.Source] — the universal contract every shipping
//     Custom-DP implements (ADR 0007); `State()` returns the typed
//     payload struct (`*LockState`, `*ClimateState`, …) that also
//     powers HA-Discovery aggregated state.
//  2. `DataPointState() any` — legacy / future hook; kept so a DP
//     that needs a non-Source shape can override.
//  3. Fallback: expose the DataPointKey so the DP stays addressable
//     even when state is unavailable.
func customDPState(dp device.AttachableDataPoint) any {
	if src, ok := dp.(payload.Source); ok {
		if p := src.State(); p != nil {
			return p
		}
	}
	type dpStater interface{ DataPointState() any }
	if s, ok := dp.(dpStater); ok {
		return s.DataPointState()
	}
	key := dp.DataPointKey()
	return map[string]any{
		"channel_address": key.ChannelAddress,
		"paramset_key":    string(key.ParamsetKey),
		"parameter":       key.Parameter,
	}
}

// customDPWireName returns the wire identity for a custom DP — the bare
// parameter name when unique on the device, `PARAM@<channel>` for
// profile channel groups. Delegates to [custom.WireName].
func customDPWireName(d *device.Device, dp device.AttachableDataPoint, channelNo int) string {
	return custom.WireName(d, dp, channelNo)
}

// lookupCustomDP resolves the named custom data point across all
// channels of the device. Accepts both the bare parameter name and the
// channel-exact `PARAM@<channel>` form. Delegates to
// [custom.FindByWireName].
func lookupCustomDP(d *device.Device, name string) (device.AttachableDataPoint, int, bool) {
	return custom.FindByWireName(d, name)
}

// ListCustomDataPoints returns all custom DPs of a device.
//
//	GET /api/v1/devices/{addr}/cdps
func ListCustomDataPoints(idx DeviceIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addr := chi.URLParam(r, "addr")
		d, ok := idx.Device(addr)
		if !ok {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Device not found", addr))
			return
		}
		out := make([]CustomDPSummary, 0)
		for _, ch := range d.Channels() {
			dp := ch.CustomDataPoint()
			if dp == nil {
				continue
			}
			cat := hmenum.DataPointCategoryUndefined
			if cdp, ok2 := dp.(device.CategorisedDataPoint); ok2 {
				cat = cdp.Category()
			}
			out = append(out, CustomDPSummary{
				Name:                customDPWireName(d, dp, ch.Number),
				Category:            string(cat),
				ChannelNo:           ch.Number,
				SupportedOperations: supportedOperationsFor(cat),
				Kind:                cdpkind.Of(dp),
				Channels:            []int{ch.Number},
				Capabilities:        cdpkind.Capabilities(dp),
				Config:              customDPConfig(dp),
				State:               customDPState(dp),
			})
		}
		JSON(w, http.StatusOK, out)
	}
}

// GetCustomDataPoint returns a single custom DP by name.
//
//	GET /api/v1/devices/{addr}/cdps/{name}
func GetCustomDataPoint(idx DeviceIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addr := chi.URLParam(r, "addr")
		d, ok := idx.Device(addr)
		if !ok {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Device not found", addr))
			return
		}
		name := chi.URLParam(r, "name")
		dp, chNo, found := lookupCustomDP(d, name)
		if !found {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Custom data point not found", name))
			return
		}
		cat := hmenum.DataPointCategoryUndefined
		if cdp, ok2 := dp.(device.CategorisedDataPoint); ok2 {
			cat = cdp.Category()
		}
		JSON(w, http.StatusOK, CustomDPDetail{
			Name:      name,
			Category:  string(cat),
			ChannelNo: chNo,
			State:     customDPState(dp),
		})
	}
}

// InvokeCustomDataPoint dispatches an operation on a custom DP.
//
//	POST /api/v1/devices/{addr}/cdps/{name}/{operation}
//	Body: {"params": {...}, "priority": "high"}
func InvokeCustomDataPoint(idx DeviceIndex, writer CustomDPWriter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if writer == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "CustomDP writer not wired", ""))
			return
		}
		addr := chi.URLParam(r, "addr")
		if _, ok := idx.Device(addr); !ok {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Device not found", addr))
			return
		}
		name := chi.URLParam(r, "name")
		operation := chi.URLParam(r, "operation")
		if operation == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "operation is required", ""))
			return
		}
		var req CustomDPOperationRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		prio := parsePriority(req.Priority)
		err := writer.InvokeCustomDP(
			r.Context(),
			addr,
			name,
			operation,
			req.Params,
			prio,
			"rest:custom-dp:POST",
		)
		if err != nil {
			if errors.Is(err, ErrUnknownOperation) {
				problem.Write(w, http.StatusBadRequest,
					problem.New(problem.TypeBadRequest, r, "Unknown operation", operation))
				return
			}
			if errors.Is(err, ErrBadParam) {
				problem.Write(w, http.StatusUnprocessableEntity,
					problem.New(problem.TypeBadRequest, r, "Bad parameter", err.Error()))
				return
			}
			// Log every 502 with the originating error so the dev /
			// support engineer can grep the daemon log without having
			// to enable DEBUG. The UI only sees the problem.detail.
			slog.WarnContext(r.Context(), "cdp.invoke.failed",
				slog.String("device", addr),
				slog.String("name", name),
				slog.String("operation", operation),
				slog.String("error", err.Error()))
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeInternal, r, "Operation failed", err.Error()))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
