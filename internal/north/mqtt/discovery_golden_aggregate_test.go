// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/event"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

var updateAggregateGolden = flag.Bool("update-aggregate-golden", false,
	"rewrite the pinned channel-aggregate discovery payloads")

var aggregateGoldenPath = filepath.Join("testdata", "discovery_golden_aggregate.json")

// aggregateGoldenBase is the topic base every fixture publishes under,
// short so a diff of a state or command topic stays readable.
const aggregateGoldenBase = "gh"

// aggregateGoldenCentral is the owning central. It travels into the node
// id of every entity on this plane (`<central>_<device address>`), which
// is why it is a constant rather than a per-case literal.
const aggregateGoldenCentral = "ccu-01"

// aggregateGoldenSerial is the hub serial the central-scoped identities
// resolve against. A virtual-remote address repeats verbatim on every
// CCU, so its unique_id carries this serial; a real device serial's must
// not.
const aggregateGoldenSerial = "3014F711A0001234"

// ─── fixtures ────────────────────────────────────────────────────────

// aggregateWireDP is one wire parameter as the CCU describes it. The
// custom-DP materializer binds its profile fields against these, so a
// fixture channel has to carry them before it can carry a custom data
// point at all.
type aggregateWireDP struct {
	parameter string
	typ       hmenum.ParameterType
	min       string
	max       string
	unit      string
	valueList []string
}

// aggregateWireDescriptor invents a plausible CCU descriptor for a
// profile-required parameter the fixture does not describe explicitly.
//
// Only a handful of descriptors reach the discovery body at all — the
// climate bounds and unit are the visible ones — and those are stated
// per fixture. Everything else only has to type-resolve so the
// materializer binds the field, which is what this covers. It is an
// approximation of the CCU's descriptor, not a capture of it: a payload
// key whose value comes from an unnamed parameter's bounds is pinned at
// this fixture's guess, not at the fleet's value.
func aggregateWireDescriptor(param string) aggregateWireDP {
	dp := aggregateWireDP{parameter: param, typ: hmenum.ParameterTypeFloat, min: "0", max: "100"}
	switch {
	case param == "STATE" || param == "STOP" || param == "BOOST_MODE" || param == "PARTY_MODE" ||
		param == "GLOBAL_BUTTON_LOCK" || param == "OPTIMUM_START_STOP" || param == "ERROR_JAMMED" ||
		param == "MIN_MAX_VALUE_NOT_RELEVANT_FOR_MANU_MODE" || strings.HasSuffix(param, "_ACTIVE"):
		dp.typ = hmenum.ParameterTypeBool
	case strings.HasSuffix(param, "_MODE") || strings.HasSuffix(param, "_STATE") ||
		strings.HasSuffix(param, "_SELECTION") || strings.HasSuffix(param, "_UNIT") ||
		param == "COLOR_BEHAVIOUR" || param == "HEATING_COOLING" || param == "HEATING_VALVE_TYPE" ||
		param == "LOCK_TARGET_LEVEL" || param == "CHANNEL_OPERATION_MODE" || param == "DOOR_COMMAND":
		dp.typ = hmenum.ParameterTypeEnum
		dp.valueList = []string{"OPTION_A", "OPTION_B"}
	case param == "COMBINED_PARAMETER":
		dp.typ = hmenum.ParameterTypeString
	}
	return dp
}

// putAggregateWireDP attaches one wire data point to ch in the generic
// shape the resolver would have classified it into.
func putAggregateWireDP(ch *device.Channel, dp aggregateWireDP) {
	spec := generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      dp.parameter,
		},
		Descriptor: hmproto.ParameterData{
			Type:       dp.typ,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent | hmenum.OperationsWrite,
			Min:        rawNumber(dp.min),
			Max:        rawNumber(dp.max),
			Unit:       dp.unit,
			ValueList:  dp.valueList,
		},
	}
	switch dp.typ {
	case hmenum.ParameterTypeBool:
		ch.Put(generic.NewSwitch(spec))
	case hmenum.ParameterTypeEnum:
		ch.Put(generic.NewSelect(spec))
	case hmenum.ParameterTypeString:
		ch.Put(generic.NewText(spec))
	default:
		ch.Put(generic.NewFloat(spec))
	}
}

