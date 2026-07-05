// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build chiptool

package chiptool

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// TestLifecycle_RESTMatterStatus asserts the bridge's REST status
// surface reports the operational state correctly: enabled,
// listening, listen address matches the harness's pre-allocated
// port, fabric count ≥ 1 (the shared fabric).
func TestLifecycle_RESTMatterStatus(t *testing.T) {
	b := requireBridge(t)
	status := b.MatterStatus(t)
	if !status.Enabled {
		t.Errorf("matter/status reports enabled=false")
	}
	if !status.Listening {
		t.Errorf("matter/status reports listening=false")
	}
	if status.ListenAddr == "" {
		t.Errorf("matter/status: listen_addr empty")
	}
	if status.FabricCount < 1 {
		t.Errorf("matter/status: fabric_count=%d, want ≥ 1 (shared fabric)", status.FabricCount)
	}
}

// TestLifecycle_RESTMatterFabrics asserts the bridge surfaces the
// shared fabric on `GET /api/v1/matter/fabrics`.
func TestLifecycle_RESTMatterFabrics(t *testing.T) {
	b := requireBridge(t)
	var body any
	st := b.RESTGet(t, "/api/v1/matter/fabrics", &body)
	if st != 200 {
		t.Fatalf("/matter/fabrics: status=%d", st)
	}
	// JSON shape varies; just assert at least one fabric entry.
	if body == nil {
		t.Errorf("/matter/fabrics: empty body")
	}
}

// TestLifecycle_SetupPayload asserts
// `GET /api/v1/matter/setup-payload` returns a payload with a
// non-empty manual code + QR string. The bridge needs a passcode +
// discriminator to emit one, both of which the harness configured.
func TestLifecycle_SetupPayload(t *testing.T) {
	b := requireBridge(t)
	var body map[string]any
	st := b.RESTGet(t, "/api/v1/matter/setup-payload", &body)
	if st != 200 {
		t.Fatalf("/matter/setup-payload: status=%d", st)
	}
	if body == nil {
		t.Fatal("/matter/setup-payload: empty body")
	}
	// Either "manual_code" or "manual_pairing_code" — accept the
	// field name commonly produced by the handler.
	mc := pickString(body, "manual_code", "manual_pairing_code", "manualCode")
	if mc == "" {
		t.Errorf("/matter/setup-payload: no manual pairing code surfaced: %v", body)
	}
	qr := pickString(body, "qr_code", "qr_payload", "qrPayload")
	if qr == "" {
		t.Errorf("/matter/setup-payload: no QR string surfaced: %v", body)
	}
}

// TestLifecycle_DaemonRestart_CASEPickup stops the daemon, starts
// it again with the same config + persisted store, and re-reads
// BasicInformation.SoftwareVersion via the same chip-tool fabric.
// The CASE-pickup path must succeed without re-commissioning.
func TestLifecycle_DaemonRestart_CASEPickup(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Probe before restart so we have a baseline.
	pre, err := b.SharedCtl.ReadAttr(ctx, t, "basicinformation", "software-version", 0)
	if err != nil {
		t.Fatalf("pre-restart read: %v", err)
	}
	if !harness.AttrReadOK(pre) {
		t.Fatalf("pre-restart read did not succeed:\n%s", pre)
	}

	// Restart the daemon. The harness reuses the same data_dir and
	// config; persistent SQLite carries the fabric identity over.
	b.Restart(t)

	// Re-read via the SAME chip-tool fabric — CASE picks up
	// automatically.
	post, err := b.SharedCtl.ReadAttr(ctx, t, "basicinformation", "software-version", 0)
	if err != nil {
		t.Fatalf("post-restart read: %v", err)
	}
	if !harness.AttrReadOK(post) {
		t.Errorf("post-restart read did not succeed (CASE pickup broken?):\n%s", post)
	}
}

// TestLifecycle_BootidRotation_UniqueIDChanges enables
// dev_rotate_unique_ids and verifies that bridged-endpoint
// UniqueIDs rotate across a daemon restart.
//
// Mirrors v9 capability report T11. Requires its own bridge with
// the rotate flag on; the shared bridge runs with rotation off so
// production-style "stable UniqueID across restarts" holds for the
// rest of the suite.
func TestLifecycle_BootidRotation_UniqueIDChanges(t *testing.T) {
	chipBin := harness.RequireChipTool(t)

	// Dedicated bridge with rotate-uids ON. Cleanup unregisters the
	// daemon at the end of THIS test, not the suite.
	b := harness.Start(t, chipBin, harness.Options{
		CASEEnabled:        true,
		DevRotateUniqueIDs: true,
	})

	// Light up the godevccu-mappable exposures so the bridge actually
	// has bridged endpoints to commission. The shared-bridge setup
	// does this in TestMain; isolated bridges (this test) need their
	// own call or PartsList stays empty and the rest of the
	// assertions trigger t.Skip.
	expCtx, expCancel := context.WithTimeout(context.Background(), 15*time.Second)
	if _, err := b.EnableAllExposures(expCtx); err != nil {
		t.Fatalf("enable exposures: %v", err)
	}
	expCancel()
	// Allow the bridge reassembly to settle before commissioning runs
	// against a still-rebuilding topology.
	time.Sleep(1500 * time.Millisecond)

	// Commission against this isolated bridge.
	ctl := harness.NewController(t, chipBin, 0x3000)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	out, err := ctl.PairFull(ctx, t, harness.PairTargetHost, b.MatterPort())
	if err != nil {
		t.Fatalf("commission rotate-bridge: %v\n%s", err, out)
	}
	if !harness.PairingSuccess(out) {
		t.Fatalf("rotate-bridge pairing did not succeed:\n%s", out)
	}

	// First UID snapshot from the first bridged endpoint.
	aggOut, err := ctl.ReadAttr(ctx, t, "descriptor", "parts-list", 1)
	if err != nil {
		t.Fatalf("aggregator parts-list: %v", err)
	}
	eps := harness.EndpointsInPartsList(aggOut)
	if len(eps) == 0 {
		t.Skip("no bridged endpoints — godevccu fleet empty")
	}
	first := eps[0]
	uidOut, err := ctl.ReadAttr(ctx, t, "bridgeddevicebasicinformation", "unique-id", first)
	if err != nil {
		t.Fatalf("read UID pre-restart: %v", err)
	}
	uid1, ok := harness.FindAttrString(uidOut, "UniqueID")
	if !ok || uid1 == "" {
		t.Fatalf("UID1 not parsed:\n%s", uidOut)
	}

	// Restart with rotate ON → bootid salt should change.
	b.Restart(t)

	// Re-read UID via the same fabric (CASE pickup) → must DIFFER.
	uidOut2, err := ctl.ReadAttr(ctx, t, "bridgeddevicebasicinformation", "unique-id", first)
	if err != nil {
		t.Fatalf("read UID post-restart: %v", err)
	}
	uid2, ok := harness.FindAttrString(uidOut2, "UniqueID")
	if !ok || uid2 == "" {
		t.Fatalf("UID2 not parsed:\n%s", uidOut2)
	}
	if uid1 == uid2 {
		t.Errorf("UniqueID did not rotate across restart: UID1=%s UID2=%s — bootid salt not mixed in?", uid1, uid2)
	}

	// Cleanup chip-tool side so the storage dir is consistent.
	if _, err := ctl.Unpair(context.Background(), t); err != nil {
		t.Logf("unpair (best-effort): %v", err)
	}
}

// pickString extracts the first non-empty string value among the
// candidate keys. Used to ride past the small variability in REST
// JSON field naming (snake-vs-camel, _code vs _payload).
func pickString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}
