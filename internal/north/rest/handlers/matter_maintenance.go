// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// MatterForceSync re-assembles the exposed endpoint topology from the
// current device model.
//
// Endpoints are assembled from that model and re-assembled when it
// changes. When something is missing anyway — a change that arrived
// while the bridge was down, an exposure edited outside the usual path —
// the only remedy was restarting the daemon, which drops every
// controller session to fix a list.
//
// The action is not destructive: a controller keeps its session and its
// fabric across it.
func MatterForceSync(r MatterTopologyReassembler, publisher MatterEventPublisher, recorder audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if r == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, req,
					"Matter bridge not enabled", "matter.force_sync_unwired"))
			return
		}
		if err := r.Reassemble(req.Context()); err != nil {
			// A silent success here would tell the operator the list is
			// correct now, which is the one thing they came to check.
			writeServerError(w, req, http.StatusInternalServerError,
				problem.TypeInternal, "Failed to re-assemble the Matter topology", err)
			return
		}
		recordMatterAudit(recorder, req, audit.ActionMatterForceSync, "")
		w.WriteHeader(http.StatusNoContent)
	}
}

// MatterFabricPurger lists and removes every fabric. Implemented by the
// daemon's fabric adapter.
type MatterFabricPurger interface {
	ListFabricIndexes(ctx context.Context) ([]uint8, error)
	RevokeFabric(ctx context.Context, fabricIndex uint8) error
}

// factoryResetConfirmation is the word a caller has to write into the
// body. It names the action rather than agreeing with it, so a client
// that replays a generic `{"confirm":"yes"}` cannot unpair a house.
const factoryResetConfirmation = "remove-all-fabrics"

// MatterFactoryReset removes every fabric, returning the bridge to its
// uncommissioned state.
//
// Every paired controller loses the bridge and has to commission it
// again, and there is no undo. The written confirmation is therefore
// part of the contract, not decoration: a POST with no body — a stray
// curl, a REST client replaying its last request, a mis-scripted
// automation — must not be able to do this.
func MatterFactoryReset(p MatterFabricPurger, publisher MatterEventPublisher, recorder audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if p == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, req,
					"Matter bridge not enabled", "matter.factory_reset_unwired"))
			return
		}
		var body struct {
			Confirm string `json:"confirm"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		if body.Confirm != factoryResetConfirmation {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, req,
					"Factory reset not confirmed",
					`send {"confirm":"`+factoryResetConfirmation+`"} — this removes every fabric `+
						`and each paired controller has to commission the bridge again`))
			return
		}
		indexes, err := p.ListFabricIndexes(req.Context())
		if err != nil {
			writeServerError(w, req, http.StatusInternalServerError,
				problem.TypeInternal, "Failed to list fabrics", err)
			return
		}
		removed := make([]uint8, 0, len(indexes))
		for _, idx := range indexes {
			if err := p.RevokeFabric(req.Context(), idx); err != nil {
				// Reported rather than skipped: a partial reset leaves
				// the bridge paired to a controller the operator
				// believes they removed. The fabrics already removed are
				// gone for good, so they are audited before the request
				// fails — the audit log is the only durable record of
				// who unpaired them.
				recordMatterAudit(recorder, req, audit.ActionMatterFactoryReset,
					fmt.Sprintf("partial: removed %d of %d fabrics %v", len(removed), len(indexes), removed))
				writeServerError(w, req, http.StatusInternalServerError,
					problem.TypeInternal,
					fmt.Sprintf("Factory reset incomplete: removed %d of %d fabrics", len(removed), len(indexes)), err)
				return
			}
			removed = append(removed, idx)
			publishMatterEvent(req.Context(), publisher,
				MatterTopicFabricRemoved, map[string]any{"fabric_index": idx})
		}
		recordMatterAudit(recorder, req, audit.ActionMatterFactoryReset, "")
		w.WriteHeader(http.StatusNoContent)
	}
}
