// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/csv"
	"encoding/json"
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

// AuditQuerier is the optional durable read path. When the wired
// [AuditService] also implements it, ListAudit pushes the device /
// timestamp / pagination filters down to SQL so the full retained
// history (not just the in-memory ring buffer) is reachable, and CSV
// export and offset-based paging work over all of it.
type AuditQuerier interface {
	Query(ctx context.Context, q audit.Query) ([]audit.Entry, error)
}

// auditFilter holds parsed query-parameter values for GET /audit.
type auditFilter struct {
	device      string    // device address prefix (case-insensitive)
	op          string    // action/source substring match (case-insensitive)
	centralName string    // exact CCU match (best-effort; empty = all)
	since       time.Time // inclusive lower bound (zero = no bound)
	until       time.Time // exclusive upper bound (zero = no bound)
	limit       int       // max entries, default 1000
	offset      int       // pagination offset (durable path only)
	format      string    // "" (JSON) or "csv"
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
		device:      q.Get("device"),
		op:          q.Get("op"),
		centralName: q.Get("central"),
		limit:       auditDefaultLimit,
		format:      strings.ToLower(q.Get("format")),
	}
	if oq := q.Get("offset"); oq != "" {
		if n, err := strconv.Atoi(oq); err == nil && n > 0 {
			f.offset = n
		}
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
		if f.centralName != "" && centralName != f.centralName {
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

		var out []AuditEntryDTO
		if querier, ok := svc.(AuditQuerier); ok {
			// Durable path: SQL applies device / since / until / limit /
			// offset; the in-memory pass then layers the op + central
			// filters (not first-class SQL columns) without re-capping.
			entries, err := querier.Query(r.Context(), audit.Query{
				Device: f.device,
				Since:  f.since,
				Until:  f.until,
				Limit:  f.limit,
				Offset: f.offset,
			})
			if err != nil {
				writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Audit query failed", err)
				return
			}
			out = applyAuditPostFilter(entries, f, centralOf)
		} else {
			// Fallback: fetch generously from the buffer and narrow the
			// whole filter set (incl. limit) in memory.
			out = applyAuditFilter(svc.List(auditMaxLimit), f, centralOf)
		}

		if f.format == "csv" {
			writeAuditCSV(w, out)
			return
		}
		JSON(w, http.StatusOK, out)
	}
}

// applyAuditPostFilter layers the op + central filters over rows the
// durable query already narrowed by device / time / pagination. It does
// not re-apply device/since/until or re-cap to limit — SQL did that.
func applyAuditPostFilter(entries []audit.Entry, f auditFilter, centralOf func(address string) string) []AuditEntryDTO {
	opLo := strings.ToLower(f.op)
	out := make([]AuditEntryDTO, 0, len(entries))
	for i := range entries {
		e := &entries[i]
		if opLo != "" && !strings.Contains(strings.ToLower(string(e.Action)), opLo) {
			continue
		}
		centralName := ""
		if centralOf != nil && e.DeviceAddress != "" {
			centralName = centralOf(e.DeviceAddress)
		}
		if f.centralName != "" && centralName != f.centralName {
			continue
		}
		out = append(out, AuditEntryDTO{Entry: *e, Central: centralName})
	}
	return out
}

// writeAuditCSV streams the entries as CSV with a download disposition.
// Changes are JSON-encoded into a single column so the row shape stays
// flat and spreadsheet-friendly.
func writeAuditCSV(w http.ResponseWriter, entries []AuditEntryDTO) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="audit-log.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{
		"timestamp", "user", "action", "central", "device_address",
		"channel_no", "paramset", "peer", "parameter", "note", "changes",
	})
	for i := range entries {
		e := &entries[i]
		changes := ""
		if len(e.Changes) > 0 {
			if raw, err := json.Marshal(e.Changes); err == nil {
				changes = string(raw)
			}
		}
		channel := ""
		if e.ChannelNo != 0 {
			channel = strconv.Itoa(e.ChannelNo)
		}
		_ = cw.Write([]string{
			e.Timestamp.UTC().Format(time.RFC3339), e.User, string(e.Action),
			e.Central, e.DeviceAddress, channel, e.Paramset, e.Peer,
			e.Parameter, e.Note, changes,
		})
	}
	cw.Flush()
}
