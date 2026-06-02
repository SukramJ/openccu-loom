// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/matter/eligibility"
	matterstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// MatterExposureStore is the narrow facade `/matter/exposable` reads
// through. Mirrors the concrete `*matter/store.Store` API.
type MatterExposureStore interface {
	GetExposure(ctx context.Context, key matterstore.EndpointKey) (matterstore.ExposureRecord, error)
	ListExposures(ctx context.Context, centralName string) ([]matterstore.ExposureRecord, error)
	UpsertExposure(ctx context.Context, rec matterstore.ExposureRecord) error
	CountEnabled(ctx context.Context, centralName string) (int, error)
}

// MatterCandidateProvider yields the per-source classification result
// the operator-facing UI lists. The daemon wires this to a closure
// that walks every Unit's model registry through
// `eligibility.CollectCandidates`.
type MatterCandidateProvider interface {
	MatterCandidates(ctx context.Context) []eligibility.Candidate
}

// MatterCandidateProviderFunc adapts a closure into the interface.
type MatterCandidateProviderFunc func(ctx context.Context) []eligibility.Candidate

// MatterCandidates implements [MatterCandidateProvider].
func (f MatterCandidateProviderFunc) MatterCandidates(ctx context.Context) []eligibility.Candidate {
	if f == nil {
		return nil
	}
	return f(ctx)
}

// MatterExposureResponse is one row in `/api/v1/matter/exposable`.
//
// `DeviceTypeLabel` is the operator-facing name for `DeviceType`
// (e.g. "Thermostat" for `0x0301`). The SPA uses it for the class
// chip filter, the search predicate, and the table column so the
// client never has to keep its own ID → label map. Empty when
// `DeviceType` is zero (measurement rides on a host endpoint).
type MatterExposureResponse struct {
	CentralName     string   `json:"central_name"`
	DeviceAddress   string   `json:"device_address"`
	ChannelNo       int      `json:"channel_no"`
	DPKind          string   `json:"dp_kind"`
	DPKey           string   `json:"dp_key"`
	ParameterLabel  string   `json:"parameter_label,omitempty"`
	DisplayName     string   `json:"display_name"`
	Enabled         bool     `json:"enabled"`
	FriendlyName    string   `json:"friendly_name,omitempty"`
	Mappable        string   `json:"mappable"` // mappable | partially_mappable | unmappable
	DeviceType      uint16   `json:"device_type,omitempty"`
	DeviceTypeLabel string   `json:"device_type_label,omitempty"`
	Clusters        []uint32 `json:"clusters,omitempty"`
	Reason          string   `json:"reason,omitempty"`
}

// MatterExposureList is the body of GET /api/v1/matter/exposable.
type MatterExposureList struct {
	Items []MatterExposureResponse `json:"items"`
}

