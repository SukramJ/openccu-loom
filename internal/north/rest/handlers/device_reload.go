// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// ReloaderService is the optional facade behind the device- and
// channel-config reload endpoints. It re-pulls the device/channel
// description from the owning CCU and recreates missing channels and
// data points so CCU-side config changes (patched MIN/MAX, new
// channels) propagate without a full restart. *adapter.DeviceReloaderAdapter
// (the same instance backing the WS reload commands) satisfies it.
type ReloaderService interface {
	// ReloadDeviceConfig re-fetches a whole device description by its
	// device address ("DDDDDDDDDD").
	ReloadDeviceConfig(ctx context.Context, address string) error
	// ReloadChannelConfig re-pulls a single channel's paramset
	// descriptions; channelAddress is the "DDDDDDDDDD:n" form.
	ReloadChannelConfig(ctx context.Context, channelAddress string) error
}

// ReloadDevice serves POST /devices/{addr}/reload. It re-pulls the
// device description for the addressed device from its CCU.
func ReloadDevice(svc ReloaderService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Reload unavailable", ""))
			return
		}
		addr := chi.URLParam(r, "addr")
		if addr == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing device address", ""))
			return
		}
		if err := svc.ReloadDeviceConfig(r.Context(), addr); err != nil {
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeUpstreamUnavailable, r, "Device reload failed", err.Error()))
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

// ReloadChannel serves POST /devices/{addr}/channels/{no}/reload.
// It composes the "DDDDDDDDDD:n" channel address from the path params
// and re-pulls that channel's paramset descriptions.
func ReloadChannel(svc ReloaderService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Reload unavailable", ""))
			return
		}
		addr := chi.URLParam(r, "addr")
		channel := chi.URLParam(r, "no")
		if addr == "" || channel == "" {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeValidation, r, "Missing channel address", ""))
			return
		}
		channelAddress := addr + ":" + channel
		if err := svc.ReloadChannelConfig(r.Context(), channelAddress); err != nil {
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeUpstreamUnavailable, r, "Channel reload failed", err.Error()))
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}
