// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// SystemStatusEntry is one entry in the [SystemStatusBuffer].
// Rendered verbatim at `GET /api/v1/system/status`.
type SystemStatusEntry struct {
	Central            string              `json:"central"`
	Component          string              `json:"component"`
	Healthy            bool                `json:"healthy"`
	Reason             string              `json:"reason,omitempty"`
	InterfaceID        string              `json:"interface_id,omitempty"`
	ErrorCode          int                 `json:"error_code,omitempty"`
	CentralState       hmenum.CentralState `json:"central_state,omitempty"`
	ConnectionState    string              `json:"connection_state,omitempty"`
	DegradedInterfaces []string            `json:"degraded_interfaces,omitempty"`
	Issues             []string            `json:"issues,omitempty"`
	EventAt            time.Time           `json:"event_at"`
}

// SystemStatusReader is the narrow contract `GET /api/v1/system/status`
// depends on.
type SystemStatusReader interface {
	SystemStatusEntries() []SystemStatusEntry
}

// SystemStatusBuffer is a bounded ring buffer of recent
// [SystemStatusEntry] values populated by subscribing to the central
// event bus via [SystemStatusBuffer.Subscribe].
//
// It satisfies [SystemStatusReader] so it can be injected directly
// into the REST router.
type SystemStatusBuffer struct {
	mu   sync.Mutex
	ring []SystemStatusEntry
	head int
	size int
}

// NewSystemStatusBuffer returns a buffer that retains the last n
// entries. n < 1 is clamped to 1.
func NewSystemStatusBuffer(n int) *SystemStatusBuffer {
	if n < 1 {
		n = 1
	}
	return &SystemStatusBuffer{ring: make([]SystemStatusEntry, n)}
}

// append adds e to the ring, evicting the oldest entry when full.
func (b *SystemStatusBuffer) append(e SystemStatusEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ring[b.head] = e
	b.head = (b.head + 1) % len(b.ring)
	if b.size < len(b.ring) {
		b.size++
	}
}

// SystemStatusEntries returns up to the last n entries in
// chronological order (oldest first). Satisfies [SystemStatusReader].
func (b *SystemStatusBuffer) SystemStatusEntries() []SystemStatusEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.size == 0 {
		return nil
	}
	out := make([]SystemStatusEntry, b.size)
	start := (b.head - b.size + len(b.ring)) % len(b.ring)
	for i := range b.size {
		out[i] = b.ring[(start+i)%len(b.ring)]
	}
	return out
}

// Subscribe attaches the buffer to every central in reg so incoming
// [hmevent.SystemStatusChangedEvent] values are appended automatically.
// Returns a closer that removes all subscriptions. Safe to call once
// after the bus is live.
func (b *SystemStatusBuffer) Subscribe(reg *central.Registry) (stop func()) {
	if reg == nil {
		return func() {}
	}
	var unsubs []func()
	for _, u := range reg.List() {
		bus := u.EventBus
		if bus == nil {
			continue
		}
		centralName := u.Name()
		unsub := events.Subscribe(bus, func(e hmevent.SystemStatusChangedEvent) {
			b.append(SystemStatusEntry{
				Central:            centralName,
				Component:          e.Component,
				Healthy:            e.Healthy,
				Reason:             e.Reason,
				InterfaceID:        e.InterfaceID,
				ErrorCode:          e.ErrorCode,
				CentralState:       e.CentralState,
				ConnectionState:    e.ConnectionState,
				DegradedInterfaces: e.DegradedInterfaces,
				Issues:             e.Issues,
				EventAt:            e.Timestamp(),
			})
		})
		unsubs = append(unsubs, unsub)
	}
	return func() {
		for _, u := range unsubs {
			u()
		}
	}
}

// SystemStatusResponse is the body of `GET /api/v1/system/status`.
type SystemStatusResponse struct {
	Events []SystemStatusEntry `json:"events"`
}

// ListSystemStatus returns a handler that renders the last N
// system-status events.
func ListSystemStatus(reader SystemStatusReader) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		var entries []SystemStatusEntry
		if reader != nil {
			entries = reader.SystemStatusEntries()
		}
		if entries == nil {
			entries = []SystemStatusEntry{}
		}
		JSON(w, http.StatusOK, SystemStatusResponse{Events: entries})
	}
}
