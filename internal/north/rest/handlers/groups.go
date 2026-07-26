// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// GroupsReader is the read facade `GET /api/v1/groups` pulls from.
// *groupsAdapter (cmd/openccu-loom) satisfies it by walking the central
// registry and reading each CCU's heating-group roster. A `central`
// query parameter scopes the listing to one central; empty aggregates
// across all of them.
type GroupsReader interface {
	List(ctx context.Context, central string) ([]GroupCentralEntry, error)
}

// GroupCentralEntry groups one central's heating groups under its name,
// keeping the response multi-CCU-aware without forcing the SPA to
// cross-reference a separate central index.
type GroupCentralEntry struct {
	// Central is the daemon-local central name the groups belong to.
	Central string `json:"central"`
	// Groups is the central's heating-group roster (possibly empty).
	Groups []GroupEntry `json:"groups"`
}

// GroupEntry is one heating group as the read surface exposes it.
type GroupEntry struct {
	// ID is the numeric CCU group id.
	ID int `json:"id"`
	// Name is the operator-facing group name.
	Name string `json:"name"`
	// GroupDeviceName is the backing virtual device's label; often empty.
	GroupDeviceName string `json:"group_device_name,omitempty"`
	// ForbidSingleOperation reports the "operate only via group" flag.
	ForbidSingleOperation bool `json:"forbid_single_operation"`
	// TypeID is the group-type key.
	TypeID string `json:"type_id"`
	// TypeLabel is the CCU-provided type label (may be a translation key).
	TypeLabel string `json:"type_label,omitempty"`
	// Members are the devices/channels wired into the group.
	Members []GroupMemberEntry `json:"members"`
}

// GroupMemberEntry is one device/channel belonging to a group.
type GroupMemberEntry struct {
	// Address is the member's device or channel address.
	Address string `json:"address"`
	// TypeID is the member-type key.
	TypeID string `json:"type_id,omitempty"`
	// DeviceName is the CCU-assigned name of the member's parent device,
	// resolved from the live device model. Empty when the member is not in the
	// model; the client then falls back to the address.
	DeviceName string `json:"device_name,omitempty"`
	// DeviceModel is the parent device model (e.g. "HmIP-STHD").
	DeviceModel string `json:"device_model,omitempty"`
	// ChannelName is the CCU-assigned channel name, when the member is a channel.
	ChannelName string `json:"channel_name,omitempty"`
	// Rooms are the member's assigned rooms (channel's, falling back to device).
	Rooms []string `json:"rooms,omitempty"`
}

// ListGroups serves the read-only heating-group listing at
// `GET /api/v1/groups`. It always returns 200 with an `entries` array —
// one object per central — for the aggregate case. A `?central=<name>`
// query narrows to a single central and returns 404 when that central is
// unknown. 503 signals an unwired service.
func ListGroups(reader GroupsReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if reader == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Groups service unwired", ""))
			return
		}
		central := r.URL.Query().Get("central")
		entries, err := reader.List(r.Context(), central)
		if err != nil {
			if errors.Is(err, hmerr.ErrUnknownCentral) {
				problem.Write(w, http.StatusNotFound,
					problem.New(problem.TypeNotFound, r, "Unknown central", central))
				return
			}
			if errors.Is(err, backends.ErrUnsupported) {
				problem.Write(w, http.StatusNotFound,
					problem.New(problem.TypeNotFound, r, "Groups not available", central))
				return
			}
			writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable,
				"Group listing failed", err)
			return
		}
		if entries == nil {
			entries = []GroupCentralEntry{}
		}
		JSON(w, http.StatusOK, map[string]any{"entries": entries})
	}
}
