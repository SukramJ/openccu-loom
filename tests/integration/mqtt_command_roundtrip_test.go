// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

// Package integration — MQTT command (write) round-trip.
//
// These tests exercise the INBOUND half of the MQTT bridge that the
// availability / discovery snapshot tests do not touch: the write path
//
//	broker → CommandSubscriber → MQTTCommandSink → ValueWriter → CCU
//
// against a real Mosquitto broker and a godevccu virtual CCU. A publish
// on the raw-plane `…/values/<param>/set` topic (and on the ADR-0011
// custom-DP service-method `…/custom/<kind>/set/<method>` topic) must
// reach godevccu and change device state; the resulting value echo is
// re-published on the raw-plane state topic, whose on-the-wire VALUE the
// state subscriber asserts.
//
// Gated exactly like the sibling real-broker tests: startMosquitto skips
// automatically when neither a Docker daemon nor a native `mosquitto`
// binary is reachable.
package integration

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

const (
	cmdRoundtripBase    = "gh"
	cmdRoundtripCentral = "ccu-01"
)

// capturedSet records one godevccu-applied write (SetValue / PutParamset)
// so the tests can assert a command reached the virtual CCU.
type capturedSet struct {
	address  string
	valueKey string
	value    any
}

// commandChainRig wires the full production MQTT write path against a
// real broker + godevccu: a real CommandSubscriber consumes the broker's
// `…/set` topics, the MQTTCommandSink dispatches into the central, the
// backendValueWriter writes to godevccu, and a wired EventBridge +
// Bridge re-publish the value echo onto the raw-plane state topic.
type commandChainRig struct {
	central *central.Unit
	topics  *mqtt.TopicBuilder
	cmdPub  *mqtt.TCPClient

	capMu    sync.Mutex
	setCalls []capturedSet

	stateMu     sync.Mutex
	stateTopics map[string][]byte
}

// recordSet appends a godevccu write to the capture log.
func (r *commandChainRig) recordSet(address, valueKey string, value any) {
	r.capMu.Lock()
	r.setCalls = append(r.setCalls, capturedSet{address: address, valueKey: valueKey, value: value})
	r.capMu.Unlock()
}

// injectEcho mirrors the production CCU→callback path: after godevccu
// applies a SetValue it would push the confirmed value back over the
// XML-RPC callback, which (a) feeds the model DP via OnWireValue
// (internal/central/adapter/callback_handlers.go:259) and (b) makes the
// EventCoordinator publish a DataPointValueChangedEvent onto the central
// bus (internal/central/coordinators/event.go:175). Both hops run here so
// the wired EventBridge re-publishes the confirmed value onto the raw-plane
// state topic exactly as it would on a real echo.
func (r *commandChainRig) injectEcho(address, valueKey string, value any) {
	if r.central == nil {
		return
	}
	devAddr := address
	if i := strings.LastIndexByte(address, ':'); i > 0 {
		devAddr = address[:i]
	}
	dev, ok := r.central.ModelRegistry.Get(devAddr)
	if !ok || dev == nil {
		return
	}
	ch := dev.Channel(address)
	if ch == nil {
		return
	}
	if dp := ch.Parameter(hmenum.Parameter(valueKey)); dp != nil {
		if setter, ok := dp.(interface{ OnWireValue(any) bool }); ok {
			setter.OnWireValue(value)
		}
	}
	pv, err := hmtypes.NewParamValue(value)
	if err != nil {
		return
	}
	events.Publish(r.central.EventBus, hmevent.DataPointValueChangedEvent{
		Base: hmevent.NewBase(),
		Key: hmtypes.DataPointKey{
			InterfaceID:    string(dev.Interface),
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      valueKey,
		},
		OldValue: hmtypes.NoneValue(),
		NewValue: pv,
	})
}

