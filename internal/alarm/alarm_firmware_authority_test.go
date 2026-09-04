// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package alarm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// This file grounds the alarm core's device rules against the CCU's own
// sources instead of against the convention that produced them. Each test
// names the source it encodes; where no source settles a rule, the test says
// so rather than dressing the convention up as a measurement.

// --- c005: the WKP channel-to-user-slot layout ---

// alarmFwWkpChannelPairs is the HmIP-WKP's declared ACCESS_TRANSCEIVER layout,
// read off the device rather than derived from our own formula:
// HmIP-WKP's device description declares :1…:16 as ACCESS_TRANSCEIVER, and
// its paramset descriptions declare VALUES PRESS_LOCK on every odd channel
// and PRESS_UNLOCK on every even one, with MASTER NUMERIC_PIN_CODE present
// only on the odd channels.
//
// The user-slot column is the CCU's, from two independent expressions:
// ../OpenCCU-Base/www/rega/pages/tabs/control/function.fn:166 labels a WKP
// channel `(chNumber + 1) / 2` ("Access Control n"), and
// ../OpenCCU-Base/www/config/easymodes/etc/hmipChannelConfigDialogs.tcl:7028
// tells a channel without NUMERIC_PIN_CODE — the even, unlock half — that it
// borrows the PIN of channel `[expr $chn / 2]`.
var alarmFwWkpChannelPairs = []struct {
	channel int
	press   hmenum.Parameter
	slot    int
}{
	{1, hmenum.ParameterPressLock, 1},
	{2, hmenum.ParameterPressUnlock, 1},
	{3, hmenum.ParameterPressLock, 2},
	{4, hmenum.ParameterPressUnlock, 2},
	{5, hmenum.ParameterPressLock, 3},
	{6, hmenum.ParameterPressUnlock, 3},
	{7, hmenum.ParameterPressLock, 4},
	{8, hmenum.ParameterPressUnlock, 4},
	{9, hmenum.ParameterPressLock, 5},
	{10, hmenum.ParameterPressUnlock, 5},
	{11, hmenum.ParameterPressLock, 6},
	{12, hmenum.ParameterPressUnlock, 6},
	{13, hmenum.ParameterPressLock, 7},
	{14, hmenum.ParameterPressUnlock, 7},
	{15, hmenum.ParameterPressLock, 8},
	{16, hmenum.ParameterPressUnlock, 8},
}

// TestAlarmFwWkpPairIndexMatchesTheCcuChannelPairFormulas pins wkpPairIndex to
// the CCU's own two expressions over the device's full declared channel span.
//
// The prior coverage only ever used slot 1 (channels 1 and 2), where several
// wrong formulas agree with the right one; the whole span is what separates
// them.
func TestAlarmFwWkpPairIndexMatchesTheCcuChannelPairFormulas(t *testing.T) {
	t.Parallel()

	for _, tc := range alarmFwWkpChannelPairs {
		got, ok := wkpPairIndex(fmt.Sprintf("WKP0001:%d", tc.channel))
		if !ok {
			t.Errorf("channel %d: wkpPairIndex reported it out of range, "+
				"but the device declares :1…:16 as ACCESS_TRANSCEIVER", tc.channel)
			continue
		}
		if got != tc.slot {
			t.Errorf("channel %d (%s): pair index = %d, want %d — the CCU labels this channel user %d",
				tc.channel, tc.press, got, tc.slot, tc.slot)
		}
		// The even half is the independent leg: the config dialog computes
		// `$chn / 2` there, an expression that is not ours.
		if tc.press == hmenum.ParameterPressUnlock && got != tc.channel/2 {
			t.Errorf("channel %d: pair index = %d, want %d (hmipChannelConfigDialogs.tcl:7028 `expr $chn / 2`)",
				tc.channel, got, tc.channel/2)
		}
	}

	for _, addr := range []string{"WKP0001:0", "WKP0001:17", "WKP0001:18", "WKP0001:-1", "WKP0001", "", "WKP0001:x"} {
		if _, ok := wkpPairIndex(addr); ok {
			t.Errorf("wkpPairIndex(%q) accepted an address outside the declared 1…16 span", addr)
		}
	}
}

