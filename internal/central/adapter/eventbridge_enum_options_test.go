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

// setPointModeLabeler translates the parameter and its VALUE_LIST, the
// way the production labeler does over the CCU translation archive.
type setPointModeLabeler struct {
	parameterLabel string
	valueLabels    map[string]string
	// humanise reproduces the production chain's last stage for values
	// the archive does not translate.
	humanise bool
}

func (l setPointModeLabeler) ParameterLabel(_, parameter string) string {
	got, _ := l.ParameterLabelOk("", parameter)
	return got
}

func (l setPointModeLabeler) ParameterLabelOk(_, parameter string) (string, bool) {
	if parameter == setPointModeParameter {
		return l.parameterLabel, true
	}
	return "", false
}

func (l setPointModeLabeler) ValueListLabels(_, _ string, values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		if label, ok := l.valueLabels[v]; ok {
			out[i] = label
			continue
		}
		if l.humanise {
			out[i] = humanizeRaw(v)
			continue
		}
		out[i] = v
	}
	return out
}

const setPointModeParameter = "SET_POINT_MODE"

// TestDiscoveryPublishesLocalisedEnumOptions asserts that the localised
// VALUE_LIST reaches the published discovery payload — not merely that
// the builder would use labels if it were handed some.
//
// The distinction is the point. The label chain, the payload field and
// the template builder each have their own unit test, and all three
// passed while Home Assistant still showed "auto_mode": nothing checked
// that the labeler wired into the daemon carries the value-label half at
// all. This test goes through the real EventBridge with the real
// resolver, so the only thing it can pass on is a complete path.
func TestDiscoveryPublishesLocalisedEnumOptions(t *testing.T) {
	t.Parallel()

	values := []string{"AUTO_MODE", "MANU_MODE", "PARTY_MODE", "BOOST_MODE"}
	labeler := setPointModeLabeler{
		parameterLabel: "Betriebsmodus",
		valueLabels: map[string]string{
			"AUTO_MODE":  "Automatik",
			"MANU_MODE":  "Manuell",
			"PARTY_MODE": "Urlaub",
			"BOOST_MODE": "Boost",
		},
	}

	body := publishEnumDiscoveryWith(t, values, labeler)

	options, ok := body["options"].([]any)
	if !ok {
		t.Fatalf("the discovery payload carries no options array: %#v", body["options"])
	}
	got := make([]string, 0, len(options))
	for _, o := range options {
		s, isString := o.(string)
		if !isString {
			t.Fatalf("option %#v is not a string", o)
		}
		got = append(got, s)
	}
	want := []string{"Automatik", "Manuell", "Urlaub", "Boost"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("published options = %v, want the localised labels %v.\n"+
			"Raw tokens reach an operator verbatim: a discovered MQTT entity has no translation "+
			"file behind it, so \"auto_mode\" stays \"auto_mode\" on screen.", got, want)
	}

	// The write has to survive the localisation: the command template
	// must carry every label back to its CCU token.
	command, _ := body["command_template"].(string)
	if command == "" {
		t.Fatal("a select with localised options must publish a command_template, or every write " +
			"hands the CCU a display string it does not accept")
	}
	for token, label := range labeler.valueLabels {
		if !strings.Contains(command, "'"+label+"': '"+token+"'") {
			t.Errorf("command_template does not map %q back to %q:\n  %s", label, token, command)
		}
	}
}

// parameterOnlyLabeler translates parameter names and nothing else. It
// is the shape of any labeler that predates the value-label port — and
// the state the bridge is in when no labeler is wired at all.
type parameterOnlyLabeler struct{}

func (parameterOnlyLabeler) ParameterLabel(_, _ string) string { return "Betriebsmodus" }

func (parameterOnlyLabeler) ParameterLabelOk(_, parameter string) (string, bool) {
	if parameter == setPointModeParameter {
		return "Betriebsmodus", true
	}
	return "", false
}