// rawNumber renders a numeric descriptor bound, or nil when unset —
// hmproto keeps MIN/MAX as raw JSON.
func rawNumber(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	return json.RawMessage(s)
}

// aggregateFixture is one synthetic device: the channels it carries and
// the descriptors that differ from [aggregateWireDescriptor]'s guess.
//
// Every fixture device is synthetic — no capture of a real CCU is
// reachable from CI. What makes them usable as a pin is that the custom
// data point on them is built by the production materializer against the
// production profile registry, so the body under test is composed the way
// a fleet composes it even though the descriptors feeding it are invented.
type aggregateFixture struct {
	model      string
	address    string
	deviceName string
	iface      string
	// channels maps channel number to the CCU channel-type marker.
	channels map[int]string
	// explicit overrides the invented descriptor for the parameters
	// whose bounds or unit reach the discovery body.
	explicit []aggregateWireDP
}

// newAggregateDevice materialises one fixture through the real registry:
// the profile catalogue picks the custom-DP family, the materializer
// binds its fields, and the channel ends up carrying the same
// [payload.Source] production hands the aggregator.
func newAggregateDevice(t *testing.T, f aggregateFixture) *device.Device {
	t.Helper()
	iface := f.iface
	if iface == "" {
		iface = "HmIP-RF"
	}
	dev := device.New(device.Config{
		InterfaceID:  iface,
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      f.address,
		Model:        f.model,
		Name:         f.deviceName,
		Manufacturer: hmenum.ManufacturerEQ3,
		ProductGroup: hmenum.ProductGroupHmIP,
	})
	reg := custom.DefaultRegistry()

	explicit := make(map[string]aggregateWireDP, len(f.explicit))
	for _, dp := range f.explicit {
		explicit[dp.parameter] = dp
	}
	var required []string
	for _, p := range reg.ForDevice(f.model) {
		for _, rp := range p.RequiredParameters() {
			required = append(required, string(rp))
		}
	}

	numbers := make([]int, 0, len(f.channels))
	for no := range f.channels {
		numbers = append(numbers, no)
	}
	sort.Ints(numbers)
	for _, no := range numbers {
		ch := dev.AddChannel(f.address+":"+strconv.Itoa(no), no, f.channels[no], hmenum.ParamsetKeyValues)
		for _, param := range required {
			if dp, ok := explicit[param]; ok {
				putAggregateWireDP(ch, dp)
				continue
			}
			putAggregateWireDP(ch, aggregateWireDescriptor(param))
		}
	}
	if err := custom.CreateCustomDataPoints(dev, reg); err != nil {
		t.Fatalf("%s: materialize custom data points: %v", f.model, err)
	}
	return dev
}

// aggregateEvent composes the bridge event for one channel of a fixture
// device, in the shape the south-bound event bridge hands the builder.
func aggregateEvent(t *testing.T, dev *device.Device, channelNo int, central string) Event {
	t.Helper()
	ch := dev.Channel(dev.Address + ":" + strconv.Itoa(channelNo))
	if ch == nil {
		t.Fatalf("%s: no channel %d", dev.Address, channelNo)
	}
	ev := Event{
		Central:        central,
		Interface:      dev.InterfaceID,
		DeviceAddress:  dev.Address,
		DeviceName:     dev.Name(),
		Model:          dev.Model,
		ChannelNo:      channelNo,
		ChannelAddress: ch.Address,
		ChannelType:    ch.Type,
		Device:         dev,
		Channel:        ch,
	}
	if cdp, ok := ch.CustomDataPoint().(payload.Source); ok {
		ev.Source = cdp
	}
	return ev
}

