// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

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
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
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

	svc, stores := newTestService(t, func(d *Deps) { d.Registry = reg })
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

// TestRebuildIndexClearsFaultOfADeviceRemovedFromTheModel pins that a
// fault can still be closed once the device that raised it is gone.
//
// Faults.Clear/applyFault both need an *indexedSource, and the wire
// path only dispatches for a source still present in s.agg.sources, so
// removing the faulty device — the obvious operator response to a
// standing fault — used to be a dead end: RebuildIndex's own reconcile
// pass walked only the fresh index, which by construction no longer
// carries the removed source either, so nothing anywhere ever revisited
// it again. The fault, and the severity it raised, stood forever.
func TestRebuildIndexClearsFaultOfADeviceRemovedFromTheModel(t *testing.T) {
	t.Parallel()
	const (
		centralName = "c1"
		deviceAddr  = "DEV0002"
	)

	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF", Address: deviceAddr, Model: "HmIP-SWDO", Name: "Back Door",
	})
	maint := dev.AddChannel(deviceAddr+":0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	unreach := generic.NewBinarySensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID: "HmIP-RF", ChannelAddress: maint.Address,
			ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterUnreach),
		},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeBool, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	unreach.OnEvent(true)
	maint.Put(unreach)

	reg := central.NewRegistry()
	unit, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	unit.ModelRegistry.Put(dev)
	// Southbound-ready gates the orphan-clear: without it, a source
	// missing from a rebuild reads as "not loaded yet", not "removed",
	// and the fault this test expects to close would rightly stay open.
	unit.MarkSouthboundReady()
	if err := reg.Register(unit); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	svc, stores := newTestService(t, func(d *Deps) { d.Registry = reg })
	ctx := context.Background()

	now := time.Now().UnixMilli()
	if err := stores.Sensors.Upsert(ctx, sqlitestore.AlarmSensorRow{
		ID: "s1", ZoneID: "z1", CentralName: centralName, InterfaceID: "HmIP-RF",
		ChannelAddress: deviceAddr + ":1", Parameter: "STATE", SensorType: hmenum.AlarmSensorTypeDoor,
		Name: "Back Door", ConfigJSON: "{}", CreatedAtMS: now, UpdatedAtMS: now,
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
		t.Fatalf("ListOpen after Start on an unreachable device = %d row(s), want 1; the rest of "+
			"this test would pass vacuously", len(rows))
	}

	// The operator's response to a permanently unreachable device:
	// remove it from the CCU. The next model change (a device deleted
	// event, in production) drives a rebuild.
	unit.ModelRegistry.Remove(deviceAddr)
	if err := svc.RebuildIndex(ctx); err != nil {
		t.Fatalf("RebuildIndex after device removal: %v", err)
	}

	rows, err = stores.Faults.ListOpen(ctx)
	if err != nil {
		t.Fatalf("ListOpen: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("ListOpen after removing the faulty device = %d row(s), want 0 — a fault whose "+
			"source is gone can never resolve on its own, the row is gone", len(rows))
	}

	snap := svc.Snapshot()
	if st, ok := snap.Classes[hmenum.SecurityClassTechnical]; ok && st.Active {
		t.Errorf("technical class state = %+v, want Active=false after the only faulting device was removed", st)
	}
	if snap.Severity != hmenum.SecuritySeverityOK {
		t.Errorf("severity = %q, want %q after the only fault cleared", snap.Severity, hmenum.SecuritySeverityOK)
	}
}

