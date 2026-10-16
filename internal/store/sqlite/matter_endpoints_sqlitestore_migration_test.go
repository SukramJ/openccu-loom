// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"database/sql"
	"testing"

	"github.com/SukramJ/go-fabric/node"
	matterstore "github.com/SukramJ/go-fabric/store"
	"github.com/pressly/goose/v3"

	"github.com/SukramJ/openccu-loom/internal/store/matterendpoint"
)

// legacyEndpointRow is one matter_endpoints row in the five-column shape
// migrations 007 and 047 created.
type legacyEndpointRow struct {
	key        matterendpoint.SourceKey
	endpointID uint16
	deviceType uint16
	nodeLabel  string
}

func legacyEndpointRows() []legacyEndpointRow {
	return []legacyEndpointRow{
		{matterendpoint.SourceKey{CentralName: "ccu-main", DeviceAddress: "00021BE9957782", ChannelNo: 4, DPKind: matterendpoint.DPKindCustom, DPKey: "switch"}, 2, 0x010A, "Bookshelf"},
		{matterendpoint.SourceKey{CentralName: "ccu-main", DeviceAddress: "0001D3C99C3C93", ChannelNo: 0, DPKind: matterendpoint.DPKindGeneric, DPKey: "OPERATING_VOLTAGE"}, 7, 0x0510, ""},
		{matterendpoint.SourceKey{CentralName: "ccu_2", DeviceAddress: "CUX2801001", ChannelNo: 12, DPKind: matterendpoint.DPKindMeasurement, DPKey: "A|B"}, 300, 0x0302, "Garden"},
		{matterendpoint.SourceKey{CentralName: "ccu_2", DeviceAddress: "INT0000001", ChannelNo: 1, DPKind: matterendpoint.DPKindCalculated, DPKey: "dew_point"}, 65534, 0x0307, ""},
		{matterendpoint.SourceKey{CentralName: "ccu_2", DeviceAddress: "VCU0000001", ChannelNo: 3, DPKind: matterendpoint.DPKindCombined, DPKey: "cover"}, 41, 0x0202, ""},
	}
}

