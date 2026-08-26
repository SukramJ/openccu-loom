// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// restartPendingProvider implements handlers.RestartPendingProvider by
// comparing the freshly assembled persisted config against the running
// boot config over the restart-required field set. Computing it on
// demand means it clears the moment a change is reverted — no flag to
// keep in sync.
type restartPendingProvider struct {
	boot *config.Config
	svc  handlers.ConfigAdminService
}

// newRestartPendingProvider snapshots the boot config so a later in-place
// hot-reload of hot-swappable fields cannot perturb the comparison
// baseline for the restart-required fields.
func newRestartPendingProvider(boot *config.Config, svc handlers.ConfigAdminService) *restartPendingProvider {
	if boot == nil {
		return &restartPendingProvider{svc: svc}
	}
	snapshot := *boot
	return &restartPendingProvider{boot: &snapshot, svc: svc}
}

func (p *restartPendingProvider) Pending(ctx context.Context) (pending bool, fields []string, err error) {
	if p == nil || p.boot == nil || p.svc == nil {
		return false, nil, nil
	}
	eff, err := p.svc.Effective(ctx)
	if err != nil {
		return false, nil, err
	}
	fields = config.RestartRequiredDiff(p.boot, eff.Config)
	return len(fields) > 0, fields, nil
}

// Changes implements handlers.ConfigChangesProvider — every config field
// that differs from the running boot config (what changed since start).
func (p *restartPendingProvider) Changes(ctx context.Context) (fields []string, err error) {
	if p == nil || p.boot == nil || p.svc == nil {
		return nil, nil
	}
	eff, err := p.svc.Effective(ctx)
	if err != nil {
		return nil, err
	}
	return config.ChangedFields(p.boot, eff.Config), nil
}
