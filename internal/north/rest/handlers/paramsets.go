// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// ParamsetService is an alias for the canonical interface in pkg/interfaces.
type ParamsetService = interfaces.ParamsetService

// EditTokenHeader carries the edit-lock token a client must present on
// a MASTER or LINK paramset write under strict edit-lock enforcement.
// The token is issued by `POST /sessions/edit` and refreshed by the
// heartbeat endpoint. VALUES writes (real-time device control) are not
// gated and ignore this header.
const EditTokenHeader = "X-Edit-Token" //nolint:gosec // G101 false positive: HTTP header name, not a credential value

// enforceEditLock is the strict gate for configuration writes: a
// MASTER/LINK paramset write must present an X-Edit-Token that
// currently holds the lock for lockKey, else the request is rejected
// 423 Locked and the caller must not touch the CCU. A nil registry
// disables enforcement — a test-only escape hatch; the production
// mount always wires the shared [EditSessions] instance (see
// cmd/openccu-loom/daemon_rest_mount.go). Returns true when the write
// may proceed; returns false after writing the 423 problem response.
func enforceEditLock(w http.ResponseWriter, r *http.Request, locks *EditSessions, lockKey string) bool {
	if locks == nil {
		return true
	}
	token := r.Header.Get(EditTokenHeader)
	if locks.Verify(lockKey, token) {
		return true
	}
	problem.Write(w, http.StatusLocked,
		problem.New(problem.TypeConflict, r, "Edit lock required",
			"hold an edit session ("+lockKey+") and present its token via "+EditTokenHeader))
	return false
}

// GetParamset proxies the read request to svc.
func GetParamset(svc ParamsetService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Paramsets unavailable", "no backend wired"))
			return
		}
		addr := chi.URLParam(r, "addr")
		key, ok := parseParamsetKey(chi.URLParam(r, "key"))
		if !ok {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid paramset key", chi.URLParam(r, "key")))
			return
		}
		values, err := svc.GetParamset(r.Context(), addr, key)
		if err != nil {
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Paramset read failed", err)
			return
		}
		JSON(w, http.StatusOK, values)
	}
}

// PutParamset writes a paramset. MASTER and LINK writes are
// configuration changes that require holding the per-resource edit
// lock: the caller must present a valid [EditTokenHeader] token via
// `locks`, else the request is rejected 423 Locked before any CCU
// call. VALUES writes (device control) are not gated. `locks` may be
// nil only in tests; the production mount always wires it.
func PutParamset(svc ParamsetService, locks *EditSessions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Paramsets unavailable", "no backend wired"))
			return
		}
		addr := chi.URLParam(r, "addr")
		key, ok := parseParamsetKey(chi.URLParam(r, "key"))
		if !ok {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid paramset key", chi.URLParam(r, "key")))
			return
		}
		// Strict edit-lock enforcement for configuration paramsets. The
		// lock key mirrors the SPA's channel:{addr}:{key} format
		// (ChannelPanel.svelte). A LINK write through this route carries
		// no peer suffix — the SPA uses the dedicated link-ps route for
		// per-peer LINK writes, so this path locks the whole LINK set.
		if key == hmenum.ParamsetKeyMaster || key == hmenum.ParamsetKeyLink {
			if !enforceEditLock(w, r, locks, "channel:"+addr+":"+string(key)) {
				return
			}
		}
		values := map[string]any{}
		if err := DecodeJSON(r, &values); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if err := svc.PutParamset(r.Context(), addr, key, values); err != nil {
			if errors.Is(err, hmerr.ErrParameterHidden) {
				problem.Write(w, http.StatusForbidden,
					problem.New(problem.TypeForbidden, r, "Parameter hidden", err.Error()))
				return
			}
			if errors.Is(err, device.ErrChannelOperationLocked) {
				writeChannelLocked(w, r)
				return
			}
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Paramset write failed", err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// GetLinkParamset serves GET /devices/{addr}/link-ps/{peer}.
func GetLinkParamset(svc ParamsetService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Paramsets unavailable", "no backend wired"))
			return
		}
		addr := chi.URLParam(r, "addr")
		peer := chi.URLParam(r, "peer")
		if peer == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "peer required", ""))
			return
		}
		values, err := svc.GetLinkParamset(r.Context(), addr, peer)
		if err != nil {
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Link paramset read failed", err)
			return
		}
		JSON(w, http.StatusOK, values)
	}
}

// PutLinkParamset serves PUT /devices/{addr}/link-ps/{peer}. A LINK
// paramset is per-peer configuration, so the write requires holding
// the edit lock for that specific peer; the caller presents its
// [EditTokenHeader] token via `locks`, else the request is rejected
// 423 Locked. `locks` may be nil only in tests.
func PutLinkParamset(svc ParamsetService, locks *EditSessions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Paramsets unavailable", "no backend wired"))
			return
		}
		addr := chi.URLParam(r, "addr")
		peer := chi.URLParam(r, "peer")
		if peer == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "peer required", ""))
			return
		}
		// Strict edit-lock enforcement. The lock key mirrors the SPA's
		// channel:{addr}:LINK:{peer} format (ChannelPanel.svelte).
		if !enforceEditLock(w, r, locks, "channel:"+addr+":"+string(hmenum.ParamsetKeyLink)+":"+peer) {
			return
		}
		values := map[string]any{}
		if err := DecodeJSON(r, &values); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if err := svc.PutLinkParamset(r.Context(), addr, peer, values); err != nil {
			if errors.Is(err, hmerr.ErrParameterHidden) {
				problem.Write(w, http.StatusForbidden,
					problem.New(problem.TypeForbidden, r, "Parameter hidden", err.Error()))
				return
			}
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Link paramset write failed", err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

func parseParamsetKey(s string) (hmenum.ParamsetKey, bool) {
	switch s {
	case string(hmenum.ParamsetKeyValues):
		return hmenum.ParamsetKeyValues, true
	case string(hmenum.ParamsetKeyMaster):
		return hmenum.ParamsetKeyMaster, true
	case string(hmenum.ParamsetKeyLink):
		return hmenum.ParamsetKeyLink, true
	}
	return "", false
}