// TestMigration_049_CarriesEveryEndpointNumberAcross pins the move onto
// go-fabric's sqlitestore schema: after the migration the daemon's store
// finds every pre-existing row through the Go rendering of its key — so
// the SQL concatenation and SourceKey.String agree byte for byte — with the
// same endpoint number and device type; go-fabric's EndpointPersistence
// reads every NodeLabel back; and the allocation counter continues from the
// old high-water mark.
func TestMigration_049_CarriesEveryEndpointNumberAcross(t *testing.T) {
	ctx := context.Background()
	db := openDBAtVersion(t, "matter_endpoints_049.db", 48)

	for _, r := range legacyEndpointRows() {
		if _, err := db.ExecContext(ctx, `
INSERT INTO matter_endpoints (central_name, device_address, channel_no, dp_kind, dp_key, endpoint_id, device_type, node_label)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			r.key.CentralName, r.key.DeviceAddress, r.key.ChannelNo, string(r.key.DPKind), r.key.DPKey,
			r.endpointID, r.deviceType, r.nodeLabel); err != nil {
			t.Fatalf("seed %s: %v", r.key, err)
		}
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO matter_metadata (key, value) VALUES ('next_endpoint_id', 301)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`); err != nil {
		t.Fatalf("seed counter: %v", err)
	}

	migrateTo(t, db, 49)

	st := matterendpoint.New(db)
	persistence := node.NewEndpointPersistence(matterstore.New(db), nil)
	for _, r := range legacyEndpointRows() {
		rec, err := st.GetEndpoint(ctx, r.key)
		if err != nil {
			t.Fatalf("GetEndpoint(%s) after migration: %v", r.key, err)
		}
		if rec.EndpointID != r.endpointID || rec.DeviceType != r.deviceType || rec.Scope != r.key.CentralName {
			t.Errorf("%s: got endpoint %d type 0x%04X scope %q, want %d 0x%04X %q",
				r.key, rec.EndpointID, rec.DeviceType, rec.Scope, r.endpointID, r.deviceType, r.key.CentralName)
		}
		if got := persistence.NodeLabel(ctx, r.key); got != r.nodeLabel {
			t.Errorf("%s: NodeLabel %q, want %q", r.key, got, r.nodeLabel)
		}
	}

	// Listing decodes every row back into the daemon's own key type — a
	// row that came back as a foreign key would look vanished to the
	// assembler's garbage collection.
	listed, err := st.ListEndpoints(ctx, "")
	if err != nil {
		t.Fatalf("ListEndpoints: %v", err)
	}
	if len(listed) != len(legacyEndpointRows()) {
		t.Fatalf("ListEndpoints: %d rows, want %d", len(listed), len(legacyEndpointRows()))
	}
	for _, rec := range listed {
		if _, ok := rec.Key.(matterendpoint.SourceKey); !ok {
			t.Errorf("listed key %q decoded as %T, want matterendpoint.SourceKey", rec.Key.String(), rec.Key)
		}
	}

	var next int
	if err := db.QueryRowContext(ctx,
		`SELECT next_endpoint_id FROM matter_endpoint_allocation WHERE id = 1`).Scan(&next); err != nil {
		t.Fatalf("read allocation counter: %v", err)
	}
	if next != 301 {
		t.Errorf("allocation counter %d, want 301 carried over from matter_metadata", next)
	}
	var leftover int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM matter_metadata WHERE key = 'next_endpoint_id'`).Scan(&leftover); err != nil {
		t.Fatalf("read old counter: %v", err)
	}
	if leftover != 0 {
		t.Errorf("matter_metadata still holds next_endpoint_id")
	}
}

// TestMigration_049_DownRestoresTheFiveColumnRows pins the way back: the
// five key columns, endpoint numbers, NodeLabels and the counter survive a
// down-migration.
func TestMigration_049_DownRestoresTheFiveColumnRows(t *testing.T) {
	ctx := context.Background()
	db := openDBAtVersion(t, "matter_endpoints_049_down.db", 48)
	for _, r := range legacyEndpointRows() {
		if _, err := db.ExecContext(ctx, `
INSERT INTO matter_endpoints (central_name, device_address, channel_no, dp_kind, dp_key, endpoint_id, device_type, node_label)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			r.key.CentralName, r.key.DeviceAddress, r.key.ChannelNo, string(r.key.DPKind), r.key.DPKey,
			r.endpointID, r.deviceType, r.nodeLabel); err != nil {
			t.Fatalf("seed %s: %v", r.key, err)
		}
	}
	migrateTo(t, db, 49)

	openMu.Lock()
	err := goose.DownToContext(ctx, db, "migrations", 48)
	openMu.Unlock()
	if err != nil {
		t.Fatalf("down to 48: %v", err)
	}

	for _, r := range legacyEndpointRows() {
		var endpointID, deviceType int
		var label string
		err := db.QueryRowContext(
			ctx, `
SELECT endpoint_id, device_type, node_label FROM matter_endpoints
WHERE central_name = ? AND device_address = ? AND channel_no = ? AND dp_kind = ? AND dp_key = ?`,
			r.key.CentralName, r.key.DeviceAddress, r.key.ChannelNo, string(r.key.DPKind), r.key.DPKey,
		).Scan(&endpointID, &deviceType, &label)
		if err != nil {
			t.Fatalf("%s after down: %v", r.key, err)
		}
		if endpointID != int(r.endpointID) || deviceType != int(r.deviceType) || label != r.nodeLabel {
			t.Errorf("%s after down: %d 0x%04X %q, want %d 0x%04X %q",
				r.key, endpointID, deviceType, label, r.endpointID, r.deviceType, r.nodeLabel)
		}
	}
	if tableExists(t, db, "matter_endpoint_allocation") {
		t.Error("matter_endpoint_allocation survives the down-migration")
	}
	var next int
	if err := db.QueryRowContext(ctx,
		`SELECT value FROM matter_metadata WHERE key = 'next_endpoint_id'`).Scan(&next); err != nil {
		t.Fatalf("counter after down: %v", err)
	}
	if next < 2 {
		t.Errorf("counter after down %d, want >= 2", next)
	}
}

// migrateTo runs the embedded migrations up to version on db, under the
// same lock openDBAtVersion takes for goose's package-level state.
func migrateTo(t *testing.T, db *sql.DB, version int64) {
	t.Helper()
	openMu.Lock()
	defer openMu.Unlock()
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect(string(goose.DialectSQLite3)); err != nil {
		t.Fatalf("migrateTo SetDialect: %v", err)
	}
	if err := goose.UpToContext(context.Background(), db, "migrations", version); err != nil {
		t.Fatalf("migrateTo %d: %v", version, err)
	}
}
