// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"
	"strings"
	"time"

	modevent "github.com/SukramJ/openccu-loom/internal/model/event"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// EventGroupSummary is one entry in
// `GET /api/v1/devices/{addr}/channels/{no}/event-groups`. It is the
// per-[modevent.Kind] aggregation of a channel's keypress / impulse /
// device-error event sources — the bootstrap shape an `event` platform
// builds its entities from. Mirrors the reference stack's
// ChannelEventGroup.
type EventGroupSummary struct {
	ChannelAddress string `json:"channel_address"`
	// UniqueID is the canonical loom-namespaced routing key for this event
	// group (the [event.Group.CanonicalUniqueID] result). Lets a client seed
	// its event-entity registry from the summary without recomputing the key.
	UniqueID string `json:"unique_id,omitempty"`
	// Kind is the short device-trigger flavour ("keypress", "impulse",
	// "device_error"), matching the group's translation key rather than the
	// fully-qualified internal Kind ("homematic.keypress").
	Kind string `json:"kind"`
	// EventTypes are the lowercased parameter names of every member source
	// (e.g. ["press_short", "press_long"]) — the canonical event-type set an
	// EventEntity exposes.
	EventTypes []string `json:"event_types"`
	// Parameters are the upper-case CCU parameter names of the member sources.
	Parameters []string `json:"parameters"`
	// Available reflects the parent device's reachability.
	Available bool `json:"available"`
	// LastTriggeredEvent is the most recent member fire, or null when none has
	// been observed yet.
	LastTriggeredEvent *TriggeredEventSummary `json:"last_triggered_event,omitempty"`
}

// TriggeredEventSummary describes the most recent event fired within a group.
type TriggeredEventSummary struct {
	Parameter   string `json:"parameter"`
	Value       any    `json:"value,omitempty"`
	TriggeredAt string `json:"triggered_at,omitempty"`
}

// ListEventGroups renders a channel's per-Kind event groups. Returns an empty
// array when the channel carries no keypress / impulse / device-error sources.
func ListEventGroups(idx DeviceIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ch, err := lookupChannel(idx, r)
		if err != nil {
			problem.WriteFromError(w, r, err)
			return
		}
		groups := ch.EventGroups()
		serial := serialSuffixForChannel(idx, ch)
		out := make([]EventGroupSummary, 0, len(groups))
		for _, g := range groups {
			out = append(out, toEventGroupSummary(g, serial))
		}
		JSON(w, http.StatusOK, out)
	}
}

func toEventGroupSummary(g *modevent.Group, serialSuffix string) EventGroupSummary {
	params := g.Parameters()
	ps := make([]string, len(params))
	for i, p := range params {
		ps[i] = string(p)
	}
	eventTypes := g.EventTypes()
	if eventTypes == nil {
		eventTypes = []string{}
	}
	s := EventGroupSummary{
		ChannelAddress: g.ChannelAddress,
		UniqueID:       g.CanonicalUniqueID(serialSuffix),
		Kind:           g.TranslationKey(),
		EventTypes:     eventTypes,
		Parameters:     ps,
		Available:      g.Available(),
	}
	if src := g.LastTriggeredEvent(); src != nil {
		if at, val, ok := src.LastFire(); ok {
			s.LastTriggeredEvent = &TriggeredEventSummary{
				Parameter:   strings.ToLower(string(src.EventParameter())),
				Value:       val,
				TriggeredAt: at.UTC().Format(time.RFC3339),
			}
		}
	}
	return s
}