// TestDiscoveryFallsBackToRawOptionsWithoutAValueLabeler pins the other
// half: a labeler that cannot localise values leaves the raw tokens in
// place, so an operator sees something ugly rather than an entity whose
// writes the CCU rejects.
func TestDiscoveryFallsBackToRawOptionsWithoutAValueLabeler(t *testing.T) {
	t.Parallel()

	values := []string{"AUTO_MODE", "MANU_MODE"}
	body := publishEnumDiscoveryWith(t, values, parameterOnlyLabeler{})

	options, ok := body["options"].([]any)
	if !ok {
		t.Fatalf("the discovery payload carries no options array: %#v", body["options"])
	}
	for i, o := range options {
		// Exact-match on purpose: a case-insensitive comparison here would
		// accept the raw upper-case token and assert nothing.
		wantToken := strings.ToLower(values[i])
		if s, _ := o.(string); s != wantToken {
			t.Errorf("option %d = %q, want the raw lower-cased token %q", i, s, wantToken)
		}
	}
	if got, _ := body["command_template"].(string); got != "{{ value | upper }}" {
		t.Errorf("command_template = %q, want the plain upper-casing form that restores the CCU token", got)
	}
}

// TestDiscoveryHumanisesUntranslatedEnumOptions pins what an operator
// sees for an ENUM the translation archive has no value table for: the
// humanised token ("Auto Mode"), which is what the REST and UI surfaces
// show for the same value. Consistency across surfaces is the property
// worth having here — the raw "auto_mode" was only ever a placeholder
// for a translation Home Assistant never applies to discovered entities.
func TestDiscoveryHumanisesUntranslatedEnumOptions(t *testing.T) {
	t.Parallel()

	values := []string{"AUTO_MODE", "MANU_MODE"}
	// The production chain (ValueListLabel) humanises when no
	// translation exists; this labeler reproduces that contract.
	labeler := setPointModeLabeler{parameterLabel: "Betriebsmodus", humanise: true}

	body := publishEnumDiscoveryWith(t, values, labeler)

	options, _ := body["options"].([]any)
	want := []string{"Auto Mode", "Manu Mode"}
	for i, o := range options {
		if s, _ := o.(string); s != want[i] {
			t.Errorf("option %d = %q, want the humanised token %q", i, s, want[i])
		}
	}
}

// publishEnumDiscovery builds a channel carrying a writable ENUM
// parameter, runs it through the real EventBridge, and returns the parsed
// HA discovery payload.
func publishEnumDiscoveryWith(t *testing.T, values []string, labeler mqtt.ParameterLabeler) map[string]any {
	t.Helper()

	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "000ENUMD",
		Model:       "HmIP-BWTH",
		Name:        "Wandthermostat",
	})
	ch := dev.AddChannel("000ENUMD:1", 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyValues)

	// A read+write ENUM is what the resolver turns into a select.
	dp := resolveDataPoint(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      setPointModeParameter,
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			ValueList:  values,
		},
	})
	if dp == nil {
		t.Fatal("the resolver produced no data point for a read+write ENUM")
	}
	if init, ok := dp.(namingInitializer); ok {
		init.SetNameData(device.BuildDataPointName(ch, setPointModeParameter, ""))
		init.SetPathData(naming.NewDataPointPathData(
			"", hmtypes.NewWireInterfaceID("", hmenum.InterfaceHmIPRF), ch.Address, ch.Number, naming.BucketValues, setPointModeParameter,
		))
		init.SetIsInMultipleChannels(ch.IsParameterInMultipleChannels(setPointModeParameter))
	}
	ch.Put(dp)

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

	wantObject := "/" + strconv.Itoa(ch.Number) + "_" + strings.ToLower(setPointModeParameter) + "/config"
	for _, p := range pub.Published() {
		topic := strings.ToLower(p.Topic)
		if !strings.HasPrefix(topic, "homeassistant/select/") || !strings.HasSuffix(topic, wantObject) {
			continue
		}
		var body map[string]any
		if err := json.Unmarshal(p.Payload, &body); err != nil {
			t.Fatalf("discovery payload on %s is not JSON: %v", p.Topic, err)
		}
		return body
	}
	var topics []string
	for _, p := range pub.Published() {
		topics = append(topics, p.Topic)
	}
	t.Fatalf("no select discovery config was published for %s — the fixture never reached the discovery "+
		"plane.\n  published: %s", setPointModeParameter, strings.Join(topics, "\n             "))
	return nil
}
