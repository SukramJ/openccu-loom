// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/configstore"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// configAdminAdapter satisfies [handlers.ConfigAdminService] by
// delegating Effective() to the configstore facade and the section
// CRUD methods to the SQLite section store directly. The translation
// between [configstore.Section] (typed string) and the section
// store's plain string parameter is a one-line cast at each
// boundary.
//
// This adapter lives in the composition root (cmd/openccu-loom/)
// rather than in the configstore package itself so the handler
// package keeps a dependency-free interface and the daemon owns the
// composition decisions.
type configAdminAdapter struct {
	store    *configstore.Store
	sections *sqlitestore.ConfigSectionStore
}

var _ handlers.ConfigAdminService = configAdminAdapter{}

// Effective forwards to the configstore facade.
func (a configAdminAdapter) Effective(ctx context.Context) (*configstore.EffectiveResult, error) {
	return a.store.Effective(ctx)
}

// GetSection forwards to the SQLite store.
func (a configAdminAdapter) GetSection(ctx context.Context, section configstore.Section) (sqlitestore.SectionRow, error) {
	return a.sections.Get(ctx, string(section))
}

// PutSection forwards to the SQLite store.
func (a configAdminAdapter) PutSection(ctx context.Context, section configstore.Section, valueJSON []byte, updatedBy string) (sqlitestore.SectionRow, error) {
	return a.sections.Put(ctx, string(section), valueJSON, updatedBy)
}

// DeleteSection forwards to the SQLite store.
func (a configAdminAdapter) DeleteSection(ctx context.Context, section configstore.Section) error {
	return a.sections.Delete(ctx, string(section))
}
