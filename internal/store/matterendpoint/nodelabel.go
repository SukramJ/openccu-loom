// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package matterendpoint

import (
	"context"
	"time"
)

// nodeLabelTimeout bounds one NodeLabel read or write. The callers hand
// no context (the write arrives through the bridge module's attribute
// callback), so the store sets its own short deadline rather than letting
// a stuck database block an interaction-model write indefinitely.
const nodeLabelTimeout = 5 * time.Second

// LoadNodeLabel returns the controller-written BridgedDeviceBasicInformation
// NodeLabel persisted for key, or "" when no controller renamed the
// endpoint, the endpoint has no row, or the read failed. A failed read
// degrades to the composed default label, which is what the endpoint
// served before any rename; the store has no logger to report it to.
func (s *Store) LoadNodeLabel(key SourceKey) string {
	ctx, cancel := context.WithTimeout(context.Background(), nodeLabelTimeout)
	defer cancel()
	var label string
	err := s.db.QueryRowContext(
		ctx, `
SELECT node_label FROM matter_endpoints
WHERE central_name = ? AND device_address = ? AND channel_no = ? AND dp_kind = ? AND dp_key = ?`,
		key.CentralName, key.DeviceAddress, key.ChannelNo, string(key.DPKind), key.DPKey,
	).Scan(&label)
	if err != nil {
		// sql.ErrNoRows (no row for key) and a failed read both mean
		// "no persisted label".
		return ""
	}
	return label
}

// SaveNodeLabel persists label as key's controller-written NodeLabel.
//
// It only updates an existing row and never creates one: a row carries
// the endpoint id, which only the assembler allocates, and a controller
// can only write the label of an endpoint the assembler already
// persisted. A write for a key without a row is therefore dropped. Write
// errors are swallowed for the same reason the read degrades: the
// callback has no error channel back to the controller and the store has
// no logger; the label then lives only until the next restart, as it did
// before it was persisted at all.
func (s *Store) SaveNodeLabel(key SourceKey, label string) {
	ctx, cancel := context.WithTimeout(context.Background(), nodeLabelTimeout)
	defer cancel()
	_, _ = s.db.ExecContext(
		ctx, `
UPDATE matter_endpoints SET node_label = ?, updated_at = CURRENT_TIMESTAMP
WHERE central_name = ? AND device_address = ? AND channel_no = ? AND dp_kind = ? AND dp_key = ?`,
		label, key.CentralName, key.DeviceAddress, key.ChannelNo, string(key.DPKind), key.DPKey,
	)
}
