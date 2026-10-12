// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/go-fabric/cluster/spec"
	onoffspec "github.com/SukramJ/go-fabric/cluster/spec/onoff"
	"github.com/SukramJ/go-fabric/endpoint"
	"github.com/SukramJ/go-fabric/im"
	"github.com/SukramJ/go-fabric/tlv"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
)

// errNoDefinition marks a command the generated registry does not decode;
// the wire test below must never hit it for OnOff.
var errNoDefinition = errors.New("no generated definition decodes this command")

// generatedFieldsReader is the decode the bridge's command-fields reader
// (go-fabric bridge/fields_reader.go commandFieldsReader) applies to an
// OnOff request: OnOff has no hand-written case there, so the payload
// falls through to spec.DecodeRequest, the generated definition linked
// into the binary through cluster/spec/onoff.
func generatedFieldsReader(path im.ConcreteCommandPath, dec *tlv.Decoder, el tlv.Element) (any, error) {
	fields, ok, err := spec.DecodeRequest(path.Cluster, path.Command, dec, el)
	if !ok {
		return nil, errNoDefinition
	}
	return fields, err
}

// encodeOnWithTimedOffInvoke builds an InvokeRequestMessage carrying
// OnOff (0x0006) OnWithTimedOff (0x42) on the given endpoint, fields per
// on-off.element.ts: [0] OnOffControl, [1] OnTime, [2] OffWaitTime.
func encodeOnWithTimedOffInvoke(t *testing.T, ep uint16, control uint8, onTime, offWait uint16) []byte {
	t.Helper()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutBool(tlv.ContextTag(0), false) // SuppressResponse
	enc.PutBool(tlv.ContextTag(1), false) // TimedRequest
	enc.StartArray(tlv.ContextTag(2))     // InvokeRequests
	enc.StartStruct(tlv.AnonymousTag())   // CommandDataIB
	enc.StartList(tlv.ContextTag(0))      // CommandPath
	enc.PutUint(tlv.ContextTag(0), uint64(ep))
	enc.PutUint(tlv.ContextTag(1), 0x0006)
	enc.PutUint(tlv.ContextTag(2), 0x42)
	_ = enc.EndContainer()
	enc.StartStruct(tlv.ContextTag(1)) // CommandFields
	enc.PutUint(tlv.ContextTag(0), uint64(control))
	enc.PutUint(tlv.ContextTag(1), uint64(onTime))
	enc.PutUint(tlv.ContextTag(2), uint64(offWait))
	_ = enc.EndContainer()
	_ = enc.EndContainer()
	_ = enc.EndContainer()
	enc.PutUint(tlv.ContextTag(0xFF), uint64(im.MatterInteractionModelRevision))
	_ = enc.EndContainer()
	b, err := enc.Bytes()
	if err != nil {
		t.Fatalf("encode InvokeRequest: %v", err)
	}
	return b
}

// TestOnWithTimedOffFromWireReachesTheCCU drives OnWithTimedOff from TLV
// bytes through the IM invoke decode with the generated OnOff definition
// and the real TopologyDispatcher onto the Light's OnOff server, and
// asserts the CCU write and the armed countdown attributes. With the generated
// definition linked, the server receives onoff.OnWithTimedOffRequest, not
// the tag map; a decoder that only knows the map answers the command with
// an error and nothing reaches the CCU.
func TestOnWithTimedOffFromWireReachesTheCCU(t *testing.T) {
	w := &stubWriter{last: -1}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	t.Cleanup(l.Close)
	l.OnLevel(0.0)

	const ep = 2
	top := &endpoint.Topology{Endpoints: []*endpoint.Endpoint{
		{ID: 0},
		{ID: ep, DeviceType: l.MatterDeviceType(), Reachable: true, Source: l},
	}}
	dispatcher := endpoint.NewTopologyDispatcher(top)

	req, err := im.UnmarshalInvokeRequestTLV(tlv.NewDecoder(encodeOnWithTimedOffInvoke(t, ep, 0, 30, 7)), generatedFieldsReader)
	if err != nil {
		t.Fatalf("UnmarshalInvokeRequestTLV: %v", err)
	}
	if len(req.Invokes) != 1 {
		t.Fatalf("decoded %d invocations, want 1", len(req.Invokes))
	}
	inv := req.Invokes[0]
	if _, ok := inv.Fields.(onoffspec.OnWithTimedOffRequest); !ok {
		t.Fatalf("decoded fields are %T, want onoff.OnWithTimedOffRequest — the generated definition is not the decode path", inv.Fields)
	}

	res := dispatcher.Invoke(context.Background(), inv.Path, inv.Fields)
	if res.Status != im.StatusSuccess {
		t.Fatalf("OnWithTimedOff status = %v, want Success", res.Status)
	}
	if w.last <= 0 {
		t.Fatalf("CCU write after OnWithTimedOff = %v, want a turn-on level", w.last)
	}
	srv := onOffServer(t, l)
	if v, ok := srv.MatterRead(0x4001); !ok || v.(uint16) != 30 {
		t.Fatalf("OnTime after wire invoke = (%v, %v), want (30, true)", v, ok)
	}
	if v, ok := srv.MatterRead(0x4002); !ok || v.(uint16) != 7 {
		t.Fatalf("OffWaitTime after wire invoke = (%v, %v), want (7, true)", v, ok)
	}
}
