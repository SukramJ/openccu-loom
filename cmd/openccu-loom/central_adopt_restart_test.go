// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/channelflags"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/measurements"
	loomendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

const (
	restartDevice  = "AAAA0001"
	restartChannel = restartDevice + ":1"
)

// restartFixture is a live orchestrator behind the real live admin service,
// with every store that holds per-central operator state wired the way the
// daemon wires it — including the per-device evictors, which delete that
// state on a device removal that is not a teardown.
type restartFixture struct {
	reg        *central.Registry
	orch       *centralOrchestrator
	admin      *liveCentralAdmin
	flags      *sqlitestore.ChannelFlagsStore
	overlay    *channelflags.Overlay
	recStore   *sqlitestore.RecordingOverrideStore
	recording  *measurements.RecordingOverrides
	visibility *sqlitestore.VisibilityUnIgnoreStore
	exposures  *loomendpoint.Store
	values     *sqlitestore.ValuesCacheStore
	exposure   loomendpoint.SourceKey
	retired    []string
}

func newRestartFixture(ctx context.Context, t *testing.T, dbName string) *restartFixture {
	t.Helper()
	db := openMigratedTestDB(t, dbName)
	f := &restartFixture{
		reg:        central.NewRegistry(),
		flags:      sqlitestore.NewChannelFlagsStore(db),
		overlay:    channelflags.New(),
		recStore:   sqlitestore.NewRecordingOverrideStore(db),
		visibility: sqlitestore.NewVisibilityUnIgnoreStore(db),
		exposures:  loomendpoint.New(db),
		values:     sqlitestore.NewValuesCacheStore(db),
	}
	f.recording = measurements.NewRecordingOverrides(f.recStore, nil, nil)

	flagsEvictor := adapter.WireChannelFlagsEviction(f.reg, f.flags, f.overlay, discardTestLogger())
	t.Cleanup(flagsEvictor.Stop)
	recEvictor := adapter.WireRecordingOverrideEviction(f.reg, f.recording, discardTestLogger())
	t.Cleanup(recEvictor.Stop)
	valuesEvictor := adapter.WireValuesCacheEviction(f.reg, f.values, discardTestLogger())
	t.Cleanup(valuesEvictor.Stop)

	f.orch = buildLiveTestOrchestrator(ctx, t, f.reg, &config.Config{})
	f.orch.channelFlagsStore = f.flags
	f.orch.channelFlagsOverlay = f.overlay
	f.orch.recordingStore = f.recStore
	f.orch.recordingOverrides = f.recording
	f.orch.valuesCacheStore = f.values
	f.orch.sbDeps.visibilityUnIgnoreStore = f.visibility
	f.orch.setMatterExposureStore(f.exposures)
	f.orch.setMatterPartitionRetirer(func(_ context.Context, name string) error {
		f.retired = append(f.retired, name)
		return nil
	})
	admin, ok := newLiveCentralAdmin(newFakeCentralAdmin(), f.orch, discardTestLogger()).(*liveCentralAdmin)
	if !ok {
		t.Fatal("newLiveCentralAdmin did not return the live admin service")
	}
	f.admin = admin
	return f
}