// captureState records a broker-delivered state publish (topic → payload).
func (r *commandChainRig) captureState(topic string, pload []byte, _ bool) {
	cp := make([]byte, len(pload))
	copy(cp, pload)
	r.stateMu.Lock()
	r.stateTopics[topic] = cp
	r.stateMu.Unlock()
}

// waitForSet polls the capture log until pred matches a recorded write or
// the timeout elapses. Returns the matching capture and true on success.
func (r *commandChainRig) waitForSet(pred func(capturedSet) bool, timeout time.Duration) (capturedSet, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r.capMu.Lock()
		for _, c := range r.setCalls {
			if pred(c) {
				r.capMu.Unlock()
				return c, true
			}
		}
		r.capMu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	return capturedSet{}, false
}

// waitForStateValue polls the captured state topics until one whose topic
// contains topicSubstr carries a payload containing payloadSubstr.
func (r *commandChainRig) waitForStateValue(topicSubstr, payloadSubstr string, timeout time.Duration) (string, string, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r.stateMu.Lock()
		for topic, pl := range r.stateTopics {
			if strings.Contains(strings.ToLower(topic), strings.ToLower(topicSubstr)) &&
				strings.Contains(string(pl), payloadSubstr) {
				r.stateMu.Unlock()
				return topic, string(pl), true
			}
		}
		r.stateMu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	return "", "", false
}

// setupCommandChain boots the entire write path and returns the rig. The
// caller publishes commands via rig.cmdPub and asserts on the capture log
// / state topics. Skips (via startMosquitto) when no broker is available.
func setupCommandChain(t *testing.T) *commandChainRig {
	t.Helper()
	return setupCommandChainWithDevices(t, defaultMockDevices)
}

