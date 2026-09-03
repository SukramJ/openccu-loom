// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package device

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// --- helpers ---

// fakeCombinedDP is a fake AttachableDataPoint that also satisfies
// [CombinedDataPoint] so tests can verify CombinedDataPoints filtering.
type fakeCombinedDP struct {
	key hmtypes.DataPointKey
}

func (f *fakeCombinedDP) DataPointKey() hmtypes.DataPointKey { return f.key }
func (f *fakeCombinedDP) IsCombined() bool                   { return true }

// fakePathDP is a fake ParameterDataPoint that also satisfies
// [DataPointProvider] so tests can verify DataPointPaths collection.
type fakePathDP struct {
	key       hmtypes.DataPointKey
	statePath string
}

func (f *fakePathDP) DataPointKey() hmtypes.DataPointKey { return f.key }
func (f *fakePathDP) StatePath() string                  { return f.statePath }

// ─── CombinedDataPoints ──────────────────────────────────────────────

// TestChannelCombinedDataPointsFiltersMarker verifies that only calculated DPs
// that implement [CombinedDataPoint] are returned from Channel.CombinedDataPoints.
func TestChannelCombinedDataPointsFiltersMarker(t *testing.T) {
	t.Parallel()

	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "SHUTTER_TRANSMITTER", hmenum.ParamsetKeyValues)

	plainKey := hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "PLAIN"}
	combinedKey := hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "COMBINED"}

	ch.AttachCalculatedDataPoint(&fakeAttachable{key: plainKey})
	ch.AttachCalculatedDataPoint(&fakeCombinedDP{key: combinedKey})

	got := ch.CombinedDataPoints()
	if len(got) != 1 {
		t.Fatalf("CombinedDataPoints(): expected 1, got %d", len(got))
	}
	if got[0].DataPointKey().Parameter != "COMBINED" {
		t.Fatalf("CombinedDataPoints(): unexpected key %s", got[0].DataPointKey().Parameter)
	}
}

// TestDeviceCombinedDataPointsAggregatesChannels verifies Device.CombinedDataPoints
// aggregates combined DPs across all channels.
func TestDeviceCombinedDataPointsAggregatesChannels(t *testing.T) {
	t.Parallel()

	d := newAggregateDevice()
	ch1 := d.AddChannel("ABC0001:1", 1, "T1", hmenum.ParamsetKeyValues)
	ch2 := d.AddChannel("ABC0001:2", 2, "T2", hmenum.ParamsetKeyValues)

	ch1.AttachCalculatedDataPoint(&fakeCombinedDP{key: hmtypes.DataPointKey{ChannelAddress: ch1.Address, Parameter: "C1"}})
	ch2.AttachCalculatedDataPoint(&fakeCombinedDP{key: hmtypes.DataPointKey{ChannelAddress: ch2.Address, Parameter: "C2"}})
	ch2.AttachCalculatedDataPoint(&fakeAttachable{key: hmtypes.DataPointKey{ChannelAddress: ch2.Address, Parameter: "CALC"}})

	got := d.CombinedDataPoints()
	if len(got) != 2 {
		t.Fatalf("Device.CombinedDataPoints(): expected 2, got %d", len(got))
	}
}

// ─── DataPointPaths ───────────────────────────────────────────────────

// TestDeviceDataPointPathsAggregates verifies that DataPointPaths returns
// every StatePath from DPs that implement DataPointProvider, across channels.
func TestDeviceDataPointPathsAggregates(t *testing.T) {
	t.Parallel()

	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T1", hmenum.ParamsetKeyValues)

	// Attach a fake path DP as a calculated DP.
	ch.AttachCalculatedDataPoint(&fakePathDP{
		key:       hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "LEVEL"},
		statePath: "device/status/ABC0001/1/LEVEL",
	})

	paths := d.DataPointPaths()
	if len(paths) != 1 {
		t.Fatalf("DataPointPaths(): expected 1 path, got %d: %v", len(paths), paths)
	}
	if paths[0] != "device/status/ABC0001/1/LEVEL" {
		t.Fatalf("DataPointPaths(): unexpected path %q", paths[0])
	}
}

