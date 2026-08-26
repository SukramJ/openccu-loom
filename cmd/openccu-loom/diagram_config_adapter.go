// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// diagramConfigAdapter bridges the SQLite diagram store to the handler
// service, converting rows and mapping store sentinels to handler
// sentinels so the handler stays store-agnostic.
type diagramConfigAdapter struct {
	store *sqlitestore.DiagramConfigStore
}

// newDiagramConfigAdapter returns a handler service over the store, or a
// genuine-nil interface when the store is nil (no durable DB) so the
// /diagrams routes are not mounted.
func newDiagramConfigAdapter(s *sqlitestore.DiagramConfigStore) handlers.DiagramConfigService {
	if s == nil {
		return nil
	}
	return &diagramConfigAdapter{store: s}
}

func convDiagram(d sqlitestore.DiagramConfig) handlers.DiagramConfig {
	return handlers.DiagramConfig{
		ID: d.ID, OwnerSubject: d.OwnerSubject, Name: d.Name, Visibility: d.Visibility,
		ConfigJSON: d.ConfigJSON, CreatedAtMs: d.CreatedAtMs, UpdatedAtMs: d.UpdatedAtMs,
	}
}

func mapDiagramErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sqlitestore.ErrDiagramNotFound):
		return handlers.ErrDiagramNotFound
	case errors.Is(err, sqlitestore.ErrDiagramForbidden):
		return handlers.ErrDiagramForbidden
	case errors.Is(err, sqlitestore.ErrDiagramInvalid):
		// Preserve the granular reason as the handler's 400 detail while
		// keeping errors.Is(err, handlers.ErrDiagramInvalid) true.
		return fmt.Errorf("%w: %s", handlers.ErrDiagramInvalid, err.Error())
	default:
		return err
	}
}

func (a *diagramConfigAdapter) List(ctx context.Context, subject string) ([]handlers.DiagramConfig, error) {
	rows, err := a.store.List(ctx, subject)
	if err != nil {
		return nil, err
	}
	out := make([]handlers.DiagramConfig, 0, len(rows))
	for _, d := range rows {
		out = append(out, convDiagram(d))
	}
	return out, nil
}

func (a *diagramConfigAdapter) Get(ctx context.Context, id, subject string, isAdmin bool) (handlers.DiagramConfig, error) {
	d, err := a.store.Get(ctx, id, subject, isAdmin)
	if err != nil {
		return handlers.DiagramConfig{}, mapDiagramErr(err)
	}
	return convDiagram(d), nil
}

func (a *diagramConfigAdapter) Create(ctx context.Context, subject, name, visibility, configJSON string) (handlers.DiagramConfig, error) {
	d, err := a.store.Create(ctx, subject, name, visibility, configJSON)
	if err != nil {
		return handlers.DiagramConfig{}, mapDiagramErr(err)
	}
	return convDiagram(d), nil
}

func (a *diagramConfigAdapter) Update(ctx context.Context, id, subject string, isAdmin bool, name, visibility, configJSON string) (handlers.DiagramConfig, error) {
	d, err := a.store.Update(ctx, id, subject, isAdmin, name, visibility, configJSON)
	if err != nil {
		return handlers.DiagramConfig{}, mapDiagramErr(err)
	}
	return convDiagram(d), nil
}

func (a *diagramConfigAdapter) Delete(ctx context.Context, id, subject string, isAdmin bool) error {
	return mapDiagramErr(a.store.Delete(ctx, id, subject, isAdmin))
}