// TestAlarmFwKeypadPressCorrelatesWithTheSlotsOwnChannelPair drives a press on
// user slot 5 — channels 9 and 10 — through the real router.
//
// Slot 5 is chosen because it is far from the fixed point of the formula: a
// wrong pairing lands on a different slot instead of coincidentally on the
// right one, which is what slot 1 could never show.
//
// The mismatch half pins the one predicate no CCU source settles (see
// wkpPairIndex's comment): that the keypad raises the press on the channel
// pair whose index equals the CODE_ID just scanned. The test does not prove
// the assumption — it pins that a mismatch is refused and journaled with both
// numbers, so a field trace can falsify it.
func TestAlarmFwKeypadPressCorrelatesWithTheSlotsOwnChannelPair(t *testing.T) {
	const slot = 5

	t.Run("press on the slot's own pair arms", func(t *testing.T) {
		h := alarmFwKeypadHarness(t, slot)
		h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:0", hmenum.ParameterCodeID, hmtypes.IntValue(slot)))
		h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:0", hmenum.ParameterCodeState, hmtypes.IntValue(1)))
		// Channels 9/10 are pair 5: (9+1)/2 = 10/2 = 5.
		h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:9", hmenum.ParameterPressLock, hmtypes.BoolValue(true)))

		if got := h.zoneState("eg"); got != hmenum.AlarmZoneStateArmed {
			t.Fatalf("zone state = %s, want armed (slot %d presses on channel 9, its own lock channel)", got, slot)
		}
	})

	t.Run("press on another pair is refused and journaled with both numbers", func(t *testing.T) {
		h := alarmFwKeypadHarness(t, slot)
		h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:0", hmenum.ParameterCodeID, hmtypes.IntValue(slot)))
		h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:0", hmenum.ParameterCodeState, hmtypes.IntValue(1)))
		h.svc.intents.onEvent(h.ctx, intentsTestCentral, wkpEvent("WKP0001:1", hmenum.ParameterPressLock, hmtypes.BoolValue(true)))

		if got := h.zoneState("eg"); got != hmenum.AlarmZoneStateDisarmed {
			t.Fatalf("zone state = %s, want disarmed (a press on pair 1 must not act for slot 5)", got)
		}
		entries, err := h.svc.Stores().Journal.Query(h.ctx, sqlitestore.AlarmJournalFilter{})
		if err != nil {
			t.Fatalf("query journal: %v", err)
		}
		var found bool
		for _, e := range entries {
			if e.Event != "keypad_press_unmatched" {
				continue
			}
			found = true
			var details map[string]any
			if err := json.Unmarshal([]byte(e.DetailsJSON), &details); err != nil {
				t.Fatalf("unmarshal journal details %q: %v", e.DetailsJSON, err)
			}
			if !alarmFwDetailsContain(details, "code_id", slot) {
				t.Errorf("unmatched entry details = %v, want code_id %d", details, slot)
			}
			if !alarmFwDetailsContain(details, "pair_index", 1) {
				t.Errorf("unmatched entry details = %v, want pair_index 1", details)
			}
		}
		if !found {
			t.Fatal("missing keypad_press_unmatched journal entry: a refused press must stay visible")
		}
	})
}

// alarmFwKeypadHarness builds the intents harness with one keypad code bound
// to slot.
func alarmFwKeypadHarness(t *testing.T, slot int) *intentsHarness {
	t.Helper()
	src := &fakeCodeSource{rows: []CodeRow{{
		ID: "c1", Name: "Alice", Kind: CodeKindKeypadSlot, Enabled: true,
		Perms:   CodePerms{Arm: true, Disarm: true},
		Binding: CodeBinding{Central: intentsTestCentral, DeviceAddress: "WKP0001", Slot: slot, ArmMode: "full", ZoneID: "eg"},
	}}}
	h := newIntentsHarness(t, src)
	h.seedZone("eg", "Erdgeschoss")
	h.start()
	return h
}

// alarmFwDetailsContain reports whether a journal detail carries want, whatever
// numeric shape the JSON round-trip left it in.
func alarmFwDetailsContain(details map[string]any, key string, want int) bool {
	v, ok := details[key]
	if !ok {
		return false
	}
	switch n := v.(type) {
	case int:
		return n == want
	case int64:
		return n == int64(want)
	case float64:
		return n == float64(want)
	default:
		return false
	}
}

// --- c007 / c010: the state parameter to reset action pairing ---