// TestChannelDataPointPathsSkipsEmpty verifies that empty state paths are
// excluded from Channel.DataPointPaths().
func TestChannelDataPointPathsSkipsEmpty(t *testing.T) {
	t.Parallel()

	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T1", hmenum.ParamsetKeyValues)

	ch.AttachCalculatedDataPoint(&fakePathDP{
		key:       hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "X"},
		statePath: "", // empty — must be excluded
	})
	ch.AttachCalculatedDataPoint(&fakePathDP{
		key:       hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "Y"},
		statePath: "device/status/ABC0001/1/Y",
	})

	paths := ch.DataPointPaths()
	if len(paths) != 1 {
		t.Fatalf("Channel.DataPointPaths(): expected 1 non-empty path, got %d: %v", len(paths), paths)
	}
}

// ─── GenericDataPoints ───────────────────────────────────────────────

// TestDeviceGenericDataPointsAliasesAllDataPoints verifies GenericDataPoints
// is an alias for AllDataPoints (same length, same set of keys).
func TestDeviceGenericDataPointsAliasesAllDataPoints(t *testing.T) {
	t.Parallel()

	d := newAggregateDevice()
	// AllDataPoints requires at least one channel with a parameter.
	// The device has no parameters in this minimal fixture, so both
	// should return empty slices.
	if want, got := len(d.AllDataPoints()), len(d.GenericDataPoints()); want != got {
		t.Fatalf("GenericDataPoints() len %d != AllDataPoints() len %d", got, want)
	}
}

// ─── Identifier ───────────────────────────────────────────────────────

// TestDeviceIdentifierFormat verifies Identifier() returns "<address>::<interfaceID>".
func TestDeviceIdentifierFormat(t *testing.T) {
	t.Parallel()

	d := New(Config{
		InterfaceID: "HmIP-RF.0",
		Address:     "ABC0001",
		Model:       "HmIP-SW2",
	})

	want := "ABC0001::HmIP-RF.0"
	if got := d.Identifier(); got != want {
		t.Fatalf("Identifier(): want %q, got %q", want, got)
	}
}

// ─── GetCalculatedDataPoint ──────────────────────────────────────────

// TestDeviceGetCalculatedDataPointFound verifies the channel+parameter lookup.
func TestDeviceGetCalculatedDataPointFound(t *testing.T) {
	t.Parallel()

	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T1", hmenum.ParamsetKeyValues)
	key := hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "DEW_POINT"}
	dp := &fakeAttachable{key: key}
	ch.AttachCalculatedDataPoint(dp)

	got := d.GetCalculatedDataPoint("ABC0001:1", "DEW_POINT")
	if got == nil {
		t.Fatal("GetCalculatedDataPoint(): expected non-nil, got nil")
	}
	if got != dp {
		t.Fatal("GetCalculatedDataPoint(): returned unexpected data point")
	}
}

// TestDeviceGetCalculatedDataPointNotFound verifies nil is returned for missing
// channel or missing parameter.
func TestDeviceGetCalculatedDataPointNotFound(t *testing.T) {
	t.Parallel()

	d := newAggregateDevice()
	if dp := d.GetCalculatedDataPoint("ABC0001:99", "X"); dp != nil {
		t.Fatalf("expected nil for unknown channel, got %v", dp)
	}
}

// ─── GetCustomDataPoint ───────────────────────────────────────────────

// TestDeviceGetCustomDataPointFound verifies lookup by channel number.
func TestDeviceGetCustomDataPointFound(t *testing.T) {
	t.Parallel()

	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:2", 2, "T1", hmenum.ParamsetKeyValues)
	custom := &fakeAttachable{key: hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "COVER"}}
	ch.SetCustomDataPoint(custom)

	got := d.GetCustomDataPoint(2)
	if got == nil {
		t.Fatal("GetCustomDataPoint(2): expected non-nil")
	}
	if got != custom {
		t.Fatal("GetCustomDataPoint(2): returned unexpected DP")
	}
}

