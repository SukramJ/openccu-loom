// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// LinksService is an alias for the canonical interface in pkg/interfaces.
type LinksService = interfaces.LinksService

// Link is an alias for the canonical DTO in pkg/hmapi.
type Link = hmapi.Link

// LinkableChannel is an alias for the canonical DTO in pkg/hmapi.
type LinkableChannel = hmapi.LinkableChannel

// AddLinkRequest is the JSON body for POST /devices/{addr}/links.
// `receiver_address` is required; `sender_address` defaults to the
// path's `{addr}` (plus a channel suffix the caller provides) so the
// Request stays symmetric
type AddLinkRequest struct {
	SenderAddress   string `json:"sender_address"`
	ReceiverAddress string `json:"receiver_address"`
	Name            string `json:"name,omitempty"`
	Description     string `json:"description,omitempty"`
}

// ListLinks GET /api/v1/devices/{addr}/links?locale=de
func ListLinks(svc LinksService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Links service unavailable", ""))
			return
		}
		addr := chi.URLParam(r, "addr")
		locale := r.URL.Query().Get("locale")
		if locale == "" {
			locale = "en"
		}
		links, err := svc.ListLinks(r.Context(), addr, locale)
		if err != nil {
			if errors.Is(err, hmerr.ErrDescriptionNotFound) {
				problem.Write(w, http.StatusNotFound,
					problem.New(problem.TypeNotFound, r, "Device not found", addr))
				return
			}
			problem.WriteFromError(w, r, err)
			return
		}
		JSON(w, http.StatusOK, links)
	}
}

// AddLink POST /api/v1/devices/{addr}/links
func AddLink(svc LinksService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Links service unavailable", ""))
			return
		}
		var req AddLinkRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if req.SenderAddress == "" || req.ReceiverAddress == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "sender_address and receiver_address required", ""))
			return
		}
		if err := svc.AddLink(r.Context(), req.SenderAddress, req.ReceiverAddress, req.Name, req.Description); err != nil {
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeUpstreamUnavailable, r, "Add link failed", err.Error()))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// RemoveLink DELETE /api/v1/devices/{addr}/links?sender=…&receiver=…
func RemoveLink(svc LinksService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Links service unavailable", ""))
			return
		}
		sender := r.URL.Query().Get("sender")
		receiver := r.URL.Query().Get("receiver")
		if sender == "" || receiver == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "sender + receiver query params required", ""))
			return
		}
		if err := svc.RemoveLink(r.Context(), sender, receiver); err != nil {
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeUpstreamUnavailable, r, "Remove link failed", err.Error()))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// LinkableChannels GET /api/v1/devices/{addr}/channels/{no}/linkable-channels?role=sender|receiver
func LinkableChannels(svc LinksService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Links service unavailable", ""))
			return
		}
		addr := chi.URLParam(r, "addr")
		no := chi.URLParam(r, "no")
		role := r.URL.Query().Get("role")
		if role != "sender" && role != "receiver" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "role must be sender or receiver", role))
			return
		}
		interfaceID := r.URL.Query().Get("interface")
		if interfaceID == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "interface query param required", ""))
			return
		}
		locale := r.URL.Query().Get("locale")
		if locale == "" {
			locale = "en"
		}
		source := addr + ":" + no
		candidates, err := svc.LinkableChannels(r.Context(), interfaceID, source, role, locale)
		if err != nil {
			problem.WriteFromError(w, r, err)
			return
		}
		JSON(w, http.StatusOK, candidates)
	}
}
