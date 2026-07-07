// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"
	"strconv"

	"github.com/SukramJ/openccu-loom/internal/north/filter"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ValuesBatchQuery is one element of `POST /api/v1/devices/values:batch`.
type ValuesBatchQuery struct {
	Address   string `json:"address"`
	Channel   int    `json:"channel"`
	Parameter string `json:"parameter"`
}

// ValuesBatchRequest is the body of `POST /api/v1/devices/values:batch`.
// The endpoint exists so external clients can avoid one round-trip
// per data point during initial sync — a fleet of ~80k data points
// would otherwise dominate the cold-start latency budget.
type ValuesBatchRequest struct {
	Queries []ValuesBatchQuery `json:"queries"`
}

// ValuesBatchResult mirrors one ValuesBatchQuery with either a
// successful summary or a per-entry error. Successful entries omit
// the error field; failed entries omit the summary. The split keeps
// the batch endpoint resilient — one bad address does not abort the
// rest of the read.
type ValuesBatchResult struct {
	Address   string            `json:"address"`
	Channel   int               `json:"channel"`
	Parameter string            `json:"parameter"`
	Summary   *DataPointSummary `json:"summary,omitempty"`
	Error     string            `json:"error,omitempty"`
}

// ValuesBatchResponse is the body of the batch read.
type ValuesBatchResponse struct {
	Results []ValuesBatchResult `json:"results"`
}

// ValuesBatchMaxQueries caps the queries per request so a misbehaving
// client cannot DoS the daemon with an unbounded payload. The cap is
// generous — most legitimate initial-sync use cases want a few
// hundred to a few thousand reads.
const ValuesBatchMaxQueries = 1000

// ValuesBatch reads multiple data-point values in one round trip.
// Errors at the per-query level surface inside the response; only
// envelope-level problems (malformed JSON, empty / oversized
// queries array) yield problem+json.
func ValuesBatch(idx DeviceIndex, labels ParameterLabeler, vis filter.VisibilitySet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Device index unavailable", ""))
			return
		}
		var req ValuesBatchRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON body", err.Error()))
			return
		}
		if len(req.Queries) == 0 {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "queries must not be empty", ""))
			return
		}
		if len(req.Queries) > ValuesBatchMaxQueries {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "queries cap exceeded",
					"max queries per request is bounded — split your batch"))
			return
		}
		results := make([]ValuesBatchResult, 0, len(req.Queries))
		for _, q := range req.Queries {
			res := ValuesBatchResult{Address: q.Address, Channel: q.Channel, Parameter: q.Parameter}
			d, ok := idx.Device(q.Address)
			if !ok {
				res.Error = "device not found"
				results = append(results, res)
				continue
			}
			chAddr := q.Address + ":" + strconv.Itoa(q.Channel)
			ch := d.Channel(chAddr)
			if ch == nil {
				res.Error = "channel not found"
				results = append(results, res)
				continue
			}
			param := hmenum.Parameter(q.Parameter)
			dp := ch.Parameter(param)
			if dp == nil {
				res.Error = "parameter not found"
				results = append(results, res)
				continue
			}
			if vis != nil && !vis.VisibleForChannel(d.Model, ch.Type, ch.Number, ch.ParamsetIn, param) {
				res.Error = "parameter hidden"
				results = append(results, res)
				continue
			}
			summary := toDataPointSummary(dp, labels, ch, serialSuffixForChannel(idx, ch))
			res.Summary = &summary
			results = append(results, res)
		}
		JSON(w, http.StatusOK, ValuesBatchResponse{Results: results})
	}
}