// TestDeviceGetCustomDataPointMissing verifies nil for unknown channel number.
func TestDeviceGetCustomDataPointMissing(t *testing.T) {
	t.Parallel()

	d := newAggregateDevice()
	if dp := d.GetCustomDataPoint(99); dp != nil {
		t.Fatalf("expected nil for unknown channel number, got %v", dp)
	}
}

// ─── SubscribeToFirmwareUpdated ──────────────────────────────────────

// TestSubscribeToFirmwareUpdatedFires verifies the subscription handler is
// called when firmware info changes via Firmware.Set().
func TestSubscribeToFirmwareUpdatedFires(t *testing.T) {
	t.Parallel()

	d := New(Config{
		InterfaceID: "HmIP-RF",
		Address:     "ABC0001",
		Model:       "HmIP-X",
	})

	var received FirmwareInfo
	var called int
	unsub := d.SubscribeToFirmwareUpdated(func(fi FirmwareInfo) {
		received = fi
		called++
	})
	defer unsub()

	next := FirmwareInfo{Current: "1.0.0", Available: "1.1.0", Updatable: true}
	d.Firmware().Set(next)

	if called != 1 {
		t.Fatalf("handler called %d times, want 1", called)
	}
	if received.Current != "1.0.0" {
		t.Fatalf("received.Current = %q, want 1.0.0", received.Current)
	}
}

// TestSubscribeToFirmwareUpdatedUnsubscribe verifies that calling unsub prevents
// further invocations.
func TestSubscribeToFirmwareUpdatedUnsubscribe(t *testing.T) {
	t.Parallel()

	d := New(Config{
		InterfaceID: "HmIP-RF",
		Address:     "ABC0001",
		Model:       "HmIP-X",
	})

	var called int
	unsub := d.SubscribeToFirmwareUpdated(func(_ FirmwareInfo) {
		called++
	})
	unsub() // unsubscribe immediately

	d.Firmware().Set(FirmwareInfo{Current: "2.0.0"})
	if called != 0 {
		t.Fatalf("handler should not be called after unsub; called %d times", called)
	}
}

// ─── Channel.NameData full name ───────────────────────────────────────
func TestChannelNameDataCarriesTheFullName(t *testing.T) {
	t.Parallel()

	d := New(Config{
		Address: "0001ABCD",
		Model:   "HmIP-SW2",
		Name:    "Wohnzimmer",
	})
	ch := d.AddChannel("0001ABCD:1", 1, "SWITCH_TRANSMITTER", "VALUES")
	ch.SetName("Wohnzimmer Licht")

	// Channel.FullName was a one-line forwarder into a naming family no
	// production code called; NameData is the path the daemon uses.
	if full := ch.NameData().FullName(); full == "" {
		t.Fatal("FullName() must not be empty when the device has a name")
	}
}

// ─── Channel.NameData ─────────────────────────────────────────────────

// TestChannelNameDataDeviceNamePropagated verifies that NameData() carries
// the device name in DeviceName.
func TestChannelNameDataDeviceNamePropagated(t *testing.T) {
	t.Parallel()

	d := New(Config{
		Address: "0001ABCD",
		Model:   "HmIP-SW2",
		Name:    "Garten",
	})
	ch := d.AddChannel("0001ABCD:1", 1, "SWITCH_TRANSMITTER", "VALUES")

	nd := ch.NameData()
	if nd.DeviceName != "Garten" {
		t.Fatalf("NameData().DeviceName = %q, want %q", nd.DeviceName, "Garten")
	}
}

// TestChannelNameDataNilSafe verifies NameData() on nil channel returns zero.
func TestChannelNameDataNilSafe(t *testing.T) {
	t.Parallel()

	var c *Channel
	if nd := c.NameData(); !nd.IsZero() {
		t.Fatal("NameData() on nil Channel must return zero NameData")
	}
}

// ─── Channel.TypeTranslation ─────────────────────────────────────────

