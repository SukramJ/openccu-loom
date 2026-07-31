// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// system_hub.go — hub-singleton endpoints: CCU system-update info,
// hub metrics, and per-interface install-mode state. They expose the
// hub model objects external clients need to mirror the reference
// stack's hub singletons (system-update entity, system-health /
// connection-latency / last-event-age sensors, per-interface
// install-mode sensor + button).

package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// SystemUpdateEntry is one entry in `GET /system/update` (one per central).
type SystemUpdateEntry struct {
	// Central is the CCU this update info belongs to.
	Central string `json:"central,omitempty"`
	// CurrentFirmware is the installed CCU firmware version.
	CurrentFirmware string `json:"current_firmware,omitempty"`
	// AvailableFirmware is the installable CCU firmware version.
	AvailableFirmware string `json:"available_firmware,omitempty"`
	// UpdateAvailable is true when AvailableFirmware is newer.
	UpdateAvailable bool `json:"update_available"`
	// InProgress is true while a system update is installing.
	InProgress bool `json:"in_progress"`
	// Observed is false until the first update-info fetch succeeded.
	Observed bool `json:"observed"`
}

// GetSystemUpdate returns the CCU system-update info per central.
func GetSystemUpdate(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Hub unavailable", "no hub wired"))
			return
		}
		out := make([]SystemUpdateEntry, 0, 2)
		for _, nh := range idx.Hubs() {
			if nh.Hub == nil || nh.Hub.Update == nil {
				continue
			}
			info, ok := nh.Hub.Update.UpdateInfo()
			out = append(out, SystemUpdateEntry{
				Central:           nh.Central,
				CurrentFirmware:   info.CurrentFirmware,
				AvailableFirmware: info.AvailableFirmware,
				UpdateAvailable:   info.UpdateAvailable,
				InProgress:        nh.Hub.Update.InProgress(),
				Observed:          ok,
			})
		}
		JSON(w, http.StatusOK, out)
	}
}

// PreUpdateBackupPort takes a durable backup of one central and returns
// only once it is stored. *adapter.BackupAdapter satisfies it via
// CreateBackupForCentral - the synchronous sibling of the fire-and-forget
// trigger, which is what makes it usable as a precondition.
type PreUpdateBackupPort interface {
	CreateBackupForCentral(ctx context.Context, centralName string) (string, error)
}

// systemUpdateInstallRequest is the optional body of the install call.
type systemUpdateInstallRequest struct {
	// BackupFirst takes a full CCU backup and only starts the update once
	// it is durably stored.
	BackupFirst bool `json:"backup_first,omitempty"`
}

