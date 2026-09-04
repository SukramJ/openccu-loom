// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
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
	// Rooms / Functions replace the channel's assignment sets. Omitted
	// fields stay untouched; an explicit empty array clears the set —
	// the same pointer semantics as the device-level patch.
	Rooms     *[]string `json:"rooms,omitempty"`
	Functions *[]string `json:"functions,omitempty"`
}

// PatchDevice applies partial updates. The MVP supports renaming
// (optionally cascading to channels), room and function assignment;
// additional fields are added as the admin surface grows.
func PatchDevice(admin DeviceAdmin, rec audit.Recorder) http.HandlerFunc {
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
		// Each assignment is its own CCU call. A later one failing does
		// not undo an earlier one, so the audit trail records what
		// actually reached the CCU, not only the all-or-nothing case.
		var appliedRooms, appliedFunctions *[]string
		if req.Rooms != nil {
			if err := admin.SetRooms(r.Context(), addr, *req.Rooms); err != nil {
				writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Room assignment failed", err)
				return
			}
			appliedRooms = req.Rooms
		}
		if req.Functions != nil {
			if err := admin.SetFunctions(r.Context(), addr, *req.Functions); err != nil {
				recordAssignment(r, rec, addr, appliedRooms, appliedFunctions, true)
				writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Function assignment failed", err)
				return
			}
			appliedFunctions = req.Functions
		}
		recordAssignment(r, rec, addr, appliedRooms, appliedFunctions, false)
		w.WriteHeader(http.StatusAccepted)
	}
}

// PatchChannel applies partial updates to a single channel: rename,
// room assignment and function assignment, mirroring the device-level
// patch semantics one level down.
func PatchChannel(admin DeviceAdmin, rec audit.Recorder) http.HandlerFunc {
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
		if req.Name == nil && req.Rooms == nil && req.Functions == nil {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "No patchable field supplied", ""))
			return
		}
		// A negative ordinal is rejected here rather than downstream:
		// the channel address is built as addr + ":" + no, so "-1"
		// would reach the CCU as a channel that cannot exist.
		no, err := strconv.Atoi(chi.URLParam(r, "no"))
		if err != nil || no < 0 {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid channel number", ""))
			return
		}
		addr := chi.URLParam(r, "addr")
		if req.Name != nil {
			if err := admin.RenameChannel(r.Context(), addr, no, *req.Name); err != nil {
				writeRenameError(w, r, err)
				return
			}
		}
		var appliedRooms, appliedFunctions *[]string
		if req.Rooms != nil {
			if err := admin.SetChannelRooms(r.Context(), addr, no, *req.Rooms); err != nil {
				writeAssignmentError(w, r, "Room assignment failed", err)
				return
			}
			appliedRooms = req.Rooms
		}
		if req.Functions != nil {
			if err := admin.SetChannelFunctions(r.Context(), addr, no, *req.Functions); err != nil {
				recordAssignment(r, rec, addr+":"+strconv.Itoa(no), appliedRooms, appliedFunctions, true)
				writeAssignmentError(w, r, "Function assignment failed", err)
				return
			}
			appliedFunctions = req.Functions
		}
		recordAssignment(r, rec, addr+":"+strconv.Itoa(no), appliedRooms, appliedFunctions, false)
		w.WriteHeader(http.StatusAccepted)
	}
}

// writeAssignmentError maps a room/function assignment failure: naming
// a channel the device does not have is the caller's mistake (404),
// everything else is an upstream failure (502).
func writeAssignmentError(w http.ResponseWriter, r *http.Request, title string, err error) {
	if errors.Is(err, interfaces.ErrChannelNotFound) {
		problem.Write(w, http.StatusNotFound,
			problem.New(problem.TypeNotFound, r, "Channel not found", err.Error()))
		return
	}
	writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, title, err)
}