// TestAlarmFwResetActionExistsOnlyForTheTwoDeclaredStateParameters pins
// resetParameterFor to the firmware's own pair set.
//
// The HmIP server registers exactly two stop-shaped state parameters,
// RESET_MOTION and RESET_PRESENCE (HMIPServer
// de.eq3.cbcs.devicedescription.channelspecification.StateParameterFactory,
// both through #createStop), and the descriptor corpus shows the co-location
// is strict: every channel declaring RESET_MOTION declares MOTION, every
// channel declaring RESET_PRESENCE declares PRESENCE_DETECTION_STATE, never
// the cross pairing, and no third RESET_* parameter exists anywhere in it.
// The negative half of the table is therefore a measurement, not caution: a
// reset invented for any other enrollable parameter would be a parameter no
// device declares.
func TestAlarmFwResetActionExistsOnlyForTheTwoDeclaredStateParameters(t *testing.T) {
	t.Parallel()

	cases := []struct {
		state hmenum.Parameter
		reset hmenum.Parameter
	}{
		{hmenum.ParameterMotion, hmenum.ParameterResetMotion},
		{hmenum.ParameterPresenceDetectionState, hmenum.ParameterResetPresence},
		// Every other parameter an alarm sensor can be enrolled on.
		{hmenum.ParameterState, ""},
		{hmenum.ParameterSmokeAlarm, ""},
		{hmenum.ParameterSmokeDetectorAlarmStatus, ""},
		{hmenum.ParameterAlarmState, ""},
		{hmenum.ParameterMoistureDetected, ""},
		{hmenum.ParameterWaterLevelDetected, ""},
		{hmenum.ParameterLowBat, ""},
	}

	for _, tc := range cases {
		got, ok := resetParameterFor(string(tc.state))
		if tc.reset == "" {
			if ok {
				t.Errorf("resetParameterFor(%q) = %q, want none — no device declares a reset for it",
					tc.state, got)
			}
			continue
		}
		if !ok {
			t.Errorf("resetParameterFor(%q) = none, want %q", tc.state, tc.reset)
			continue
		}
		if got != tc.reset {
			t.Errorf("resetParameterFor(%q) = %q, want %q", tc.state, got, tc.reset)
		}
	}
}

// TestAlarmFwClassicMotionDetectorIsNotResettable pins the family boundary the
// port's own contract used to state wrongly.
//
// RESET_MOTION is HmIP-only: no file under ../OpenCCU-Base/src/devicetypes/
// mentions it (148 XMLs, 0 hits, while 5 declare `id="MOTION"`), and
// HM-Sec-MDIR's paramset description declares its MOTION_DETECTOR channel
// with MOTION and no reset at all. A classic
// BidCos detector must therefore report Supports=false — the CCU un-latches it
// on a server-side timer, and there is nothing to write.
//
// The fixture is the point: the channel type is MOTION_DETECTOR on a classic
// model, not the HmIP MOTIONDETECTOR_TRANSCEIVER, because all ten
// MOTIONDETECTOR_TRANSCEIVER channels in the corpus — on eight devices, from
// HmIP-SMI to HmIPW-SMI55 — declare RESET_MOTION. A "no reset here" case built
// on that type describes no device.
func TestAlarmFwClassicMotionDetectorIsNotResettable(t *testing.T) {
	t.Parallel()

	const centralName, deviceAddress = "my-ccu", "MEQ0000251"
	channelAddress := deviceAddress + ":1"

	d, ch := alarmFwChannel(t, "HM-Sec-MDIR", deviceAddress, channelAddress, 1, "MOTION_DETECTOR")
	alarmFwPutBoolSensor(ch, hmenum.ParameterMotion)
	reg := newCandidatesRegistry(t, centralName, d)

	m := newMotionResetter(reg)
	row := sensorRow(centralName, channelAddress, hmenum.ParameterMotion)
	if m.Supports(row) {
		t.Error("Supports = true for a classic BidCos motion detector, which declares no RESET_MOTION")
	}
	if err := m.Reset(t.Context(), row); err == nil {
		t.Error("Reset returned no error on a channel that carries no RESET_MOTION")
	}

	// The HmIP counterpart, so the guard distinguishes the two families rather
	// than reporting false for everything.
	hd, hch := alarmFwChannel(t, "HmIP-SMO", "0001D3C99C1234", "0001D3C99C1234:1", 1, "MOTIONDETECTOR_TRANSCEIVER")
	alarmFwPutBoolSensor(hch, hmenum.ParameterMotion)
	putTriggerDataPoint(hch, hmenum.ParameterResetMotion, &recordingWriter{}, "button")
	hreg := newCandidatesRegistry(t, centralName, hd)
	if !newMotionResetter(hreg).Supports(sensorRow(centralName, hch.Address, hmenum.ParameterMotion)) {
		t.Error("Supports = false for an HmIP MOTIONDETECTOR_TRANSCEIVER carrying RESET_MOTION")
	}
}

