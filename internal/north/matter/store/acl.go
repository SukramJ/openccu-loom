// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// Privilege mirrors Matter §11.2.12 AccessControlEntryPrivilege.
type Privilege uint8

// Privilege values per Matter §11.2.12.
const (
	PrivilegeView       Privilege = 1
	PrivilegeProxyView  Privilege = 2
	PrivilegeOperate    Privilege = 3
	PrivilegeManage     Privilege = 4
	PrivilegeAdminister Privilege = 5
)

// AuthMode mirrors Matter §11.2.12 AccessControlEntryAuthMode.
type AuthMode uint8

// AuthMode values per Matter §11.2.12.
const (
	AuthModePASE  AuthMode = 1
	AuthModeCASE  AuthMode = 2
	AuthModeGroup AuthMode = 3
)

// ACLTarget is one entry in the Targets list of an ACE. Each field
// is optional (nullable) per Matter §11.2.12.4: a missing field
// matches anything in that dimension.
type ACLTarget struct {
	Cluster    *uint32 `json:"cluster,omitempty"`
	Endpoint   *uint16 `json:"endpoint,omitempty"`
	DeviceType *uint32 `json:"deviceType,omitempty"`
}

// ACLEntry is one access-control entry. The list of entries per
// fabric is ordered (position field) — Matter evaluates ACEs in
// list order.
type ACLEntry struct {
	ID          int64
	FabricIndex uint8
	Privilege   Privilege
	AuthMode    AuthMode
	Subjects    []uint64
	Targets     []ACLTarget
	Position    uint16
}

// ListACL returns every ACE for fabricIndex ordered by position
// ascending.
func (s *Store) ListACL(ctx context.Context, fabricIndex uint8) ([]ACLEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, fabric_index, privilege, auth_mode, subjects_json, targets_json, position
FROM matter_acl_entries WHERE fabric_index = ?
ORDER BY position ASC`, fabricIndex)
	if err != nil {
		return nil, fmt.Errorf("matter store: list acl: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []ACLEntry
	for rows.Next() {
		entry, err := scanACLEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("matter store: list acl: scan: %w", err)
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("matter store: list acl: rows: %w", err)
	}
	return out, nil
}

// ReplaceACL atomically swaps the ACL of fabricIndex with entries.
// Position values are rewritten to be 0..len(entries)-1 in input
// order; callers do not need to set Position themselves.
func (s *Store) ReplaceACL(ctx context.Context, fabricIndex uint8, entries []ACLEntry) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("matter store: replace acl: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM matter_acl_entries WHERE fabric_index = ?`, fabricIndex); err != nil {
		return fmt.Errorf("matter store: replace acl: delete: %w", err)
	}

	const ins = `
INSERT INTO matter_acl_entries
    (fabric_index, privilege, auth_mode, subjects_json, targets_json, position)
VALUES (?, ?, ?, ?, ?, ?)`
	stmt, err := tx.PrepareContext(ctx, ins)
	if err != nil {
		return fmt.Errorf("matter store: replace acl: prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for i := range entries {
		subjectsJSON, err := json.Marshal(entries[i].Subjects)
		if err != nil {
			return fmt.Errorf("matter store: replace acl: marshal subjects: %w", err)
		}
		var targetsJSON any
		if entries[i].Targets != nil {
			b, err := json.Marshal(entries[i].Targets)
			if err != nil {
				return fmt.Errorf("matter store: replace acl: marshal targets: %w", err)
			}
			targetsJSON = string(b)
		}
		//nolint:gosec // i fits in uint16 — ACL list size capped well below 65k
		if _, err := stmt.ExecContext(ctx, fabricIndex, uint8(entries[i].Privilege), uint8(entries[i].AuthMode),
			string(subjectsJSON), targetsJSON, uint16(i)); err != nil {
			return fmt.Errorf("matter store: replace acl: insert %d: %w", i, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("matter store: replace acl: commit: %w", err)
	}
	return nil
}

func scanACLEntry(r scanRow) (ACLEntry, error) {
	var (
		entry        ACLEntry
		priv         uint8
		auth         uint8
		subjectsJSON string
		targetsJSON  sql.NullString
	)
	if err := r.Scan(&entry.ID, &entry.FabricIndex, &priv, &auth, &subjectsJSON, &targetsJSON, &entry.Position); err != nil {
		return ACLEntry{}, err
	}
	entry.Privilege = Privilege(priv)
	entry.AuthMode = AuthMode(auth)
	if err := json.Unmarshal([]byte(subjectsJSON), &entry.Subjects); err != nil {
		return ACLEntry{}, fmt.Errorf("unmarshal subjects: %w", err)
	}
	if targetsJSON.Valid {
		if err := json.Unmarshal([]byte(targetsJSON.String), &entry.Targets); err != nil {
			return ACLEntry{}, fmt.Errorf("unmarshal targets: %w", err)
		}
	}
	return entry, nil
}
