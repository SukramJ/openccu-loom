// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// ErrDeviceNotFound is returned when Get can't find a device.
var ErrDeviceNotFound = errors.New("sqlite: device not found")

// DeviceRecord persists alongside every device description.
type DeviceRecord struct {
	CentralName  string
	InterfaceID  string
	Address      string
	Type         string
	Parent       string
	Firmware     string
	Model        string
	Manufacturer hmenum.Manufacturer
	ProductGroup hmenum.ProductGroup
	Hash         string
	Description  hmproto.DeviceDescription
}

// DeviceStore persists device descriptions.
type DeviceStore struct {
	db *sql.DB
}

// NewDeviceStore returns a store backed by db.
//
// loom:reachable:reason="constructed in daemon.go central wiring alongside other SQLite stores; device persistence for warm-boot cache"
func NewDeviceStore(db *sql.DB) *DeviceStore { return &DeviceStore{db: db} }

// Upsert writes or replaces rec.
func (s *DeviceStore) Upsert(ctx context.Context, rec DeviceRecord) error {
	raw, err := json.Marshal(rec.Description)
	if err != nil {
		return fmt.Errorf("sqlite: marshal device: %w", err)
	}
	const q = `
INSERT INTO devices (central_name, interface_id, address, type, parent, firmware, model, manufacturer, product_group, hash, description_json, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(central_name, interface_id, address) DO UPDATE SET
    type = excluded.type,
    parent = excluded.parent,
    firmware = excluded.firmware,
    model = excluded.model,
    manufacturer = excluded.manufacturer,
    product_group = excluded.product_group,
    hash = excluded.hash,
    description_json = excluded.description_json,
    updated_at = CURRENT_TIMESTAMP;
`
	_, err = s.db.ExecContext(
		ctx, q,
		rec.CentralName, rec.InterfaceID, rec.Address, rec.Type, rec.Parent,
		rec.Firmware, rec.Model, string(rec.Manufacturer), string(rec.ProductGroup),
		rec.Hash, string(raw),
	)
	if err != nil {
		return fmt.Errorf("sqlite: upsert device: %w", err)
	}
	return nil
}

// Get returns the record for (central, interface, address).
func (s *DeviceStore) Get(ctx context.Context, centralName, ifaceID, address string) (DeviceRecord, error) {
	const q = `
SELECT type, parent, firmware, model, manufacturer, product_group, hash, description_json
FROM devices WHERE central_name = ? AND interface_id = ? AND address = ?`
	var rec DeviceRecord
	rec.CentralName = centralName
	rec.InterfaceID = ifaceID
	rec.Address = address

	var mfr, pg, raw string
	err := s.db.QueryRowContext(ctx, q, centralName, ifaceID, address).
		Scan(&rec.Type, &rec.Parent, &rec.Firmware, &rec.Model, &mfr, &pg, &rec.Hash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return DeviceRecord{}, ErrDeviceNotFound
	}
	if err != nil {
		return DeviceRecord{}, fmt.Errorf("sqlite: get device: %w", err)
	}
	rec.Manufacturer = hmenum.Manufacturer(mfr)
	rec.ProductGroup = hmenum.ProductGroup(pg)
	if err := json.Unmarshal([]byte(raw), &rec.Description); err != nil {
		return DeviceRecord{}, fmt.Errorf("sqlite: unmarshal device: %w", err)
	}
	return rec, nil
}

// Delete removes a device. Returns the number of rows affected.
func (s *DeviceStore) Delete(ctx context.Context, centralName, ifaceID, address string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM devices WHERE central_name = ? AND interface_id = ? AND address = ?`,
		centralName, ifaceID, address)
	if err != nil {
		return 0, fmt.Errorf("sqlite: delete device: %w", err)
	}
	return res.RowsAffected()
}

// Size returns the total number of device records stored for central.
func (s *DeviceStore) Size(ctx context.Context, centralName string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM devices WHERE central_name = ?`, centralName).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("sqlite: device size: %w", err)
	}
	return n, nil
}

// FindDeviceDescription returns the record for (central, interface, address),
// returning nil when not found instead of an error.
func (s *DeviceStore) FindDeviceDescription(ctx context.Context, centralName, ifaceID, address string) (*DeviceRecord, error) {
	rec, err := s.Get(ctx, centralName, ifaceID, address)
	if err != nil {
		if errors.Is(err, ErrDeviceNotFound) {
			return nil, nil //nolint:nilnil // intentional: None-return parity with Python
		}
		return nil, err
	}
	return &rec, nil
}