// setupCommandChainWithDevices is setupCommandChain with an explicit
// godevccu device fleet, for tests that need a model outside the default
// set (e.g. the HmIP-RCV-50 virtual remote).
func setupCommandChainWithDevices(t *testing.T, devices []string) *commandChainRig {
	t.Helper()
	broker := startMosquitto(t) // skips the whole test when unavailable

	rig := &commandChainRig{stateTopics: make(map[string][]byte)}

	onSet := func(address, valueKey string, value any) {
		rig.recordSet(address, valueKey, value)
		rig.injectEcho(address, valueKey, value)
	}
	mock := startMockCCUWithOptions(t, devices, onSet)

	xmlClient := newXMLRPCClient(t, mock.URL())
	caller := &xmlrpcBackendCaller{client: xmlClient}
	backend := backends.NewCcuBackend(caller, nil, nil)

	c, err := central.New(central.Config{Name: cmdRoundtripCentral})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}
	rig.central = c

	valueWriter := backendValueWriter{backend: backend}
	translations, err := ccudata.LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("LoadTranslationsEmbedded: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	ingestCtx, ingestCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer ingestCancel()
	pipeline := adapter.NewDevicePipeline(c).WithTranslations(translations, snapshotLocale())
	if err := pipeline.IngestFromBackend(ingestCtx, "HmIP-RF", hmenum.InterfaceHmIPRF, backend, valueWriter, nil, logger); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}
	if c.ModelRegistry.Len() == 0 {
		t.Fatal("ingest produced no devices")
	}

	topics := mqtt.NewTopicBuilder(cmdRoundtripBase)
	rig.topics = topics

	// A daemon-lifetime context for the always-on subscribers/bridge; the
	// per-op connect uses its own short-lived context.
	lifeCtx, lifeCancel := context.WithCancel(context.Background())
	t.Cleanup(lifeCancel)

	connectCtx, connectCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer connectCancel()

	// --- state subscriber: capture the raw-plane republish ----------------
	stateSub := mqtt.NewTCPClient(mqtt.TCPConfig{
		BrokerURL: broker.URL(), ClientID: "cmd-state-sub", KeepAlive: 30 * time.Second, CleanStart: true,
	})
	if err := stateSub.Connect(connectCtx); err != nil {
		t.Fatalf("state subscriber connect: %v", err)
	}
	t.Cleanup(func() { _ = stateSub.Disconnect(context.Background()) })
	if _, err := stateSub.Subscribe(connectCtx, cmdRoundtripBase+"/#", mqtt.QoS1, mqtt.LegacyHandler(rig.captureState)); err != nil {
		t.Fatalf("state subscribe: %v", err)
	}

	// --- bridge publisher + EventBridge: the state republish path ---------
	bridgePub := mqtt.NewTCPClient(mqtt.TCPConfig{
		BrokerURL: broker.URL(), ClientID: "cmd-bridge-pub", KeepAlive: 30 * time.Second, CleanStart: true,
	})
	if err := bridgePub.Connect(connectCtx); err != nil {
		t.Fatalf("bridge publisher connect: %v", err)
	}
	t.Cleanup(func() { _ = bridgePub.Disconnect(context.Background()) })
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:               cmdRoundtripBase,
		CentralName:        cmdRoundtripCentral,
		RawEnabled:         true,
		HADiscoveryEnabled: true,
	}, bridgePub)
	wiring := mqtt.NewWiring(bridge, logger)
	eb := adapter.NewEventBridge(reg, nil, wiring)
	eb.Start(lifeCtx)
	t.Cleanup(eb.Stop)

	// --- command subscriber: the real inbound write path ------------------
	cmdSubClient := mqtt.NewTCPClient(mqtt.TCPConfig{
		BrokerURL: broker.URL(), ClientID: "cmd-sub", KeepAlive: 30 * time.Second, CleanStart: true,
	})
	if err := cmdSubClient.Connect(connectCtx); err != nil {
		t.Fatalf("command subscriber connect: %v", err)
	}
	t.Cleanup(func() { _ = cmdSubClient.Disconnect(context.Background()) })
	sink := adapter.NewMQTTCommandSink(reg, valueWriter)
	cmdSub := mqtt.NewCommandSubscriber(cmdSubClient, topics, sink, logger).
		WithCDPSink(sink).
		WithLifecycleContext(lifeCtx)
	if err := cmdSub.Start(lifeCtx); err != nil {
		t.Fatalf("command subscriber start: %v", err)
	}
	t.Cleanup(cmdSub.Close)

	// --- command publisher ------------------------------------------------
	cmdPub := mqtt.NewTCPClient(mqtt.TCPConfig{
		BrokerURL: broker.URL(), ClientID: "cmd-pub", KeepAlive: 30 * time.Second, CleanStart: true,
	})
	if err := cmdPub.Connect(connectCtx); err != nil {
		t.Fatalf("command publisher connect: %v", err)
	}
	t.Cleanup(func() { _ = cmdPub.Disconnect(context.Background()) })
	rig.cmdPub = cmdPub

	// Let every SUBACK land before the tests publish.
	time.Sleep(400 * time.Millisecond)
	return rig
}

// findWritableStateChannel returns the first model channel carrying a
// writable STATE data point (a switch output). The godevccu default
// fleet includes HmIP-BSM, whose switching channels satisfy this.
func findWritableStateChannel(t *testing.T, c *central.Unit) (iface string, dev *device.Device, ch *device.Channel) {
	t.Helper()
	for _, d := range c.ModelRegistry.List() {
		if d == nil {
			continue
		}
		for _, cc := range d.Channels() {
			if cc == nil {
				continue
			}
			dp := cc.Parameter(hmenum.ParameterState)
			if dp == nil {
				continue
			}
			// IsWritable is a pointer method on ParameterData; bind to an
			// addressable local before calling it.
			desc := dp.ParameterData()
			if desc.IsWritable() {
				return string(d.Interface), d, cc
			}
		}
	}
	t.Fatal("no writable STATE channel in the godevccu fleet")
	return "", nil, nil
}

