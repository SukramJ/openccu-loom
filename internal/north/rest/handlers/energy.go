// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// energyDefaultGroup is used when the "group" query parameter is absent.
const energyDefaultGroup = "day"

// energyParameterPower/EnergyCounter/EnergyCounterFeedIn mirror
// pkg/hmenum's parameter constants without importing hmenum here — the
// handler package folds rows purely by the parameter string the store
// already filtered on. See docs/plans/A2-timeseries-energy.md "Power vs.
// energy (the key distinction)".
const (
	energyParameterPower               = "POWER"
	energyParameterEnergyCounter       = "ENERGY_COUNTER"
	energyParameterEnergyCounterFeedIn = "ENERGY_COUNTER_FEED_IN"
)

// EnergyBucket is one aggregated point in a device's energy breakdown.
// ConsumedWh/FeedInWh are the cumulative-counter deltas over the bucket
// (Wh); AvgPowerW/PeakPowerW summarise the instantaneous POWER samples in
// the same bucket (W). Reset is set when a cumulative counter went
// backwards within the bucket (meter reset on re-pair / firmware event) —
// see [FoldEnergyRows].
type EnergyBucket struct {
	TS         time.Time `json:"ts"`
	ConsumedWh float64   `json:"consumed_wh"`
	FeedInWh   float64   `json:"feed_in_wh"`
	AvgPowerW  float64   `json:"avg_power_w"`
	PeakPowerW float64   `json:"peak_power_w"`
	Reset      bool      `json:"reset"`
}

// EnergyDevice is one device's bucketed energy breakdown plus its
// range totals.
type EnergyDevice struct {
	Address         string         `json:"address"`
	Name            string         `json:"name"`
	Buckets         []EnergyBucket `json:"buckets"`
	TotalConsumedWh float64        `json:"total_consumed_wh"`
	TotalFeedInWh   float64        `json:"total_feed_in_wh"`
}

// EnergyResponse is the `GET /api/v1/energy` response body: one bucketed
// breakdown per device plus central totals, in Wh. The SPA divides by
// 1000 to render kWh.
type EnergyResponse struct {
	Group           string         `json:"group"`
	From            time.Time      `json:"from"`
	To              time.Time      `json:"to"`
	Devices         []EnergyDevice `json:"devices"`
	TotalConsumedWh float64        `json:"total_consumed_wh"`
	TotalFeedInWh   float64        `json:"total_feed_in_wh"`
}

// EnergyQuery is the parsed, validated request for the energy handler.
type EnergyQuery struct {
	Central string
	Device  string
	From    time.Time
	To      time.Time
	Group   string
}

// EnergyService is the read-side handle the SPA energy view needs. The
// cmd layer adapts the measurement store (+ device registry, for names)
// to this interface so the handler stays decoupled from the store and
// central packages — the same split [HistoryService] uses.
type EnergyService interface {
	Energy(ctx context.Context, q EnergyQuery) (EnergyResponse, error)
}

// GetEnergy serves `GET /api/v1/energy` — a per-device power/energy
// breakdown over time, sized for the energy view's chart + table.
//
// Query parameters (central, from, to are required):
//
//	?central=<name>
//	?from=<RFC3339>&to=<RFC3339>
//	?group=hour|day|month   default day
//	?device=<address>       optional; omitted = every energy device on the central
//
// Requires the opt-in history feature (the same one /history depends
// on); a nil service means the feature is disabled and the route is
// unmounted — mirrors [GetHistory]'s nil handling exactly so both
// endpoints fail the same way for the SPA's 404/503 mapping.
func GetEnergy(svc EnergyService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Energy aggregation unavailable", ""))
			return
		}
		q, errMsg := parseEnergyQuery(r)
		if errMsg != "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid query parameter", errMsg))
			return
		}
		resp, err := svc.Energy(r.Context(), q)
		if err != nil {
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, r, "Energy query failed", err.Error()))
			return
		}
		if resp.Devices == nil {
			resp.Devices = []EnergyDevice{}
		}
		JSON(w, http.StatusOK, resp)
	}
}

// parseEnergyQuery extracts and validates the query parameters. It
// returns a non-empty errMsg when a required parameter is missing or a
// value is malformed.
func parseEnergyQuery(r *http.Request) (q EnergyQuery, errMsg string) { //nolint:gocritic // named returns clarify dual-return semantics
	v := r.URL.Query()
	q = EnergyQuery{
		Central: v.Get("central"),
		Device:  v.Get("device"),
		Group:   energyDefaultGroup,
	}
	if q.Central == "" {
		return EnergyQuery{}, "central: required"
	}
	from, errMsg := parseRequiredTime(v.Get("from"), "from")
	if errMsg != "" {
		return EnergyQuery{}, errMsg
	}
	to, errMsg := parseRequiredTime(v.Get("to"), "to")
	if errMsg != "" {
		return EnergyQuery{}, errMsg
	}
	if !to.After(from) {
		return EnergyQuery{}, "to: must be after from"
	}
	q.From, q.To = from, to
	if g := v.Get("group"); g != "" {
		switch g {
		case "hour", "day", "month":
			q.Group = g
		default:
			return EnergyQuery{}, "group: must be one of hour, day, month"
		}
	}
	return q, ""
}

