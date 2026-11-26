// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build chiptool

package chiptool

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// TestLifecycle_Erase_PairAgain pairs chip-tool with a bridge of its own,
// erases the node through the maintenance page's route and pairs a second
// chip-tool against the node the daemon brought up without a restart: the
// erase took every fabric and the node's identity, so the new node is
// commissionable on the same address and serves another root UniqueID.
// It runs on its own bridge because the erase removes every fabric, the
// suite's shared one included.
func TestLifecycle_Erase_PairAgain(t *testing.T) {
	chipBin := harness.RequireChipTool(t)
	b := harness.Start(t, chipBin, harness.Options{CASEEnabled: true})

	expCtx, expCancel := context.WithTimeout(context.Background(), 15*time.Second)
	if _, err := b.EnableAllExposures(expCtx); err != nil {
		t.Fatalf("enable exposures: %v", err)
	}
	expCancel()
	// Let the reassembly settle before commissioning reads the topology.
	time.Sleep(1500 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	first := harness.NewController(t, chipBin, 0x3100)
	out, err := first.PairFull(ctx, t, harness.PairTargetHost, b.MatterPort())
	if err != nil || !harness.PairingSuccess(out) {
		t.Fatalf("first pairing did not succeed (err %v):\n%s", err, out)
	}
	uidOut, err := first.ReadAttr(ctx, t, "basicinformation", "unique-id", 0)
	if err != nil {
		t.Fatalf("read root UniqueID before the erase: %v", err)
	}
	uid1, ok := harness.FindAttrString(uidOut, "UniqueID")
	if !ok || uid1 == "" {
		t.Fatalf("root UniqueID before the erase not parsed:\n%s", uidOut)
	}

	if body, status := b.RESTPost(t, "/api/v1/node/maintenance/erase", map[string]string{"confirm": "erase-node"}); status != http.StatusNoContent {
		t.Fatalf("POST /api/v1/node/maintenance/erase = %d: %s", status, body)
	}

	second := harness.NewController(t, chipBin, 0x3101)
	out, err = second.PairFull(ctx, t, harness.PairTargetHost, b.MatterPort())
	if err != nil || !harness.PairingSuccess(out) {
		t.Fatalf("pairing the erased bridge again did not succeed (err %v):\n%s", err, out)
	}
	uidOut, err = second.ReadAttr(ctx, t, "basicinformation", "unique-id", 0)
	if err != nil {
		t.Fatalf("read root UniqueID after the erase: %v", err)
	}
	uid2, ok := harness.FindAttrString(uidOut, "UniqueID")
	if !ok || uid2 == "" {
		t.Fatalf("root UniqueID after the erase not parsed:\n%s", uidOut)
	}
	if uid1 == uid2 {
		t.Errorf("the erased bridge serves its old root UniqueID %s: the erase did not reach the store", uid1)
	}

	if _, err := second.Unpair(context.Background(), t); err != nil {
		t.Logf("unpair (best-effort): %v", err)
	}
}