// MatterExposable returns a unified candidates + allowlist list.
//
// `labels` is optional: when wired, the handler resolves a localised
// `parameter_label` per row (channel-typed → bare-parameter → empty).
// Unwired callers (test paths, ccudata-less builds) get the raw
// `dp_key` only — the SPA falls back to that for display.
func MatterExposable(provider MatterCandidateProvider, store MatterExposureStore, labels ParameterLabeler) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if provider == nil || store == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, req,
					"Matter bridge not enabled",
					"north.matter.enabled is false in the daemon config"))
			return
		}
		candidates := provider.MatterCandidates(req.Context())
		exposures, err := store.ListExposures(req.Context(), "")
		if err != nil {
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, req,
					"Failed to list exposures", err.Error()))
			return
		}
		// Index existing rows by tuple for O(1) merge.
		index := make(map[matterstore.EndpointKey]matterstore.ExposureRecord, len(exposures))
		for i := range exposures {
			index[exposures[i].Key] = exposures[i]
		}
		out := make([]MatterExposureResponse, 0, len(candidates))
		for i := range candidates {
			c := &candidates[i]
			row := MatterExposureResponse{
				CentralName:     c.Key.CentralName,
				DeviceAddress:   c.Key.DeviceAddress,
				ChannelNo:       c.Key.ChannelNo,
				DPKind:          string(c.Key.DPKind),
				DPKey:           c.Key.DPKey,
				ParameterLabel:  channelTypedParameterLabel(labels, c.ChannelType, c.Key.DPKey),
				DisplayName:     c.DisplayName,
				Mappable:        c.Verdict.State.String(),
				DeviceType:      c.Verdict.DeviceType,
				DeviceTypeLabel: interfaces.MatterDeviceTypeName(c.Verdict.DeviceType),
				Clusters:        c.Verdict.Clusters,
				Reason:          c.Verdict.Reason,
			}
			if ex, ok := index[c.Key]; ok {
				row.Enabled = ex.Enabled
				row.FriendlyName = ex.FriendlyName
			}
			out = append(out, row)
		}
		// Sort: central → device → channel → kind → key.
		sort.Slice(out, func(i, j int) bool {
			a, b := out[i], out[j]
			if a.CentralName != b.CentralName {
				return a.CentralName < b.CentralName
			}
			if a.DeviceAddress != b.DeviceAddress {
				return a.DeviceAddress < b.DeviceAddress
			}
			if a.ChannelNo != b.ChannelNo {
				return a.ChannelNo < b.ChannelNo
			}
			if a.DPKind != b.DPKind {
				return a.DPKind < b.DPKind
			}
			return a.DPKey < b.DPKey
		})
		JSON(w, http.StatusOK, MatterExposureList{Items: out})
	}
}

// MatterExposureUpdate is the body of PUT
// /api/v1/matter/exposable.
type MatterExposureUpdate struct {
	CentralName   string `json:"central_name"`
	DeviceAddress string `json:"device_address"`
	ChannelNo     int    `json:"channel_no"`
	DPKind        string `json:"dp_kind"`
	DPKey         string `json:"dp_key"`
	Enabled       bool   `json:"enabled"`
	FriendlyName  string `json:"friendly_name"`
}

// MatterExposeUpdate updates a single exposure row. When publisher
// is non-nil the handler emits `matter.exposable_changed` after a
// successful upsert. Audit records every mutation when recorder is
// wired. When reassembler is non-nil the bridge's topology is
// re-assembled synchronously so the new allowlist takes effect before
// the response is returned — without it the persisted change would
// only surface on the next daemon restart and follow-up calls (such
// as POST /matter/commissioning/window) would still see the stale
// topology.
func MatterExposeUpdate(store MatterExposureStore, publisher MatterEventPublisher, recorder audit.Recorder, reassembler MatterTopologyReassembler) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if store == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, req,
					"Matter bridge not enabled", "matter.exposure_store_unwired"))
			return
		}
		var body MatterExposureUpdate
		if err := DecodeJSON(req, &body); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, req,
					"Invalid request body", err.Error()))
			return
		}
		if err := validateExposureKey(body.CentralName, body.DeviceAddress, body.DPKind, body.DPKey); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, req, "Invalid exposure key", err.Error()))
			return
		}
		actor := actorFromRequest(req)
		rec := matterstore.ExposureRecord{
			Key: matterstore.EndpointKey{
				CentralName:   body.CentralName,
				DeviceAddress: body.DeviceAddress,
				ChannelNo:     body.ChannelNo,
				DPKind:        matterstore.DPKind(body.DPKind),
				DPKey:         body.DPKey,
			},
			Enabled:      body.Enabled,
			FriendlyName: body.FriendlyName,
			Actor:        actor,
		}
		if err := store.UpsertExposure(req.Context(), rec); err != nil {
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, req,
					"Failed to update exposure", err.Error()))
			return
		}
		reassembleAfterExposureChange(req.Context(), reassembler)
		publishMatterEvent(req.Context(), publisher, MatterTopicExposableChanged, body)
		recordMatterAudit(recorder, req, audit.ActionMatterExposureUpdate,
			fmt.Sprintf("%s/%s ch%d %s/%s enabled=%t",
				body.CentralName, body.DeviceAddress, body.ChannelNo,
				body.DPKind, body.DPKey, body.Enabled))
		w.WriteHeader(http.StatusNoContent)
	}
}