// TestChannelTypeTranslationFallsBackToType verifies that before SetTypeTranslation
// is called, TypeTranslation() returns the channel Type.
func TestChannelTypeTranslationFallsBackToType(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "0001ABCD", Model: "HmIP-SW2", Name: "Test"})
	ch := d.AddChannel("0001ABCD:1", 1, "SWITCH_TRANSMITTER", "VALUES")

	if got := ch.TypeTranslation(); got != "SWITCH_TRANSMITTER" {
		t.Fatalf("TypeTranslation() without set = %q, want %q", got, "SWITCH_TRANSMITTER")
	}
}

// TestChannelTypeTranslationAfterSet verifies that SetTypeTranslation changes
// the returned value.
func TestChannelTypeTranslationAfterSet(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "0001ABCD", Model: "HmIP-SW2", Name: "Test"})
	ch := d.AddChannel("0001ABCD:1", 1, "SWITCH_TRANSMITTER", "VALUES")
	ch.SetTypeTranslation("Schalter Transceiver")

	if got := ch.TypeTranslation(); got != "Schalter Transceiver" {
		t.Fatalf("TypeTranslation() after set = %q, want %q", got, "Schalter Transceiver")
	}
}

// ─── Channel.LinkPeerSourceCategories ────────────────────────────────

// TestChannelLinkPeerSourceCategoriesDefaultNil verifies initial value is nil.
func TestChannelLinkPeerSourceCategoriesDefaultNil(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "0001ABCD", Model: "HmIP-SW2", Name: "Test"})
	ch := d.AddChannel("0001ABCD:1", 1, "SWITCH_TRANSMITTER", "VALUES")

	if cats := ch.LinkPeerSourceCategories(); cats != nil {
		t.Fatalf("LinkPeerSourceCategories() default = %v, want nil", cats)
	}
}

// TestChannelLinkPeerSourceCategoriesAfterSet verifies set + get round-trip.
func TestChannelLinkPeerSourceCategoriesAfterSet(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "0001ABCD", Model: "HmIP-SW2", Name: "Test"})
	ch := d.AddChannel("0001ABCD:1", 1, "SWITCH_TRANSMITTER", "VALUES")
	input := []string{"switch", "sensor"}
	ch.SetLinkPeerSourceCategories(input)

	got := ch.LinkPeerSourceCategories()
	if len(got) != 2 || got[0] != "switch" || got[1] != "sensor" {
		t.Fatalf("LinkPeerSourceCategories() = %v, want %v", got, input)
	}
}

// TestChannelLinkPeerSourceCategoriesReturnsCopy verifies mutations don't bleed.
func TestChannelLinkPeerSourceCategoriesReturnsCopy(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "0001ABCD", Model: "HmIP-SW2", Name: "Test"})
	ch := d.AddChannel("0001ABCD:1", 1, "SWITCH_TRANSMITTER", "VALUES")
	ch.SetLinkPeerSourceCategories([]string{"a"})

	got := ch.LinkPeerSourceCategories()
	got[0] = "mutated"

	got2 := ch.LinkPeerSourceCategories()
	if got2[0] == "mutated" {
		t.Fatal("LinkPeerSourceCategories() must return a copy, not share the slice")
	}
}

// ─── Channel.LinkPeerTargetCategories ────────────────────────────────

// TestChannelLinkPeerTargetCategoriesAfterSet verifies set + get round-trip.
func TestChannelLinkPeerTargetCategoriesAfterSet(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "0001ABCD", Model: "HmIP-SW2", Name: "Test"})
	ch := d.AddChannel("0001ABCD:1", 1, "SWITCH_TRANSMITTER", "VALUES")
	ch.SetLinkPeerTargetCategories([]string{"climate"})

	got := ch.LinkPeerTargetCategories()
	if len(got) != 1 || got[0] != "climate" {
		t.Fatalf("LinkPeerTargetCategories() = %v, want [climate]", got)
	}
}

// ─── Channel.LinkRoles (raw CCU LINK_*_ROLES) ────────────────────────

func TestChannelLinkRolesDefaultNil(t *testing.T) {
	t.Parallel()
	d := New(Config{Address: "0001ABCD", Model: "HmIP-WRC", Name: "Test"})
	ch := d.AddChannel("0001ABCD:1", 1, "KEY_TRANSCEIVER", "VALUES")
	if r := ch.LinkSourceRoles(); r != nil {
		t.Errorf("LinkSourceRoles() default = %v, want nil", r)
	}
	if r := ch.LinkTargetRoles(); r != nil {
		t.Errorf("LinkTargetRoles() default = %v, want nil", r)
	}
}

