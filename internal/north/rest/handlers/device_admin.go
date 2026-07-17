// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
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
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Unpair failed", err)
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
			problem.Write(w, DecodeJSONStatus(err),
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
				writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Rename failed", err)
				return
			}
		}
		if req.Rooms != nil {
			if err := admin.SetRooms(r.Context(), addr, *req.Rooms); err != nil {
				writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Room assignment failed", err)
				return
			}
		}
		if req.Functions != nil {
			if err := admin.SetFunctions(r.Context(), addr, *req.Functions); err != nil {
				writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Function assignment failed", err)
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
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Firmware update failed", err)
			return
		}
		JSON(w, http.StatusAccepted, map[string]string{"status": "scheduled"})
	}
}

// FirmwareRefresher force-refreshes the per-device firmware data by
// re-pulling device descriptions from every CCU and applying them to
// the live device models. Same contract as the WS `firmware.refresh`
// command.
type FirmwareRefresher interface {
	RefreshFirmwareData(ctx context.Context) error
}

// RefreshFirmwareData serves `POST /devices/firmware/refresh`: a
// synchronous sweep across every configured CCU so the firmware
// overview reflects updates the CCU performed without waiting for the
// next scheduled poll.
func RefreshFirmwareData(refresher FirmwareRefresher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if refresher == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Firmware refresh unavailable", ""))
			return
		}
		if err := refresher.RefreshFirmwareData(r.Context()); err != nil {
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Firmware refresh failed", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
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
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Accept failed", err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}