// --- c008: the intrusion state parameter is an enumeration, not a boolean ---

// TestAlarmFwIntrusionCandidateCarriesTheDeclaredEnumValueList pins that an
// intrusion candidate reaches the picker with the vocabulary the device
// declares.
//
// The HmIP firmware builds these as enumerations, not booleans: HMIPServer
// de.eq3.cbcs.devicedescription.channelspecification.stateparameter.GeneralStateParameterFactory
// #createStateWindowOpenClosed uses {CLOSED, OPEN} and
// #createStateWindowOpenTiltedClosed uses {CLOSED, TILTED, OPEN}. The corpus
// agrees: SHUTTER_CONTACT STATE is ENUM {CLOSED, OPEN} on the five HmIP
// contacts and BOOL only on the eight classic ones; ROTARY_HANDLE_TRANSCEIVER
// and ROTARY_HANDLE_SENSOR STATE are ENUM {CLOSED, TILTED, OPEN}.
//
// Nothing else exercises SensorCandidates' value-list copy, so an operator
// picking active values for a window contact would silently be offered none.
func TestAlarmFwIntrusionCandidateCarriesTheDeclaredEnumValueList(t *testing.T) {
	t.Parallel()

	const centralName = "my-ccu"
	windowStates := []string{"CLOSED", "OPEN"}
	handleStates := []string{"CLOSED", "TILTED", "OPEN"}

	swdo, swdoCh := alarmFwChannel(t, "HmIP-SWDO", "0001D3C99C0001", "0001D3C99C0001:1", 1, "SHUTTER_CONTACT")
	alarmFwPutEnumSensor(swdoCh, hmenum.ParameterState, windowStates...)
	srh, srhCh := alarmFwChannel(t, "HmIP-SRH", "0001D3C99C0002", "0001D3C99C0002:1", 1, "ROTARY_HANDLE_TRANSCEIVER")
	alarmFwPutEnumSensor(srhCh, hmenum.ParameterState, handleStates...)

	reg := newCandidatesRegistry(t, centralName, swdo, srh)
	svc := alarmFwService(t, reg)

	got := map[string][]string{}
	for _, c := range svc.SensorCandidates(context.Background()) {
		got[c.Model] = c.ValueList
	}
	if !slices.Equal(got["HmIP-SWDO"], windowStates) {
		t.Errorf("HmIP-SWDO candidate ValueList = %v, want %v (the device declares STATE as an ENUM)",
			got["HmIP-SWDO"], windowStates)
	}
	if !slices.Equal(got["HmIP-SRH"], handleStates) {
		t.Errorf("HmIP-SRH candidate ValueList = %v, want %v", got["HmIP-SRH"], handleStates)
	}
}

// --- c011: the default activation rule on an enumeration ---

// alarmFwSmokeStatusValueList is SMOKE_DETECTOR_ALARM_STATUS as the firmware
// declares it: HMIPServer
// de.eq3.cbcs.devicedescription.channelspecification.stateparameter.SmokeDetectorAlarmStatus
// declares IDLE_OFF, PRIMARY_ALARM, INTRUSION_ALARM, SECONDARY_ALARM in that
// order and #getNames() fills the array in ordinal order; both HmIP-SWSD and
// HmIP-SWSD-2 carry exactly that VALUE_LIST in the descriptor corpus.
var alarmFwSmokeStatusValueList = []string{"IDLE_OFF", "PRIMARY_ALARM", "INTRUSION_ALARM", "SECONDARY_ALARM"}

