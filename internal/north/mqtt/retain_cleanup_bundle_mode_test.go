// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"testing"
	"time"
)

// sweepBridge builds a bridge over a broker holding the given retained
// topics, in either discovery mode.
func sweepBridge(retained []retainedMsg, bundles bool) (*Bridge, *filterKeyedBroker) {
	broker := newFilterKeyedBroker(retained)
	b := NewBridge(BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-a",
		RawEnabled: true, HADiscoveryEnabled: true,
		HADiscoveryBundles: bundles,
	}, broker).WithSubscriber(broker)
	return b, broker
}

// TestSweepKeepsTheBundleItJustPublished. The sweep evicts every retained
// config this build did not publish, and in device-bundle mode the thing it
// publishes is a shape it only learned to recognise in the first slice of
// step 13. Getting the claim wrong here does not lose one entity — it
// retracts the whole device in one message.
func TestSweepKeepsTheBundleItJustPublished(t *testing.T) {
	const ours = "homeassistant/device/ccu-a_000a/config"
	b, broker := sweepBridge([]retainedMsg{
		{topic: ours, payload: []byte(`{"device":{"identifiers":["x"]},"components":{}}`)},
	}, true)

	// Claim it the way a publish does — which, since the claim set moved
	// into the shared runtime, means actually publishing it.
	seedDeclared(t, b, ours, []byte(`{"device":{"identifiers":["x"]},"components":{}}`))

	if _, err := b.RunDiscoveryOrphanCleanupOnce(context.Background(), "ccu-a", 120*time.Millisecond); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	broker.wg.Wait()
	if broker.evicted()[ours] {
		t.Error("the sweep retracted the device document this build published")
	}
}

// TestSweepEvictsTheEntityConfigsTheBundleReplaced. On the migration boot
// the broker still holds the previous build's per-entity configs. The
// publish path retracts the ones its store knows about; the sweep is what
// catches an entity the current build no longer produces at all.
func TestSweepEvictsTheEntityConfigsTheBundleReplaced(t *testing.T) {
	const (
		ours    = "homeassistant/device/ccu-a_000a/config"
		legacy  = "homeassistant/sensor/ccu-a_000a/1_temperature/config"
		retired = "homeassistant/sensor/ccu-a_000a/1_retired/config"
	)
	b, broker := sweepBridge([]retainedMsg{
		{topic: ours, payload: []byte(`{"device":{"identifiers":["x"]},"components":{}}`)},
		{topic: legacy, payload: []byte(`{"name":"Temperature"}`)},
		{topic: retired, payload: []byte(`{"name":"Retired"}`)},
	}, true)

	seedDeclared(t, b, ours, []byte(`{"device":{"identifiers":["x"]},"components":{}}`))

	if _, err := b.RunDiscoveryOrphanCleanupOnce(context.Background(), "ccu-a", 120*time.Millisecond); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	broker.wg.Wait()

	got := broker.evicted()
	for _, want := range []string{legacy, retired} {
		if !got[want] {
			t.Errorf("a superseded per-entity config survived: %q", want)
		}
	}
	if got[ours] {
		t.Error("the sweep took the document down with the configs it replaced")
	}
}

// TestSweepSpareTheHubPlanesDocumentBeforeItDeclares. The hub plane
// publishes long after the device snapshot that triggers the sweep, so it is
// gated on having declared. The gate reads the node id, which the two
// discovery forms spell identically — but nothing had ever run it against a
// bundle topic, and the pass runs once per boot: whatever it deletes stays
// deleted until the daemon restarts.
func TestSweepSparesTheHubPlanesDocumentBeforeItDeclares(t *testing.T) {
	const hubDoc = "homeassistant/device/ccu-a_sysvars/config"
	b, broker := sweepBridge([]retainedMsg{
		{topic: hubDoc, payload: []byte(`{"device":{"identifiers":["h"]},"components":{}}`)},
	}, true)

	if _, err := b.RunDiscoveryOrphanCleanupOnce(context.Background(), "ccu-a", 120*time.Millisecond); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	broker.wg.Wait()
	if broker.evicted()[hubDoc] {
		t.Error("the hub plane's document was swept before the plane had declared")
	}
}