// adoptAndSeed brings row up live, puts a device into its model and seeds the
// operator state stored under its name: a channel flag, an un-ignore pattern,
// a recording override, a Matter exposure, and a VALUES cache row per
// configured interface.
func (f *restartFixture) adoptAndSeed(ctx context.Context, t *testing.T, row sqlitestore.CentralRow) {
	t.Helper()
	if err := f.admin.Put(ctx, row); err != nil {
		t.Fatalf("Put(initial): %v", err)
	}
	unit, ok := f.reg.Get(row.Name)
	if !ok {
		t.Fatal("precondition: central not live after the initial Put")
	}
	d := device.New(device.Config{
		InterfaceID: row.Name + "-HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
		Address: restartDevice, Model: "HmIP-PS", Name: "Switch",
	})
	d.AddChannel(restartChannel, 1, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	unit.ModelRegistry.Put(d)

	if err := f.flags.Set(ctx, row.Name, restartChannel, true, false, "test"); err != nil {
		t.Fatalf("seed channel flag: %v", err)
	}
	f.overlay.Set(row.Name, restartChannel, channelflags.Flags{Hidden: true})
	if err := f.visibility.Replace(ctx, row.Name, []string{"ACTIVE"}, "test"); err != nil {
		t.Fatalf("seed un-ignore pattern: %v", err)
	}
	// The override is keyed by the device's interface id, the value the
	// removal event carries and the recording evictor matches on.
	if err := f.recording.Set(ctx, row.Name, d.InterfaceID, restartChannel, "STATE", false, "test"); err != nil {
		t.Fatalf("seed recording override: %v", err)
	}
	f.exposure = loomendpoint.SourceKey{CentralName: row.Name, DeviceAddress: restartDevice, ChannelNo: 1, DPKind: loomendpoint.DPKindCustom, DPKey: "switch"}
	if err := f.exposures.UpsertExposure(ctx, loomendpoint.ExposureRecord{Key: f.exposure, Enabled: true, Actor: "test"}); err != nil {
		t.Fatalf("seed Matter exposure: %v", err)
	}
	now := time.Now()
	for _, ifc := range row.Interfaces {
		if err := f.values.SaveValue(ctx, row.Name, row.Name+"-"+ifc.Name, restartChannel, "STATE", true, now, now); err != nil {
			t.Fatalf("seed values cache %s: %v", ifc.Name, err)
		}
	}
}

// valuesRows counts the VALUES cache rows of one interface of the named central.
func (f *restartFixture) valuesRows(ctx context.Context, t *testing.T, name, iface string) int {
	t.Helper()
	rows, err := f.values.LoadChannel(ctx, name, name+"-"+iface, restartChannel)
	if err != nil {
		t.Fatalf("LoadChannel(%s): %v", iface, err)
	}
	return len(rows)
}

// TestLiveCentralAdminPutRestartKeepsTheCentralsStoredState pins that an edit
// changing how a running central is reached is a restart, not a removal. The
// central comes back live on the new configuration, and everything the
// operator stored under its name — hidden/locked channels, visibility
// un-ignore patterns, recording overrides, Matter exposures and the Matter
// partition — is still there; only the cache rows of the interface the edit
// dropped are gone. The move of a central to openccu-lite is such an edit.
func TestLiveCentralAdminPutRestartKeepsTheCentralsStoredState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	f := newRestartFixture(ctx, t, "central_restart_keeps_state.db")

	const name = "restart-keeps"
	row := sqlitestore.CentralRow{
		Name: name, Host: "127.0.0.1", Enabled: true,
		Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF", Port: 1}, {Name: "CUxD", Port: 1}},
	}
	f.adoptAndSeed(ctx, t, row)
	before, _ := f.reg.Get(name)

	edited := row
	edited.Host = "127.0.0.2"
	edited.SystemType = "openccu-lite"
	edited.Interfaces = []config.InterfaceSpec{{Name: "HmIP-RF", Port: 1}}
	if err := f.admin.Put(ctx, edited); err != nil {
		t.Fatalf("Put(edited): %v", err)
	}
	t.Cleanup(func() { _ = f.orch.removeCentral(context.Background(), name) })

	after, ok := f.reg.Get(name)
	if !ok || after == before {
		t.Fatalf("after the edit live=%v, same unit=%v; want a new unit on the new configuration", ok, after == before)
	}
	if cc, ok := f.orch.centralConfigFor(name); !ok || cc.Host != "127.0.0.2" || len(cc.Interfaces) != 1 {
		t.Errorf("live config after the edit = %+v, want host 127.0.0.2 with one interface", cc)
	}

	// The per-device evictors run on the bus; give a wrongly flavoured
	// removal event the window it would need to delete anything.
	time.Sleep(250 * time.Millisecond)

	rows, err := f.flags.List(ctx)
	if err != nil {
		t.Fatalf("channel flags List: %v", err)
	}
	if len(rows) != 1 || !f.overlay.Get(name, restartChannel).Hidden {
		t.Errorf("channel flags after the restart: %d row(s), overlay hidden=%v; want the hidden channel kept",
			len(rows), f.overlay.Get(name, restartChannel).Hidden)
	}
	if patterns, err := f.visibility.Patterns(ctx, name); err != nil || len(patterns) != 1 {
		t.Errorf("un-ignore patterns after the restart = %v (err %v), want the pattern kept", patterns, err)
	}
	if f.recording.Decide(name, name+"-HmIP-RF", restartChannel, "STATE", true) {
		t.Error("the recording override no longer applies after the restart")
	}
	if overrides, err := f.recStore.List(ctx); err != nil || len(overrides) != 1 {
		t.Errorf("recording override rows after the restart = %+v (err %v), want the row kept", overrides, err)
	}
	if _, err := f.exposures.GetExposure(ctx, f.exposure); err != nil {
		t.Errorf("Matter exposure after the restart: %v, want it kept", err)
	}
	if len(f.retired) != 0 {
		t.Errorf("the restart retired the Matter partition of %v; its endpoint numbers must stay", f.retired)
	}
	if n := f.valuesRows(ctx, t, name, "HmIP-RF"); n != 1 {
		t.Errorf("values cache rows of the kept interface = %d, want 1", n)
	}
	if n := f.valuesRows(ctx, t, name, "CUxD"); n != 0 {
		t.Errorf("values cache rows of the dropped interface = %d, want 0", n)
	}
}

