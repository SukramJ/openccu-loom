// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// --- DTOs ---

// CalculatedDPSummary is one entry in GET .../calc-dps.
type CalculatedDPSummary struct {
	Name string `json:"name"`
	// UniqueID is the canonical loom-namespaced routing key for this
	// calculated data point (the [routingkey.CanonicalUniqueID] result over
	// its channel address + parameter) — the same canonical key generic DPs
	// carry. Lets a client seed its entity registry from the summary without
	// recomputing the algorithm. Always present and non-empty (the central's
	// serial is resolved before any entity is served — see
	// [DataPointSummary.UniqueID]).
	UniqueID string `json:"unique_id"`
	Category string `json:"category,omitempty"`
	Value    any    `json:"value"`
	// DisplayValue is Value expressed in the data point's reported unit
	// (Value × multiplier), present only when that projection is
	// non-trivial. Same contract as [DataPointSummary.DisplayValue]; every
	// shipping calculated data point currently reports a trivial
	// multiplier (1.0), so this stays absent today and starts populating
	// automatically for any future one that does not.
	DisplayValue any  `json:"display_value,omitempty"`
	Observed     bool `json:"observed"`
	// Available reports whether the value is a confirmed reading, using the
	// same rule as the generic data-point state payload: observed AND valid.
	// For a calculated data point validity folds in the validity of every
	// source it derives from — a derived value is only as good as its inputs,
	// so a source the CCU flagged (bad paired STATUS, reading outside the
	// declared bounds) takes the calculated data point down with it even
	// though the derived number itself keeps updating. Clients that restore a
	// previous state for unavailable entities need this flag; `observed`
	// alone stays true across a source fault.
	Available bool `json:"available"`
	// TranslatedName is the locale-aware per-entity name, resolved
	// through the same chain as generic data points (channel-typed
	// OCCU translation → bare-parameter translation → title-cased
	// parameter). The reference stack consults the identical
	// translation catalogue for calculated and combined data points
	// and falls back to `parameter.title().replace("_", " ")`, so a
	// client rendering this field spawns identically-named entities.
	TranslatedName string `json:"translated_name,omitempty"`
	ModifiedAt     string `json:"modified_at,omitempty"`
}

// CalculatedDPDetail extends [CalculatedDPSummary] with the dependency list.
type CalculatedDPDetail struct {
	CalculatedDPSummary
	DependsOn []string `json:"depends_on,omitempty"`
}

// toCalculatedDPSummary renders an AttachableDataPoint as a CalculatedDPSummary.
// ch and labels feed the translated-name resolution; both are
// nil-tolerant (the field is simply omitted then).
func toCalculatedDPSummary(dp device.AttachableDataPoint, ch *device.Channel, labels ParameterLabeler, serialSuffix string) CalculatedDPSummary {
	key := dp.DataPointKey()
	s := CalculatedDPSummary{
		Name:     key.Parameter,
		UniqueID: cdpUniqueID(dp, serialSuffix),
	}
	s.TranslatedName = CalculatedDPTranslatedName(ch, key.Parameter, labels)
	if cdp, ok := dp.(device.CategorisedDataPoint); ok {
		s.Category = string(cdp.Category())
	}
	// Try to extract the value through the RawFloatValue accessor that
	// all *generic.Sensor[float64]-backed types expose.
	type floatValuer interface {
		RawValue() (any, bool)
		ModifiedAt() time.Time
	}
	if fv, ok := dp.(floatValuer); ok {
		raw, observed := fv.RawValue()
		s.Value = raw
		s.Observed = observed
		if observed {
			if m, mOK := dp.(interface{ Multiplier() float64 }); mOK {
				if dv, dvOK := generic.DisplayValue(raw, m.Multiplier()); dvOK {
					s.DisplayValue = dv
				}
			}
		}
		if t := fv.ModifiedAt(); !t.IsZero() {
			s.ModifiedAt = t.UTC().Format("2006-01-02T15:04:05.000Z")
		}
	}
	s.Available = CalculatedDPAvailable(dp, s.Observed)
	return s
}

// CalculatedDPAvailable mirrors the `available` flag the generic data-point
// state payload computes (observed AND IsValid). A data point that does not
// expose IsValid falls back to plain observation, matching the north-bound
// convention that an unclassifiable data point is treated as available.
//
// Exported so the WebSocket calc-dp renderer emits the identical flag —
// REST and WS consumers must not disagree about a data point's availability.
func CalculatedDPAvailable(dp device.AttachableDataPoint, observed bool) bool {
	if !observed {
		return false
	}
	if v, ok := dp.(interface{ IsValid() bool }); ok {
		return v.IsValid()
	}
	return true
}

