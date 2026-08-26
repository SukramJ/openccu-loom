// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
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

// UpdateLinkRequest is the JSON body for PATCH /devices/{addr}/links.
// Both channel addresses identify the existing link; name and
// description carry the new metadata. They are written verbatim, so an
// empty string clears the corresponding field on the CCU.
type UpdateLinkRequest struct {
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

// ListAllLinks serves the global links overview at `GET /api/v1/links`.
// It returns 200 with a `{"links": [...]}` object aggregating every
// direct link across all centrals; each link self-identifies via
// `central_name` + `interface_id`. A `?central=<name>` query narrows to
// one central and returns 404 when that central is unknown. 503 signals
// an unwired service.
func ListAllLinks(svc LinksService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Links service unavailable", ""))
			return
		}
		centralName := r.URL.Query().Get("central")
		locale := r.URL.Query().Get("locale")
		if locale == "" {
			locale = "en"
		}
		links, err := svc.ListAllLinks(r.Context(), centralName, locale)
		if err != nil {
			if errors.Is(err, hmerr.ErrUnknownCentral) {
				problem.Write(w, http.StatusNotFound,
					problem.New(problem.TypeNotFound, r, "Unknown central", centralName))
				return
			}
			problem.WriteFromError(w, r, err)
			return
		}
		if links == nil {
			links = []Link{}
		}
		JSON(w, http.StatusOK, map[string]any{"links": links})
	}
}

// TestLinkAtDeviceRequest is the JSON body for
// POST /devices/{addr}/links/test.
type TestLinkAtDeviceRequest struct {
	ReceiverAddress string `json:"receiver_address"`
	SenderAddress   string `json:"sender_address"`
	LongPress       bool   `json:"long_press"`
}

// TestLinkAtDevice POST /api/v1/devices/{addr}/links/test — triggers the
// receiver's LINK-paramset behaviour for the sender (short/long keypress),
// the CCU's "test link" probe. It physically actuates the device, so it is
// operator-gated and fire-and-forget (202). 501 when the interface does
// not support link activation (CUxD / Homegear), 502 on a wire fault.
func TestLinkAtDevice(svc LinksService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Links service unavailable", ""))
			return
		}
		var req TestLinkAtDeviceRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if req.ReceiverAddress == "" || req.SenderAddress == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r,
					"receiver_address and sender_address are required", ""))
			return
		}
		err := svc.ActivateLink(r.Context(), req.ReceiverAddress, req.SenderAddress, req.LongPress)
		if err != nil {
			if errors.Is(err, hmerr.ErrDescriptionNotFound) {
				problem.Write(w, http.StatusNotFound,
					problem.New(problem.TypeNotFound, r, "Device not found", req.ReceiverAddress))
				return
			}
			if errors.Is(err, backends.ErrUnsupported) {
				problem.Write(w, http.StatusNotImplemented,
					problem.New(problem.TypeUnsupported, r, "Link activation not supported on this interface", ""))
				return
			}
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable,
				"Link activation failed", err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
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
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if req.SenderAddress == "" || req.ReceiverAddress == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "sender_address and receiver_address required", ""))
			return
		}
		if err := svc.AddLink(r.Context(), req.SenderAddress, req.ReceiverAddress, req.Name, req.Description); err != nil {
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Add link failed", err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// UpdateLink PATCH /api/v1/devices/{addr}/links
//
// Changes the name / description of an existing direct link. The two
// channel addresses in the body identify the link; the device address
// in the path scopes the owning central + interface.
func UpdateLink(svc LinksService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Links service unavailable", ""))
			return
		}
		var req UpdateLinkRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if req.SenderAddress == "" || req.ReceiverAddress == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "sender_address and receiver_address required", ""))
			return
		}
		if err := svc.SetLinkInfo(r.Context(), req.SenderAddress, req.ReceiverAddress, req.Name, req.Description); err != nil {
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Update link failed", err)
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
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Remove link failed", err)
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
