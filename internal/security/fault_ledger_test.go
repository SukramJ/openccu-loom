// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package security

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// --- M8: a fault standing at boot reaches the ledger ---

// TestRebuildIndexRaisesLedgerFaultForAlreadyActiveSource pins the M8
// fix: before it, RebuildIndex's boot-time seeding only ever wrote
// `agg.active` — the in-memory activation map — for a source the
// device model already reports active. A device that went unreachable
// while the daemon was down therefore showed as active in the class
// view immediately on restart (the seed worked), but the fault ledger
// stayed empty forever: no row, no `since`, no report, and — because
// Clear only closes a row that exists — no clear event either once the
// device came back.
//
// central.New only requires Config.Name and ModelRegistry.Put is a bare
// map write (see internal/alarm/candidates_test.go's newCandidatesRegistry),
// so this drives the real classification path
// (Service.RebuildIndex -> indexUnit -> classify) over an in-memory
// device model rather than the lighter but weaker alternative of
// pre-seeding the ledger directly.
func TestRebuildIndexRaisesLedgerFaultForAlreadyActiveSource(t *testing.T) {
	t.Parallel()
	const (
		centralName = "c1"
		deviceAddr  = "DEV0001"
	)

	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF", Address: deviceAddr, Model: "HmIP-SWDO", Name: "Front Door",
	})
	// UNREACH lives on the maintenance channel (0) on a real CCU device;
	// hmenum.ParameterUnreach classifies to Technical/Unreachable for
	// any model or channel type (internal/model/safety/classify.go's
	// model/channel-agnostic byParameter table), so the channel type
	// here is not load-bearing.
	maint := dev.AddChannel(deviceAddr+":0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	unreach := generic.NewBinarySensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID: "HmIP-RF", ChannelAddress: maint.Address,
			ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterUnreach),
		},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeBool, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	// The device is already unreachable when the daemon (re)starts —
	// exactly the case a restart during a radio outage produces.
	unreach.OnEvent(true)
	maint.Put(unreach)

	reg := central.NewRegistry()
	unit, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	unit.ModelRegistry.Put(dev)
	if err := reg.Register(unit); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	svc, stores, _ := newTestService(t, func(d *Deps) { d.Registry = reg })
	ctx := context.Background()

	// The diagnostic classes (tamper/battery/technical) are only
	// aggregated for a device the alarm domain has enrolled a sensor
	// on (index.go's classify: relevant = cls.Class.Hazard() ||
	// deviceRelevant) — otherwise `technical` would stand permanently
	// on for every unenrolled device on a large fleet. Enrolling a
	// different channel of the same device is enough: loadEnrollment
	// derives device-relevance from the device address alone.
	now := time.Now().UnixMilli()
	if err := stores.Sensors.Upsert(ctx, sqlitestore.AlarmSensorRow{
		ID: "s1", ZoneID: "z1", CentralName: centralName, InterfaceID: "HmIP-RF",
		ChannelAddress: deviceAddr + ":1", Parameter: "STATE", SensorType: hmenum.AlarmSensorTypeDoor,
		Name: "Front Door", ConfigJSON: "{}", CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		t.Fatalf("seed alarm sensor enrollment: %v", err)
	}

	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	rows, err := stores.Faults.ListOpen(ctx)
	if err != nil {
		t.Fatalf("ListOpen: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListOpen after Start on an already-unreachable device = %d row(s), want 1 — a fault that stands at boot never reached the ledger, so it has no `since`, produces no report, and can never close once the device recovers", len(rows))
	}
	if rows[0].Reason != string(hmenum.SecurityFaultReasonUnreachable) {
		t.Errorf("ledger row reason = %q, want %q", rows[0].Reason, hmenum.SecurityFaultReasonUnreachable)
	}
	if rows[0].DeviceAddress != deviceAddr {
		t.Errorf("ledger row device_address = %q, want %q", rows[0].DeviceAddress, deviceAddr)
	}

	// The class view is also active — that half worked even before the
	// fix. Confirming it here documents the asymmetry the fix closed:
	// active-in-memory but absent-from-the-ledger.
	snap := svc.Snapshot()
	st, ok := snap.Classes[hmenum.SecurityClassTechnical]
	if !ok || !st.Active {
		t.Errorf("technical class state = %+v, want Active=true", st)
	}
}

