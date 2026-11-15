// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"go/ast"
	"go/token"
	"testing"
)

// centralBringUpSource holds the cache-reset re-pull of a central.
const centralBringUpSource = "internal/central/adapter/central_bringup.go"

// TestPin_ModelReloadsHoldTheMQTTPlanes pins the composition-root half of the
// deferred MQTT retraction: the planes defer only while the central's model
// generation is incomplete, and this pin holds the two lifecycle paths that
// must open one to their order.
//
// A restart and the cache-reset re-pull must open the generation BEFORE the
// first device leaves the model — a removal handled first is retracted, and
// Home Assistant deletes the entity with its registry entry. A restart holds
// the hub plane instead of retracting it, a removal releases what an earlier
// restart still holds, and a restart whose adopt fails releases it too:
// nothing else ever settles a hold of a central that is gone.
func TestPin_ModelReloadsHoldTheMQTTPlanes(t *testing.T) {
	t.Parallel()

	adopt := parsePinFuncs(t, centralAdoptSource)
	teardown, ok := adopt["teardownCentralLocked"]
	if !ok {
		t.Fatalf("%s no longer declares teardownCentralLocked", centralAdoptSource)
	}
	begin := firstCallPos(teardown.Body, "BeginModelTeardown")
	evict := firstCallPos(teardown.Body, "evict")
	switch {
	case begin == token.NoPos:
		t.Error("teardownCentralLocked no longer opens a model generation: a restart retracts every MQTT config of the central")
	case evict == token.NoPos:
		t.Error("teardownCentralLocked no longer calls evict; re-read this pin")
	case begin > evict:
		t.Error("teardownCentralLocked opens the model generation after the eviction: the removals are retracted, not held")
	}
	if firstCallPos(teardown.Body, "HoldCentral") == token.NoPos {
		t.Error("teardownCentralLocked does not hold the hub plane on a restart")
	}
	if r := firstCallPos(teardown.Body, "ReleaseModelHold"); r == token.NoPos || r > evict {
		t.Error("teardownCentralLocked does not release a held central before a removal's eviction")
	}

	restart, ok := adopt["restartCentral"]
	if !ok {
		t.Fatalf("%s no longer declares restartCentral", centralAdoptSource)
	}
	for _, release := range []string{"ReleaseModelHold", "ReleaseHeldCentral"} {
		if firstCallPos(restart.Body, release) == token.NoPos {
			t.Errorf("restartCentral does not call %s when the adopt fails: the held configs of a central "+
				"that is live nowhere stay on the broker", release)
		}
	}

	bringUp := parsePinFuncs(t, centralBringUpSource)
	reinit, ok := bringUp["reinit"]
	if !ok {
		t.Fatalf("%s no longer declares reinit", centralBringUpSource)
	}
	b, c := firstCallPos(reinit.Body, "BeginModelTeardown"), firstCallPos(reinit.Body, "clearModel")
	if b == token.NoPos || c == token.NoPos || b > c {
		t.Error("the cache-reset re-pull does not open a model generation before clearModel: every device's " +
			"MQTT configs are retracted on a cache reset")
	}
}

// firstCallPos returns the position of the first call to fn (a plain or
// selector call) in body, or token.NoPos.
func firstCallPos(body *ast.BlockStmt, fn string) token.Pos {
	pos := token.NoPos
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		var name string
		switch f := call.Fun.(type) {
		case *ast.Ident:
			name = f.Name
		case *ast.SelectorExpr:
			name = f.Sel.Name
		}
		if name == fn && (pos == token.NoPos || call.Pos() < pos) {
			pos = call.Pos()
		}
		return true
	})
	return pos
}
