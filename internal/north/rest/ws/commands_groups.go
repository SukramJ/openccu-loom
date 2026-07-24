// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// Heating-group administration WS commands (GR02). They mirror the REST
// group-admin surface and share its implementation via handlers.GroupsWriter
// (the same cmd-level adapter backs both transports). Role-gating lives in
// writeCommandRoles / readOnlyCommands: create/update/delete are admin,
// types/suitable_members are reads.

// groupsTypesHandler implements `groups.types`. Request:
// { "central": str (optional) }. Response: { "types": [...] }.
func groupsTypesHandler(w handlers.GroupsWriter) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Central string `json:"central"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		types, err := w.GroupTypes(ctx, p.Central)
		if err != nil {
			return nil, fmt.Errorf("groups.types: %w", err)
		}
		if types == nil {
			types = []handlers.GroupTypeEntry{}
		}
		return map[string]any{"types": types}, nil
	}
}

// groupsSuitableMembersHandler implements `groups.suitable_members`. Request:
// { "type_id": str, "central": str (optional) }.
func groupsSuitableMembersHandler(w handlers.GroupsWriter) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Central string `json:"central"`
			TypeID  string `json:"type_id"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if strings.TrimSpace(p.TypeID) == "" {
			return nil, errors.New("groups.suitable_members: type_id is required")
		}
		res, err := w.SuitableMembers(ctx, p.Central, p.TypeID)
		if err != nil {
			return nil, fmt.Errorf("groups.suitable_members: %w", err)
		}
		return res, nil
	}
}

// groupsCreateHandler implements `groups.create`. Request:
// { "type_id": str, "name": str, "forbid_single_operation"?: bool,
//
//	"members"?: [str], "central"?: str }. Response: the created group.
func groupsCreateHandler(w handlers.GroupsWriter) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Central               string   `json:"central"`
			TypeID                string   `json:"type_id"`
			Name                  string   `json:"name"`
			ForbidSingleOperation bool     `json:"forbid_single_operation"`
			Members               []string `json:"members"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.TypeID) == "" {
			return nil, errors.New("groups.create: name and type_id are required")
		}
		g, err := w.CreateGroup(ctx, p.Central, handlers.CreateGroupRequest{
			TypeID:                p.TypeID,
			Name:                  p.Name,
			ForbidSingleOperation: p.ForbidSingleOperation,
			Members:               p.Members,
		})
		if err != nil {
			return nil, fmt.Errorf("groups.create: %w", err)
		}
		return g, nil
	}
}

// groupsUpdateHandler implements `groups.update`. Request:
// { "id": int, "name": str, "forbid_single_operation"?: bool,
//
//	"members"?: [str], "central"?: str }.
func groupsUpdateHandler(w handlers.GroupsWriter) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Central               string   `json:"central"`
			ID                    int      `json:"id"`
			Name                  string   `json:"name"`
			ForbidSingleOperation bool     `json:"forbid_single_operation"`
			Members               []string `json:"members"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if strings.TrimSpace(p.Name) == "" {
			return nil, errors.New("groups.update: name is required")
		}
		if err := w.UpdateGroup(ctx, p.Central, p.ID, handlers.UpdateGroupRequest{
			Name:                  p.Name,
			ForbidSingleOperation: p.ForbidSingleOperation,
			Members:               p.Members,
		}); err != nil {
			return nil, fmt.Errorf("groups.update: %w", err)
		}
		return map[string]any{"ok": true}, nil
	}
}

// groupsDeleteHandler implements `groups.delete`. Request:
// { "id": int, "central"?: str }.
func groupsDeleteHandler(w handlers.GroupsWriter) CommandHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			Central string `json:"central"`
			ID      int    `json:"id"`
		}
		if err := decodeOrEmpty(raw, &p); err != nil {
			return nil, err
		}
		if err := w.DeleteGroup(ctx, p.Central, p.ID); err != nil {
			return nil, fmt.Errorf("groups.delete: %w", err)
		}
		return map[string]any{"ok": true}, nil
	}
}
