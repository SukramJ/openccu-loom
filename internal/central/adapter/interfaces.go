// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// InterfacesAdapter satisfies restapi.InterfaceIndex.
type InterfacesAdapter struct {
	registry    *central.Registry
	reconnector Reconnector
}

// Reconnector is the contract the `POST /interfaces/{id}/reconnect`
// endpoint uses. Implementations typically wrap the client layer's
// ConnectionRecoveryCoordinator.
type Reconnector interface {
	Reconnect(ctx context.Context, centralName, interfaceID string) error
}

// NewInterfacesAdapter wires the adapter. `reconnector` may be nil
// — reconnect requests then return [ErrNoReconnector].
func NewInterfacesAdapter(r *central.Registry, rc Reconnector) *InterfacesAdapter {
	return &InterfacesAdapter{registry: r, reconnector: rc}
}

// ErrNoReconnector is returned when the adapter has no reconnector.
var ErrNoReconnector = errors.New("adapter: no reconnector wired")

// Interfaces enumerates every configured interface across every
// central.
func (a *InterfacesAdapter) Interfaces() []hmapi.InterfaceState {
	if a.registry == nil {
		return nil
	}
	var out []hmapi.InterfaceState
	for _, u := range a.registry.List() {
		for _, e := range u.Clients.List() {
			out = append(out, hmapi.InterfaceState{
				ID:        e.InterfaceID,
				Name:      e.InterfaceID,
				Connected: e.Connected(),
				Interface: string(e.Interface),
				CentralID: u.Name(),
				Host:      e.Host,
			})
		}
	}
	return out
}

// Interface returns the state for id, searching every central.
func (a *InterfacesAdapter) Interface(id string) (hmapi.InterfaceState, bool) {
	for _, s := range a.Interfaces() {
		if s.ID == id {
			return s, true
		}
	}
	return hmapi.InterfaceState{}, false
}

// Reconnect dispatches through the configured reconnector.
func (a *InterfacesAdapter) Reconnect(ctx context.Context, id string) error {
	if a.reconnector == nil {
		return ErrNoReconnector
	}
	iface, ok := a.Interface(id)
	if !ok {
		return errors.New("adapter: unknown interface " + id)
	}
	return a.reconnector.Reconnect(ctx, iface.CentralID, id)
}