// ─── the fixture matrix ──────────────────────────────────────────────

// aggregateGoldenCase is one built entity plus the name a diff reports.
// The builder is named per case because this plane has four entry points
// and a case has to say which one it came through.
type aggregateGoldenCase struct {
	name  string
	build func(t *testing.T, b *DefaultDiscoveryBuilder) (component, nodeID, objectID string, buf []byte, ok bool)
	// subDevices selects the builder with the sub-device split enabled.
	subDevices bool
}

// climateFixture is the thermostat. Its temperature bounds and unit are
// the one set of wire descriptors on this plane that reach the body
// verbatim (min_temp / max_temp / temperature_unit), so they are stated
// rather than invented.
func climateFixture() aggregateFixture {
	return aggregateFixture{
		model: "HmIP-BWTH", address: "0001BWTH0001", deviceName: "Wandthermostat Wohnzimmer",
		channels: map[int]string{1: "HEATING_CLIMATECONTROL_TRANSCEIVER"},
		explicit: []aggregateWireDP{
			{parameter: "SET_POINT_TEMPERATURE", typ: hmenum.ParameterTypeFloat, min: "4.5", max: "30.5", unit: "°C"},
			{parameter: "ACTUAL_TEMPERATURE", typ: hmenum.ParameterTypeFloat, min: "-20.0", max: "70.0", unit: "°C"},
			{parameter: "HUMIDITY", typ: hmenum.ParameterTypeInteger, min: "0", max: "100", unit: "%"},
			{parameter: "LEVEL", typ: hmenum.ParameterTypeFloat, min: "0.0", max: "1.0", unit: "100%"},
		},
	}
}

