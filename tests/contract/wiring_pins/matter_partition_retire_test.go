// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestMatterPartitionRetirementIsWiredThroughTheCompositionRoot pins the
// seam a removed central's Matter endpoint numbers are collected through:
// the reconciler's Retire reaches the bundle, the bundle's retire reaches
// the REST/WS wiring, and the composition root hands it to the central
// orchestrator whose removeCentral calls it. A setter without the call
// here leaves every removed central's numbers in the store for good —
// and, worse, nothing would retire the partition the node keeps for it.
func TestMatterPartitionRetirementIsWiredThroughTheCompositionRoot(t *testing.T) {
	t.Parallel()
	matter := collapseSpaces(readMatterComposition(t))
	for _, built := range []struct{ what, expr string }{
		{"the bundle's retire", "retire: reconciler.Retire,"},
		{"the wiring's retire", "wiring.retire = bundle.retire"},
	} {
		if !strings.Contains(matter, built.expr) {
			t.Errorf("startMatterBridge / wireMatterRuntime never wires %s (%q)", built.what, built.expr)
		}
	}
	daemon := collapseSpaces(readRepoFile(t, filepath.Join("cmd", "openccu-loom", "daemon.go")))
	if !strings.Contains(daemon, "centralOrch.setMatterPartitionRetirer(matter.retire)") {
		t.Error("the composition root never hands the central orchestrator the Matter partition retirer")
	}
}