// GetAddresses returns the sorted set of all known device/channel addresses
// for (central, interface).
func (s *DeviceStore) GetAddresses(ctx context.Context, centralName, ifaceID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT address FROM devices WHERE central_name = ? AND interface_id = ? ORDER BY address`,
		centralName, ifaceID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get addresses: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var addr string
		if err := rows.Scan(&addr); err != nil {
			return nil, fmt.Errorf("sqlite: scan address: %w", err)
		}
		out = append(out, addr)
	}
	return out, rows.Err()
}

// GetDeviceWithChannels returns the device description plus all channel
// descriptions in a single query. The returned map is keyed by address.
func (s *DeviceStore) GetDeviceWithChannels(ctx context.Context, centralName, ifaceID, deviceAddress string) (map[string]DeviceRecord, error) {
	const q = `
SELECT address, type, parent, firmware, model, manufacturer, product_group, hash, description_json
FROM devices
WHERE central_name = ? AND interface_id = ? AND (address = ? OR parent = ?)
ORDER BY address`
	rows, err := s.db.QueryContext(ctx, q, centralName, ifaceID, deviceAddress, deviceAddress)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get device with channels: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]DeviceRecord)
	for rows.Next() {
		var rec DeviceRecord
		rec.CentralName = centralName
		rec.InterfaceID = ifaceID
		var mfr, pg, raw string
		if err := rows.Scan(&rec.Address, &rec.Type, &rec.Parent, &rec.Firmware,
			&rec.Model, &mfr, &pg, &rec.Hash, &raw); err != nil {
			return nil, fmt.Errorf("sqlite: scan device with channels: %w", err)
		}
		rec.Manufacturer = hmenum.Manufacturer(mfr)
		rec.ProductGroup = hmenum.ProductGroup(pg)
		if err := json.Unmarshal([]byte(raw), &rec.Description); err != nil {
			return nil, fmt.Errorf("sqlite: unmarshal device with channels: %w", err)
		}
		out[rec.Address] = rec
	}
	return out, rows.Err()
}

// GetInterfaceIDs returns all interface IDs that have device records for central.
func (s *DeviceStore) GetInterfaceIDs(ctx context.Context, centralName string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT interface_id FROM devices WHERE central_name = ? ORDER BY interface_id`,
		centralName)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get interface ids: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("sqlite: scan interface id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// GetModel returns the model string for deviceAddress across all interfaces
// in central, or the empty string when the device is not found.
func (s *DeviceStore) GetModel(ctx context.Context, centralName, deviceAddress string) (string, error) {
	var model string
	err := s.db.QueryRowContext(ctx,
		`SELECT model FROM devices WHERE central_name = ? AND address = ? LIMIT 1`,
		centralName, deviceAddress).Scan(&model)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("sqlite: get model: %w", err)
	}
	return model, nil
}

// HasDeviceDescriptions reports whether any device records exist for
// (central, interface).
func (s *DeviceStore) HasDeviceDescriptions(ctx context.Context, centralName, ifaceID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM devices WHERE central_name = ? AND interface_id = ? LIMIT 1`,
		centralName, ifaceID).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("sqlite: has device descriptions: %w", err)
	}
	return n > 0, nil
}

// Clear removes all device records for (central, ifaceID). Returns the number
// of rows deleted.
func (s *DeviceStore) Clear(ctx context.Context, centralName, ifaceID string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM devices WHERE central_name = ? AND interface_id = ?`,
		centralName, ifaceID)
	if err != nil {
		return 0, fmt.Errorf("sqlite: clear devices: %w", err)
	}
	return res.RowsAffected()
}

// ListByInterface returns every device belonging to (central, iface)
// sorted by address ascending.
func (s *DeviceStore) ListByInterface(ctx context.Context, centralName, ifaceID string) ([]DeviceRecord, error) {
	const q = `
SELECT address, type, parent, firmware, model, manufacturer, product_group, hash, description_json
FROM devices WHERE central_name = ? AND interface_id = ? ORDER BY address`
	rows, err := s.db.QueryContext(ctx, q, centralName, ifaceID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list devices: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []DeviceRecord
	for rows.Next() {
		var rec DeviceRecord
		rec.CentralName = centralName
		rec.InterfaceID = ifaceID
		var mfr, pg, raw string
		if err := rows.Scan(&rec.Address, &rec.Type, &rec.Parent, &rec.Firmware,
			&rec.Model, &mfr, &pg, &rec.Hash, &raw); err != nil {
			return nil, fmt.Errorf("sqlite: scan device: %w", err)
		}
		rec.Manufacturer = hmenum.Manufacturer(mfr)
		rec.ProductGroup = hmenum.ProductGroup(pg)
		if err := json.Unmarshal([]byte(raw), &rec.Description); err != nil {
			return nil, fmt.Errorf("sqlite: unmarshal device: %w", err)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}