// aggregateCustomDPCases covers the custom-DP aggregate — one case per
// builder family the plane can reach, plus the naming and identity
// branches the aggregator adds on top of whatever the family returned.
//
// One family per case and no more: the body below the frame belongs to
// the model-side builder, and pinning six light variants here would pin
// that plane through this one.
func aggregateCustomDPCases() []aggregateGoldenCase {
	agg := func(f aggregateFixture, channelNo int) func(*testing.T, *DefaultDiscoveryBuilder) (string, string, string, []byte, bool) {
		return func(t *testing.T, b *DefaultDiscoveryBuilder) (string, string, string, []byte, bool) {
			t.Helper()
			dev := newAggregateDevice(t, f)
			return b.aggregateChannel(aggregateEvent(t, dev, channelNo, aggregateGoldenCentral))
		}
	}
	return []aggregateGoldenCase{
		// Identity hazard: an absent entity-id seed. A device with one
		// primary custom data point publishes `name: null`, which tells
		// Home Assistant to seed the entity id from the device name
		// alone. An empty string, or the key going missing, both make HA
		// derive a different id and orphan the entity that already
		// exists under the old one. Every single-primary case below
		// carries it; this is the one that exists to say so.
		{name: "switch/single-primary-name-null", build: agg(aggregateFixture{
			model: "HmIP-BSM", address: "0001BSM00001", deviceName: "Deckenlicht Flur",
			channels: map[int]string{3: "SWITCH_TRANSMITTER", 4: "SWITCH_VIRTUAL_RECEIVER"},
		}, 4)},
		{name: "climate/thermostat", build: agg(climateFixture(), 1)},
		{name: "cover/blind", build: agg(aggregateFixture{
			model: "HmIP-BROLL", address: "0001BROLL001", deviceName: "Rollladen Küche",
			channels: map[int]string{3: "BLIND_TRANSMITTER", 4: "BLIND_VIRTUAL_RECEIVER"},
			explicit: []aggregateWireDP{
				{parameter: "LEVEL", typ: hmenum.ParameterTypeFloat, min: "0.0", max: "1.0", unit: "100%"},
				{parameter: "LEVEL_2", typ: hmenum.ParameterTypeFloat, min: "0.0", max: "1.0", unit: "100%"},
			},
		}, 4)},
		// The second cover builder. Garage resolves to the same
		// `cover` component and the same object id as a blind would on
		// the same channel, so the two are only told apart by the body.
		{name: "cover/garage", build: agg(aggregateFixture{
			model: "HmIP-MOD-HO", address: "0001MODHO001", deviceName: "Garagentor",
			channels: map[int]string{1: "GARAGE_DOOR_CHANNEL"},
		}, 1)},
		{name: "lock/door-lock-drive", build: agg(aggregateFixture{
			model: "HmIP-DLD", address: "0001DLD00001", deviceName: "Haustür",
			channels: map[int]string{1: "DOOR_LOCK_STATE_TRANSMITTER"},
		}, 1)},
		{name: "light/dimmer", build: agg(aggregateFixture{
			model: "HmIP-BDT", address: "0001BDT00001", deviceName: "Esstisch",
			channels: map[int]string{4: "DIMMER_VIRTUAL_RECEIVER"},
			explicit: []aggregateWireDP{
				{parameter: "LEVEL", typ: hmenum.ParameterTypeFloat, min: "0.0", max: "1.0", unit: "100%"},
			},
		}, 4)},
		// The siren's `available_tones` comes straight from the
		// ACOUSTIC_ALARM_SELECTION VALUE_LIST, which this fixture
		// invents — the pinned tone names are the fixture's, and only
		// the fact that the list is carried through is a claim about
		// the plane.
		{name: "siren/alarm-siren", build: agg(aggregateFixture{
			model: "HmIP-ASIR", address: "0001ASIR0001", deviceName: "Sirene Diele",
			channels: map[int]string{3: "SIREN_VIRTUAL_RECEIVER"},
		}, 3)},
		{name: "valve/irrigation", build: agg(aggregateFixture{
			model: "ELV-SH-WSM", address: "0001WSM00001", deviceName: "Bewässerung Beet",
			channels: map[int]string{4: "WATERING_ACTUATOR"},
		}, 4)},
		// A device with four primary custom data points of the same
		// family. The entity name is no longer null but `ch<N>`, and
		// that N is the entity-id seed for all four: collapse it and
		// Home Assistant dedups the four onto one id with `_2` suffixes.
		{name: "cover/multi-primary-ch-name", build: agg(multiGroupCoverFixture(), 10)},
		// Identity hazard: a node id that is not the device identifier.
		// With sub-devices on, the device block re-identifies as
		// `<parent>-<group>` and re-parents to the physical device,
		// while the discovery topic's node id stays the physical device.
		// The two are derived by different code from different inputs; a
		// move that quietly aligned them would re-key every entity on
		// every multi-group actuator on the fleet.
		{name: "cover/sub-device-split", subDevices: true, build: agg(multiGroupCoverFixture(), 10)},
	}
}

// multiGroupCoverFixture is the four-group blind actuator. Its channel
// groups are what make both the `ch<N>` naming branch and the sub-device
// split reachable — a single-group device produces neither.
func multiGroupCoverFixture() aggregateFixture {
	channels := map[int]string{}
	for no := 9; no <= 24; no++ {
		channels[no] = "BLIND_VIRTUAL_RECEIVER"
	}
	return aggregateFixture{
		model: "HmIP-DRBLI4", address: "0001DRBLI401", deviceName: "Jalousieaktor Süd",
		channels: channels,
		explicit: []aggregateWireDP{
			{parameter: "LEVEL", typ: hmenum.ParameterTypeFloat, min: "0.0", max: "1.0", unit: "100%"},
			{parameter: "LEVEL_2", typ: hmenum.ParameterTypeFloat, min: "0.0", max: "1.0", unit: "100%"},
		},
	}
}