// CalculatedDPTranslatedName resolves the locale-aware entity name for
// a calculated/combined data point through the same primitives as the
// generic data-point handler (device.TranslatedDataPointLabel →
// naming.EntityDisplayName), so REST, WS, and MQTT consumers spawn
// entities with identical names. The custom translation catalogue
// carries entries for the synthetic calculated parameters (lowercase
// keys: dew_point → "Taupunkt", duration → "Zeitdauer", …) — the same
// catalogue the reference stack consults — so the usual outcome is the
// localised label; the title-cased fallback only covers parameters
// without an entry. Verified live against a de-locale daemon:
// DEW_POINT/ENTHALPY/VAPOR_CONCENTRATION/OPERATING_VOLTAGE_LEVEL and
// the combined DURATION all resolve to the reference labels.
func CalculatedDPTranslatedName(ch *device.Channel, parameter string, labels ParameterLabeler) string {
	if ch == nil {
		return ""
	}
	t, ok := labels.(device.ParameterTranslator)
	if !ok {
		return naming.TitleCaseParameter(parameter)
	}
	label, labelOmitted := device.TranslatedDataPointLabel(ch, parameter, ch.Type, t)
	name, _ := naming.EntityDisplayName(label, labelOmitted, parameter)
	return name
}

// sourceParameterProvider is implemented by every calculated sensor that
// embeds the model's sourceSink (internal/model/calculated/state_uncertain.go)
// — every shipping calculated data point. dependsOn reads the actual
// resolved source set instead of re-deriving it from the parameter name, so
// the REST projection tracks whatever a sensor's Subscribe hook wires up
// (including a fallback like OPERATING_VOLTAGE_LEVEL's OPERATING_VOLTAGE /
// BATTERY_STATE choice) without needing its own copy of that logic.
type sourceParameterProvider interface {
	SourceParameters() []string
}

// dependsOn returns the wire parameters dp actually depends on, as resolved
// by the model's Subscribe wiring. A calculated data point that has not been
// subscribed yet (no channel to resolve sources from) or that does not
// implement [sourceParameterProvider] reports no dependencies rather than a
// guessed one.
func dependsOn(dp device.AttachableDataPoint) []string {
	if sp, ok := dp.(sourceParameterProvider); ok {
		return sp.SourceParameters()
	}
	return nil
}

// lookupCalculatedDP finds the calculated DP by channel number and name.
// Returns the hosting channel alongside so callers can resolve the
// channel-typed translated name.
func lookupCalculatedDPByChannelAndName(d *device.Device, channelNo int, name string) (device.AttachableDataPoint, *device.Channel, bool) {
	// Find the channel by number.
	for _, ch := range d.Channels() {
		if ch.Number != channelNo {
			continue
		}
		for _, dp := range ch.CalculatedDataPoints() {
			if dp.DataPointKey().Parameter == name {
				return dp, ch, true
			}
		}
	}
	return nil, nil, false
}

// --- handlers ---

// ListCalculatedDataPoints returns all calculated DPs on a channel.
//
//	GET /api/v1/devices/{addr}/channels/{no}/calc-dps
func ListCalculatedDataPoints(idx DeviceIndex, labels ParameterLabeler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addr := chi.URLParam(r, "addr")
		d, ok := idx.Device(addr)
		if !ok {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Device not found", addr))
			return
		}
		noStr := chi.URLParam(r, "no")
		chNo, err := strconv.Atoi(noStr)
		if err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid channel number", noStr))
			return
		}
		// Find the channel.
		chAddr := addr + ":" + noStr
		ch := d.Channel(chAddr)
		if ch == nil {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Channel not found", chAddr))
			return
		}
		_ = chNo
		dps := ch.CalculatedDataPoints()
		serial := serialSuffixForChannel(idx, ch)
		out := make([]CalculatedDPSummary, 0, len(dps))
		for _, dp := range dps {
			out = append(out, toCalculatedDPSummary(dp, ch, labels, serial))
		}
		JSON(w, http.StatusOK, out)
	}
}

// GetCalculatedDataPoint returns a single calculated DP by name.
//
//	GET /api/v1/devices/{addr}/channels/{no}/calc-dps/{name}
func GetCalculatedDataPoint(idx DeviceIndex, labels ParameterLabeler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addr := chi.URLParam(r, "addr")
		d, ok := idx.Device(addr)
		if !ok {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Device not found", addr))
			return
		}
		noStr := chi.URLParam(r, "no")
		chNo, err := strconv.Atoi(noStr)
		if err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid channel number", noStr))
			return
		}
		name := chi.URLParam(r, "name")
		dp, ch, found := lookupCalculatedDPByChannelAndName(d, chNo, name)
		if !found {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Calculated data point not found", name))
			return
		}
		summary := toCalculatedDPSummary(dp, ch, labels, serialSuffixForChannel(idx, ch))
		detail := CalculatedDPDetail{
			CalculatedDPSummary: summary,
			DependsOn:           dependsOn(dp),
		}
		JSON(w, http.StatusOK, detail)
	}
}