// TestLiveCentralAdminPutRestartRemovesTheDevicesOfADroppedInterface pins
// the one thing a restart does delete: a device on an interface the new
// configuration no longer has cannot come back, so it is removed as by a
// removal — its hidden channel, recording override and Matter exposure go —
// while the devices of the kept interface keep theirs. The move of a central
// to openccu-lite drops CUxD this way.
func TestLiveCentralAdminPutRestartRemovesTheDevicesOfADroppedInterface(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	f := newRestartFixture(ctx, t, "central_restart_dropped_iface.db")

	const name = "restart-drops"
	row := sqlitestore.CentralRow{
		Name: name, Host: "127.0.0.1", Enabled: true,
		Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF", Port: 1}, {Name: "CUxD", Port: 1}},
	}
	f.adoptAndSeed(ctx, t, row)

	const cuxdDevice = "CUX2801001"
	const cuxdChannel = cuxdDevice + ":1"
	unit, _ := f.reg.Get(name)
	d := device.New(device.Config{
		InterfaceID: name + "-CUxD", Interface: hmenum.InterfaceCUxD,
		Address: cuxdDevice, Model: "HM-LC-Sw1-Pl", Name: "CUxD switch",
	})
	d.AddChannel(cuxdChannel, 1, "SWITCH", hmenum.ParamsetKeyValues)
	unit.ModelRegistry.Put(d)
	if err := f.flags.Set(ctx, name, cuxdChannel, true, false, "test"); err != nil {
		t.Fatalf("seed channel flag: %v", err)
	}
	f.overlay.Set(name, cuxdChannel, channelflags.Flags{Hidden: true})
	if err := f.recording.Set(ctx, name, d.InterfaceID, cuxdChannel, "STATE", false, "test"); err != nil {
		t.Fatalf("seed recording override: %v", err)
	}
	cuxdExposure := loomendpoint.SourceKey{CentralName: name, DeviceAddress: cuxdDevice, ChannelNo: 1, DPKind: loomendpoint.DPKindCustom, DPKey: "switch"}
	if err := f.exposures.UpsertExposure(ctx, loomendpoint.ExposureRecord{Key: cuxdExposure, Enabled: true, Actor: "test"}); err != nil {
		t.Fatalf("seed Matter exposure: %v", err)
	}

	edited := row
	edited.SystemType = "openccu-lite"
	edited.Interfaces = []config.InterfaceSpec{{Name: "HmIP-RF", Port: 1}}
	if err := f.admin.Put(ctx, edited); err != nil {
		t.Fatalf("Put(edited): %v", err)
	}
	t.Cleanup(func() { _ = f.orch.removeCentral(context.Background(), name) })

	// The per-device evictors run on the bus.
	deadline := time.Now().Add(5 * time.Second)
	for f.overlay.Get(name, cuxdChannel).Hidden && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(250 * time.Millisecond)

	if f.overlay.Get(name, cuxdChannel).Hidden {
		t.Error("the dropped interface's device keeps its hidden channel in the overlay")
	}
	if !f.overlay.Get(name, restartChannel).Hidden {
		t.Error("the kept interface's device lost its hidden channel")
	}
	if rows, err := f.flags.List(ctx); err != nil || len(rows) != 1 {
		t.Errorf("channel flag rows after the restart = %+v (err %v), want the kept device's row only", rows, err)
	}
	if overrides, err := f.recStore.List(ctx); err != nil || len(overrides) != 1 {
		t.Errorf("recording override rows after the restart = %+v (err %v), want the kept device's row only", overrides, err)
	}
	if _, err := f.exposures.GetExposure(ctx, cuxdExposure); err == nil {
		t.Error("the dropped interface's device keeps its Matter exposure")
	}
	if _, err := f.exposures.GetExposure(ctx, f.exposure); err != nil {
		t.Errorf("the kept interface's device lost its Matter exposure: %v", err)
	}
}

