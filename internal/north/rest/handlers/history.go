// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// HistoryBucket is one aggregated point in a `GET /api/v1/history`
// response: the average / min / max / sample-count over a time bucket.
type HistoryBucket struct {
	TS    time.Time `json:"ts"`
	Avg   float64   `json:"avg"`
	Min   float64   `json:"min"`
	Max   float64   `json:"max"`
	Count int64     `json:"count"`
}

// HistoryQuery is the parsed, validated request for the history handler.
type HistoryQuery struct {
	Central        string
	InterfaceID    string
	ChannelAddress string
	Parameter      string
	From           time.Time
	To             time.Time
	Buckets        int
}

// HistoryService is the read-side handle the SPA chart needs. The cmd
// layer adapts the measurement store to this interface so handlers stay
// decoupled from the store package.
type HistoryService interface {
	Query(ctx context.Context, q HistoryQuery) ([]HistoryBucket, error)
}

const (
	historyDefaultBuckets = 200
	historyMaxBuckets     = 2000
)

// GetHistory serves `GET /api/v1/history` — a server-side-bucketed view
// of one data point's recorded measurement history, sized for a chart.
//
// Query parameters (all of central, interface_id, channel, parameter,
// from, to are required):
//
//	?central=<name>
//	?interface_id=<ccu-iface>
//	?channel=<address:channel>
//	?parameter=<NAME>
//	?from=<RFC3339>&to=<RFC3339>
//	?buckets=<int>   default 200, max 2000
//
// The server aggregates the raw rows into at most `buckets` evenly
// spaced buckets so the browser never pulls the raw series.
func GetHistory(svc HistoryService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Measurement history unavailable", ""))
			return
		}
		q, errMsg := parseHistoryQuery(r)
		if errMsg != "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid query parameter", errMsg))
			return
		}
		buckets, err := svc.Query(r.Context(), q)
		if err != nil {
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "History query failed", err)
			return
		}
		if buckets == nil {
			buckets = []HistoryBucket{}
		}
		JSON(w, http.StatusOK, buckets)
	}
}

// parseHistoryQuery extracts and validates the query parameters. It
// returns a non-empty errMsg when a required parameter is missing or a
// value is malformed.
func parseHistoryQuery(r *http.Request) (q HistoryQuery, errMsg string) { //nolint:gocritic // named returns clarify dual-return semantics
	v := r.URL.Query()
	q = HistoryQuery{
		Central:        v.Get("central"),
		InterfaceID:    v.Get("interface_id"),
		ChannelAddress: v.Get("channel"),
		Parameter:      v.Get("parameter"),
		Buckets:        historyDefaultBuckets,
	}
	switch {
	case q.Central == "":
		return HistoryQuery{}, "central: required"
	case q.InterfaceID == "":
		return HistoryQuery{}, "interface_id: required"
	case q.ChannelAddress == "":
		return HistoryQuery{}, "channel: required"
	case q.Parameter == "":
		return HistoryQuery{}, "parameter: required"
	}
	from, errMsg := parseRequiredTime(v.Get("from"), "from")
	if errMsg != "" {
		return HistoryQuery{}, errMsg
	}
	to, errMsg := parseRequiredTime(v.Get("to"), "to")
	if errMsg != "" {
		return HistoryQuery{}, errMsg
	}
	if !to.After(from) {
		return HistoryQuery{}, "to: must be after from"
	}
	q.From, q.To = from, to
	if bq := v.Get("buckets"); bq != "" {
		n, err := strconv.Atoi(bq)
		if err != nil {
			return HistoryQuery{}, "buckets: not an integer: " + bq
		}
		switch {
		case n <= 0:
			q.Buckets = historyDefaultBuckets
		case n > historyMaxBuckets:
			q.Buckets = historyMaxBuckets
		default:
			q.Buckets = n
		}
	}
	return q, ""
}

// parseRequiredTime parses an RFC3339 timestamp, returning an error
// message for an empty or malformed value.
func parseRequiredTime(raw, field string) (t time.Time, errMsg string) { //nolint:gocritic // named returns clarify dual-return semantics
	if raw == "" {
		return time.Time{}, field + ": required"
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, field + ": invalid RFC3339 timestamp: " + raw
	}
	return parsed, ""
}