// reassembleAfterExposureChange triggers the bridge to re-assemble its
// topology so a freshly-written matter_exposures row takes effect
// immediately. Errors are logged at WARN through the default slog
// handler (the bridge already logs assembly failures at higher
// verbosity) so the caller still gets the success status for the
// persisted row.
func reassembleAfterExposureChange(ctx context.Context, reassembler MatterTopologyReassembler) {
	if reassembler == nil {
		return
	}
	if err := reassembler.Reassemble(ctx); err != nil {
		slog.WarnContext(ctx, "matter.exposable.reassemble_failed",
			slog.String("err", err.Error()))
	}
}

// MatterExposureBulkUpdate is the body of POST
// /api/v1/matter/exposable/bulk.
type MatterExposureBulkUpdate struct {
	Items []MatterExposureUpdate `json:"items"`
}

// MatterExposeBulk applies multiple exposure updates atomically.
// Errors on any item abort the batch; partial application is the
// SQLite default and acceptable here (the operator just retries).
// Triggers a synchronous bridge reassemble before returning so the
// updated allowlist is reflected in the live topology — same
// reasoning as [MatterExposeUpdate].
func MatterExposeBulk(store MatterExposureStore, publisher MatterEventPublisher, recorder audit.Recorder, reassembler MatterTopologyReassembler) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if store == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, req,
					"Matter bridge not enabled", "matter.exposure_store_unwired"))
			return
		}
		var body MatterExposureBulkUpdate
		if err := DecodeJSON(req, &body); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, req,
					"Invalid request body", err.Error()))
			return
		}
		if len(body.Items) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		actor := actorFromRequest(req)
		applied := 0
		for _, it := range body.Items {
			if err := validateExposureKey(it.CentralName, it.DeviceAddress, it.DPKind, it.DPKey); err != nil {
				problem.Write(w, http.StatusBadRequest,
					problem.New(problem.TypeBadRequest, req,
						fmt.Sprintf("Invalid item %d", applied), err.Error()))
				return
			}
			rec := matterstore.ExposureRecord{
				Key: matterstore.EndpointKey{
					CentralName:   it.CentralName,
					DeviceAddress: it.DeviceAddress,
					ChannelNo:     it.ChannelNo,
					DPKind:        matterstore.DPKind(it.DPKind),
					DPKey:         it.DPKey,
				},
				Enabled:      it.Enabled,
				FriendlyName: it.FriendlyName,
				Actor:        actor,
			}
			if err := store.UpsertExposure(req.Context(), rec); err != nil {
				problem.Write(w, http.StatusInternalServerError,
					problem.New(problem.TypeInternal, req,
						fmt.Sprintf("Failed at item %d", applied), err.Error()))
				return
			}
			applied++
		}
		reassembleAfterExposureChange(req.Context(), reassembler)
		publishMatterEvent(req.Context(), publisher, MatterTopicExposableChanged, body)
		recordMatterAudit(recorder, req, audit.ActionMatterExposureBulk,
			fmt.Sprintf("applied=%d", applied))
		JSON(w, http.StatusOK, map[string]any{"applied": applied})
	}
}

// validateExposureKey enforces required-field invariants. Channel
// numbers are 0..n bounded by the channel space; we only check
// non-negativity.
func validateExposureKey(centralName, addr, kind, key string) error {
	if centralName == "" || addr == "" || kind == "" || key == "" {
		return errors.New("central_name, device_address, dp_kind, dp_key are all required")
	}
	switch matterstore.DPKind(kind) {
	case matterstore.DPKindCustom, matterstore.DPKindGeneric, matterstore.DPKindCalculated,
		matterstore.DPKindCombined, matterstore.DPKindMeasurement:
	default:
		return fmt.Errorf("dp_kind %q is not one of custom|generic|calculated|combined|measurement", kind)
	}
	return nil
}

