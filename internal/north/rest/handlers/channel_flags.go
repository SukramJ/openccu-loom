// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/channelflags"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// ChannelFlagsWriter persists the per-channel operator overrides (G12) —
// the SQLite [ChannelFlagsStore] satisfies it directly.
type ChannelFlagsWriter interface {
	Set(ctx context.Context, centralName, channelAddress string, hidden, locked bool, updatedBy string) error
}

type channelFlagsResponse struct {
	Hidden bool `json:"hidden"`
	Locked bool `json:"locked"`
}

// channelFlagsRequest is a partial update: an absent field keeps its
// current value, so a caller can toggle one flag without echoing the other.
type channelFlagsRequest struct {
	Hidden *bool `json:"hidden"`
	Locked *bool `json:"locked"`
}

// GetChannelFlags GET /api/v1/devices/{addr}/channels/{no}/flags — the
// channel's current operator overrides.
func GetChannelFlags(idx DeviceIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ch, err := lookupChannel(idx, r)
		if err != nil {
			problem.WriteFromError(w, r, err)
			return
		}
		JSON(w, http.StatusOK, channelFlagsResponse{Hidden: ch.IsHidden(), Locked: ch.IsLocked()})
	}
}

// PutChannelFlags PUT /api/v1/devices/{addr}/channels/{no}/flags — set the
// channel's hidden / locked overrides. Persists to the store, updates the
// overlay (so the next ingest re-applies), and applies to the live channel
// immediately. Operator-gated, audit-logged.
func PutChannelFlags(idx DeviceIndex, store ChannelFlagsWriter, overlay *channelflags.Overlay, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil || overlay == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Channel flags unavailable", ""))
			return
		}
		ch, err := lookupChannel(idx, r)
		if err != nil {
			problem.WriteFromError(w, r, err)
			return
		}
		var req channelFlagsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", ""))
			return
		}
		hidden, locked := ch.IsHidden(), ch.IsLocked()
		if req.Hidden != nil {
			hidden = *req.Hidden
		}
		if req.Locked != nil {
			locked = *req.Locked
		}

		central := ch.CentralName()
		subject := identityFromCtx(r.Context())
		if err := store.Set(r.Context(), central, ch.Address, hidden, locked, subject); err != nil {
			problem.WriteFromError(w, r, err)
			return
		}
		overlay.Set(central, ch.Address, channelflags.Flags{Hidden: hidden, Locked: locked})
		ch.SetOperatorFlags(hidden, locked)
		if rec != nil {
			rec.Record(audit.Entry{
				User:          subject,
				Action:        audit.ActionChannelFlags,
				DeviceAddress: ch.Address,
				Note:          fmt.Sprintf("%s hidden=%v locked=%v", central, hidden, locked),
			})
		}
		JSON(w, http.StatusOK, channelFlagsResponse{Hidden: hidden, Locked: locked})
	}
}
