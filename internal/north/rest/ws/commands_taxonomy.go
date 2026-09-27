// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"context"
	"encoding/json"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// taxonomyListHandler serves taxonomy.list: every central's taxonomy, or
// one central's with `central`.
func taxonomyListHandler(src handlers.TaxonomySource) CommandHandler {
	return func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Central string `json:"central"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		out, ok := handlers.BuildTaxonomyResponse(src, p.Central)
		if !ok {
			return nil, NewCommandError(CommandErrorNotFound, "taxonomy.list: unknown central "+p.Central)
		}
		return out, nil
	}
}

// taxonomyNodeCreateHandler serves taxonomy.node_create.
func taxonomyNodeCreateHandler(a handlers.TaxonomyNodeAdmin) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Central    string `json:"central"`
			Enum       string `json:"enum"`
			ParentPath string `json:"parent_path"`
			Name       string `json:"name"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.Enum == "" || p.Name == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "enum and name are required")
		}
		path, err := a.CreateNode(ctx, p.Central, p.Enum, p.ParentPath, p.Name)
		if err != nil {
			return nil, commandErr(CommandErrorInternal, "taxonomy.node_create: ", err)
		}
		return map[string]any{"path": path}, nil
	}
}

// taxonomyNodeUpdateHandler serves taxonomy.node_update: rename and/or
// move (parent_path "" moves to the root).
func taxonomyNodeUpdateHandler(a handlers.TaxonomyNodeAdmin) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Central    string  `json:"central"`
			Enum       string  `json:"enum"`
			Path       string  `json:"path"`
			Name       *string `json:"name"`
			ParentPath *string `json:"parent_path"`
			Position   *int    `json:"position"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.Enum == "" || p.Path == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "enum and path are required")
		}
		if err := a.UpdateNode(ctx, p.Central, p.Enum, p.Path, p.Name, p.ParentPath, p.Position); err != nil {
			return nil, commandErr(CommandErrorInternal, "taxonomy.node_update: ", err)
		}
		return map[string]any{"ok": true}, nil
	}
}

// taxonomyNodeDeleteHandler serves taxonomy.node_delete.
func taxonomyNodeDeleteHandler(a handlers.TaxonomyNodeAdmin) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Central string `json:"central"`
			Enum    string `json:"enum"`
			Path    string `json:"path"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if p.Enum == "" || p.Path == "" {
			return nil, NewCommandError(CommandErrorBadRequest, "enum and path are required")
		}
		if err := a.DeleteNode(ctx, p.Central, p.Enum, p.Path); err != nil {
			return nil, commandErr(CommandErrorInternal, "taxonomy.node_delete: ", err)
		}
		return map[string]any{"ok": true}, nil
	}
}
