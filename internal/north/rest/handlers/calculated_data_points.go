// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// --- DTOs ---

// CalculatedDPSummary is one entry in GET .../calc-dps.
type CalculatedDPSummary struct {
	Name       string `json:"name"`
	Category   string `json:"category,omitempty"`
	Value      any    `json:"value"`
	Observed   bool   `json:"observed"`
	ModifiedAt string `json:"modified_at,omitempty"`
}

// CalculatedDPDetail extends [CalculatedDPSummary] with the dependency list.
type CalculatedDPDetail struct {
	CalculatedDPSummary
	DependsOn []string `json:"depends_on,omitempty"`
}

// toCalculatedDPSummary renders an AttachableDataPoint as a CalculatedDPSummary.
func toCalculatedDPSummary(dp device.AttachableDataPoint) CalculatedDPSummary {
	key := dp.DataPointKey()
	s := CalculatedDPSummary{
		Name: key.Parameter,
	}
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
		if t := fv.ModifiedAt(); !t.IsZero() {
			s.ModifiedAt = t.UTC().Format("2006-01-02T15:04:05.000Z")
		}
	}
	return s
}

// dependsOnForKey returns the wire parameters a calculated DP depends on,
// inferred from the parameter name conventions. This is a best-effort
// mapping that covers the known sensor types.
func dependsOnForKey(key hmtypes.DataPointKey) []string {
	switch key.Parameter {
	case "DEW_POINT", "FROST_POINT", "VAPOR_CONCENTRATION", "DEW_POINT_SPREAD", "ENTHALPY", "APPARENT_TEMPERATURE":
		return []string{"ACTUAL_TEMPERATURE", "HUMIDITY"}
	case "OPERATING_VOLTAGE_LEVEL":
		return []string{"OPERATING_VOLTAGE"}
	}
	return nil
}

// lookupCalculatedDP finds the calculated DP by channel number and name.
func lookupCalculatedDPByChannelAndName(d *device.Device, channelNo int, name string) (device.AttachableDataPoint, bool) {
	// Find the channel by number.
	for _, ch := range d.Channels() {
		if ch.Number != channelNo {
			continue
		}
		for _, dp := range ch.CalculatedDataPoints() {
			if dp.DataPointKey().Parameter == name {
				return dp, true
			}
		}
	}
	return nil, false
}

// --- handlers ---

// ListCalculatedDataPoints returns all calculated DPs on a channel.
//
//	GET /api/v1/devices/{addr}/channels/{no}/calc-dps
func ListCalculatedDataPoints(idx DeviceIndex) http.HandlerFunc {
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
		out := make([]CalculatedDPSummary, 0, len(dps))
		for _, dp := range dps {
			out = append(out, toCalculatedDPSummary(dp))
		}
		JSON(w, http.StatusOK, out)
	}
}

// GetCalculatedDataPoint returns a single calculated DP by name.
//
//	GET /api/v1/devices/{addr}/channels/{no}/calc-dps/{name}
func GetCalculatedDataPoint(idx DeviceIndex) http.HandlerFunc {
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
		dp, found := lookupCalculatedDPByChannelAndName(d, chNo, name)
		if !found {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Calculated data point not found", name))
			return
		}
		summary := toCalculatedDPSummary(dp)
		detail := CalculatedDPDetail{
			CalculatedDPSummary: summary,
			DependsOn:           dependsOnForKey(dp.DataPointKey()),
		}
		JSON(w, http.StatusOK, detail)
	}
}
