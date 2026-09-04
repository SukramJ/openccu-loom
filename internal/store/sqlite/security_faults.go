// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// SecurityFault is one open or historical fault of a security-relevant
// data point.
type SecurityFault struct {
	ID             string
	Ref            string
	Class          string
	Reason         string
	Severity       string
	CentralName    string
	InterfaceID    string
	DeviceAddress  string
	ChannelAddress string
	Parameter      string
	Name           string
	SinceMS        int64
	ClearedAtMS    int64
	AcknowledgedAt int64
	AcknowledgedBy string
}

// Open reports whether the fault is still standing.
func (f SecurityFault) Open() bool { return f.ClearedAtMS == 0 }

// SecurityFaultStore persists the fault ledger.
type SecurityFaultStore struct {
	db *sql.DB
}

// NewSecurityFaultStore returns a store backed by db.
func NewSecurityFaultStore(db *sql.DB) *SecurityFaultStore { return &SecurityFaultStore{db: db} }

// Raise opens a fault, or leaves an already-open one untouched.
//
// Keeping the existing row is the point: `since` answers "how long has
// this been broken", and a device that re-reports the same fault every
// few minutes would otherwise reset that clock on every report and make
// a three-day outage look like it started seconds ago.
//
// It returns the effective row and whether this call opened it.
func (s *SecurityFaultStore) Raise(ctx context.Context, f SecurityFault) (SecurityFault, bool, error) {
	existing, found, err := s.OpenByRefReason(ctx, f.Ref, f.Reason)
	if err != nil {
		return SecurityFault{}, false, err
	}
	if found {
		return existing, false, nil
	}
	const q = `
INSERT INTO security_faults (id, ref, class, reason, severity, central_name, interface_id,
    device_address, channel_address, parameter, name, since_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := s.db.ExecContext(ctx, q, f.ID, f.Ref, f.Class, f.Reason, f.Severity,
		f.CentralName, f.InterfaceID, f.DeviceAddress, f.ChannelAddress, f.Parameter,
		f.Name, f.SinceMS); err != nil {
		return SecurityFault{}, false, fmt.Errorf("sqlite: raise security fault: %w", err)
	}
	return f, true, nil
}

// Clear closes the open fault of a (ref, reason) pair. It reports
// whether a fault was actually standing, so a caller only announces a
// real transition.
func (s *SecurityFaultStore) Clear(ctx context.Context, ref, reason string, atMS int64) (bool, error) {
	const q = `UPDATE security_faults SET cleared_at_ms = ? WHERE ref = ? AND reason = ? AND cleared_at_ms = 0`
	res, err := s.db.ExecContext(ctx, q, atMS, ref, reason)
	if err != nil {
		return false, fmt.Errorf("sqlite: clear security fault: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ClearByCentral closes every open fault of one central. A central that
// goes away takes its faults with it — leaving them standing would show
// a permanently unreachable device that is simply no longer configured.
func (s *SecurityFaultStore) ClearByCentral(ctx context.Context, centralName string, atMS int64) (int64, error) {
	const q = `UPDATE security_faults SET cleared_at_ms = ? WHERE central_name = ? AND cleared_at_ms = 0`
	res, err := s.db.ExecContext(ctx, q, atMS, centralName)
	if err != nil {
		return 0, fmt.Errorf("sqlite: clear security faults of central: %w", err)
	}
	return res.RowsAffected()
}

// Acknowledge marks an open fault as seen. Acknowledgement never closes
// the fault: the condition is still there, the operator has merely
// stopped needing to be told.
func (s *SecurityFaultStore) Acknowledge(ctx context.Context, id string, atMS int64, by string) (bool, error) {
	const q = `
UPDATE security_faults
SET acknowledged_at_ms = ?, acknowledged_by = ?
WHERE id = ? AND cleared_at_ms = 0 AND acknowledged_at_ms = 0`
	res, err := s.db.ExecContext(ctx, q, atMS, by, id)
	if err != nil {
		return false, fmt.Errorf("sqlite: acknowledge security fault: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// OpenByRefReason returns the standing fault of a (ref, reason) pair.
func (s *SecurityFaultStore) OpenByRefReason(ctx context.Context, ref, reason string) (SecurityFault, bool, error) {
	const q = securityFaultSelect + ` WHERE ref = ? AND reason = ? AND cleared_at_ms = 0 LIMIT 1`
	f, err := scanSecurityFault(s.db.QueryRowContext(ctx, q, ref, reason))
	if errors.Is(err, sql.ErrNoRows) {
		return SecurityFault{}, false, nil
	}
	if err != nil {
		return SecurityFault{}, false, fmt.Errorf("sqlite: get security fault: %w", err)
	}
	return f, true, nil
}

// ListOpen returns every standing fault, oldest first — the order in
// which they need attention.
func (s *SecurityFaultStore) ListOpen(ctx context.Context) ([]SecurityFault, error) {
	const q = securityFaultSelect + ` WHERE cleared_at_ms = 0 ORDER BY since_ms ASC, id ASC`
	return s.query(ctx, q)
}

// PurgeClearedBefore drops closed faults older than the cutoff.
func (s *SecurityFaultStore) PurgeClearedBefore(ctx context.Context, cutoffMS int64) (int64, error) {
	const q = `DELETE FROM security_faults WHERE cleared_at_ms > 0 AND cleared_at_ms < ?`
	res, err := s.db.ExecContext(ctx, q, cutoffMS)
	if err != nil {
		return 0, fmt.Errorf("sqlite: purge security faults: %w", err)
	}
	return res.RowsAffected()
}

func (s *SecurityFaultStore) query(ctx context.Context, q string, args ...any) ([]SecurityFault, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list security faults: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []SecurityFault
	for rows.Next() {
		f, err := scanSecurityFault(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan security fault: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func scanSecurityFault(sc scannable) (SecurityFault, error) {
	var f SecurityFault
	err := sc.Scan(&f.ID, &f.Ref, &f.Class, &f.Reason, &f.Severity, &f.CentralName,
		&f.InterfaceID, &f.DeviceAddress, &f.ChannelAddress, &f.Parameter, &f.Name,
		&f.SinceMS, &f.ClearedAtMS, &f.AcknowledgedAt, &f.AcknowledgedBy)
	return f, err
}

const securityFaultSelect = `
SELECT id, ref, class, reason, severity, central_name, interface_id, device_address,
       channel_address, parameter, name, since_ms, cleared_at_ms,
       acknowledged_at_ms, acknowledged_by
FROM security_faults`