// eventFixture is a channel-level event fixture: the parameters the
// channel exposes decide which of the three event builders fires and
// what its `event_types` list says.
type eventFixture struct {
	model       string
	address     string
	deviceName  string
	iface       string
	channelNo   int
	channelType string
	channelName string
	parameters  []string
	central     string
}

// newEventChannel builds a device carrying one event channel. No custom
// data point is materialised: the event builders read the channel's
// parameter list, not a profile.
func newEventChannel(t *testing.T, f eventFixture) (*device.Device, *device.Channel) {
	t.Helper()
	iface := f.iface
	if iface == "" {
		iface = "HmIP-RF"
	}
	dev := device.New(device.Config{
		InterfaceID:  iface,
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      f.address,
		Model:        f.model,
		Name:         f.deviceName,
		Manufacturer: hmenum.ManufacturerEQ3,
		ProductGroup: hmenum.ProductGroupHmIP,
	})
	ch := dev.AddChannel(f.address+":"+strconv.Itoa(f.channelNo), f.channelNo, f.channelType,
		hmenum.ParamsetKeyValues)
	if f.channelName != "" {
		ch.SetName(f.channelName)
	}
	for _, p := range f.parameters {
		putAggregateWireDP(ch, aggregateWireDP{parameter: p, typ: hmenum.ParameterTypeAction})
	}
	return dev, ch
}

// eventEvent composes the bridge event for one wire parameter on an
// event channel.
func eventEvent(t *testing.T, f eventFixture, parameter string) Event {
	t.Helper()
	dev, ch := newEventChannel(t, f)
	central := f.central
	if central == "" {
		central = aggregateGoldenCentral
	}
	return Event{
		Central:        central,
		Interface:      dev.InterfaceID,
		DeviceAddress:  dev.Address,
		DeviceName:     dev.Name(),
		Model:          dev.Model,
		ChannelNo:      f.channelNo,
		ChannelAddress: ch.Address,
		Parameter:      parameter,
		Category:       hmenum.DataPointCategoryEvent,
		Device:         dev,
		Channel:        ch,
	}
}

// remoteFixture is the four-press-type wall remote the keypress cases
// vary. Only the fields a case changes are set by the case itself.
func remoteFixture() eventFixture {
	return eventFixture{
		model: "HmIP-BRC2", address: "0001BRC20001", deviceName: "Taster Wohnzimmer",
		channelNo: 1, channelType: "KEY_TRANSCEIVER",
		parameters: []string{"PRESS_SHORT", "PRESS_LONG", "PRESS_LONG_START", "PRESS_LONG_RELEASE"},
	}
}

