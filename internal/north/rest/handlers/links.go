// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// LinksService is the narrow facade the /links endpoints depend on.
// Mirrors `LinkCoordinator` (minus the link-paramset
// operations, which are served through the existing paramset routes
// with the peer address as the paramset key).
type LinksService interface {
	ListLinks(ctx context.Context, deviceAddress, locale string) ([]Link, error)
	AddLink(ctx context.Context, senderAddress, receiverAddress, name, description string) error
	RemoveLink(ctx context.Context, senderAddress, receiverAddress string) error
	LinkableChannels(
		ctx context.Context,
		interfaceID, sourceChannelAddress, role, locale string,
	) ([]LinkableChannel, error)
}

// Link is the enriched view of a direct link between two channels.
// Field layout mirrors.
type Link struct {
	Sender                   string `json:"sender_address"`
	Receiver                 string `json:"receiver_address"`
	Name                     string `json:"name,omitempty"`
	Description              string `json:"description,omitempty"`
	Flags                    int    `json:"flags,omitempty"`
	SenderDeviceName         string `json:"sender_device_name,omitempty"`
	SenderDeviceModel        string `json:"sender_device_model,omitempty"`
	SenderChannelType        string `json:"sender_channel_type,omitempty"`
	SenderChannelTypeLabel   string `json:"sender_channel_type_label,omitempty"`
	SenderChannelName        string `json:"sender_channel_name,omitempty"`
	ReceiverDeviceName       string `json:"receiver_device_name,omitempty"`
	ReceiverDeviceModel      string `json:"receiver_device_model,omitempty"`
	ReceiverChannelType      string `json:"receiver_channel_type,omitempty"`
	ReceiverChannelTypeLabel string `json:"receiver_channel_type_label,omitempty"`
	ReceiverChannelName      string `json:"receiver_channel_name,omitempty"`
	PeerAddress              string `json:"peer_address"`
	PeerDeviceName           string `json:"peer_device_name,omitempty"`
	PeerDeviceModel          string `json:"peer_device_model,omitempty"`
	Direction                string `json:"direction"`
}

// LinkableChannel is one candidate returned by
// GET /channels/{no}/linkable-channels.
type LinkableChannel struct {
	Address          string `json:"address"`
	ChannelType      string `json:"channel_type,omitempty"`
	ChannelTypeLabel string `json:"channel_type_label,omitempty"`
	ChannelName      string `json:"channel_name,omitempty"`
	DeviceAddress    string `json:"device_address"`
	DeviceName       string `json:"device_name,omitempty"`
	DeviceModel      string `json:"device_model,omitempty"`
}

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
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, r, "Links list failed", err.Error()))
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
			problem.Write(w, http.StatusInternalServerError,
				problem.New(problem.TypeInternal, r, "Linkable-channels failed", err.Error()))
			return
		}
		JSON(w, http.StatusOK, candidates)
	}
}
