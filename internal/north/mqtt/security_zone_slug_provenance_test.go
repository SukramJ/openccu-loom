// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/internal/i18n"
	securitydomain "github.com/SukramJ/openccu-loom/internal/security"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestSecurityZoneTopicsCarryTheStoredSlug pins the provenance
// [sqlitestore.AlarmZoneRow.Slug] claims for itself: that the frozen
// slug, and not the row's UUID, is what reaches the MQTT topic tree.
//
// The plane's own round-trip guard builds its zones as hand-written
// [security.ZoneState] literals, so it compares the plane against
// itself and never touches the store. That leaves the store's stated
// reason for carrying a slug beside the UUID — "it reaches consumer
// entity ids and MQTT topics, so letting it follow a rename would
// orphan every entity of that zone" — resting on a fact nothing
// measured. This test starts at the row, projects it through the
// security domain's own zone-slug pass, and reads the topics the real
// publisher writes.
func TestSecurityZoneTopicsCarryTheStoredSlug(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const (
		zoneID   = "6b1f3a90-2c4d-4f18-9a77-0d5e8c3b1a42"
		zoneSlug = "erdgeschoss"
		base     = "gh"
	)

	db, err := sqlitestore.Open(ctx, sqlitestore.FileDSN(filepath.Join(t.TempDir(), "zones.db")))
	if err != nil {
		t.Fatalf("sqlitestore.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cats, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("i18n.NewCatalogs: %v", err)
	}
	stores := &securitydomain.Stores{
		Faults:  sqlitestore.NewSecurityFaultStore(db),
		Sources: sqlitestore.NewSecuritySourceStore(db),
		Sensors: sqlitestore.NewAlarmSensorStore(db),
		Zones:   sqlitestore.NewAlarmZoneStore(db),
	}
	svc, err := securitydomain.New(securitydomain.Deps{
		Registry: central.NewRegistry(),
		Stores:   stores,
		AlarmBus: events.NewBus(),
		Clock:    clock.NewFake(time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)),
		Logger:   slog.New(slog.DiscardHandler),
		Catalogs: cats,
	})
	if err != nil {
		t.Fatalf("security.New: %v", err)
	}

	if err := stores.Zones.Upsert(ctx, sqlitestore.AlarmZoneRow{
		ID: zoneID, Name: "Erdgeschoss", Slug: zoneSlug, Position: 1,
		ConfigJSON: "{}", CreatedAtMS: 1000, UpdatedAtMS: 1000,
	}); err != nil {
		t.Fatalf("seed zone: %v", err)
	}
	if err := svc.RebuildIndex(ctx); err != nil {
		t.Fatalf("RebuildIndex: %v", err)
	}

	snap := svc.Snapshot()
	if _, ok := snap.Zones[zoneSlug]; !ok {
		t.Fatalf("domain snapshot lost the stored slug: zones=%v", snap.Zones)
	}

	obs := newObservedPlane()
	bridge := NewBridge(BridgeConfig{
		Base: base, CentralName: "ccu-01",
		RawEnabled: true, HADiscoveryEnabled: true,
	}, obs)
	if err := bridge.AnnounceOnline(ctx); err != nil {
		t.Fatalf("bridge announce: %v", err)
	}
	p := NewSecurityMQTTPublisher(staticSecuritySnapshot{snap},
		NewWiring(bridge, slog.Default()), "en", "", slog.Default())
	bus := events.NewBus()
	p.Start(bus)
	t.Cleanup(p.Stop)
	events.Publish(bus, hmevent.SecurityStateChangedEvent{Base: hmevent.NewBaseAt(time.Now())})
	obs.settle(t)

	want := base + "/security/zone/" + zoneSlug
	published := obs.publishedTopics()
	if !published[want] {
		t.Fatalf("zone state topic %q was never written; published=%v", want, published)
	}
	declared := obs.declaredTopics(t)
	if !declared[want] {
		t.Fatalf("zone state topic %q was never declared; declared=%v", want, declared)
	}
	for topic := range published {
		if strings.Contains(topic, zoneID) {
			t.Fatalf("published topic %q carries the zone UUID; the slug is the external identifier", topic)
		}
	}
}
