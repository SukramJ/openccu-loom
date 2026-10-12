// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"testing"

	"github.com/SukramJ/go-fabric/im"
	"github.com/SukramJ/go-fabric/tlv"

	matterendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"
)

// BridgedDeviceBasicInformation NodeLabel (core spec §9.13), mirrored
// from matter.js packages/types/src/clusters/bridged-device-basic-information.ts.
const (
	bridgedBasicInfoClusterID uint32 = 0x0039
	bridgedAttrNodeLabel      uint32 = 0x0005
)

// TestStartMatterBridgePersistsBridgedNodeLabel pins, through the daemon's
// own Matter start path, that a controller-written bridged NodeLabel is
// persisted in the endpoint-identity store and served again after the
// bridge re-assembles its topology. matter.js keeps a written NodeLabel
// in the endpoint's persisted state; without the store handed to the
// assembler the label lives in memory only and a restart reverts every
// controller rename.
func TestStartMatterBridgePersistsBridgedNodeLabel(t *testing.T) {
	t.Parallel()
	ctx, bundle, sess, onOffEP := bootCommissionedBridge(t)

	path := im.ConcreteAttributePath{
		Endpoint: onOffEP, Cluster: bridgedBasicInfoClusterID, Attribute: bridgedAttrNodeLabel,
		HasEndpoint: true, HasCluster: true, HasAttribute: true,
	}
	const label = "pin-label"
	status, err := sess.Write(ctx, path, func(e *tlv.Encoder, tag tlv.Tag) {
		e.PutUTF8(tag, label)
	})
	if err != nil || status != im.StatusSuccess {
		t.Fatalf("NodeLabel write on endpoint %d: status=%v err=%v", onOffEP, status, err)
	}

	var key matterendpoint.SourceKey
	recs, err := bundle.identity.ListEndpoints(ctx, "")
	if err != nil {
		t.Fatalf("ListEndpoints: %v", err)
	}
	for _, rec := range recs {
		if rec.EndpointID == onOffEP {
			k, ok := rec.Key.(matterendpoint.SourceKey)
			if !ok {
				t.Fatalf("identity row for endpoint %d carries key %T", onOffEP, rec.Key)
			}
			key = k
		}
	}
	if key == (matterendpoint.SourceKey{}) {
		t.Fatalf("no identity row for bridged endpoint %d among %d rows", onOffEP, len(recs))
	}
	if got := bundle.identity.LoadNodeLabel(key); got != label {
		t.Fatalf("persisted NodeLabel for %s = %q, want %q — "+
			"startMatterBridge does not wire matteradapter.Config.NodeLabels to the identity store, "+
			"so a controller rename is lost on restart", key, got, label)
	}

	if err := bundle.bridge.Reassemble(ctx); err != nil {
		t.Fatalf("Reassemble: %v", err)
	}
	rep, err := sess.Read(ctx, path)
	if err != nil {
		t.Fatalf("NodeLabel read after Reassemble: %v", err)
	}
	v, ok := rep.Value(onOffEP, bridgedBasicInfoClusterID, bridgedAttrNodeLabel)
	if !ok {
		st, _ := rep.Status(onOffEP, bridgedBasicInfoClusterID, bridgedAttrNodeLabel)
		t.Fatalf("NodeLabel read after Reassemble on endpoint %d returned no value (status %v)", onOffEP, st)
	}
	if v.String != label {
		t.Fatalf("NodeLabel after Reassemble = %q, want %q — the assembler did not load the persisted label", v.String, label)
	}
}