// TestLiveCentralAdminPutDisableStillPurgesTheCentralsStoredState pins the
// other half: a disabled save of a running central is a removal, and a
// removal deletes everything stored under the central's name.
func TestLiveCentralAdminPutDisableStillPurgesTheCentralsStoredState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	f := newRestartFixture(ctx, t, "central_disable_purges_state.db")

	const name = "disable-purges"
	row := sqlitestore.CentralRow{
		Name: name, Host: "127.0.0.1", Enabled: true,
		Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF", Port: 1}},
	}
	f.adoptAndSeed(ctx, t, row)

	row.Enabled = false
	if err := f.admin.Put(ctx, row); err != nil {
		t.Fatalf("Put(disabled): %v", err)
	}
	if _, ok := f.reg.Get(name); ok {
		t.Fatal("central still live after a disabled save")
	}

	if rows, err := f.flags.List(ctx); err != nil || len(rows) != 0 {
		t.Errorf("channel flag rows after the removal = %d (err %v), want 0", len(rows), err)
	}
	if f.overlay.Get(name, restartChannel).Set() {
		t.Error("the channel-flags overlay kept the removed central's entry")
	}
	if patterns, err := f.visibility.Patterns(ctx, name); err != nil || len(patterns) != 0 {
		t.Errorf("un-ignore patterns after the removal = %v (err %v), want none", patterns, err)
	}
	if overrides, err := f.recStore.List(ctx); err != nil || len(overrides) != 0 {
		t.Errorf("recording override rows after the removal = %+v (err %v), want none", overrides, err)
	}
	if _, err := f.exposures.GetExposure(ctx, f.exposure); !errors.Is(err, loomendpoint.ErrExposureNotFound) {
		t.Errorf("Matter exposure after the removal: err=%v, want ErrExposureNotFound", err)
	}
	if len(f.retired) != 1 || f.retired[0] != name {
		t.Errorf("retired partitions = %v, want [%s]", f.retired, name)
	}
	if n := f.valuesRows(ctx, t, name, "HmIP-RF"); n != 0 {
		t.Errorf("values cache rows after the removal = %d, want 0", n)
	}
}

