// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// frostProtectionParameter is the wire parameter this test names. It has
// no hmenum constant — it reaches an operator only by being un-hidden in
// the hidden-parameters screen (it is listed in
// internal/store/visibility/rules.go), which is the path that surfaced
// the naming divergence.
//
// Its channel layout is what makes it the right witness: every HmIP-eTRV
// carries it on channel 1 alone, while HmIP-BWTH carries it on channels 1
// and 8, so one parameter exercises both the single-channel and the
// multi-channel branch of the naming rule.
const frostProtectionParameter = "FROST_PROTECTION"

// frostProtectionLabeler translates one parameter and nothing else, so
// the name under test comes from the composition rules rather than from
// the catalogue.
type frostProtectionLabeler struct{ label string }

func (f frostProtectionLabeler) ParameterLabel(_, parameter string) string {
	l, _ := f.ParameterLabelOk("", parameter)
	return l
}

func (f frostProtectionLabeler) ParameterLabelOk(_, parameter string) (string, bool) {
	if parameter == frostProtectionParameter {
		return f.label, true
	}
	return "", false
}

// ChannelTypedParameterLabelOk satisfies device.ParameterTranslator, the
// interface the REST summary path asserts on to reach the same catalogue.
func (f frostProtectionLabeler) ChannelTypedParameterLabelOk(_, parameter string) (string, bool) {
	return f.ParameterLabelOk("", parameter)
}

// TestMQTTAndRESTAgreeOnTheEntityName pins the two north-bound naming
// paths to each other for a per-parameter data point.
//
// The daemon is the only naming authority: device.BuildDataPointName
// composes the name, and REST reaches it live through
// device.TranslatedDataPointNameData. The MQTT discovery path does not —
// it starts from the NameData cached on the data point at hydration time
// and re-derives the multi-channel postfix itself, from two of the three
// conditions the authority applies. Two derivations of one rule is one
// too many, and only a consumer ever sees the difference: the same data
// point arrives in Home Assistant as "Frostschutz" over the REST drop-in
// and as "Frostschutz ch1" over MQTT discovery.
//
// The assertion is the equality, not a literal string. A literal pins
// today's spelling of the rule; the equality pins the invariant that
// there is only one rule, whatever it says.
func TestMQTTAndRESTAgreeOnTheEntityName(t *testing.T) {
	t.Parallel()

	const label = "Frostschutz"

	cases := []struct {
		name  string
		model string
		// deviceName is the CCU device name. channels lists the channel
		// numbers that carry the parameter, mirroring the real wire
		// layout; channelNames optionally names them (an absent entry
		// leaves the CCU-derived "<device>:<no>" scheme in place).
		deviceName   string
		channels     []int
		channelNames map[int]string
		// subject is the channel whose entity name is compared.
		subject int
	}{
		{
			name: "eTRV carries the parameter on one channel", model: "HmIP-eTRV-2",
			deviceName: "Stellantrieb", channels: []int{1}, subject: 1,
		},
		{
			name: "eTRV with a named channel", model: "HmIP-eTRV-2",
			deviceName: "Stellantrieb", channels: []int{1},
			channelNames: map[int]string{1: "Bad"}, subject: 1,
		},
		{
			name: "BWTH carries it on two channels, both CCU-derived", model: "HmIP-BWTH",
			deviceName: "Wandthermostat", channels: []int{1, 8}, subject: 1,
		},
		{
			name: "BWTH with the primary channel named for its room", model: "HmIP-BWTH",
			deviceName: "Wandthermostat", channels: []int{1, 8},
			channelNames: map[int]string{1: "Wohnzimmer"}, subject: 1,
		},
		{
			name: "BWTH with both channels named distinctly", model: "HmIP-BWTH",
			deviceName: "Wandthermostat", channels: []int{1, 8},
			channelNames: map[int]string{1: "Wohnzimmer", 8: "Wohnzimmer Fenster"}, subject: 1,
		},
		{
			name: "BWTH with both channels sharing a name", model: "HmIP-BWTH",
			deviceName: "Wandthermostat", channels: []int{1, 8},
			channelNames: map[int]string{1: "Wohnzimmer", 8: "Wohnzimmer"}, subject: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dev := device.New(device.Config{
				InterfaceID: "HmIP-RF",
				Interface:   hmenum.InterfaceHmIPRF,
				Address:     "000FROST",
				Model:       tc.model,
				Name:        tc.deviceName,
			})
			var subject *device.Channel
			for _, no := range tc.channels {
				ch := addFrostProtectionChannel(t, dev, no, tc.channelNames[no])
				if no == tc.subject {
					subject = ch
				}
			}
			if subject == nil {
				t.Fatalf("channel %d is not in the fixture", tc.subject)
			}
			// The cached name quadruple is stamped exactly as the
			// hydration pipeline stamps it, and only after every channel
			// exists — otherwise the fixture would decide the outcome by
			// hiding the sibling channel from IsParameterInMultipleChannels.
			for _, no := range tc.channels {
				stampPipelineNaming(t, dev, no)
			}

			labeler := frostProtectionLabeler{label: label}

			// REST: the authority, resolved live.
			nd, omitted := device.TranslatedDataPointNameData(subject, frostProtectionParameter, subject.Type, labeler)
			restName, _ := naming.ComposedEntityName(nd, omitted, frostProtectionParameter)

			// MQTT: whatever the discovery payload carries.
			mqttName := mqttDiscoveryEntityName(t, dev, subject, labeler)

			if mqttName != restName {
				t.Errorf("the same data point reaches Home Assistant under two names:\n"+
					"  REST (naming authority): %q\n"+
					"  MQTT discovery:          %q\n"+
					"  model=%s device=%q channel=%s\n"+
					"The MQTT path re-derives the multi-channel postfix from the cached NameData instead "+
					"of asking the authority, and it checks two of the three conditions the authority "+
					"checks. Compose the MQTT name from the authority's own postfix.",
					restName, mqttName, tc.model, tc.deviceName, subject.Address)
			}
		})
	}
}