// findSwitchServiceChannel returns the first model channel whose
// CustomDataPoint exposes the `turn_on` service method and a topic slot,
// so the test can drive the ADR-0011 service-method command topic.
func findSwitchServiceChannel(t *testing.T, c *central.Unit) (iface string, dev *device.Device, ch *device.Channel, slot payload.TopicSlot) {
	t.Helper()
	for _, d := range c.ModelRegistry.List() {
		if d == nil {
			continue
		}
		for _, cc := range d.Channels() {
			if cc == nil {
				continue
			}
			cdp := cc.CustomDataPoint()
			if cdp == nil {
				continue
			}
			src, ok := cdp.(payload.Source)
			if !ok {
				continue
			}
			slotted, ok := cdp.(payload.Slotted)
			if !ok {
				continue
			}
			if !hasServiceMethod(src.ServiceMethodNames(), "turn_on") {
				continue
			}
			return string(d.Interface), d, cc, slotted.TopicSlot()
		}
	}
	t.Fatal("no switch CustomDataPoint with a turn_on service method in the godevccu fleet")
	return "", nil, nil, payload.TopicSlot{}
}

func hasServiceMethod(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// TestMQTTRawSetCommandDrivesWriteAndRepublishesState proves the full
// inbound write loop against a real broker + godevccu:
//
//  1. A publish on the raw-plane bucket-aware command topic
//     `<base>/<central>/<iface>/<addr>/<ch>/values/STATE/set` reaches the
//     real CommandSubscriber, flows through MQTTCommandSink →
//     backendValueWriter → CcuBackend, and lands as a SetValue on
//     godevccu — the raw `/set` drives a write end-to-end.
//  2. godevccu applies the value (device state change) and the confirmed
//     echo is re-published on the raw-plane state topic, whose on-the-wire
//     VALUE the state subscriber asserts is `"value":true` — not merely
//     the topic shape.
func TestMQTTRawSetCommandDrivesWriteAndRepublishesState(t *testing.T) {
	rig := setupCommandChain(t)

	iface, dev, ch := findWritableStateChannel(t, rig.central)
	channelAddress := ch.Address // "<addr>:<ch>"

	cmdTopic := rig.topics.ParameterCommand(cmdRoundtripCentral, iface, dev.Address, ch.Number, "values", "STATE")
	if err := rig.cmdPub.Publish(context.Background(), cmdTopic, []byte("true"), mqtt.QoS1, false); err != nil {
		t.Fatalf("publish command: %v", err)
	}

	// (1) the write reached godevccu on the target channel's STATE.
	got, ok := rig.waitForSet(func(c capturedSet) bool {
		return strings.EqualFold(c.address, channelAddress) && strings.EqualFold(c.valueKey, "STATE")
	}, 15*time.Second)
	if !ok {
		t.Fatalf("STATE write never reached godevccu for %s (captured=%v)", channelAddress, rig.setCalls)
	}
	t.Logf("godevccu applied SetValue: addr=%s key=%s value=%v", got.address, got.valueKey, got.value)

	// (2) the raw-plane state topic re-published the confirmed value on the
	// wire — assert the VALUE, not just the topic shape.
	topic, plStr, ok := rig.waitForStateValue("/values/state", `"value":true`, 15*time.Second)
	if !ok {
		rig.stateMu.Lock()
		keys := make([]string, 0, len(rig.stateTopics))
		for k := range rig.stateTopics {
			if strings.Contains(strings.ToLower(k), "/values/state") {
				keys = append(keys, k)
			}
		}
		rig.stateMu.Unlock()
		t.Fatalf("raw-plane STATE state topic never carried \"value\":true; matching topics=%v", keys)
	}
	t.Logf("raw-plane republish: topic=%s payload=%s", topic, plStr)
}

// TestMQTTServiceMethodCommandReachesCustomDP proves the ADR-0011
// custom-DP service-method write path over a real broker: a publish on
// `<base>/<central>/<iface>/<addr>/<ch>/custom/<kind>/set/turn_on`
// reaches the CommandSubscriber, routes through
// MQTTCommandSink.InvokeChannelService → the channel's CustomDataPoint
// `turn_on` → the switch writer → godevccu, landing as a real STATE
// write on the virtual CCU.
func TestMQTTServiceMethodCommandReachesCustomDP(t *testing.T) {
	rig := setupCommandChain(t)

	iface, dev, ch, slot := findSwitchServiceChannel(t, rig.central)
	channelAddress := ch.Address
	_ = dev

	svcTopic := rig.topics.CustomDPServiceMethod(cmdRoundtripCentral, iface, slot, "turn_on")
	if err := rig.cmdPub.Publish(context.Background(), svcTopic, []byte(""), mqtt.QoS1, false); err != nil {
		t.Fatalf("publish service-method command: %v", err)
	}

	got, ok := rig.waitForSet(func(c capturedSet) bool {
		// turn_on drives the switch's STATE on the target channel.
		return strings.EqualFold(c.address, channelAddress)
	}, 15*time.Second)
	if !ok {
		t.Fatalf("turn_on service method never reached godevccu for %s (captured=%v)", channelAddress, rig.setCalls)
	}
	t.Logf("service-method turn_on applied on godevccu: addr=%s key=%s value=%v", got.address, got.valueKey, got.value)
}

// findVirtualRemotePressChannel returns the first channel of the virtual
// remote (HmIP-RCV-50) that carries a PRESS_SHORT data point — the wire
// target behind every HA `button` entity the discovery builder emits for
// the virtual remote's key channels.
func findVirtualRemotePressChannel(t *testing.T, c *central.Unit) (iface string, dev *device.Device, ch *device.Channel) {
	t.Helper()
	for _, d := range c.ModelRegistry.List() {
		if d == nil || !strings.Contains(strings.ToUpper(d.Model), "RCV") {
			continue
		}
		for _, cc := range d.Channels() {
			if cc == nil {
				continue
			}
			if dp := cc.Parameter(hmenum.Parameter("PRESS_SHORT")); dp != nil {
				return d.InterfaceID, d, cc
			}
		}
	}
	t.Fatal("no virtual-remote channel with PRESS_SHORT in the godevccu fleet")
	return "", nil, nil
}

// TestMQTTVirtualRemotePressButtonRoundTrip pins the HA `button` → CCU
// path for the virtual remote: HA publishes the discovery payload's
// `payload_press` token ("PRESS") on the button's bucket-aware command
// topic `<base>/<central>/<iface>/<addr>/<ch>/values/PRESS_SHORT/set`;
// the CommandSubscriber must coerce it to `true` and the write must land
// as a SetValue on the CCU. This is the wire behaviour behind the
// operator report "pressing the RCV button in Home Assistant does
// nothing on the CCU".
func TestMQTTVirtualRemotePressButtonRoundTrip(t *testing.T) {
	rig := setupCommandChainWithDevices(t, []string{"HmIP-RCV-50"})

	iface, dev, ch := findVirtualRemotePressChannel(t, rig.central)
	channelAddress := ch.Address

	cmdTopic := rig.topics.ParameterCommand(cmdRoundtripCentral, iface, dev.Address, ch.Number, "values", "PRESS_SHORT")
	if err := rig.cmdPub.Publish(context.Background(), cmdTopic, []byte("PRESS"), mqtt.QoS1, false); err != nil {
		t.Fatalf("publish PRESS command: %v", err)
	}

	got, ok := rig.waitForSet(func(c capturedSet) bool {
		return strings.EqualFold(c.address, channelAddress) && strings.EqualFold(c.valueKey, "PRESS_SHORT")
	}, 15*time.Second)
	if !ok {
		t.Fatalf("PRESS_SHORT write never reached godevccu for %s (captured=%v)", channelAddress, rig.setCalls)
	}
	if got.value != true {
		t.Fatalf("PRESS payload must coerce to boolean true, godevccu saw %v (%T)", got.value, got.value)
	}
	t.Logf("virtual-remote press applied on godevccu: addr=%s key=%s value=%v", got.address, got.valueKey, got.value)
}
