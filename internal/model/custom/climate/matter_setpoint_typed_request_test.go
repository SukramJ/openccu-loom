// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package climate

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/go-fabric/cluster/spec"
	thermostatspec "github.com/SukramJ/go-fabric/cluster/spec/thermostat"
	"github.com/SukramJ/go-fabric/endpoint"
	"github.com/SukramJ/go-fabric/im"
	"github.com/SukramJ/go-fabric/tlv"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
)

// TestExtractSetpointRaiseLowerAcceptsBothBridgeShapes pins that the decoder
// reads the generated request struct (the bridge's decode while
// cluster/spec/thermostat is linked) and the tag-keyed map (its generic
// fallback without a linked definition) to the same values, so the command
// does not depend on which definition packages the binary links.
func TestExtractSetpointRaiseLowerAcceptsBothBridgeShapes(t *testing.T) {
	cases := []struct {
		name   string
		fields any
	}{
		{"generated struct", thermostatspec.SetpointRaiseLowerRequest{Mode: thermostatspec.SetpointRaiseLowerModeEnum(2), Amount: -15}},
		{"tag map", map[uint8]any{0: uint64(2), 1: int64(-15)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, amount, err := extractSetpointRaiseLower(tc.fields)
			if err != nil {
				t.Fatalf("extractSetpointRaiseLower: %v", err)
			}
			if mode != 2 || amount != -15 {
				t.Fatalf("got mode=%d amount=%d, want 2/-15", mode, amount)
			}
		})
	}
}

// errNoDefinition marks a command no generated definition decodes.
var errNoDefinition = errors.New("no generated definition decodes this command")

// generatedFieldsReader is the decode the bridge's command-fields reader
// (go-fabric bridge/fields_reader.go commandFieldsReader) applies to a
// Thermostat request: Thermostat has no hand-written case there, so the
// payload falls through to spec.DecodeRequest.
func generatedFieldsReader(path im.ConcreteCommandPath, dec *tlv.Decoder, el tlv.Element) (any, error) {
	fields, ok, err := spec.DecodeRequest(path.Cluster, path.Command, dec, el)
	if !ok {
		return nil, errNoDefinition
	}
	return fields, err
}

// TestThermostatSetpointRaiseLowerFromWireReachesTheCCU drives
// SetpointRaiseLower from TLV bytes through the IM invoke decode with the
// generated Thermostat definition and the real TopologyDispatcher, and
// asserts the setpoint write that reaches the CCU.
func TestThermostatSetpointRaiseLowerFromWireReachesTheCCU(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{
		MinTemperature: 4.5,
		MaxTemperature: 30.5,
	})
	r.setpoint.OnEvent(20.0)

	const ep = 2
	dispatcher := endpoint.NewTopologyDispatcher(&endpoint.Topology{Endpoints: []*endpoint.Endpoint{
		{ID: 0},
		{ID: ep, DeviceType: r.climate.MatterDeviceType(), Reachable: true, Source: r.climate},
	}})

	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutBool(tlv.ContextTag(0), false) // SuppressResponse
	enc.PutBool(tlv.ContextTag(1), false) // TimedRequest
	enc.StartArray(tlv.ContextTag(2))     // InvokeRequests
	enc.StartStruct(tlv.AnonymousTag())   // CommandDataIB
	enc.StartList(tlv.ContextTag(0))      // CommandPath
	enc.PutUint(tlv.ContextTag(0), ep)
	enc.PutUint(tlv.ContextTag(1), 0x0201)
	enc.PutUint(tlv.ContextTag(2), uint64(matterCmdSetpointRaiseLower))
	_ = enc.EndContainer()
	enc.StartStruct(tlv.ContextTag(1)) // CommandFields
	enc.PutUint(tlv.ContextTag(0), 0)  // Mode: Heat
	enc.PutInt(tlv.ContextTag(1), -15) // Amount: -1.5 °C
	_ = enc.EndContainer()
	_ = enc.EndContainer()
	_ = enc.EndContainer()
	enc.PutUint(tlv.ContextTag(0xFF), uint64(im.MatterInteractionModelRevision))
	_ = enc.EndContainer()
	payload, err := enc.Bytes()
	if err != nil {
		t.Fatalf("encode InvokeRequest: %v", err)
	}

	req, err := im.UnmarshalInvokeRequestTLV(tlv.NewDecoder(payload), generatedFieldsReader)
	if err != nil {
		t.Fatalf("UnmarshalInvokeRequestTLV: %v", err)
	}
	if len(req.Invokes) != 1 {
		t.Fatalf("decoded %d invocations, want 1", len(req.Invokes))
	}
	inv := req.Invokes[0]
	if _, ok := inv.Fields.(thermostatspec.SetpointRaiseLowerRequest); !ok {
		t.Fatalf("decoded fields are %T, want thermostat.SetpointRaiseLowerRequest", inv.Fields)
	}
	res := dispatcher.Invoke(context.Background(), inv.Path, inv.Fields)
	if res.Status != im.StatusSuccess {
		t.Fatalf("SetpointRaiseLower status = %v, want Success", res.Status)
	}
	if got := w.last(); got.value.(float64) != 18.5 {
		t.Fatalf("lower (wire) reached the CCU as %v, want 18.5", got.value)
	}
}