// --- M2: retention ---

// TestSecurityFaultRetentionPurgesOnlyFaultsPastTheWindow pins the
// arithmetic [Service.startRetention]'s daily tick depends on: a fault
// cleared before the retention cutoff is dropped, one cleared inside the
// window survives.
//
// The daily tick itself is a [time.AfterFunc] on the real wall clock —
// waiting 24 hours in a unit test is not practical, so this drives the
// same store call ([sqlitestore.SecurityFaultStore.PurgeClearedBefore])
// with the same cutoff formula (RetentionDays days back from now) the
// production tick uses. The wiring that actually arms and disarms the
// tick — Start honouring Settings.RetentionDays, Stop not leaking the
// timer — is pinned separately by
// TestServiceStopClearsRetentionTimerWhenRetentionEnabled below; before
// this fix neither Settings.RetentionDays nor the retention timer
// existed at all, so the ledger grew forever on every install.
func TestSecurityFaultRetentionPurgesOnlyFaultsPastTheWindow(t *testing.T) {
	t.Parallel()
	svc, stores, clk := newTestService(t, func(d *Deps) { d.Settings.RetentionDays = 1 })
	ctx := context.Background()

	seedClearedFault := func(ref, deviceAddr string, sinceAgo, clearedAgo time.Duration) {
		t.Helper()
		row := sqlitestore.SecurityFault{
			ID: ref + "|unreachable|seed", Ref: ref, Class: string(hmenum.SecurityClassTechnical),
			Reason: string(hmenum.SecurityFaultReasonUnreachable), Severity: string(hmenum.SecuritySeverityInfo),
			CentralName: "c1", InterfaceID: "HmIP-RF", DeviceAddress: deviceAddr,
			ChannelAddress: deviceAddr + ":1", Parameter: "UNREACH", Name: deviceAddr,
			SinceMS: clk.Now().Add(-sinceAgo).UnixMilli(),
		}
		if _, _, err := stores.Faults.Raise(ctx, row); err != nil {
			t.Fatalf("seed raise %s: %v", ref, err)
		}
		if _, err := stores.Faults.Clear(ctx, ref, row.Reason, clk.Now().Add(-clearedAgo).UnixMilli()); err != nil {
			t.Fatalf("seed clear %s: %v", ref, err)
		}
	}

	// Cleared three days ago: past a 1-day retention window.
	seedClearedFault("c1|HmIP-RF|OLD1:1|UNREACH", "OLD1", 5*24*time.Hour, 3*24*time.Hour)
	// Cleared thirty minutes ago: well inside the window.
	seedClearedFault("c1|HmIP-RF|NEW1:1|UNREACH", "NEW1", time.Hour, 30*time.Minute)

	// The exact formula startRetention uses, read off the service's own
	// configured setting rather than the literal passed above, so a
	// regression in how RetentionDays is interpreted (days vs hours,
	// off-by-one) would show up here too.
	maxAge := time.Duration(svc.settings.RetentionDays) * 24 * time.Hour
	cutoff := clk.Now().Add(-maxAge).UnixMilli()

	purged, err := stores.Faults.PurgeClearedBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("PurgeClearedBefore: %v", err)
	}
	if purged != 1 {
		t.Fatalf("PurgeClearedBefore(cutoff=%d) purged %d row(s), want exactly 1 (the fault cleared past the retention window) — an SD-card install either loses recent history or never shrinks the ledger", cutoff, purged)
	}

	// Prove the recent row is still there rather than merely "not this
	// one": purging everything cleared up to "now" must catch exactly
	// the survivor.
	purgedRest, err := stores.Faults.PurgeClearedBefore(ctx, clk.Now().Add(time.Second).UnixMilli())
	if err != nil {
		t.Fatalf("PurgeClearedBefore (sweep remainder): %v", err)
	}
	if purgedRest != 1 {
		t.Fatalf("expected the fault cleared inside the retention window to still exist after the first purge; got %d remaining row(s) instead of 1", purgedRest)
	}
}

