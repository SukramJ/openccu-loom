// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package endpoint

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
)

// identifyCluster / identifyTimeAttr / identifyCmd mirror the Identify
// cluster surface (Matter §1.2) the dispatcher routes to.
const (
	identifyCluster   uint32 = 0x0003
	identifyTimeAttr  uint32 = 0x0000
	identifyCmd       uint32 = 0x00
	identifyTestValue uint16 = 3
)

// makeIdentifiableEndpoint returns a bridged endpoint whose materialised
// cluster set contains the mandatory Identify server. A non-empty
// FriendlyName is required: without it the BridgedDeviceBasicInformation
// constructor rejects the endpoint and [ClusterServers] falls back to the
// source servers alone.
func makeIdentifiableEndpoint(id uint16) *Endpoint {
	return &Endpoint{
		ID:           id,
		FriendlyName: "Identify test endpoint",
		Source:       fullSource{servers: []*fakeServerFull{{id: 0x0006, readVal: uint8(0), readOK: true}}},
	}
}

// TestInvoke_IdentifyTimeSurvivesToTheNextDispatch pins that the Identify
// state a commissioner writes through one IM dispatch is observable on the
// NEXT dispatch. Every dispatch re-materialises the bridged cluster-server
// set, so an Identify instance created per materialisation would answer the
// Identify command with Success and then be discarded — the follow-up read
// of IdentifyTime would report 0 forever and each invoke would strand its
// own countdown goroutine. The two dispatches below are deliberately
// separate calls on the same dispatcher: that is the only way to prove the
// cluster server is bound to the endpoint and not to the call.
func TestInvoke_IdentifyTimeSurvivesToTheNextDispatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := NewTopologyDispatcher(makeTopology(makeIdentifiableEndpoint(2)))

	res := d.Invoke(ctx, concreteCmdPath(2, identifyCluster, identifyCmd), identifyTestValue)
	if res.Status != im.StatusSuccess {
		t.Fatalf("Invoke(Identify) status = %v, want StatusSuccess", res.Status)
	}

	results := d.Read(ctx, concreteAttrPath(2, identifyCluster, identifyTimeAttr))
	if len(results) != 1 {
		t.Fatalf("Read(IdentifyTime): want 1 result, got %d", len(results))
	}
	got, ok := results[0].Value.Value.(uint16)
	if !ok {
		t.Fatalf("IdentifyTime value = %#v, want uint16", results[0].Value.Value)
	}
	if got == 0 {
		t.Fatal("IdentifyTime reads 0 after a successful Identify command — the cluster state was discarded with the dispatch")
	}
}

// TestWrite_IdentifyTimeSurvivesToTheNextDispatch is the attribute-write
// twin of the command path: Matter §1.2.5.1 makes IdentifyTime writable, so
// a controller may drive Identify by writing the attribute instead of
// invoking the command. The written value must be readable afterwards.
func TestWrite_IdentifyTimeSurvivesToTheNextDispatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := NewTopologyDispatcher(makeTopology(makeIdentifiableEndpoint(2)))

	writes := d.Write(ctx, concreteAttrPath(2, identifyCluster, identifyTimeAttr), im.AttributeValue{Value: identifyTestValue})
	if len(writes) != 1 || writes[0].Status != im.StatusSuccess {
		t.Fatalf("Write(IdentifyTime) = %+v, want one StatusSuccess", writes)
	}

	results := d.Read(ctx, concreteAttrPath(2, identifyCluster, identifyTimeAttr))
	if len(results) != 1 {
		t.Fatalf("Read(IdentifyTime): want 1 result, got %d", len(results))
	}
	got, ok := results[0].Value.Value.(uint16)
	if !ok {
		t.Fatalf("IdentifyTime value = %#v, want uint16", results[0].Value.Value)
	}
	if got == 0 {
		t.Fatal("IdentifyTime reads 0 after a successful write — the cluster state was discarded with the dispatch")
	}
}
