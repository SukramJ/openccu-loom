// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/central"
)

// SysvarRefresher re-pulls the CCU system-variable catalogue on demand
// and updates the hub model. It is the mid-life counterpart to the
// boot-time sysvar load: both end up calling the same JSON-RPC
// SysVar.getAll path via the hub coordinator's sysvar-refresh hook.
//
// Mirrors the Python reference's fetch_system_variables — the operator-
// triggered "force re-pull sysvars from the CCU" action.
type SysvarRefresher interface {
	// FetchSystemVariables re-pulls all sysvars from the CCU and refreshes
	// the hub model. When centralName is empty every registered central is
	// refreshed; otherwise only the named one.
	FetchSystemVariables(ctx context.Context, centralName string) error
}

// SysvarFetchAdapter implements [SysvarRefresher] against the central
// registry. The actual fetch is delegated to each central's
// HubCoordinator.RefreshSysvars, which serialises against the periodic
// scheduler job and reuses the existing boot-time loadSysvars closure
// wired in WireHub.
type SysvarFetchAdapter struct {
	registry *central.Registry
}

// NewSysvarFetchAdapter wires the adapter.
func NewSysvarFetchAdapter(r *central.Registry) *SysvarFetchAdapter {
	return &SysvarFetchAdapter{registry: r}
}

// ErrNoSysvarRefreshTarget is returned when no central could be resolved
// for a fetch request (unknown name, or no centrals registered at all).
var ErrNoSysvarRefreshTarget = errors.New("sysvar_fetch: no central available")

// FetchSystemVariables re-pulls the sysvar catalogue. With an explicit
// centralName only that central is refreshed; with an empty name every
// registered central is refreshed and the first error (if any) is
// returned after all have been attempted.
func (a *SysvarFetchAdapter) FetchSystemVariables(ctx context.Context, centralName string) error {
	if a.registry == nil {
		return ErrNoSysvarRefreshTarget
	}
	if centralName != "" {
		u, ok := a.registry.Get(centralName)
		if !ok || u == nil || u.Hub == nil {
			return fmt.Errorf("%w: %s", ErrNoSysvarRefreshTarget, centralName)
		}
		if err := u.Hub.RefreshSysvars(ctx); err != nil {
			return fmt.Errorf("sysvar_fetch: %s: %w", centralName, err)
		}
		return nil
	}

	units := a.registry.List()
	if len(units) == 0 {
		return ErrNoSysvarRefreshTarget
	}
	var firstErr error
	for _, u := range units {
		if u == nil || u.Hub == nil {
			continue
		}
		if err := u.Hub.RefreshSysvars(ctx); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("sysvar_fetch: %s: %w", u.Name(), err)
		}
	}
	return firstErr
}