// recordAssignment appends the audit entry for the room / function
// assignments that reached the CCU. Rename-only patches record nothing —
// the name change is already observable through the device model itself.
// partial marks a patch whose remaining assignments failed, so the row
// says what landed rather than implying the whole patch did.
func recordAssignment(r *http.Request, rec audit.Recorder, address string, rooms, functions *[]string, partial bool) {
	if rec == nil || (rooms == nil && functions == nil) {
		return
	}
	var parts []string
	if rooms != nil {
		parts = append(parts, fmt.Sprintf("rooms=%v", *rooms))
	}
	if functions != nil {
		parts = append(parts, fmt.Sprintf("functions=%v", *functions))
	}
	if partial {
		parts = append(parts, "(partial: the rest of the patch failed)")
	}
	rec.Record(audit.Entry{
		User:          identityFromCtx(r.Context()),
		Action:        audit.ActionDeviceAssignment,
		DeviceAddress: address,
		Note:          strings.Join(parts, " "),
	})
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

// FirmwareUpdateResponse is the 202 body of the device firmware-update
// endpoint. DutyCycleWarning is present only when the device's radio
// interface reports a transmit duty cycle at or above
// [coordinators.DutyCycleWarningThreshold]; it is advisory — the update is scheduled
// regardless. Absent when the duty cycle is unknown or below the
// threshold.
type FirmwareUpdateResponse struct {
	Status           string `json:"status"`
	DutyCycleWarning *int   `json:"duty_cycle_warning,omitempty"`
}

// UpdateDeviceFirmware kicks off a firmware update for the device.
// The CCU runs the actual transfer asynchronously; this endpoint
// returns 202 once the request was accepted. When the device's radio
// interface reports a high transmit duty cycle the response carries an
// advisory `duty_cycle_warning` field so the caller can surface the
// risk of a stalled OTA transfer; the update is never rejected on that
// basis.
func UpdateDeviceFirmware(admin DeviceAdmin) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if admin == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Device admin unavailable", ""))
			return
		}
		addr := chi.URLParam(r, "addr")
		if err := admin.UpdateFirmware(r.Context(), addr); err != nil {
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Firmware update failed", err)
			return
		}
		resp := FirmwareUpdateResponse{Status: "scheduled"}
		if dc, ok := admin.InterfaceDutyCycle(addr); ok && coordinators.FirmwareUpdateRisky(dc) {
			resp.DutyCycleWarning = &dc
		}
		JSON(w, http.StatusAccepted, resp)
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

// AcceptInboxRequest is the optional body of `POST /devices/{addr}/accept`.
// An empty or omitted body accepts the device with no first-time
// configuration (the historical behaviour). Any supplied field is
// applied best-effort right after the accept. Pointer fields let an
// omitted key ("leave untouched") be told apart from an explicit empty
// value ("clear").
type AcceptInboxRequest struct {
	Name *string `json:"name,omitempty"`
	// IncludeChannels cascades the rename to every channel
	// ("<name>:<channelNo>"). Only consulted together with Name.
	IncludeChannels *bool     `json:"include_channels,omitempty"`
	Rooms           *[]string `json:"rooms,omitempty"`
	Functions       *[]string `json:"functions,omitempty"`
}

// ReleaseDevice finishes onboarding a device: it stops being withheld
// from the ecosystems and is published to them.
//
// Separate from the accept on purpose. Between the two the device is
// materialised and configurable — which is when the operator names it and
// places it in a room — but invisible to Home Assistant, Matter and the
// outbound webhook. An ecosystem that sees a device first and is
// corrected afterwards keeps the identity it saw, so the order is what
// makes the naming stick.
func ReleaseDevice(admin DeviceAdmin) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if admin == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Device admin unavailable", ""))
			return
		}
		if err := admin.ReleaseDevice(r.Context(), chi.URLParam(r, "addr")); err != nil {
			if errors.Is(err, interfaces.ErrInboxDeviceNotFound) {
				// Nothing withholds the address: it was released already,
				// or it never went through the wizard. Either way this is
				// a stale view, not an upstream failure — 404 so the SPA
				// refreshes instead of retrying.
				problem.Write(w, http.StatusNotFound, problem.New(problem.TypeNotFound, r,
					"Device not awaiting release",
					"The device is not being withheld; it may already be released."))
				return
			}
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Release failed", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// AcceptInboxDevice pairs a device that is waiting in the inbox and,
// when the optional body carries first-time configuration, applies the
// name / room / function assignment right after the accept.
func AcceptInboxDevice(admin DeviceAdmin) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if admin == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Device admin unavailable", ""))
			return
		}
		// The body is optional: an empty request stream decodes to io.EOF,
		// which we treat as "no first-time configuration" so the endpoint
		// stays backward compatible.
		var req AcceptInboxRequest
		if err := DecodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		opts := interfaces.AcceptInboxOptions{}
		if req.Name != nil {
			opts.Name = *req.Name
		}
		if req.IncludeChannels != nil {
			opts.IncludeChannels = *req.IncludeChannels
		}
		if req.Rooms != nil {
			opts.Rooms = *req.Rooms
		}
		if req.Functions != nil {
			opts.Functions = *req.Functions
		}
		if err := admin.AcceptInboxDevice(r.Context(), chi.URLParam(r, "addr"), opts); err != nil {
			if errors.Is(err, interfaces.ErrInboxDeviceNotFound) {
				// The device is no longer in any central's inbox (it settled or
				// was removed on the CCU). This is a stale entry, not an upstream
				// failure — surface 404 so the SPA distinguishes it from a 502
				// and refreshes the inbox instead of retrying.
				problem.Write(w, http.StatusNotFound, problem.New(problem.TypeNotFound, r,
					"Device not in inbox",
					"The device is no longer waiting in the inbox; it may have already "+
						"been accepted or removed on the CCU."))
				return
			}
			if errors.Is(err, interfaces.ErrAcceptConfigIncomplete) {
				// The device WAS accepted; only the optional first-time
				// configuration failed. Surface a distinct title so the
				// operator re-applies the configuration instead of the accept.
				writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable,
					"Device accepted but initial configuration failed", err)
				return
			}
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Accept failed", err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}