// PostSystemUpdateInstall triggers the CCU system update on one central
// (selected via the `central` query parameter; optional for single-CCU).
//
// With `{"backup_first": true}` a backup is taken first and the update
// starts only if it succeeded. A failed backup aborts: the entire reason
// to ask for one before a firmware update is to have something to go back
// to, so proceeding without it would defeat the request. The call then
// blocks for as long as the backup takes (minutes on a large
// configuration) - the response is what tells the operator whether the
// safety net exists, so it cannot be detached.
func PostSystemUpdateInstall(idx HubIndex, backup PreUpdateBackupPort, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		centralName := r.URL.Query().Get("central")
		h := resolveHubForMutation(idx, centralName)
		if h == nil || h.Update == nil {
			problem.Write(w, http.StatusNotFound,
				problem.New(problem.TypeNotFound, r, "Central not found", "no matching central"))
			return
		}
		var req systemUpdateInstallRequest
		if err := decodeOptionalJSON(r, &req); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid JSON body", err.Error()))
			return
		}
		if req.BackupFirst {
			if backup == nil {
				problem.Write(w, http.StatusServiceUnavailable,
					problem.New(problem.TypeServiceUnready, r, "Backup unavailable",
						"a pre-update backup was requested but backup storage is not wired"))
				return
			}
			// resolveHubForMutation accepts an empty central on a
			// single-CCU install; the backup needs the real name, so
			// resolve it from the index rather than guessing.
			target := centralName
			if target == "" {
				if hubs := idx.Hubs(); len(hubs) == 1 {
					target = hubs[0].Central
				}
			}
			if target == "" {
				problem.Write(w, http.StatusBadRequest,
					problem.New(problem.TypeValidation, r, "Central required",
						"a pre-update backup needs an explicit central on a multi-CCU install"))
				return
			}
			id, err := backup.CreateBackupForCentral(r.Context(), target)
			if err != nil {
				writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable,
					"Pre-update backup failed; the update was not started", err)
				return
			}
			if rec != nil {
				rec.Record(audit.Entry{
					User:   identityFromCtx(r.Context()),
					Action: audit.ActionBackupPreUpdate,
					Note:   target + " " + id,
				})
			}
		}
		if err := h.Update.Install(r.Context()); err != nil {
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "System update failed", err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// HubMetricsEntry is one entry in `GET /system/metrics` (one per central).
// Metric fields are pointers: nil means "not observed yet".
type HubMetricsEntry struct {
	// Central is the CCU these metrics belong to.
	Central string `json:"central,omitempty"`
	// SystemHealth is the aggregate health score in percent (0-100).
	SystemHealth *float64 `json:"system_health,omitempty"`
	// ConnectionLatencyMs is the average CCU round-trip latency.
	ConnectionLatencyMs *float64 `json:"connection_latency_ms,omitempty"`
	// LastEventAgeSeconds is the age of the newest backend event.
	LastEventAgeSeconds *float64 `json:"last_event_age_seconds,omitempty"`
}

// GetHubMetrics returns the hub metric samples per central as JSON —
// the machine-readable twin of the Prometheus `/metrics` endpoint for
// the three hub sensors external clients mirror.
func GetHubMetrics(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Hub unavailable", "no hub wired"))
			return
		}
		out := make([]HubMetricsEntry, 0, 2)
		for _, nh := range idx.Hubs() {
			if nh.Hub == nil || nh.Hub.Metrics == nil {
				continue
			}
			entry := HubMetricsEntry{Central: nh.Central}
			snap := nh.Hub.Metrics.Snapshot()
			if s, ok := snap[hub.MetricSystemHealth]; ok {
				v := s.Value
				entry.SystemHealth = &v
			}
			if s, ok := snap[hub.MetricConnectionLatMs]; ok {
				v := s.Value
				entry.ConnectionLatencyMs = &v
			}
			if s, ok := snap[hub.MetricLastEventAgeSecs]; ok {
				v := s.Value
				entry.LastEventAgeSeconds = &v
			}
			out = append(out, entry)
		}
		JSON(w, http.StatusOK, out)
	}
}

// InstallModeInterfaceEntry is one entry in `GET /install-mode/interfaces`.
type InstallModeInterfaceEntry struct {
	// Central is the CCU the interface belongs to.
	Central string `json:"central,omitempty"`
	// Interface is the interface ID the install-mode DP controls.
	Interface string `json:"interface"`
	// Active is true while install mode is running on the interface.
	Active bool `json:"active"`
	// Seconds is the remaining install-mode time (0 when inactive).
	Seconds int `json:"seconds"`
	// Observed is false until the first state fetch succeeded.
	Observed bool `json:"observed"`
}

// InstallModeInterfaceRequest is the body of `POST /install-mode/interfaces`.
type InstallModeInterfaceRequest struct {
	// Interface selects the install-mode DP (required).
	Interface string `json:"interface"`
	// Active starts (true) or stops (false) install mode.
	Active bool `json:"active"`
	// Seconds is the install-mode duration (default 60).
	Seconds int `json:"seconds,omitempty"`
	// Central disambiguates the CCU when several centrals expose the
	// same interface name. Empty matches the first interface entry
	// across all centrals (the historical behaviour).
	Central string `json:"central,omitempty"`
	// DeviceAddress restricts pairing to one already-known device
	// address (targeted teach-in / re-pairing by serial). Only
	// meaningful with active=true; ignored on stop.
	DeviceAddress string `json:"device_address,omitempty"`
	// SGTIN + Key request the keyserver-less HmIP LOCAL teach-in: the
	// CCU pairs exactly the device whose SGTIN and device key (from the
	// label; the short Base32 form is converted automatically) are
	// given. Both must come together, require active=true and are
	// mutually exclusive with DeviceAddress.
	SGTIN string `json:"sgtin,omitempty"`
	Key   string `json:"key,omitempty"`
}

// GetInstallModeInterfaces returns the per-interface install-mode state.
func GetInstallModeInterfaces(idx HubIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Hub unavailable", "no hub wired"))
			return
		}
		out := make([]InstallModeInterfaceEntry, 0, 4)
		for _, nh := range idx.Hubs() {
			if nh.Hub == nil {
				continue
			}
			for _, dp := range nh.Hub.InstallModeDPs() {
				active, remaining, observed := dp.InstallState()
				out = append(out, InstallModeInterfaceEntry{
					Central:   nh.Central,
					Interface: dp.InterfaceID,
					Active:    active,
					Seconds:   int(remaining.Seconds()),
					Observed:  observed,
				})
			}
		}
		JSON(w, http.StatusOK, out)
	}
}