// restartSecurityFault starts the real Security & Safety domain through the
// composition root's wiring helper, hooks it onto f's orchestrator and seeds
// one open, hour-old fault for the central name before the domain starts, as
// a fault that stood across the daemon's last start does. It returns the
// domain's fault store and the seeded row.
func restartSecurityFault(ctx context.Context, t *testing.T, f *restartFixture, dbName, name string) (
	*sqlitestore.SecurityFaultStore, func() []string, sqlitestore.SecurityFault,
) {
	t.Helper()
	db := openMigratedTestDB(t, dbName)
	faults := sqlitestore.NewSecurityFaultStore(db)
	ref := hmevent.SecurityRefKey(name, name+"-HmIP-RF", restartDevice+":0", string(hmenum.ParameterUnreach))
	seed := sqlitestore.SecurityFault{
		ID: ref + "|seed", Ref: ref,
		Class: string(hmenum.SecurityClassTechnical), Reason: string(hmenum.SecurityFaultReasonUnreachable),
		Severity:    string(hmenum.SeverityForClass(hmenum.SecurityClassTechnical)),
		CentralName: name, InterfaceID: name + "-HmIP-RF", DeviceAddress: restartDevice,
		ChannelAddress: restartDevice + ":0", Parameter: string(hmenum.ParameterUnreach), Name: "Switch",
		SinceMS: time.Now().Add(-time.Hour).UnixMilli(),
	}
	if _, _, err := faults.Raise(ctx, seed); err != nil {
		t.Fatalf("seed security fault: %v", err)
	}
	svc := wireSecurityService(config.Default(), f.reg, db, nil, nil, nil, discardTestLogger())
	if svc == nil {
		t.Fatal("wireSecurityService returned nil")
	}
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("security service Start: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })
	f.orch.setSecurityCentralHook(securityCentralHook(svc))
	shown := func() []string {
		shownFaults := svc.Faults()
		ids := make([]string, 0, len(shownFaults))
		for i := range shownFaults {
			ids = append(ids, shownFaults[i].ID)
		}
		return ids
	}
	return faults, shown, seed
}

// openSecurityFaults returns the ids of the open ledger rows of the central.
func openSecurityFaults(ctx context.Context, t *testing.T, faults *sqlitestore.SecurityFaultStore, name string) []string {
	t.Helper()
	rows, err := faults.ListOpen(ctx)
	if err != nil {
		t.Fatalf("security faults ListOpen: %v", err)
	}
	var ids []string
	for i := range rows {
		if rows[i].CentralName == name {
			ids = append(ids, rows[i].ID)
		}
	}
	return ids
}

// TestLiveCentralAdminPutRestartKeepsOpenSecurityFaults pins that restarting
// a running central — the move to openccu-lite is such an edit — keeps the
// open faults the security domain holds for it.
//
// The restart ran the domain's removal detach, which closed every open fault
// of the central; the re-adopted central's reconcile then raised each
// still-active condition as a new fault with a new id, a reset `since`, the
// acknowledgement lost and a second "raised" report to every sink.
func TestLiveCentralAdminPutRestartKeepsOpenSecurityFaults(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	f := newRestartFixture(ctx, t, "central_restart_keeps_faults.db")

	const name = "restart-keeps-faults"
	faults, shown, seed := restartSecurityFault(ctx, t, f, "central_restart_keeps_faults_security.db", name)
	row := sqlitestore.CentralRow{
		Name: name, Host: "127.0.0.1", Enabled: true,
		Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF", Port: 1}},
	}
	if err := f.admin.Put(ctx, row); err != nil {
		t.Fatalf("Put(initial): %v", err)
	}
	t.Cleanup(func() { _ = f.orch.removeCentral(context.Background(), name) })
	if ids := openSecurityFaults(ctx, t, faults, name); !slices.Equal(ids, []string{seed.ID}) {
		t.Fatalf("precondition: open faults after the adopt = %v, want [%s]", ids, seed.ID)
	}

	edited := row
	edited.Host = "127.0.0.2"
	edited.SystemType = "openccu-lite"
	if err := f.admin.Put(ctx, edited); err != nil {
		t.Fatalf("Put(edited): %v", err)
	}

	if ids := openSecurityFaults(ctx, t, faults, name); !slices.Equal(ids, []string{seed.ID}) {
		t.Errorf("open faults after the restart = %v, want [%s] — the restart closed the central's faults "+
			"like a removal", ids, seed.ID)
	}
	if ids := shown(); !slices.Equal(ids, []string{seed.ID}) {
		t.Errorf("faults the domain shows after the restart = %v, want [%s]", ids, seed.ID)
	}
}

// TestRestartCentralWhoseReadoptFailsClosesItsSecurityFaults pins the
// failure half of the kept ledger: when the adopt after a restart teardown
// fails, the central is live nowhere, so its faults are closed as for a
// removal. Kept, they would stand forever: a later delete of a central that
// is not live tolerates errCentralNotLive and never reaches the domain.
func TestRestartCentralWhoseReadoptFailsClosesItsSecurityFaults(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	f := newRestartFixture(ctx, t, "central_restart_failed_faults.db")

	const name = "restart-fails"
	faults, shown, seed := restartSecurityFault(ctx, t, f, "central_restart_failed_faults_security.db", name)
	row := sqlitestore.CentralRow{
		Name: name, Host: "127.0.0.1", Enabled: true,
		Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF", Port: 1}},
	}
	if err := f.admin.Put(ctx, row); err != nil {
		t.Fatalf("Put(initial): %v", err)
	}
	if ids := openSecurityFaults(ctx, t, faults, name); !slices.Equal(ids, []string{seed.ID}) {
		t.Fatalf("precondition: open faults after the adopt = %v, want [%s]", ids, seed.ID)
	}

	// The re-adopt fails at its registration: a different unit already holds
	// the name by the time the new one would enter the registry. Every domain
	// hook attaches after that point, so no adopt rollback reaches them.
	f.orch.setCentralSeed(func(*central.Unit, string) {
		squatter, err := central.New(central.Config{Name: name, Logger: discardTestLogger()})
		if err != nil {
			t.Errorf("central.New(squatter): %v", err)
			return
		}
		if err := f.reg.Register(squatter); err != nil {
			t.Errorf("register squatter: %v", err)
		}
	})
	t.Cleanup(func() { f.reg.Unregister(name) })

	edited := row
	edited.Host = "127.0.0.2"
	if err := f.admin.Put(ctx, edited); err == nil {
		t.Fatal("Put(edited) succeeded; the test needs the re-adopt to fail")
	}

	if ids := openSecurityFaults(ctx, t, faults, name); len(ids) != 0 {
		t.Errorf("open faults after a failed restart = %v, want none — the central is live nowhere and "+
			"nothing else will ever close them", ids)
	}
	if ids := shown(); len(ids) != 0 {
		t.Errorf("faults the domain shows after a failed restart = %v, want none", ids)
	}
}

// TestLiveCentralAdminPutRestartDropsTheWarningOfADroppedInterface pins that a
// restart dropping an interface takes that interface's incidents off the
// warning list the SPA and the box's Status page read, while the incidents
// stay in the journal. The move of a central to openccu-lite drops CUxD this
// way; before, its incidents kept an error warning up for a day.
func TestLiveCentralAdminPutRestartDropsTheWarningOfADroppedInterface(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	f := newRestartFixture(ctx, t, "central_restart_incident_warning.db")

	const name = "restart-incidents"
	row := sqlitestore.CentralRow{
		Name: name, Host: "127.0.0.1", Enabled: true,
		Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF", Port: 1}, {Name: "CUxD", Port: 1}},
	}
	f.adoptAndSeed(ctx, t, row)

	store := sqlitestore.NewIncidentStore(openMigratedTestDB(t, "central_restart_incident_journal.db"))
	// Seeded under the id the incident producers record with.
	kept := central.WireInterfaceID(name, hmenum.InterfaceHmIPRF)
	dropped := central.WireInterfaceID(name, hmenum.InterfaceCUxD)
	for _, iface := range []string{kept, dropped} {
		if _, err := store.Record(ctx, sqlitestore.Incident{
			CentralName: name, InterfaceID: iface,
			Type: hmenum.IncidentTypeCallbackTimeout, Severity: hmenum.IncidentSeverityError, Message: "test",
		}); err != nil {
			t.Fatalf("seed incident %s: %v", iface, err)
		}
	}
	reader := adapter.NewIncidentsStoreReader(store, f.reg, discardTestLogger())
	agg := newWarningsAggregator(nil, reader, nil, nil, nil, f.reg)
	warningIDs := func() map[string]bool {
		out := map[string]bool{}
		for _, w := range agg.Active() {
			out[w.ID] = true
		}
		return out
	}
	keptID, droppedID := "incident:"+name+"/"+kept, "incident:"+name+"/"+dropped
	if got := warningIDs(); !got[keptID] || !got[droppedID] {
		t.Fatalf("precondition: warnings before the restart = %v, want %s and %s", got, keptID, droppedID)
	}

	edited := row
	edited.SystemType = "openccu-lite"
	edited.Interfaces = []config.InterfaceSpec{{Name: "HmIP-RF", Port: 1}}
	if err := f.admin.Put(ctx, edited); err != nil {
		t.Fatalf("Put(edited): %v", err)
	}
	t.Cleanup(func() { _ = f.orch.removeCentral(context.Background(), name) })

	got := warningIDs()
	if got[droppedID] {
		t.Errorf("the dropped interface still raises %s after the restart", droppedID)
	}
	if !got[keptID] {
		t.Errorf("the kept interface lost its warning %s: warnings %v", keptID, got)
	}
	journal := 0
	for _, inc := range reader.Incidents() {
		if inc.InterfaceID == dropped {
			journal++
		}
	}
	if journal != 1 {
		t.Errorf("journal rows of the dropped interface after the restart = %d, want 1 (history is kept)", journal)
	}
}
