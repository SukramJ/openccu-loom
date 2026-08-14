// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package alarm

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/clock"
	switchcdp "github.com/SukramJ/openccu-loom/internal/model/custom/switch"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// reconcileBootStart keeps the fake clock past the engine's
// clock-plausibility epoch, as the other alarm harnesses do.
var reconcileBootStart = time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)

// recordingSwitchWriter records what reaches the wire.
type recordingSwitchWriter struct {
	mu    chan struct{}
	calls []string
}

func newRecordingSwitchWriter() *recordingSwitchWriter {
	return &recordingSwitchWriter{mu: make(chan struct{}, 1)}
}

func (w *recordingSwitchWriter) SetValue(
	_ context.Context, _ string, parameter hmenum.Parameter, value any, _ hmenum.CommandPriority,
) error {
	w.mu <- struct{}{}
	defer func() { <-w.mu }()
	w.calls = append(w.calls, string(parameter))
	if parameter == hmenum.ParameterState {
		if on, ok := value.(bool); ok && !on {
			w.calls = append(w.calls, "off")
		}
	}
	return nil
}

func (w *recordingSwitchWriter) sawStop() bool {
	w.mu <- struct{}{}
	defer func() { <-w.mu }()
	for _, c := range w.calls {
		if c == "off" {
			return true
		}
	}
	return false
}

// TestReconcileActsOnASirenThatWasAlreadySoundingWhenTheModelArrived
// pins S4 against the boot order production actually uses.
//
// S4 says the engine reads the active state of every known siren on
// daemon start and reconciles: a siren whose zone is armed is adopted as
// a triggered incident, one whose zone is disarmed is stopped. Both
// branches leave from the same pass, so covering one covers the pass.
//
// The pass ran at the end of Start — which, in a daemon, is the same
// second as daemon.start and tens of seconds before the readiness-gated
// southbound bring-up has loaded a single device. It therefore asked an
// empty registry, found nothing, and never ran again: the second entry
// point hangs off the runtime central-adoption hook and the third off an
// all-down-to-up connectivity transition, neither of which a plain
// restart produces. Measured against a real CCU: a lamp enrolled as a
// switched siren burned for 153 s after a restart into a disarmed zone,
// while the daemon reported its data point as observed-true 23 s in — the
// state was there, only the second look was missing.
//
// The ordering below is the whole test. Registering the central with an
// empty model, starting the service, and only then letting the device
// arrive is what production does; seeding the model first would invert
// it and pass however broken the wiring is.
//
// This does not live in tests/e2e, where a black-box boot-order test
// would be stronger, because the simulated CCU cannot reproduce the
// precondition: a data point becomes observed at bring-up through the
// bulk value seed, which is a ReGa script, and godevccu never evaluates
// ReGa script bodies. A siren that is already sounding is therefore not
// expressible there.
func TestReconcileActsOnASirenThatWasAlreadySoundingWhenTheModelArrived(t *testing.T) {
	t.Parallel()

	const (
		centralName = "ccu"
		ifaceID     = "ccu-HmIP-RF"
		devAddress  = "0001D3C99ABCDE"
		chAddress   = "0001D3C99ABCDE:4"
	)

	dsn := sqlitestore.FileDSN(filepath.Join(t.TempDir(), "alarm-reconcile-boot.db"))
	db, err := sqlitestore.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	unit, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(unit); err != nil {
		t.Fatalf("register central: %v", err)
	}

	stores := NewStores(db)
	ctx := context.Background()
	zoneID := seedDisarmedZoneWithSwitchedSiren(ctx, t, stores, centralName, chAddress)

	svc, err := NewService(Deps{
		Settings: Settings{Enabled: true},
		Registry: reg,
		Stores:   stores,
		Clock:    clock.NewFake(reconcileBootStart),
		Logger:   slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	// The service starts against a central that holds no devices — the
	// readiness gate has not let the bring-up through yet.
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { svc.Stop(ctx) })

	// The bring-up completes: the device arrives, already switched on,
	// and the central announces that its southbound model is there.
	writer := newRecordingSwitchWriter()
	addSoundingSwitch(t, unit, ifaceID, devAddress, chAddress, writer)
	events.Publish(unit.EventBus, hmevent.CentralSouthboundReadyEvent{CentralName: centralName})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if writer.sawStop() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no stop reached the siren on %s (zone %s) after the device model arrived. S4 requires "+
		"reconciliation to stop a sounding siren whose zone is disarmed; a pass that runs only at "+
		"service start reads an empty registry, so a siren still sounding after a restart is neither "+
		"stopped nor — in an armed zone — adopted as an incident", chAddress, zoneID)
}

// seedDisarmedZoneWithSwitchedSiren persists the operator configuration
// that outlives a restart: one disarmed zone with one switched-siren
// output. Returns the zone id.
func seedDisarmedZoneWithSwitchedSiren(
	ctx context.Context, t *testing.T, stores *Stores, centralName, channelAddress string,
) string {
	t.Helper()
	const zoneID = "zone-boot-order"
	if err := stores.Zones.Upsert(ctx, sqlitestore.AlarmZoneRow{
		ID: zoneID, Name: "Boot order", Slug: "boot-order",
	}); err != nil {
		t.Fatalf("upsert zone: %v", err)
	}
	if err := stores.Outputs.Upsert(ctx, sqlitestore.AlarmOutputRow{
		ID:             "output-boot-order",
		ZoneID:         zoneID,
		Class:          hmenum.AlarmOutputClassSwitchedSiren,
		CentralName:    centralName,
		ChannelAddress: channelAddress,
		Name:           "Siren",
	}); err != nil {
		t.Fatalf("upsert output: %v", err)
	}
	return zoneID
}

// addSoundingSwitch puts a switch actuator into the central's model
// with STATE already observed as on — a siren that was sounding while
// the daemon was down.
func addSoundingSwitch(
	t *testing.T, unit *central.Unit, interfaceID, deviceAddress, channelAddress string,
	writer generic.Writer,
) {
	t.Helper()
	dev := device.New(device.Config{
		InterfaceID: interfaceID,
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     deviceAddress,
		Model:       "HMIP-PS",
	})
	ch := dev.AddChannel(channelAddress, 4, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	sw := generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    interfaceID,
			ChannelAddress: channelAddress,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: writer,
	})
	ch.Put(sw)
	// Observed, not optimistic: this is what the CCU reported.
	if !sw.OnWireValue(true) {
		t.Fatal("the wire value was rejected; the data point would read as unobserved")
	}
	cdp := switchcdp.New(ch)
	if cdp == nil {
		t.Fatal("switch custom data point was not constructed")
	}
	ch.SetCustomDataPoint(cdp)
	unit.ModelRegistry.Put(dev)
}
