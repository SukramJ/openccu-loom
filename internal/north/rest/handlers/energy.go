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
// already filtered on. POWER is an instantaneous reading; the ENERGY_*
// counters are monotonic totals — the two are folded differently.
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

// counterReading is one bucket's (first, last) pair for a single cumulative
// counter series (one channel, one parameter), tagged with the bucket start
// so the readings can be ordered before the range total is computed.
type counterReading struct {
	ts    int64
	first float64
	last  float64
}

// FoldEnergyRows folds per-channel per-bucket rollup rows into the
// per-device [EnergyResponse] shape. This is the correctness-critical
// part of the energy endpoint — instantaneous POWER and monotonic
// ENERGY_* counters fold differently — kept here, in the handler
// package, rather than in the cmd-layer adapter, so it is unit-testable
// without a live store or central registry:
//
//   - POWER (instantaneous, W): avg_power_w = sum/count (mean of the raw
//     samples folded into the bucket), peak_power_w = max. Multiple
//     POWER channels on the same device accumulate additively (their
//     instantaneous loads sum; the running mean of each channel is
//     summed as an approximation of the device's total average load).
//   - ENERGY_COUNTER (cumulative, Wh): each bucket reports consumed_wh =
//     last - first for the chart. Counter-reset rule: the meter can reset
//     to 0 on device re-pair or a firmware event, so last < first is
//     possible; when that happens the bucket's contribution is last
//     (energy accumulated since the reset) and Reset is set — a negative
//     delta is never emitted.
//   - ENERGY_COUNTER_FEED_IN: identical delta/reset logic, folded into
//     feed_in_wh instead of consumed_wh.
//
// Device totals are NOT a sum of per-bucket deltas — that drops the
// consumption between one bucket's last reading and the next bucket's
// first. Each cumulative series (one channel) is instead treated as one
// ordered reading sequence over the whole range and totalled with
// reset-aware segmentation (see [counterRangeTotal]); the per-channel
// totals then sum into the device total. The response totals are the sum
// of the device totals. rows need not be pre-sorted — the output devices
// and each device's buckets are always sorted (address, then bucket start)
// for a deterministic response.
func FoldEnergyRows(q EnergyQuery, rows []EnergyRawRow, nameOf DeviceNamer) EnergyResponse {
	type bucketKey struct {
		device string
		ts     int64
	}
	buckets := make(map[bucketKey]*EnergyBucket)
	deviceOf := make(map[string]struct{})
	series := make(map[energySeriesKey][]counterReading)

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
			sk := energySeriesKey{dev, row.ChannelAddress, row.Parameter}
			series[sk] = append(series[sk], counterReading{row.BucketTS.UnixMilli(), row.First, row.Last})
		case energyParameterEnergyCounterFeedIn:
			delta, reset := energyDelta(row.First, row.Last)
			b.FeedInWh += delta
			b.Reset = b.Reset || reset
			sk := energySeriesKey{dev, row.ChannelAddress, row.Parameter}
			series[sk] = append(series[sk], counterReading{row.BucketTS.UnixMilli(), row.First, row.Last})
		}
	}

	totals := deviceCounterTotals(series)

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
		dt := totals[dev]
		d := EnergyDevice{
			Address:         dev,
			Name:            name,
			Buckets:         bs,
			TotalConsumedWh: dt.consumed,
			TotalFeedInWh:   dt.feedIn,
		}
		out.Devices = append(out.Devices, d)
		out.TotalConsumedWh += d.TotalConsumedWh
		out.TotalFeedInWh += d.TotalFeedInWh
	}
	return out
}

// energySeriesKey identifies one cumulative-counter series: a single
// parameter on a single channel of a device. Each series is totalled
// independently (see [deviceCounterTotals]).
type energySeriesKey struct {
	device  string
	channel string
	param   string
}

// deviceEnergyTotals holds a device's reset-aware range totals in Wh.
type deviceEnergyTotals struct {
	consumed float64
	feedIn   float64
}

// deviceCounterTotals computes per-device reset-aware range totals from the
// per-series counter readings, summing each device's channels. Series keys
// are visited in a deterministic (sorted) order so the float accumulation is
// reproducible across runs.
func deviceCounterTotals(series map[energySeriesKey][]counterReading) map[string]deviceEnergyTotals {
	keys := make([]energySeriesKey, 0, len(series))
	for sk := range series {
		keys = append(keys, sk)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.device != b.device {
			return a.device < b.device
		}
		if a.channel != b.channel {
			return a.channel < b.channel
		}
		return a.param < b.param
	})
	totals := make(map[string]deviceEnergyTotals)
	for _, sk := range keys {
		total := counterRangeTotal(series[sk])
		dt := totals[sk.device]
		switch sk.param {
		case energyParameterEnergyCounter:
			dt.consumed += total
		case energyParameterEnergyCounterFeedIn:
			dt.feedIn += total
		}
		totals[sk.device] = dt
	}
	return totals
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

// counterRangeTotal returns the total consumption of one cumulative counter
// over its buckets, counting inter-bucket rise and segmenting on resets.
// Sorted by bucket time, the per-bucket (first, last) readings flatten to
// the ordered sequence first0, last0, first1, last1, …; each adjacent step
// contributes its rise (b - a) or, when the counter went backwards (a
// reset to 0), the post-reset reading b. The very first reading is the
// baseline and is not itself counted. This recovers the consumption a plain
// Σ(last - first) per bucket drops in the gap between adjacent buckets.
func counterRangeTotal(readings []counterReading) float64 {
	if len(readings) == 0 {
		return 0
	}
	sort.Slice(readings, func(i, j int) bool { return readings[i].ts < readings[j].ts })
	seq := make([]float64, 0, len(readings)*2)
	for _, r := range readings {
		seq = append(seq, r.first, r.last)
	}
	var total float64
	for i := 1; i < len(seq); i++ {
		total += counterSegment(seq[i-1], seq[i])
	}
	return total
}

// counterSegment is one step of a cumulative-counter reading sequence: the
// rise b - a, or — when the counter decreased (a reset to 0) — the
// post-reset reading b. Never negative. Mirrors [energyDelta]'s per-bucket
// rule at the inter-reading granularity.
func counterSegment(a, b float64) float64 {
	if b >= a {
		return b - a
	}
	return b
}
