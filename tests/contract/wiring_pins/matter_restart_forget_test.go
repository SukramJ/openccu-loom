// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestMatterRestartForgetIsWiredThroughTheCompositionRoot pins the seam a
// central restart frees the Matter endpoint numbers of a dropped
// interface's devices through: the reconciler's QueueForget reaches the
// bundle, the bundle's forget reaches the wiring, and the composition root
// hands it to the central orchestrator, whose restart purge calls it. The
// per-central hook cannot do it: the teardown unsubscribes it before the
// model is evicted. Without the call here, a central that moves to
// openccu-lite keeps its CUxD devices' numbers in its partition for good.
func TestMatterRestartForgetIsWiredThroughTheCompositionRoot(t *testing.T) {
	t.Parallel()
	matter := collapseSpaces(readMatterComposition(t))
	for _, built := range []struct{ what, expr string }{
		{"the bundle's forget", "forget: reconciler.QueueForget,"},
		{"the wiring's forget", "wiring.forget = bundle.forget"},
	} {
		if !strings.Contains(matter, built.expr) {
			t.Errorf("startMatterBridge / wireMatterRuntime never wires %s (%q)", built.what, built.expr)
		}
	}
	daemon := collapseSpaces(readRepoFile(t, filepath.Join("cmd", "openccu-loom", "daemon.go")))
	if !strings.Contains(daemon, "centralOrch.setMatterDeviceForgetter(matter.forget)") {
		t.Error("the composition root never hands the central orchestrator the Matter device forget")
	}
}