// aggregateEventCases cover the three channel-level event entities and
// the branches that move their identity or their announced vocabulary.
func aggregateEventCases() []aggregateGoldenCase {
	press := func(f eventFixture) func(*testing.T, *DefaultDiscoveryBuilder) (string, string, string, []byte, bool) {
		return func(t *testing.T, b *DefaultDiscoveryBuilder) (string, string, string, []byte, bool) {
			t.Helper()
			return b.BuildChannelEvent(eventEvent(t, f, "PRESS_SHORT"))
		}
	}
	kind := func(f eventFixture, parameter string, k event.Kind) func(*testing.T, *DefaultDiscoveryBuilder) (string, string, string, []byte, bool) {
		return func(t *testing.T, b *DefaultDiscoveryBuilder) (string, string, string, []byte, bool) {
			t.Helper()
			return b.BuildChannelKindEvent(eventEvent(t, f, parameter), k)
		}
	}

	namedRemote := remoteFixture()
	namedRemote.channelName = "Taster Wohnzimmer oben links"

	// A channel the operator never renamed: the CCU reports the bare
	// `<address>:<channel no>` form, which Home Assistant would slugify
	// down to the address alone and collide two channels of the same
	// device onto one entity id.
	bareRemote := remoteFixture()
	bareRemote.channelName = "0001BRC20001:1"

	doorbell := remoteFixture()
	doorbell.model, doorbell.address = "HmIP-DBB", "0001DBB00001"
	doorbell.deviceName = "Klingeltaster"
	doorbell.parameters = []string{"PRESS_SHORT"}

	// Identity hazard: a synthetic device on a central-scoped address.
	// The CCU's virtual-remote buses are not hardware — every CCU mints
	// the same `BidCoS-RF` address — so the key has to carry the
	// central's serial or two CCUs collide into one entity and Home
	// Assistant keeps whichever config arrived first.
	virtualRemote := eventFixture{
		model: "HM-RCV-50", address: "BidCoS-RF", deviceName: "Virtuelle Taste",
		iface: "BidCos-RF", channelNo: 3, channelType: "KEY",
		parameters: []string{"PRESS_SHORT", "PRESS_LONG"},
	}
	virtualRemoteCCU2 := virtualRemote
	virtualRemoteCCU2.central = "ccu-02"

	impulse := eventFixture{
		model: "HmIP-SRD", address: "0001SRD00001", deviceName: "Regensensor",
		channelNo: 1, channelType: "RAIN_DETECTION_TRANSMITTER",
		parameters: []string{"SEQUENCE_OK"},
	}
	// The device-error channel carries an open-ended ERROR_* parameter
	// on top of the two known roots. Home Assistant drops an event whose
	// type is not announced, so the announced list has to come from the
	// channel's own parameter names rather than from the root list.
	deviceError := eventFixture{
		model: "HmIP-SWDO", address: "0001SWDO0001", deviceName: "Fenster Bad",
		channelNo: 1, channelType: "SHUTTER_CONTACT",
		parameters: []string{"ERROR", "SENSOR_ERROR", "ERROR_OVERHEAT"},
	}

	return []aggregateGoldenCase{
		{name: "event/keypress-channel-number", build: press(remoteFixture())},
		// The operator's channel name is the entity-id seed here, not a
		// decoration: renaming the channel in the CCU moves the entity.
		{name: "event/keypress-operator-named", build: press(namedRemote)},
		{name: "event/keypress-bare-address-name", build: press(bareRemote)},
		// A doorbell announces `ring` where every other remote announces
		// `press_short`, and carries device_class doorbell.
		{name: "event/keypress-doorbell", build: press(doorbell)},
		{name: "event/keypress-virtual-remote-ccu-01", build: press(virtualRemote)},
		// The same synthetic address on a second central. Pinned as the
		// pair: the two unique_ids must differ, and they must differ by
		// the serial rather than by anything a rename could touch.
		{name: "event/keypress-virtual-remote-ccu-02", build: press(virtualRemoteCCU2)},
		{name: "event/impulse", build: kind(impulse, "SEQUENCE_OK", event.KindImpulse)},
		{name: "event/device-error", build: kind(deviceError, "ERROR", event.KindDeviceError)},
	}
}

func aggregateGoldenCases() []aggregateGoldenCase {
	return append(aggregateCustomDPCases(), aggregateEventCases()...)
}

// ─── the pin ─────────────────────────────────────────────────────────