// EnergyRawRow is one (channel, parameter, bucket) rollup row as read
// from the store — the input [FoldEnergyRows] folds into the per-device
// response shape. It mirrors sqlite.EnergyRow field-for-field without
// importing the store package, keeping the handler import graph
// decoupled (same pattern as HistoryBucket vs. MeasurementBucket).
type EnergyRawRow struct {
	ChannelAddress string
	Parameter      string
	BucketTS       time.Time
	Sum            float64
	Min            float64
	Max            float64
	First          float64
	Last           float64
	Count          int64
}

// DeviceNamer resolves a device address to its display name for the
// energy device breakdown. ok is false when the address is unknown (the
// caller falls back to the bare address as the name).
type DeviceNamer func(address string) (name string, ok bool)

// deviceAddressOf returns the device address prefix of a channel
// address ("ABC0000001:4" -> "ABC0000001"; a bare device address without
// a channel suffix is returned unchanged).
func deviceAddressOf(channelAddress string) string {
	if i := strings.IndexByte(channelAddress, ':'); i >= 0 {
		return channelAddress[:i]
	}
	return channelAddress
}

// FoldEnergyRows folds per-channel per-bucket rollup rows into the
// per-device [EnergyResponse] shape. This is the correctness-critical
// part of the energy endpoint (docs/plans/A2-timeseries-energy.md "Power
// vs. energy (the key distinction)") — kept here, in the handler
// package, rather than in the cmd-layer adapter, so it is unit-testable
// without a live store or central registry:
//
//   - POWER (instantaneous, W): avg_power_w = sum/count (mean of the raw
//     samples folded into the bucket), peak_power_w = max. Multiple
//     POWER channels on the same device accumulate additively (their
//     instantaneous loads sum; the running mean of each channel is
//     summed as an approximation of the device's total average load).
//   - ENERGY_COUNTER (cumulative, Wh): consumed_wh = last - first.
//     Counter-reset rule: the meter can reset to 0 on device re-pair or
//     a firmware event, so last < first is possible; when that happens
//     the bucket's contribution is last (energy accumulated since the
//     reset) and Reset is set — a negative delta is never emitted.
//   - ENERGY_COUNTER_FEED_IN: identical delta/reset logic, folded into
//     feed_in_wh instead of consumed_wh.
//
// Device totals are the sum of their own buckets; the response totals
// are the sum of the device totals. rows need not be pre-sorted — the
// output devices and each device's buckets are always sorted
// (address, then bucket start) for a deterministic response.
func FoldEnergyRows(q EnergyQuery, rows []EnergyRawRow, nameOf DeviceNamer) EnergyResponse {
	type bucketKey struct {
		device string
		ts     int64
	}
	buckets := make(map[bucketKey]*EnergyBucket)
	deviceOf := make(map[string]struct{})

	for i := range rows {
		row := &rows[i]
		dev := deviceAddressOf(row.ChannelAddress)
		deviceOf[dev] = struct{}{}
		key := bucketKey{device: dev, ts: row.BucketTS.UnixMilli()}
		b, ok := buckets[key]
		if !ok {
			b = &EnergyBucket{TS: row.BucketTS}
			buckets[key] = b
		}
		switch row.Parameter {
		case energyParameterPower:
			if row.Count > 0 {
				b.AvgPowerW += row.Sum / float64(row.Count)
			}
			if row.Max > b.PeakPowerW {
				b.PeakPowerW = row.Max
			}
		case energyParameterEnergyCounter:
			delta, reset := energyDelta(row.First, row.Last)
			b.ConsumedWh += delta
			b.Reset = b.Reset || reset
		case energyParameterEnergyCounterFeedIn:
			delta, reset := energyDelta(row.First, row.Last)
			b.FeedInWh += delta
			b.Reset = b.Reset || reset
		}
	}

	devices := make([]string, 0, len(deviceOf))
	for dev := range deviceOf {
		devices = append(devices, dev)
	}
	sort.Strings(devices)

	out := EnergyResponse{Group: q.Group, From: q.From, To: q.To}
	for _, dev := range devices {
		var bs []EnergyBucket
		for key, b := range buckets {
			if key.device == dev {
				bs = append(bs, *b)
			}
		}
		sort.Slice(bs, func(i, j int) bool { return bs[i].TS.Before(bs[j].TS) })

		name := dev
		if nameOf != nil {
			if n, ok := nameOf(dev); ok && n != "" {
				name = n
			}
		}
		d := EnergyDevice{Address: dev, Name: name, Buckets: bs}
		for _, b := range bs {
			d.TotalConsumedWh += b.ConsumedWh
			d.TotalFeedInWh += b.FeedInWh
		}
		out.Devices = append(out.Devices, d)
		out.TotalConsumedWh += d.TotalConsumedWh
		out.TotalFeedInWh += d.TotalFeedInWh
	}
	return out
}

// energyDelta applies the counter-reset rule to one cumulative-counter
// bucket: the normal case is last-first; when the meter reset within the
// bucket (last < first), the delta is last (energy accumulated since the
// reset) and reset is true. Never returns a negative delta.
func energyDelta(first, last float64) (delta float64, reset bool) {
	if last < first {
		return last, true
	}
	return last - first, false
}