// TestServiceStopClearsRetentionTimerWhenRetentionEnabled pins the
// wiring gap itself: before this fix, Service carried no retention
// timer field and Start never armed one, so Settings.RetentionDays had
// no effect no matter what an operator configured — the fault ledger
// grew without bound on every SD-card deployment.
//
// Start must arm the timer when RetentionDays > 0, and Stop must clear
// the reference (not merely call Stop on the old *time.Timer and leave
// a dangling pointer) so a second Stop — which every graceful-shutdown
// path can trigger — does not panic and does not re-arm anything.
func TestServiceStopClearsRetentionTimerWhenRetentionEnabled(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService(t, func(d *Deps) { d.Settings.RetentionDays = 1 })
	ctx := context.Background()

	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	svc.mu.Lock()
	armed := svc.retention != nil
	svc.mu.Unlock()
	if !armed {
		t.Fatal("Start with RetentionDays=1 must arm the retention timer; Settings.RetentionDays has no effect otherwise")
	}

	if err := svc.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	svc.mu.Lock()
	leaked := svc.retention
	svc.mu.Unlock()
	if leaked != nil {
		t.Fatal("Stop must clear the retention timer reference; leaving it set is a leaked *time.Timer reference across restarts")
	}

	// Idempotent: a second Stop (every daemon shutdown path can call
	// Stop more than once) must not panic on the now-nil timer.
	if err := svc.Stop(ctx); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

// --- M5: acknowledge announces the real OpenCount ---

// TestAcknowledgeFaultAnnouncesRealOpenCount pins the acknowledge path:
// before this fix, the published SecurityFaultChangedEvent carried
// OpenCount at its zero value regardless of how many faults actually
// stood, so a consumer reading OpenCount off an acknowledgement — the
// one transition proving a fault stands — was told "no fault stands".
func TestAcknowledgeFaultAnnouncesRealOpenCount(t *testing.T) {
	t.Parallel()
	svc, stores, clk := newTestService(t)
	ctx := context.Background()

	seed := func(ref, deviceAddr string) {
		t.Helper()
		row := sqlitestore.SecurityFault{
			ID: ref + "|unreachable|seed", Ref: ref, Class: string(hmenum.SecurityClassTechnical),
			Reason: string(hmenum.SecurityFaultReasonUnreachable), Severity: string(hmenum.SecuritySeverityInfo),
			CentralName: "c1", InterfaceID: "HmIP-RF", DeviceAddress: deviceAddr,
			ChannelAddress: deviceAddr + ":1", Parameter: "UNREACH", Name: deviceAddr,
			SinceMS: clk.Now().UnixMilli(),
		}
		if _, _, err := stores.Faults.Raise(ctx, row); err != nil {
			t.Fatalf("seed raise %s: %v", ref, err)
		}
	}
	seed("c1|HmIP-RF|DEV1:1|UNREACH", "DEV1")
	seed("c1|HmIP-RF|DEV2:1|UNREACH", "DEV2")

	// Start loads both standing faults from the ledger into the
	// in-memory aggregate via loadFaults — the same path a restarted
	// daemon takes.
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	faults := svc.Faults()
	if len(faults) != 2 {
		t.Fatalf("Faults() after Start = %d, want 2 (both seeded rows loaded from the ledger)", len(faults))
	}
	ackID := faults[0].ID

	got := collectFaultEvents(t, svc)
	ok, err := svc.AcknowledgeFault(ctx, ackID, "tester")
	if err != nil {
		t.Fatalf("AcknowledgeFault: %v", err)
	}
	if !ok {
		t.Fatal("AcknowledgeFault reported not-found for a fault that Faults() just listed")
	}

	if len(*got) != 1 {
		t.Fatalf("AcknowledgeFault published %d SecurityFaultChangedEvent(s), want exactly 1", len(*got))
	}
	ev := (*got)[0]
	if !ev.Open {
		t.Errorf("acknowledged fault event Open = false, want true — the fault is still standing, only seen")
	}
	if !ev.Acknowledged {
		t.Errorf("acknowledged fault event Acknowledged = false, want true")
	}
	if ev.OpenCount != 2 {
		t.Errorf("acknowledged fault event OpenCount = %d, want 2 — a consumer reading this field on the one transition that proves a fault stands was told 'no fault stands'", ev.OpenCount)
	}
}
