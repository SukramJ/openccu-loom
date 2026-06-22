// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/cdpkind"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// CustomDPWriter is an alias for the canonical interface in pkg/interfaces.
type CustomDPWriter = interfaces.CustomDPWriter

// ErrUnknownOperation is an alias for the sentinel in pkg/hmapi.
var ErrUnknownOperation = hmapi.ErrUnknownOperation

// ErrBadParam is an alias for the sentinel in pkg/hmapi.
var ErrBadParam = hmapi.ErrBadParam

// --- DTOs ---

// CustomDPSummary is one entry in GET .../cdps.
type CustomDPSummary struct {
	Name string `json:"name"`
	// UniqueID is the canonical loom-namespaced routing key for this
	// Custom-DP (the [routingkey.CanonicalUniqueID] result over the CDP's
	// primary channel address + parameter) — identical to the value on the
	// WS `custom_data_point.state_changed` payload. Lets a client seed its
	// entity registry from the summary without recomputing the algorithm.
	UniqueID            string   `json:"unique_id,omitempty"`
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

// cdpUniqueID stamps the canonical loom routing key for a Custom-DP from its
// primary channel address + parameter — identical to the WS
// custom_data_point.state_changed payload (see eventbridge). Empty serial
// suffix (central serial not yet known) yields "" so the omitempty field stays
// absent.
func cdpUniqueID(dp device.AttachableDataPoint, serialSuffix string) string {
	if dp == nil || serialSuffix == "" {
		return ""
	}
	k := dp.DataPointKey()
	return routingkey.CanonicalUniqueID(serialSuffix, k.ChannelAddress, k.Parameter, "")
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

// cdpName reads the `{name}` path parameter and URL-decodes it. Channel-
// group wire names embed an `@` (e.g. `STATE@3`); a conformant client
// that percent-encodes the path segment sends `STATE%403`. chi keeps the
// raw segment, so without decoding the lookup would miss and the invoke
// path would 502 with "data point STATE%403 not found". Falls back to the
// raw value when the segment is not valid percent-encoding so a literal
// `@` (which most clients send unencoded) keeps working.
func cdpName(r *http.Request) string {
	raw := chi.URLParam(r, "name")
	if decoded, err := url.PathUnescape(raw); err == nil {
		return decoded
	}
	return raw
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
		serial := idx.SerialSuffix(idx.CentralOf(d.Address))
		out := make([]CustomDPSummary, 0)
		for _, ch := range d.Channels() {
			dp := ch.CustomDataPoint()
			if dp == nil {
				continue
			}
			// Operation-mode secondary channels (e.g. HmIP-RGBW secondary
			// colour channels in the current mode) are folded into the primary
			// channel's aggregate and must not surface as their own entity.
			if h, ok2 := dp.(interface{ HiddenByOperationMode() bool }); ok2 && h.HiddenByOperationMode() {
				continue
			}
			cat := hmenum.DataPointCategoryUndefined
			if cdp, ok2 := dp.(device.CategorisedDataPoint); ok2 {
				cat = cdp.Category()
			}
			out = append(out, CustomDPSummary{
				Name:                customDPWireName(d, dp, ch.Number),
				UniqueID:            cdpUniqueID(dp, serial),
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
		name := cdpName(r)
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
		name := cdpName(r)
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
				problem.New(problem.TypeUpstreamUnavailable, r, "Operation failed", err.Error()))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
