// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// DeviceTeamPort is the channel-team assignment facade.
// *adapter.DeviceAdminDomain satisfies it.
type DeviceTeamPort interface {
	TeamCandidates(ctx context.Context, deviceAddr string, channelNo int) ([]hmapi.TeamCandidate, error)
	SetChannelTeam(ctx context.Context, deviceAddr string, channelNo int, teamChannelAddress string) error
}

// setTeamRequest is the body of PUT /devices/{addr}/channels/{no}/team.
// A null / empty team resets the channel to its own default team.
type setTeamRequest struct {
	Team *string `json:"team"`
}

// GetDeviceTeamCandidates serves
// `GET /devices/{addr}/channels/{no}/team-candidates`: the team channels
// the channel may be assigned to (same team tag). Read-only.
func GetDeviceTeamCandidates(svc DeviceTeamPort) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "device team admin unwired", ""))
			return
		}
		no, err := strconv.Atoi(chi.URLParam(r, "no"))
		if err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid channel number", ""))
			return
		}
		candidates, err := svc.TeamCandidates(r.Context(), chi.URLParam(r, "addr"), no)
		if err != nil {
			if errors.Is(err, backends.ErrUnsupported) {
				problem.Write(w, http.StatusUnprocessableEntity,
					problem.New(problem.TypeValidation, r, "Team assignment not supported on this interface", ""))
				return
			}
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Team candidates lookup failed", err)
			return
		}
		if candidates == nil {
			candidates = []hmapi.TeamCandidate{}
		}
		JSON(w, http.StatusOK, map[string]any{"candidates": candidates})
	}
}

// SetDeviceChannelTeam serves
// `PUT /devices/{addr}/channels/{no}/team`: assign the channel to a team
// (or reset to default when team is null/empty). Operator-gated,
// audit-logged.
func SetDeviceChannelTeam(svc DeviceTeamPort, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "device team admin unwired", ""))
			return
		}
		no, err := strconv.Atoi(chi.URLParam(r, "no"))
		if err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid channel number", ""))
			return
		}
		var body setTeamRequest
		if err := DecodeJSON(r, &body); err != nil {
			problem.Write(w, DecodeJSONStatus(err),
				problem.New(problem.TypeBadRequest, r, "Invalid request body", err.Error()))
			return
		}
		team := ""
		if body.Team != nil {
			team = *body.Team
		}
		addr := chi.URLParam(r, "addr")
		if err := svc.SetChannelTeam(r.Context(), addr, no, team); err != nil {
			if errors.Is(err, backends.ErrUnsupported) {
				problem.Write(w, http.StatusUnprocessableEntity,
					problem.New(problem.TypeValidation, r, "Team assignment not supported on this interface", ""))
				return
			}
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Team assignment failed", err)
			return
		}
		if rec != nil {
			note := "team=" + team
			if team == "" {
				note = "team=reset"
			}
			rec.Record(audit.Entry{
				User:          identityFromCtx(r.Context()),
				Action:        audit.ActionDeviceTeamSet,
				DeviceAddress: addr + ":" + strconv.Itoa(no),
				Note:          note,
			})
		}
		w.WriteHeader(http.StatusAccepted)
	}
}