// TestSweepEvictsAStaleHubDocumentOnceThePlaneHasDeclared: the gate defers
// the judgement, it does not cancel it. A hub node the current build no
// longer drives has to go, or its entities come back as phantoms on the next
// Home Assistant restart.
func TestSweepEvictsAStaleHubDocumentOnceThePlaneHasDeclared(t *testing.T) {
	const stale = "homeassistant/device/ccu-a_sysvars/config"
	b, broker := sweepBridge([]retainedMsg{
		{topic: stale, payload: []byte(`{"device":{"identifiers":["h"]},"components":{}}`)},
	}, true)
	b.MarkHubPlaneDeclared("ccu-a")

	if _, err := b.RunDiscoveryOrphanCleanupOnce(context.Background(), "ccu-a", 120*time.Millisecond); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	broker.wg.Wait()
	if !broker.evicted()[stale] {
		t.Error("a stale hub document survived a sweep the plane had declared for")
	}
}

// TestSweepLeavesForeignDocumentsAlone: the Zigbee2MQTT on this broker
// publishes device documents under node ids of its own, and the scoping that
// protects them is the same one the per-entity form relies on.
func TestSweepLeavesForeignDocumentsAlone(t *testing.T) {
	const theirs = "homeassistant/device/0x00158d0001abcdef/config"
	b, broker := sweepBridge([]retainedMsg{
		{topic: theirs, payload: []byte(`{"device":{"identifiers":["z"]},"components":{}}`)},
	}, true)

	if _, err := b.RunDiscoveryOrphanCleanupOnce(context.Background(), "ccu-a", 120*time.Millisecond); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	broker.wg.Wait()
	if broker.evicted()[theirs] {
		t.Errorf("the sweep cleared another integration's document: %q", theirs)
	}
}

// TestSweepEvictsAStaleBundleWhenBundleModeIsOff drives the direction the
// other five tests in this file do not: every one of them runs with
// `HADiscoveryBundles: true`, so the whole file measured the migration and
// none of it measured the rollback.
//
// The rollback is the direction ADR 0070's fourth amendment singles out as
// the one that fails silently. Home Assistant refuses the two discovery
// forms for one `unique_id` symmetrically: with a device document still
// retained from a bundle-mode boot, every per-entity config this build
// publishes afterwards is refused, with a WARNING in the Home Assistant log
// and nothing on the wire. The daemon sees a successful publish; the
// operator sees no entities. Turning the flag back off is therefore not
// "stop publishing bundles" — the stale document has to come down, and the
// orphan sweep is the only thing in this daemon positioned to take it down.
//
// So: bundles off, a device document for one of this central's own nodes
// still on the broker, and the per-entity configs of the current build
// declared. The document is an orphan by the sweep's own definition — this
// build did not publish it — and it belongs to this central, which is what
// separates it from the Zigbee2MQTT documents the sweep must leave alone.
func TestSweepEvictsAStaleBundleWhenBundleModeIsOff(t *testing.T) {
	const (
		stale   = "homeassistant/device/ccu-a_000a/config"
		current = "homeassistant/sensor/ccu-a_000a/1_temperature/config"
	)
	b, broker := sweepBridge([]retainedMsg{
		{topic: stale, payload: []byte(`{"device":{"identifiers":["x"]},"components":{}}`)},
		{topic: current, payload: []byte(`{"name":"Temperature"}`)},
	}, false)

	// The per-entity form is what this build publishes now.
	seedDeclared(t, b, current, []byte(`{"name":"Temperature"}`))

	if _, err := b.RunDiscoveryOrphanCleanupOnce(context.Background(), "ccu-a", 120*time.Millisecond); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	broker.wg.Wait()

	got := broker.evicted()
	if !got[stale] {
		t.Error("the device document from a previous bundle-mode boot survived the rollback sweep; " +
			"Home Assistant refuses every per-entity config published against it, and the only " +
			"evidence is a line in its log")
	}
	if got[current] {
		t.Errorf("the sweep retracted the per-entity config this build published: %q", current)
	}
}
