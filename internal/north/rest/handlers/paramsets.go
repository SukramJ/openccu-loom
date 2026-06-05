// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// ParamsetService is the narrow facade `GET/PUT /paramsets/{key}`
// depends on. The paramset key is one of VALUES / MASTER / LINK.
//
// LINK paramsets need the peer channel address (the CCU uses it as
// the paramset key on the wire) and therefore get their own method
// pair. The REST surface reflects this with a dedicated route —
// `/devices/{addr}/link-ps/{peer}` — to keep the `{key}` URL parameter
// free of ambiguity.
type ParamsetService interface {
	GetParamset(ctx context.Context, address string, key hmenum.ParamsetKey) (map[string]any, error)
	PutParamset(ctx context.Context, address string, key hmenum.ParamsetKey, values map[string]any) error
	GetLinkParamset(ctx context.Context, channelAddress, peerAddress string) (map[string]any, error)
	PutLinkParamset(ctx context.Context, channelAddress, peerAddress string, values map[string]any) error
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
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeInternal, r, "Paramset read failed", err.Error()))
			return
		}
		JSON(w, http.StatusOK, values)
	}
}

// PutParamset writes a paramset.
func PutParamset(svc ParamsetService) http.HandlerFunc {
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
		values := map[string]any{}
		if err := DecodeJSON(r, &values); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if err := svc.PutParamset(r.Context(), addr, key, values); err != nil {
			if errors.Is(err, hmerr.ErrParameterHidden) {
				problem.Write(w, http.StatusForbidden,
					problem.New(problem.TypeForbidden, r, "Parameter hidden", err.Error()))
				return
			}
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeInternal, r, "Paramset write failed", err.Error()))
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
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeInternal, r, "Link paramset read failed", err.Error()))
			return
		}
		JSON(w, http.StatusOK, values)
	}
}

// PutLinkParamset serves PUT /devices/{addr}/link-ps/{peer}.
func PutLinkParamset(svc ParamsetService) http.HandlerFunc {
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
		values := map[string]any{}
		if err := DecodeJSON(r, &values); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if err := svc.PutLinkParamset(r.Context(), addr, peer, values); err != nil {
			if errors.Is(err, hmerr.ErrParameterHidden) {
				problem.Write(w, http.StatusForbidden,
					problem.New(problem.TypeForbidden, r, "Parameter hidden", err.Error()))
				return
			}
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeInternal, r, "Link paramset write failed", err.Error()))
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