func TestChannelSetLinkRolesRoundTrip(t *testing.T) {
	t.Parallel()
	d := New(Config{Address: "0001ABCD", Model: "HmIP-WRC", Name: "Test"})
	ch := d.AddChannel("0001ABCD:1", 1, "KEY_TRANSCEIVER", "VALUES")
	ch.SetLinkRoles([]string{"SWITCH", "REMOTECONTROL_RECEIVER"}, []string{"WEATHER"})

	src := ch.LinkSourceRoles()
	if len(src) != 2 || src[0] != "SWITCH" || src[1] != "REMOTECONTROL_RECEIVER" {
		t.Errorf("LinkSourceRoles() = %v", src)
	}
	tgt := ch.LinkTargetRoles()
	if len(tgt) != 1 || tgt[0] != "WEATHER" {
		t.Errorf("LinkTargetRoles() = %v", tgt)
	}
}

func TestChannelLinkRolesReturnsCopy(t *testing.T) {
	t.Parallel()
	d := New(Config{Address: "0001ABCD", Model: "HmIP-WRC", Name: "Test"})
	ch := d.AddChannel("0001ABCD:1", 1, "KEY_TRANSCEIVER", "VALUES")
	ch.SetLinkRoles([]string{"SWITCH"}, []string{"WEATHER"})

	got := ch.LinkSourceRoles()
	got[0] = "mutated"
	if again := ch.LinkSourceRoles(); again[0] == "mutated" {
		t.Fatal("LinkSourceRoles() must return a copy, not share the slice")
	}
}

// ─── Update.Name / Update.FullName ─────────────────────────────

// TestUpdateNameReturnsUpdate verifies DpUpdate.name = "Update".
func TestUpdateNameReturnsUpdate(t *testing.T) {
	t.Parallel()

	d := newTestDevice(t)
	u := d.Update()
	if got := u.Name(); got != "Update" {
		t.Fatalf("Update.Name() = %q, want %q", got, "Update")
	}
}

// TestUpdateFullNamePrefixedWithDeviceName verifies DpUpdate.full_name format.
func TestUpdateFullNamePrefixedWithDeviceName(t *testing.T) {
	t.Parallel()

	d := newTestDevice(t) // device named "Wohnzimmer Sensor"
	u := d.Update()
	want := "Wohnzimmer Sensor Update"
	if got := u.FullName(); got != want {
		t.Fatalf("Update.FullName() = %q, want %q", got, want)
	}
}

// TestUpdateFullNameFallsBackWhenNoDeviceName verifies edge case with empty name.
func TestUpdateFullNameFallsBackWhenNoDeviceName(t *testing.T) {
	t.Parallel()

	d := New(Config{Address: "0001ABCD", Updatable: true})
	u := d.Update()
	if got := u.FullName(); got != "Update" {
		t.Fatalf("Update.FullName() with no device name = %q, want %q", got, "Update")
	}
}

// ─── Update.Register / Unregister ────────────────────────────────────

// TestUpdateRegisterUnregister verifies the register/unregister cycle.
func TestUpdateRegisterUnregister(t *testing.T) {
	t.Parallel()

	d := newTestDevice(t)
	u := d.Update()

	if u.IsRegistered() {
		t.Fatal("Update must not be registered before Register() is called")
	}

	u.Register()
	if !u.IsRegistered() {
		t.Fatal("Update must be registered after Register()")
	}

	u.Unregister()
	if u.IsRegistered() {
		t.Fatal("Update must not be registered after Unregister()")
	}
}

// TestUpdateRegisterNilSafe verifies nil Update doesn't panic.
func TestUpdateRegisterNilSafe(t *testing.T) {
	t.Parallel()

	var u *Update
	// These must not panic.
	u.Register()
	u.Unregister()
	if u.IsRegistered() {
		t.Fatal("nil Update.IsRegistered() must return false")
	}
}