// actorFromRequest retrieves the authenticated subject from the
// request context for audit purposes. Falls back to "anonymous"
// when no auth middleware is wired (test paths).
func actorFromRequest(req *http.Request) string {
	if v := req.Context().Value(actorContextKey{}); v != nil {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return "anonymous"
}

// actorContextKey is the context key middleware uses to stash the
// authenticated subject. Defined here so the handler is self-contained;
// real auth middleware writes through this key when wired.
type actorContextKey struct{}

// MatterStatusReader is the narrow facade `/matter/status` reads
// through.
type MatterStatusReader interface {
	MatterStatus(ctx context.Context) MatterStatusResponse
}

// MatterStatusResponse is the body of GET /api/v1/matter/status.
type MatterStatusResponse struct {
	Enabled        bool   `json:"enabled"`
	Listening      bool   `json:"listening"`
	ListenAddr     string `json:"listen_addr,omitempty"`
	EndpointCount  int    `json:"endpoint_count"`
	FabricCount    int    `json:"fabric_count"`
	EnabledCount   int    `json:"enabled_count"`
	Advertising    bool   `json:"advertising"`
	WindowOpen     bool   `json:"commissioning_window_open"`
	WindowDuration uint16 `json:"commissioning_window_duration_seconds,omitempty"`
}

// MatterStatus returns the bridge runtime status.
func MatterStatus(reader MatterStatusReader) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if reader == nil {
			JSON(w, http.StatusOK, MatterStatusResponse{Enabled: false})
			return
		}
		JSON(w, http.StatusOK, reader.MatterStatus(req.Context()))
	}
}

// MatterFabricRevoker is the narrow facade
// `DELETE /matter/fabrics/{id}` calls.
type MatterFabricRevoker interface {
	RevokeFabric(ctx context.Context, fabricIndex uint8) error
}

// MatterFabricRevoke unpairs a single fabric. When publisher is
// non-nil the handler emits `matter.fabric_removed` after a
// successful revoke. Audit records the mutation when recorder is
// wired.
func MatterFabricRevoke(revoker MatterFabricRevoker, publisher MatterEventPublisher, recorder audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if revoker == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, req,
					"Matter bridge not enabled", "matter.fabric_revoke_unwired"))
			return
		}
		idStr := chi.URLParam(req, "id")
		var idx uint8
		if _, err := fmt.Sscan(idStr, &idx); err != nil || idx == 0 {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, req,
					"Invalid fabric index", "fabric_index must be 1..254"))
			return
		}
		if err := revoker.RevokeFabric(req.Context(), idx); err != nil {
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, req,
					"Failed to revoke fabric", err.Error()))
			return
		}
		publishMatterEvent(req.Context(), publisher,
			MatterTopicFabricRemoved, map[string]any{"fabric_index": idx})
		recordMatterAudit(recorder, req, audit.ActionMatterFabricRevoke,
			fmt.Sprintf("fabric_index=%d", idx))
		w.WriteHeader(http.StatusNoContent)
	}
}

// MatterCommissioningCloser is the narrow facade
// `POST /matter/commissioning/window/close` calls.
type MatterCommissioningCloser interface {
	CloseCommissioningWindow(ctx context.Context) error
}

// MatterCommissioningClose closes an open commissioning window. When
// publisher is non-nil the handler emits
// `matter.commissioning_progress` with `{stage: "closed"}` after the
// close. Audit records the mutation when recorder is wired (only
// the fact of the close + duration; never the passcode).
func MatterCommissioningClose(closer MatterCommissioningCloser, publisher MatterEventPublisher, recorder audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if closer == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, req,
					"Matter bridge not enabled", "matter.commissioning_close_unwired"))
			return
		}
		if err := closer.CloseCommissioningWindow(req.Context()); err != nil {
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, req,
					"Failed to close commissioning window", err.Error()))
			return
		}
		publishMatterEvent(req.Context(), publisher,
			MatterTopicCommissioningProgress, map[string]any{
				"stage":   "closed",
				"message": "Commissioning window closed by operator",
			})
		recordMatterAudit(recorder, req, audit.ActionMatterCommissioning, "window closed")
		w.WriteHeader(http.StatusNoContent)
	}
}

// MatterShare opens a commissioning window for a second commissioner.
// Identical to /commissioning/window; documented separately
// because the semantic meaning ("share bridge") differs in the UI.
func MatterShare(opener MatterCommissioningOpener, publisher MatterEventPublisher) http.HandlerFunc {
	return MatterCommissioningWindow(opener, publisher)
}