// PostInstallModeInterface starts or stops install mode on one interface.
// Beyond the plain broadcast toggle it supports two targeted flavours:
// device_address (re-pairing an already-known device by serial) and
// sgtin+key (the keyserver-less HmIP LOCAL teach-in with a one-device
// whitelist).
//
//nolint:gocognit // sequential validation matrix + dispatch; splitting obscures the flow
func PostInstallModeInterface(idx HubIndex, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idx == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Hub unavailable", "no hub wired"))
			return
		}
		var req InstallModeInterfaceRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if msg := validateInstallModeRequest(req); msg != "" {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, msg, ""))
			return
		}
		duration := time.Duration(req.Seconds) * time.Second
		if duration == 0 {
			duration = 60 * time.Second
		}
		for _, nh := range idx.Hubs() {
			if nh.Hub == nil || (req.Central != "" && nh.Central != req.Central) {
				continue
			}
			for _, dp := range nh.Hub.InstallModeDPs() {
				if dp.InterfaceID != req.Interface {
					continue
				}
				var err error
				switch {
				case req.SGTIN != "":
					err = dp.EnableLocal(r.Context(), duration, req.SGTIN, req.Key)
				case req.Active && req.DeviceAddress != "":
					err = dp.EnableForDevice(r.Context(), duration, req.DeviceAddress)
				case req.Active:
					err = dp.Enable(r.Context(), duration)
				default:
					err = dp.Disable(r.Context())
				}
				if err != nil {
					writeInstallModeError(w, r, err)
					return
				}
				recordInstallMode(r, rec, nh.Central, req)
				w.WriteHeader(http.StatusAccepted)
				return
			}
		}
		problem.Write(w, http.StatusNotFound,
			problem.New(problem.TypeNotFound, r, "Interface not found", "no install-mode DP for interface"))
	}
}

// validateInstallModeRequest returns the 422 message for an invalid
// install-mode request, or "" when the request is consistent.
func validateInstallModeRequest(req InstallModeInterfaceRequest) string {
	switch {
	case req.Interface == "":
		return "interface is required"
	case req.Seconds < 0:
		return "seconds must be >= 0"
	case (req.SGTIN != "") != (req.Key != ""):
		return "sgtin and key must be supplied together"
	case req.SGTIN != "" && !req.Active:
		return "sgtin/key require active=true"
	case req.SGTIN != "" && req.DeviceAddress != "":
		return "sgtin/key and device_address are mutually exclusive"
	}
	return ""
}

// writeInstallModeError maps an install-mode write failure: whitelist
// input the operator can fix (bad SGTIN/key, LOCAL unsupported on the
// interface, invalid duration) answers 422, everything else 502.
func writeInstallModeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, hub.ErrInstallModeInvalidLocalInput) ||
		errors.Is(err, hub.ErrLocalInstallModeUnsupported) ||
		errors.Is(err, hub.ErrInstallModeInvalidDuration) ||
		errors.Is(err, backends.ErrUnsupported) {
		problem.Write(w, http.StatusUnprocessableEntity,
			problem.New(problem.TypeValidation, r, "Install mode request rejected", err.Error()))
		return
	}
	writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Install mode write failed", err)
}

// recordInstallMode appends the audit entry for a successful interface
// install-mode write. The device key is credential material and never
// recorded; a LOCAL teach-in notes the SGTIN via DeviceAddress instead.
func recordInstallMode(r *http.Request, rec audit.Recorder, central string, req InstallModeInterfaceRequest) {
	if rec == nil {
		return
	}
	entry := audit.Entry{
		User:   identityFromCtx(r.Context()),
		Action: audit.ActionInstallMode,
	}
	switch {
	case req.SGTIN != "":
		entry.Action = audit.ActionInstallModeLocal
		entry.DeviceAddress = req.SGTIN
		entry.Note = fmt.Sprintf("local teach-in (SGTIN whitelist) central=%s interface=%s", central, req.Interface)
	case req.Active:
		entry.DeviceAddress = req.DeviceAddress
		entry.Note = fmt.Sprintf("enable central=%s interface=%s seconds=%d", central, req.Interface, req.Seconds)
	default:
		entry.Note = fmt.Sprintf("disable central=%s interface=%s", central, req.Interface)
	}
	rec.Record(entry)
}