// TestAggregateDiscoveryPayloadsArePinned pins what the channel-aggregate
// plane (discovery_aggregate.go) puts on the wire, so moving it onto the
// shared go-hamqtt model (ADR 0070) can be shown to change nothing an
// installed Home Assistant sees.
//
// Topic and payload are both pinned. The topic is not decoration: this
// plane's node id is `<central>_<device address>` while the device block
// identifies the same device as `openccu-loom_<address>`, and the object
// id carries the channel number and the family leaf. A changed node id, a
// changed object id or a changed unique_id each orphan the installed
// entity and stand a new one beside it — with nothing in any log and
// nothing in Home Assistant's UI to say what happened.
//
// What it covers: the four entry points this plane exposes — the
// custom-DP aggregate ([DefaultDiscoveryBuilder.aggregateChannel]), the
// channel-level keypress entity
// ([DefaultDiscoveryBuilder.BuildChannelEvent]) and the impulse and
// device-error entities
// ([DefaultDiscoveryBuilder.BuildChannelKindEvent]) — with one case per
// custom-DP family the aggregate can reach (switch, climate, cover, lock,
// light, siren, valve), plus the identity hazards the plane carries:
// `name: null`, the `ch<N>` seed on a multi-primary device, the
// sub-device split, and a synthetic central-scoped address.
//
// What this does NOT cover, stated plainly because the matrix reads
// broader than it is:
//
//   - The bodies BELOW the frame belong to the model-side custom-DP
//     builders, not to this plane. One family is pinned per builder
//     package; the variants inside a family (ColorLight, EffectLight,
//     RGBWLight, SmokeSiren, SoundPlayer, Modulating valve,
//     AccessPermission) are not, and neither is the TextDisplay path —
//     this plane suppresses it and publishes a notify companion
//     elsewhere.
//   - The wire descriptors these fixtures feed the materializer are
//     invented, not captured. Where a descriptor reaches the body it is
//     stated per fixture (the climate bounds and unit); anywhere else the
//     pinned value is this fixture's guess and says nothing about a
//     fleet's.
//   - Entity names are pinned at their untranslated fallbacks, so a
//     locale change is out of scope, as is the climate preset localiser
//     for any preset the catalogue does not carry.
//   - The state, command and availability topics inside these bodies are
//     pinned as strings; whether anything publishes to them is the
//     round-trip guard's job.
//   - It is a per-shape pin, not a fleet capture. The ~9,996 retained
//     configs a real CCU produces cannot be reconstructed in CI, and a
//     pin that looked complete would be worse than one that admits its
//     scope — see notes/parity/ for the operator-side capture.
//
// A diff here is a regression until someone shows otherwise. Refresh with:
//
//	go test ./internal/north/mqtt/ -run TestAggregateDiscoveryPayloadsArePinned -update-aggregate-golden
func TestAggregateDiscoveryPayloadsArePinned(t *testing.T) {
	tb := NewTopicBuilder(aggregateGoldenBase)

	got := map[string]goldenEntry{}
	for _, c := range aggregateGoldenCases() {
		component, nodeID, objectID, buf, ok := newAggregateGoldenBuilder(c.subDevices).
			buildGoldenCase(t, c)
		if !ok {
			t.Fatalf("%s: builder returned ok=false — the fixture no longer produces an entity", c.name)
		}
		// Decoded to a map so the pin is stable against field order in
		// the builder and unstable against a changed key or value.
		var body map[string]any
		if err := json.Unmarshal(buf, &body); err != nil {
			t.Fatalf("%s: unmarshal: %v", c.name, err)
		}
		got[c.name] = goldenEntry{
			Topic:   tb.DiscoveryConfig(component, nodeID, objectID),
			Payload: body,
		}
	}

	if *updateAggregateGolden {
		writeAggregateGolden(t, got)
		t.Logf("rewrote %s with %d payloads", aggregateGoldenPath, len(got))
		return
	}

	want := readAggregateGolden(t)
	names := make([]string, 0, len(got))
	for name := range got {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		w, pinned := want[name]
		if !pinned {
			t.Errorf("%s: not in the pin — a new shape appeared; refresh the pin deliberately", name)
			continue
		}
		if got[name].Topic != w.Topic {
			t.Errorf("%s: topic\n got %s\nwant %s\n(a changed node id or object id orphans every entity under it, silently)",
				name, got[name].Topic, w.Topic)
		}
		if g, wantBody := canonical(t, got[name].Payload), canonical(t, w.Payload); g != wantBody {
			t.Errorf("%s: payload\n got %s\nwant %s", name, g, wantBody)
		}
	}
	for name := range want {
		if _, still := got[name]; !still {
			t.Errorf("%s: in the pin but no longer produced — an entity disappeared", name)
		}
	}
}

