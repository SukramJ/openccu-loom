// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// AuditService is the read-side handle the SPA needs.
type AuditService interface {
	List(limit int) []audit.Entry
}

// auditFilter holds parsed query-parameter values for GET /audit.
type auditFilter struct {
	device  string    // device address prefix (case-insensitive)
	op      string    // action/source substring match (case-insensitive)
	central string    // exact CCU match (best-effort; empty = all)
	since   time.Time // inclusive lower bound (zero = no bound)
	until   time.Time // exclusive upper bound (zero = no bound)
	limit   int       // max entries, default 1000
}

// AuditEntryDTO is one entry in `GET /api/v1/audit`. It embeds the change-log
// entry and adds the CCU it belongs to, derived best-effort from the device
// address (empty for daemon-wide entries such as CCU management).
type AuditEntryDTO struct {
	audit.Entry
	Central string `json:"central,omitempty"`
}

const (
	auditDefaultLimit = 1000
	auditMaxLimit     = 10000
)

// parseAuditFilter extracts and validates the query parameters from r.
// It returns a non-nil errMsg when a parameter is malformed (e.g.
// invalid RFC3339 timestamp).
func parseAuditFilter(r *http.Request) (f auditFilter, errMsg string) { //nolint:gocritic // named returns clarify dual-return semantics
	q := r.URL.Query()
	f = auditFilter{
		device:  q.Get("device"),
		op:      q.Get("op"),
		central: q.Get("central"),
		limit:   auditDefaultLimit,
	}
	if lq := q.Get("limit"); lq != "" {
		if n, err := strconv.Atoi(lq); err == nil {
			switch {
			case n <= 0:
				f.limit = auditDefaultLimit
			case n > auditMaxLimit:
				f.limit = auditMaxLimit
			default:
				f.limit = n
			}
		}
	}
	if sq := q.Get("since"); sq != "" {
		t, err := time.Parse(time.RFC3339, sq)
		if err != nil {
			return auditFilter{}, "since: invalid RFC3339 timestamp: " + sq
		}
		f.since = t
	}
	if uq := q.Get("until"); uq != "" {
		t, err := time.Parse(time.RFC3339, uq)
		if err != nil {
			return auditFilter{}, "until: invalid RFC3339 timestamp: " + uq
		}
		f.until = t
	}
	return f, ""
}

// applyAuditFilter runs the in-memory filter pass over entries, derives each
// entry's CCU via centralOf (best-effort, from the device address), applies the
// central filter, and returns the filtered, limit-capped DTO slice. centralOf
// may be nil (central derivation skipped → all entries are daemon-wide).
func applyAuditFilter(entries []audit.Entry, f auditFilter, centralOf func(address string) string) []AuditEntryDTO {
	deviceLo := strings.ToLower(f.device)
	opLo := strings.ToLower(f.op)

	out := make([]AuditEntryDTO, 0, len(entries))
	for i := range entries {
		e := &entries[i]
		if deviceLo != "" && !strings.HasPrefix(strings.ToLower(e.DeviceAddress), deviceLo) {
			continue
		}
		if opLo != "" && !strings.Contains(strings.ToLower(string(e.Action)), opLo) {
			continue
		}
		if !f.since.IsZero() && e.Timestamp.Before(f.since) {
			continue
		}
		if !f.until.IsZero() && !e.Timestamp.Before(f.until) {
			continue
		}
		centralName := ""
		if centralOf != nil && e.DeviceAddress != "" {
			centralName = centralOf(e.DeviceAddress)
		}
		if f.central != "" && centralName != f.central {
			continue
		}
		out = append(out, AuditEntryDTO{Entry: *e, Central: centralName})
		if len(out) == f.limit {
			break
		}
	}
	return out
}

// ListAudit returns the most recent change-log entries.
//
// Supported query parameters:
//
//	?device=<prefix>          filter by device address prefix (case-insensitive)
//	?op=<substring>           filter by action/source tag substring (case-insensitive)
//	?since=<RFC3339>          only entries at-or-after the timestamp
//	?until=<RFC3339>          only entries strictly before the timestamp
//	?central=<name>           only entries belonging to the named CCU
//	?limit=<int>              max results, default 1000, max 10000
//
// idx is optional: when non-nil, each entry's CCU is derived best-effort from
// its device address (idx.CentralOf); daemon-wide entries stay central-less.
func ListAudit(svc AuditService, idx DeviceIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Audit log unavailable", ""))
			return
		}
		f, errMsg := parseAuditFilter(r)
		if errMsg != "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid query parameter", errMsg))
			return
		}
		var centralOf func(string) string
		if idx != nil {
			centralOf = idx.CentralOf
		}
		// Fetch generously from the buffer; the filter pass narrows it
		// down to the requested limit.
		entries := svc.List(auditMaxLimit)
		JSON(w, http.StatusOK, applyAuditFilter(entries, f, centralOf))
	}
}