// addFrostProtectionChannel adds one channel carrying a FROST_PROTECTION
// data point built by the real resolver from the real wire descriptor
// (read-only BOOL, OPERATIONS 5 = READ|EVENT — what every eTRV and BWTH
// reports for it).
func addFrostProtectionChannel(t *testing.T, dev *device.Device, number int, name string) *device.Channel {
	t.Helper()
	addr := dev.Address + ":" + strconv.Itoa(number)
	ch := dev.AddChannel(addr, number, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyValues)
	if name != "" {
		ch.SetName(name)
	}
	dp := resolveDataPoint(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      frostProtectionParameter,
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	if dp == nil {
		t.Fatal("the resolver produced no data point for a read-only BOOL — the fixture no longer matches the wire")
	}
	ch.Put(dp)
	return ch
}

// stampPipelineNaming installs the cached presentation surface the way
// device_pipeline.go does: the authority builds the NameData with no
// translation, and it is cached from then on.
func stampPipelineNaming(t *testing.T, dev *device.Device, number int) {
	t.Helper()
	ch := dev.Channel(dev.Address + ":" + strconv.Itoa(number))
	if ch == nil {
		t.Fatalf("channel %d vanished from the fixture", number)
	}
	dp := ch.Parameter(hmenum.Parameter(frostProtectionParameter))
	if dp == nil {
		t.Fatalf("channel %d carries no %s data point", number, frostProtectionParameter)
	}
	init, ok := dp.(namingInitializer)
	if !ok {
		t.Fatalf("the %s data point does not implement namingInitializer — the pipeline could not stamp it either",
			frostProtectionParameter)
	}
	init.SetNameData(device.BuildDataPointName(ch, frostProtectionParameter, ""))
	init.SetPathData(naming.NewDataPointPathData(
		"", hmtypes.NewWireInterfaceID("", hmenum.InterfaceHmIPRF), ch.Address, ch.Number, naming.BucketValues, frostProtectionParameter,
	))
	init.SetIsInMultipleChannels(ch.IsParameterInMultipleChannels(frostProtectionParameter))
}

// mqttDiscoveryEntityName publishes dev's snapshot through the real
// EventBridge and returns the `name` field of the HA discovery payload
// for the subject channel. It returns "" when the payload carries no
// name (HA's "collapse to the device name" signal).
func mqttDiscoveryEntityName(
	t *testing.T, dev *device.Device, subject *device.Channel, labeler frostProtectionLabeler,
) string {
	t.Helper()

	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}
	c.ModelRegistry.Put(dev)
	c.MarkSouthboundReady()

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:               "openccu-loom",
		CentralName:        "ccu-01",
		RawEnabled:         true,
		HADiscoveryEnabled: true,
	}, pub)
	eb := NewEventBridge(reg, nil, mqtt.NewWiring(bridge, nil)).WithParameterLabels(labeler)
	eb.Start(context.Background())
	defer eb.Stop()
	eb.PublishInitialSnapshot(context.Background())

	// The discovery object id is "<channel-no>_<parameter>", lower-cased.
	wantObject := "/" + strconv.Itoa(subject.Number) + "_" + strings.ToLower(frostProtectionParameter) + "/config"
	for _, p := range pub.Published() {
		topic := strings.ToLower(p.Topic)
		if !strings.HasPrefix(topic, "homeassistant/") || !strings.HasSuffix(topic, wantObject) {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(p.Payload, &payload); err != nil {
			t.Fatalf("discovery payload on %s is not JSON: %v", p.Topic, err)
		}
		switch name := payload["name"].(type) {
		case string:
			return name
		case nil:
			return ""
		default:
			t.Fatalf("discovery payload on %s carries a non-string name %T", p.Topic, payload["name"])
		}
	}
	var topics []string
	for _, p := range pub.Published() {
		topics = append(topics, p.Topic)
	}
	t.Fatalf("no HA discovery config was published for %s on %s — the fixture never reached the discovery "+
		"plane, so this test would compare nothing.\n  published: %s",
		frostProtectionParameter, subject.Address, strings.Join(topics, "\n             "))
	return ""
}
