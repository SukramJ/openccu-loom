// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// DeviceAdmin is an alias for the canonical interface in pkg/interfaces.
type DeviceAdmin = interfaces.DeviceAdmin

// DeleteDevice removes a device (unpair via CCU). The optional query flags
// `reset` and `force` map onto the CCU delete bitmask: reset factory-resets
// the device during removal, force removes an unreachable device even when the
// CCU cannot complete the handshake. A backend without a pairing concept
// (CUxD) surfaces [backends.ErrUnsupported] and becomes 422.
func DeleteDevice(admin DeviceAdmin) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if admin == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Device admin unavailable", ""))
			return
		}
		reset := queryBool(r, "reset")
		force := queryBool(r, "force")
		if err := admin.UnpairDevice(r.Context(), chi.URLParam(r, "addr"), reset, force); err != nil {
			if errors.Is(err, backends.ErrUnsupported) {
				problem.Write(w, http.StatusUnprocessableEntity,
					problem.New(problem.TypeValidation, r, "Unpair not supported by this backend", ""))
				return
			}
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Unpair failed", err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// queryBool parses a boolean query flag. A missing or malformed value is
// treated as false, matching the "flag defaults off" contract of the delete
// options.
func queryBool(r *http.Request, name string) bool {
	v, err := strconv.ParseBool(r.URL.Query().Get(name))
	return err == nil && v
}

// DevicePatchRequest is the body of `PATCH /devices/{addr}`.
type DevicePatchRequest struct {
	Name *string `json:"name,omitempty"`
	// IncludeChannels, when true, also renames every channel to
	// "<name>:<channelNo>" (the CCU WebUI convention). Only consulted
	// together with Name. Omitted defaults to false (device name only).
	IncludeChannels *bool     `json:"include_channels,omitempty"`
	Rooms           *[]string `json:"rooms,omitempty"`
	Functions       *[]string `json:"functions,omitempty"`
}

// ChannelPatchRequest is the body of `PATCH /devices/{addr}/channels/{no}`.
type ChannelPatchRequest struct {
	Name *string `json:"name,omitempty"`
}

// PatchDevice applies partial updates. The MVP supports renaming
// (optionally cascading to channels), room and function assignment;
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
			includeChannels := req.IncludeChannels != nil && *req.IncludeChannels
			if err := admin.RenameDevice(r.Context(), addr, *req.Name, includeChannels); err != nil {
				writeRenameError(w, r, err)
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

// PatchChannel renames a single channel. Room and function assignment
// per channel is out of scope here — only the name is patchable.
func PatchChannel(admin DeviceAdmin) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if admin == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Device admin unavailable", ""))
			return
		}
		var req ChannelPatchRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if req.Name == nil {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "No patchable field supplied", ""))
			return
		}
		no, err := strconv.Atoi(chi.URLParam(r, "no"))
		if err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid channel number", ""))
			return
		}
		if err := admin.RenameChannel(r.Context(), chi.URLParam(r, "addr"), no, *req.Name); err != nil {
			writeRenameError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// writeRenameError maps a rename failure to its HTTP response: a backend
// that cannot rename (no JSON-RPC — Homegear, CUxD) surfaces
// [backends.ErrUnsupported] and becomes 422, every other failure (CCU
// unreachable, ISE-ID not found) becomes 502.
func writeRenameError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, backends.ErrUnsupported) {
		problem.Write(w, http.StatusUnprocessableEntity,
			problem.New(problem.TypeValidation, r, "Rename not supported by this backend", ""))
		return
	}
	writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Rename failed", err)
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