// aggregateGoldenBuilder is the discovery builder the fixtures run
// through, with the hub serial the central-scoped identities need.
type aggregateGoldenBuilder struct{ *DefaultDiscoveryBuilder }

func newAggregateGoldenBuilder(subDevices bool) aggregateGoldenBuilder {
	b := NewDefaultDiscoveryBuilder(NewTopicBuilder(aggregateGoldenBase), aggregateGoldenCentral).
		WithSubDevices(subDevices)
	b.SetHubInfoFor("ccu-01", HubInfo{Serial: aggregateGoldenSerial})
	b.SetHubInfoFor("ccu-02", HubInfo{Serial: "3014F711B0005678"})
	return aggregateGoldenBuilder{b}
}

func (b aggregateGoldenBuilder) buildGoldenCase(t *testing.T, c aggregateGoldenCase) (
	component, nodeID, objectID string, buf []byte, ok bool,
) {
	t.Helper()
	return c.build(t, b.DefaultDiscoveryBuilder)
}

// aggregateGoldenSchemaRejections names the pinned cases whose body the
// Home Assistant schema rejects today, with the reason each is recorded
// rather than fixed.
//
// Unlike the per-parameter plane's unreachable shapes, this one is LIVE:
// the device-error event entity is published for every channel carrying
// an ERROR / SENSOR_ERROR / ERROR_* parameter, which is most sensors on
// a fleet.
//
//   - event/device-error: the builder sets `device_class: "problem"`
//     (discovery_aggregate.go, the device-error arm of
//     BuildChannelKindEvent). Home Assistant's event platform declares
//     exactly three device classes — button, doorbell, motion — so the
//     config is rejected and the entity is never created. The channel's
//     errors reach the REST and WebSocket planes either way, which is
//     why nothing surfaced it.
//
// Not fixed here on purpose: this change is a pin, and a pin that also
// changes behaviour cannot show that behaviour did not change. The fix
// belongs in its own change, where the entity appearing for the first
// time is the visible, intended effect.
var aggregateGoldenSchemaRejections = map[string]struct{}{
	"event/device-error": {},
}

// TestAggregatePinnedPayloadsAreValid reads the same fixtures back
// through the extracted Home Assistant MQTT schema, so a payload that is
// pinned is also a payload HA accepts. A pin on its own only says the
// bytes did not move; it cannot say they were right to begin with.
//
// [aggregateGoldenSchemaRejections] is asserted rather than skipped: if
// one of those bodies ever starts validating, the entry has outlived its
// reason and the case belongs back in the checked set.
func TestAggregatePinnedPayloadsAreValid(t *testing.T) {
	for _, c := range aggregateGoldenCases() {
		component, _, _, buf, ok := newAggregateGoldenBuilder(c.subDevices).buildGoldenCase(t, c)
		if !ok {
			t.Fatalf("%s: builder returned ok=false", c.name)
		}
		err := ValidateDiscoveryBody(component, buf)
		if _, rejected := aggregateGoldenSchemaRejections[c.name]; rejected {
			if err == nil {
				t.Errorf("%s: now valid — drop it from aggregateGoldenSchemaRejections", c.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func readAggregateGolden(t *testing.T) map[string]goldenEntry {
	t.Helper()
	raw, err := os.ReadFile(aggregateGoldenPath)
	if err != nil {
		t.Fatalf("read %s: %v (create it with -update-aggregate-golden)", aggregateGoldenPath, err)
	}
	var out map[string]goldenEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse %s: %v", aggregateGoldenPath, err)
	}
	return out
}

func writeAggregateGolden(t *testing.T, entries map[string]goldenEntry) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(aggregateGoldenPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(aggregateGoldenPath, append(raw, '\n'), 0o600); err != nil {
		t.Fatalf("write %s: %v", aggregateGoldenPath, err)
	}
}