// TestRebuildIndexNeverClearsAFaultOfACentralNotYetSouthboundReady pins
// the safety rail on the fix above: RebuildIndex can run before a
// registered central's model has finished loading (attachUnit's own
// doc: the model "is populated asynchronously ... long after this
// service starts"), and a source missing from that partial pass is not
// evidence it is gone. Judging removal against an incomplete model
// would clear every standing fault on a plain restart — a false
// all-clear for smoke, water, door and duress monitoring at exactly
// the moment nothing has been checked yet.
func TestRebuildIndexNeverClearsAFaultOfACentralNotYetSouthboundReady(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	unit, err := central.New(central.Config{Name: "c1"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	// Registered, but never marked southbound-ready: the model has not
	// finished loading, exactly the window between Start and the first
	// CentralSouthboundReadyEvent.
	if err := reg.Register(unit); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	svc, stores := newTestService(t, func(d *Deps) { d.Registry = reg })
	ctx := context.Background()

	row := sqlitestore.SecurityFault{
		ID: "c1|HmIP-RF|DEV1:1|UNREACH|seed", Ref: "c1|HmIP-RF|DEV1:1|UNREACH",
		Class: string(hmenum.SecurityClassTechnical), Reason: string(hmenum.SecurityFaultReasonUnreachable),
		Severity: string(hmenum.SecuritySeverityInfo), CentralName: "c1", InterfaceID: "HmIP-RF",
		DeviceAddress: "DEV1", ChannelAddress: "DEV1:1", Parameter: "UNREACH", Name: "DEV1",
		SinceMS: time.Now().UnixMilli(),
	}
	if _, _, err := stores.Faults.Raise(ctx, row); err != nil {
		t.Fatalf("seed raise: %v", err)
	}

	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	rows, err := stores.Faults.ListOpen(ctx)
	if err != nil {
		t.Fatalf("ListOpen: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListOpen after Start against a not-yet-ready central = %d row(s), want 1 — a "+
			"fault must never be cleared before its owning central's model has finished loading",
			len(rows))
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
	svc, stores := newTestService(t, func(d *Deps) { d.Settings.RetentionDays = 1 })
	ctx := context.Background()

	seedClearedFault := func(ref, deviceAddr string, sinceAgo, clearedAgo time.Duration) {
		t.Helper()
		row := sqlitestore.SecurityFault{
			ID: ref + "|unreachable|seed", Ref: ref, Class: string(hmenum.SecurityClassTechnical),
			Reason: string(hmenum.SecurityFaultReasonUnreachable), Severity: string(hmenum.SecuritySeverityInfo),
			CentralName: "c1", InterfaceID: "HmIP-RF", DeviceAddress: deviceAddr,
			ChannelAddress: deviceAddr + ":1", Parameter: "UNREACH", Name: deviceAddr,
			SinceMS: time.Now().Add(-sinceAgo).UnixMilli(),
		}
		if _, _, err := stores.Faults.Raise(ctx, row); err != nil {
			t.Fatalf("seed raise %s: %v", ref, err)
		}
		if _, err := stores.Faults.Clear(ctx, ref, row.Reason, time.Now().Add(-clearedAgo).UnixMilli()); err != nil {
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
	cutoff := time.Now().Add(-maxAge).UnixMilli()

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
	purgedRest, err := stores.Faults.PurgeClearedBefore(ctx, time.Now().Add(time.Second).UnixMilli())
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
	svc, _ := newTestService(t, func(d *Deps) { d.Settings.RetentionDays = 1 })
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
	svc, stores := newTestService(t)
	ctx := context.Background()

	seed := func(ref, deviceAddr string) {
		t.Helper()
		row := sqlitestore.SecurityFault{
			ID: ref + "|unreachable|seed", Ref: ref, Class: string(hmenum.SecurityClassTechnical),
			Reason: string(hmenum.SecurityFaultReasonUnreachable), Severity: string(hmenum.SecuritySeverityInfo),
			CentralName: "c1", InterfaceID: "HmIP-RF", DeviceAddress: deviceAddr,
			ChannelAddress: deviceAddr + ":1", Parameter: "UNREACH", Name: deviceAddr,
			SinceMS: time.Now().UnixMilli(),
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

// --- restart keeps the fault ledger ---

const (
	restartCentral = "c1"
	restartDevice  = "DEV0009"
	restartIface   = "HmIP-RF"
)

// newRestartTestUnit builds a southbound-ready central holding one device
// whose UNREACH data point reports unreachable, as a unit adopted again
// after a restart does once its model has loaded.
func newRestartTestUnit(t *testing.T, unreachable bool) *central.Unit {
	t.Helper()
	dev := device.New(device.Config{
		InterfaceID: restartIface, Address: restartDevice, Model: "HmIP-SWDO", Name: "Side Door",
	})
	maint := dev.AddChannel(restartDevice+":0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	unreach := generic.NewBinarySensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID: restartIface, ChannelAddress: maint.Address,
			ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterUnreach),
		},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeBool, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	unreach.OnEvent(unreachable)
	maint.Put(unreach)

	unit, err := central.New(central.Config{Name: restartCentral})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	unit.ModelRegistry.Put(dev)
	unit.MarkSouthboundReady()
	return unit
}

// restartLedgerFixture is a started service over one central with one
// unreachable device and an acknowledged fault for it, opened an hour ago.
type restartLedgerFixture struct {
	svc    *Service
	stores *Stores
	reg    *central.Registry
	seed   sqlitestore.SecurityFault
}

func newRestartLedgerFixture(t *testing.T) *restartLedgerFixture {
	t.Helper()
	ctx := context.Background()
	reg := central.NewRegistry()
	if err := reg.Register(newRestartTestUnit(t, true)); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	svc, stores := newTestService(t, func(d *Deps) { d.Registry = reg })

	now := time.Now().UnixMilli()
	// Diagnostic classes are aggregated only for a device the alarm domain
	// has enrolled a sensor on; any channel of the device enrolls it.
	if err := stores.Sensors.Upsert(ctx, sqlitestore.AlarmSensorRow{
		ID: "s1", ZoneID: "z1", CentralName: restartCentral, InterfaceID: restartIface,
		ChannelAddress: restartDevice + ":1", Parameter: "STATE", SensorType: hmenum.AlarmSensorTypeDoor,
		Name: "Side Door", ConfigJSON: "{}", CreatedAtMS: now, UpdatedAtMS: now,
	}); err != nil {
		t.Fatalf("seed alarm sensor enrollment: %v", err)
	}
	// A fault that has stood for an hour, with an id no raise in this test
	// can produce, so a re-raise is told apart from the kept row.
	ref := hmevent.NewSecuritySourceRef(restartCentral, restartIface, restartDevice+":0", string(hmenum.ParameterUnreach))
	seed := sqlitestore.SecurityFault{
		ID: ref.Ref + "|seed", Ref: ref.Ref,
		Class: string(hmenum.SecurityClassTechnical), Reason: string(hmenum.SecurityFaultReasonUnreachable),
		Severity:    string(hmenum.SeverityForClass(hmenum.SecurityClassTechnical)),
		CentralName: restartCentral, InterfaceID: restartIface, DeviceAddress: restartDevice,
		ChannelAddress: restartDevice + ":0", Parameter: string(hmenum.ParameterUnreach), Name: "Side Door",
		SinceMS: now - time.Hour.Milliseconds(),
	}
	if _, _, err := stores.Faults.Raise(ctx, seed); err != nil {
		t.Fatalf("seed raise: %v", err)
	}
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(ctx) })

	rows, err := stores.Faults.ListOpen(ctx)
	if err != nil {
		t.Fatalf("ListOpen: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != seed.ID {
		t.Fatalf("precondition: open rows after Start = %+v, want only the seeded fault — the seed does not "+
			"match the source the index derives, and the rest of the test would pass vacuously", rows)
	}
	if ok, err := svc.AcknowledgeFault(ctx, seed.ID, "operator"); err != nil || !ok {
		t.Fatalf("precondition: AcknowledgeFault = %v, %v", ok, err)
	}
	return &restartLedgerFixture{svc: svc, stores: stores, reg: reg, seed: seed}
}

// restart replaces the registered unit by a fresh one under the same name,
// around the restart detach, the way the orchestrator's restart does.
func (f *restartLedgerFixture) restart(t *testing.T, unreachable bool) {
	t.Helper()
	f.svc.DetachCentralForRestart(restartCentral)
	f.reg.Unregister(restartCentral)
	if err := f.reg.Register(newRestartTestUnit(t, unreachable)); err != nil {
		t.Fatalf("reg.Register(restarted): %v", err)
	}
	f.svc.AttachCentral(restartCentral)
}

// TestRestartDetachKeepsStandingFaultIDSinceAndStaysSilent pins that a
// central restarted under the same name keeps its standing faults.
//
// The restart used to run the removal detach, which closed every open fault
// of the central. The re-adopted central's reconcile then raised the
// still-active condition again: a new id, `since` reset to the restart, the
// operator's acknowledgement gone, and a "raised" report to every sink for a
// device whose state had not changed.
func TestRestartDetachKeepsStandingFaultIDSinceAndStaysSilent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newRestartLedgerFixture(t)

	faultEvents := collectFaultEvents(t, f.svc)
	var raised []hmevent.SecurityNotificationEvent
	unsub := f.svc.Bus().Subscribe(func(e hmevent.SecurityNotificationEvent) {
		if e.Verb == hmenum.SecurityVerbRaised {
			raised = append(raised, e)
		}
	})
	t.Cleanup(unsub)

	f.restart(t, true)

	for _, e := range *faultEvents {
		if e.Open {
			t.Errorf("the restart published an opening fault transition %+v; the fault never closed", e)
		}
	}
	if len(raised) != 0 {
		t.Errorf("the restart sent %d \"raised\" report(s); a fault that stood throughout must stay silent", len(raised))
	}

	rows, err := f.stores.Faults.ListOpen(ctx)
	if err != nil {
		t.Fatalf("ListOpen: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != f.seed.ID || rows[0].SinceMS != f.seed.SinceMS {
		t.Fatalf("open rows after the restart = %+v, want only the original fault %q since %d — the restart "+
			"closed it and raised it again", rows, f.seed.ID, f.seed.SinceMS)
	}
	faults := f.svc.Faults()
	if len(faults) != 1 || faults[0].ID != f.seed.ID {
		t.Fatalf("Faults() after the restart = %+v, want the original fault — the ledger row stands but "+
			"no surface shows it", faults)
	}
	if faults[0].SinceMS != f.seed.SinceMS || faults[0].AcknowledgedAtMS == 0 || faults[0].AcknowledgedBy != "operator" {
		t.Errorf("fault after the restart = since %d, acknowledged %d by %q; want since %d, acknowledged by operator",
			faults[0].SinceMS, faults[0].AcknowledgedAtMS, faults[0].AcknowledgedBy, f.seed.SinceMS)
	}
}

// TestRestartDetachClosesFaultThatEndedMeanwhile pins the other half of the
// kept ledger: a condition that ended while the central was down is closed
// by the re-adopted central's reconcile, not left standing because the
// restart detach kept its row.
func TestRestartDetachClosesFaultThatEndedMeanwhile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newRestartLedgerFixture(t)

	f.restart(t, false)

	rows, err := f.stores.Faults.ListOpen(ctx)
	if err != nil {
		t.Fatalf("ListOpen: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("open rows after a restart on a reachable device = %+v, want none — the kept fault "+
			"stands for a condition that has ended", rows)
	}
	if faults := f.svc.Faults(); len(faults) != 0 {
		t.Errorf("Faults() after the restart = %+v, want none", faults)
	}
}

// TestDetachCentralStillClearsEveryOpenFault pins that the removal detach
// keeps closing the faults of a central that leaves for good: the restart
// flavour must not have taken that half with it.
func TestDetachCentralStillClearsEveryOpenFault(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newRestartLedgerFixture(t)

	f.svc.DetachCentral(restartCentral)

	rows, err := f.stores.Faults.ListOpen(ctx)
	if err != nil {
		t.Fatalf("ListOpen: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("open rows after removing the central = %+v, want none — a removed central's faults "+
			"would stand for a CCU that is no longer configured", rows)
	}
	if faults := f.svc.Faults(); len(faults) != 0 {
		t.Errorf("Faults() after removing the central = %+v, want none", faults)
	}
}
