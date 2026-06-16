// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// DeviceAdmin is an alias for the canonical interface in pkg/interfaces.
type DeviceAdmin = interfaces.DeviceAdmin

// DeleteDevice removes a device (unpair via CCU).
func DeleteDevice(admin DeviceAdmin) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if admin == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Device admin unavailable", ""))
			return
		}
		if err := admin.UnpairDevice(r.Context(), chi.URLParam(r, "addr")); err != nil {
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeUpstreamUnavailable, r, "Unpair failed", err.Error()))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// DevicePatchRequest is the body of `PATCH /devices/{addr}`.
type DevicePatchRequest struct {
	Name      *string   `json:"name,omitempty"`
	Rooms     *[]string `json:"rooms,omitempty"`
	Functions *[]string `json:"functions,omitempty"`
}

// PatchDevice applies partial updates. The MVP supports renaming;
// additional fields are added as the admin surface grows.
func PatchDevice(admin DeviceAdmin) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if admin == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Device admin unavailable", ""))
			return
		}
		var req DevicePatchRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if req.Name == nil && req.Rooms == nil && req.Functions == nil {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "No patchable field supplied", ""))
			return
		}
		addr := chi.URLParam(r, "addr")
		if req.Name != nil {
			if err := admin.RenameDevice(r.Context(), addr, *req.Name); err != nil {
				problem.Write(w, http.StatusBadGateway,
					problem.New(problem.TypeUpstreamUnavailable, r, "Rename failed", err.Error()))
				return
			}
		}
		if req.Rooms != nil {
			if err := admin.SetRooms(r.Context(), addr, *req.Rooms); err != nil {
				problem.Write(w, http.StatusBadGateway,
					problem.New(problem.TypeUpstreamUnavailable, r, "Room assignment failed", err.Error()))
				return
			}
		}
		if req.Functions != nil {
			if err := admin.SetFunctions(r.Context(), addr, *req.Functions); err != nil {
				problem.Write(w, http.StatusBadGateway,
					problem.New(problem.TypeUpstreamUnavailable, r, "Function assignment failed", err.Error()))
				return
			}
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// UpdateDeviceFirmware kicks off a firmware update for the device.
// The CCU runs the actual transfer asynchronously; this endpoint
// returns 202 once the request was accepted.
func UpdateDeviceFirmware(admin DeviceAdmin) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if admin == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Device admin unavailable", ""))
			return
		}
		if err := admin.UpdateFirmware(r.Context(), chi.URLParam(r, "addr")); err != nil {
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeUpstreamUnavailable, r, "Firmware update failed", err.Error()))
			return
		}
		JSON(w, http.StatusAccepted, map[string]string{"status": "scheduled"})
	}
}

// AcceptInboxDevice pairs a device that is waiting in the inbox.
func AcceptInboxDevice(admin DeviceAdmin) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if admin == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Device admin unavailable", ""))
			return
		}
		if err := admin.AcceptInboxDevice(r.Context(), chi.URLParam(r, "addr")); err != nil {
			problem.Write(w, http.StatusBadGateway,
				problem.New(problem.TypeUpstreamUnavailable, r, "Accept failed", err.Error()))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}