// TestAlarmFwSmokeStatusNeedsActiveValuesToRejectTheSirenCommand pins why the
// per-sensor active-value selection exists.
//
// The default rule is positional — anything that is not index 0 counts as an
// activation — and index 2 of this list is INTRUSION_ALARM, the command the
// installation sends to drive the smoke detector as a burglary siren. Under
// the default rule the alarm system reads its own output back as a fire.
//
// Both halves matter: the first shows the default rule really does fire on
// index 2, the second that a narrowed selection resolves the same wire value
// through the declared list and refuses it.
func TestAlarmFwSmokeStatusNeedsActiveValuesToRejectTheSirenCommand(t *testing.T) {
	t.Parallel()

	const centralName, channelAddress = "my-ccu", "0001D3C99C0003:1"
	d, ch := alarmFwChannel(t, "HmIP-SWSD", "0001D3C99C0003", channelAddress, 1, "SMOKE_DETECTOR")
	alarmFwPutEnumSensor(ch, hmenum.ParameterSmokeDetectorAlarmStatus, alarmFwSmokeStatusValueList...)
	reg := newCandidatesRegistry(t, centralName, d)

	s := &Service{reg: reg, enums: newEnumResolver(reg), log: slog.New(slog.DiscardHandler)}
	base := sensorBinding{
		id: "sensor-1", centralName: centralName, interfaceID: "HmIP-RF",
		channelAddress: channelAddress, parameter: string(hmenum.ParameterSmokeDetectorAlarmStatus),
	}

	const intrusionAlarmIndex = 2
	if got := alarmFwSmokeStatusValueList[intrusionAlarmIndex]; got != "INTRUSION_ALARM" {
		t.Fatalf("value list index %d = %q, want INTRUSION_ALARM", intrusionAlarmIndex, got)
	}

	active, known := s.active(base, hmtypes.IntValue(intrusionAlarmIndex))
	if !known {
		t.Fatal("known = false for a declared enumeration index")
	}
	if !active {
		t.Error("the default rule read INTRUSION_ALARM as inactive; " +
			"it is positional and index 2 is not index 0, so this test no longer measures what it claims")
	}

	narrowed := base
	narrowed.activeValues = []string{"PRIMARY_ALARM", "SECONDARY_ALARM"}
	active, known = s.active(narrowed, hmtypes.IntValue(intrusionAlarmIndex))
	if !known {
		t.Fatal("known = false for a narrowed enumeration index")
	}
	if active {
		t.Error("INTRUSION_ALARM counted as a smoke detection although the enrolment names only the two real alarms")
	}
	if active, _ := s.active(narrowed, hmtypes.IntValue(1)); !active {
		t.Error("PRIMARY_ALARM (index 1) did not count as a detection under the narrowed selection")
	}
	if active, _ := s.active(narrowed, hmtypes.IntValue(0)); active {
		t.Error("IDLE_OFF (index 0) counted as a detection")
	}
}

// --- fixtures ---

// alarmFwChannel builds a device of the given model with one channel, so a
// fixture can state which device family it stands for. newTestChannel hard-codes
// a synthetic model, which is fine where the model is not read and misleading
// where a rule is family-specific.
func alarmFwChannel(
	t *testing.T,
	model, deviceAddress, channelAddress string,
	channelNo int,
	channelType string,
) (*device.Device, *device.Channel) {
	t.Helper()
	d := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Address:     deviceAddress,
		Model:       model,
		Name:        model + " " + deviceAddress,
	})
	ch := d.AddChannel(channelAddress, channelNo, channelType, hmenum.ParamsetKeyValues)
	return d, ch
}

// alarmFwPutEnumSensor attaches a read-only ENUM parameter in the shape the
// model's own resolver builds for one: an integer sensor carrying the declared
// VALUE_LIST (internal/central/adapter/datapoint_resolver.go:195 maps ENUM and
// INTEGER onto the same read-only sensor).
func alarmFwPutEnumSensor(ch *device.Channel, p hmenum.Parameter, valueList ...string) {
	ch.Put(generic.NewIntegerSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			ValueList:  valueList,
		},
	}))
}

// alarmFwPutBoolSensor attaches a read-only boolean parameter — the shape a
// classic BidCos MOTION carries (rf_sec_mdir.xml:227 declares
// `<parameter id="MOTION" operations="read,event">` over `<logical
// type="boolean"/>`).
func alarmFwPutBoolSensor(ch *device.Channel, p hmenum.Parameter) {
	ch.Put(generic.NewBinarySensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	}))
}

// alarmFwService builds a Service on a fresh temp-file database around reg —
// enough for the read-only candidate surfaces, which consult the registry and
// the enrolled-sensor table.
func alarmFwService(t *testing.T, reg *central.Registry) *Service {
	t.Helper()
	dsn := sqlitestore.FileDSN(filepath.Join(t.TempDir(), "alarm-firmware-authority.db"))
	db, err := sqlitestore.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc, err := NewService(Deps{
		Settings: Settings{Enabled: true},
		Registry: reg,
		Stores:   NewStores(db),
		Clock:    clock.NewFake(time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)),
		Logger:   slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}
