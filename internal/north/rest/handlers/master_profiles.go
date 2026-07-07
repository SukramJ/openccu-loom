// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/internal/store/masterprofile"
)

// MasterProfilesService is the read-only facade the master-profiles REST
// endpoints depend on. *masterprofile.Store satisfies it directly; REST
// and the WS `master_profiles.list/get/match` commands
// (internal/north/rest/ws/commands_extended.go) share the same domain
// calls rather than re-implementing the lookup/match logic.
type MasterProfilesService interface {
	Profiles(deviceType, channelType string) ([]masterprofile.Profile, error)
	Profile(deviceType, channelType string, id int) (masterprofile.Profile, error)
	MatchActiveProfile(deviceType, channelType string, currentValues map[string]any) int
}

// masterProfileSummary is one entry in the ListMasterProfiles response,
// mirroring the per-profile shape the WS `master_profiles.list` command
// returns (id, localised name/description, param_count — not the full
// parameter map, which GetMasterProfile carries).
type masterProfileSummary struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ParamCount  int    `json:"param_count"`
}

// resolveMasterProfileTarget resolves the (device_type, channel_type) pair
// a master-profiles request targets from the {addr}/{no} path parameters.
// device_type is the device's Model (e.g. "HmIP-eTRV"); channel_type is
// the channel's Type (e.g. "CLIMATECONTROL") — the same two identifiers
// the WS master_profiles.* commands take as explicit request fields.
func resolveMasterProfileTarget(idx DeviceIndex, r *http.Request) (deviceType, channelType string, err error) {
	addr := chi.URLParam(r, "addr")
	d, ok := idx.Device(addr)
	if !ok {
		return "", "", errors.Join(problem.ErrNotFound, errors.New("device "+addr))
	}
	numStr := chi.URLParam(r, "no")
	if numStr == "" {
		return "", "", errors.Join(problem.ErrNotFound, errors.New("channel"))
	}
	chAddr := addr + ":" + numStr
	ch := d.Channel(chAddr)
	if ch == nil {
		return "", "", errors.Join(problem.ErrNotFound, errors.New("channel "+chAddr))
	}
	return d.Model, ch.Type, nil
}

// ListMasterProfiles returns the master profiles available for a
// channel's (device_type, channel_type) pair. An unknown pair (no
// profiles catalogued) returns an empty array rather than 404 — absence
// of a master-profile catalogue is not an error condition for a channel
// that simply has none. Supports ?locale= for the localised name/description.
func ListMasterProfiles(idx DeviceIndex, svc MasterProfilesService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deviceType, channelType, err := resolveMasterProfileTarget(idx, r)
		if err != nil {
			problem.WriteFromError(w, r, err)
			return
		}
		profiles, err := svc.Profiles(deviceType, channelType)
		if err != nil {
			if errors.Is(err, masterprofile.ErrNotFound) {
				JSON(w, http.StatusOK, []masterProfileSummary{})
				return
			}
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "List master profiles failed", err)
			return
		}
		locale := r.URL.Query().Get("locale")
		out := make([]masterProfileSummary, 0, len(profiles))
		for _, p := range profiles {
			out = append(out, masterProfileSummary{
				ID:          p.ID,
				Name:        p.LocalisedName(locale),
				Description: p.LocalisedDescription(locale),
				ParamCount:  len(p.Params),
			})
		}
		JSON(w, http.StatusOK, out)
	}
}

// GetMasterProfile returns the full parameter set of a single master
// profile by device_type (resolved from the channel's device) and id.
func GetMasterProfile(idx DeviceIndex, svc MasterProfilesService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deviceType, channelType, err := resolveMasterProfileTarget(idx, r)
		if err != nil {
			problem.WriteFromError(w, r, err)
			return
		}
		idStr := chi.URLParam(r, "id")
		id, convErr := strconv.Atoi(idStr)
		if convErr != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid profile id", idStr))
			return
		}
		prof, err := svc.Profile(deviceType, channelType, id)
		if err != nil {
			if errors.Is(err, masterprofile.ErrNotFound) {
				problem.Write(w, http.StatusNotFound,
					problem.New(problem.TypeNotFound, r, "Master profile not found", idStr))
				return
			}
			writeServerError(w, r, http.StatusInternalServerError, problem.TypeInternal, "Get master profile failed", err)
			return
		}
		JSON(w, http.StatusOK, prof)
	}
}

// matchMasterProfileRequest is the POST body for MatchMasterProfile.
type matchMasterProfileRequest struct {
	CurrentValues map[string]any `json:"current_values"`
}

// MatchMasterProfile matches the observed parameter values in the request
// body against the master-profile constraint set for the target channel
// and returns the active profile id (0 = Expert / no match). Mirrors the
// WS `master_profiles.match` command's response shape.
func MatchMasterProfile(idx DeviceIndex, svc MasterProfilesService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deviceType, channelType, err := resolveMasterProfileTarget(idx, r)
		if err != nil {
			problem.WriteFromError(w, r, err)
			return
		}
		var body matchMasterProfileRequest
		if r.Body != nil && r.ContentLength != 0 {
			if decErr := json.NewDecoder(r.Body).Decode(&body); decErr != nil {
				problem.Write(w, http.StatusBadRequest,
					problem.New(problem.TypeBadRequest, r, "Invalid JSON body", decErr.Error()))
				return
			}
		}
		id := svc.MatchActiveProfile(deviceType, channelType, body.CurrentValues)
		JSON(w, http.StatusOK, map[string]any{"active_id": id})
	}
}
