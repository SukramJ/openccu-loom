// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ccudata

import (
	"bytes"
	"strings"
	"testing"
)

var pngMagic = []byte("\x89PNG\r\n\x1a\n")

// TestDeviceImage_EveryDeviceIconResolves pins the embedded snapshot to
// its own device_icons table: every filename a device model can resolve
// to must be served from the embed, including the coupling/ entries.
func TestDeviceImage_EveryDeviceIconResolves(t *testing.T) {
	t.Parallel()
	tr, err := LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("LoadTranslationsEmbedded: %v", err)
	}
	if len(tr.DeviceIcons) == 0 {
		t.Fatal("embedded translations carry no device_icons")
	}
	coupling := 0
	for model, name := range tr.DeviceIcons {
		data, ok := DeviceImage(name)
		if !ok {
			t.Errorf("DeviceImage(%q) for model %q: not found", name, model)
			continue
		}
		if !bytes.HasPrefix(data, pngMagic) {
			t.Errorf("DeviceImage(%q): not a PNG", name)
		}
		if strings.HasPrefix(name, "coupling/") {
			coupling++
		}
	}
	if coupling == 0 {
		t.Error("no coupling/ entry among device_icons; the subdirectory case is untested")
	}
}

// TestDeviceImage_CouplingModel resolves a model whose icon lives below
// coupling/ end to end: model → filename → embedded bytes.
func TestDeviceImage_CouplingModel(t *testing.T) {
	t.Parallel()
	tr, err := LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("LoadTranslationsEmbedded: %v", err)
	}
	name := tr.DeviceModelIcon("VIR-LG-DIM")
	if name != "coupling/hm-coupling-dim.png" {
		t.Fatalf("DeviceModelIcon(VIR-LG-DIM) = %q, want coupling/hm-coupling-dim.png", name)
	}
	if data, ok := DeviceImage(name); !ok || !bytes.HasPrefix(data, pngMagic) {
		t.Fatalf("DeviceImage(%q) = ok=%v, want a PNG", name, ok)
	}
}

// TestDeviceImage_RejectsUnsafeNames keeps a name from reaching any
// embedded file outside the image directory.
func TestDeviceImage_RejectsUnsafeNames(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"",
		"../../translation_extract.json.gz",
		"../250/113_hmip-psm.png",
		"coupling/../113_hmip-psm.png",
		"/113_hmip-psm.png",
		"113_hmip-psm.png\x00",
		"113 hmip-psm.png",
		"device_semantics.json",
		"not-in-snapshot.png",
	} {
		if _, ok := DeviceImage(name); ok {
			t.Errorf("DeviceImage(%q) = ok, want rejected", name)
		}
	}
	if _, ok := DeviceImage("113_hmip-psm.png"); !ok {
		t.Error("DeviceImage(113_hmip-psm.png) rejected, want served")
	}
}
