// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// fakeParameterTranslator is a table-driven [ParameterTranslator]. A present
// key returns (value, true); a key whose value is the empty string models the
// "primary parameter" marker (explicit-empty translation). An absent key
// returns ("", false) — no catalogue entry.
type fakeParameterTranslator struct {
	entries map[string]string // "<channelType>|<parameter>" → label
}

func (f fakeParameterTranslator) ChannelTypedParameterLabelOk(channelType, parameter string) (string, bool) {
	v, ok := f.entries[channelType+"|"+parameter]
	return v, ok
}

func TestTranslatedDataPointLabel(t *testing.T) {
	t.Parallel()

	newCh := func() *Channel {
		d := makeDevice("Wohnzimmer", "HmIP-PSM", "000ABC")
		return d.AddChannel("000ABC:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	}

	t.Run("locale label present → label, not omitted", func(t *testing.T) {
		t.Parallel()
		ch := newCh()
		tr := fakeParameterTranslator{entries: map[string]string{"SWITCH|LEVEL": "Helligkeit"}}
		label, omitted := TranslatedDataPointLabel(ch, "LEVEL", "SWITCH", tr)
		if label != "Helligkeit" || omitted {
			t.Fatalf("got (%q, %v), want (%q, false)", label, omitted, "Helligkeit")
		}
	})

	t.Run("explicit-empty translation → omitted (primary marker)", func(t *testing.T) {
		t.Parallel()
		ch := newCh()
		tr := fakeParameterTranslator{entries: map[string]string{"SWITCH|STATE": ""}}
		// labelOmitted is the decisive output; the label itself falls back to
		// the title-cased name (BuildDataPointName.TranslatedName) and is
		// discarded downstream by EntityDisplayName when omitted.
		label, omitted := TranslatedDataPointLabel(ch, "STATE", "SWITCH", tr)
		if !omitted {
			t.Fatalf("explicit-empty translation must set labelOmitted=true; got label=%q omitted=%v", label, omitted)
		}
	})

	t.Run("no catalogue entry → title-cased fallback, not omitted", func(t *testing.T) {
		t.Parallel()
		ch := newCh()
		tr := fakeParameterTranslator{entries: map[string]string{}}
		label, omitted := TranslatedDataPointLabel(ch, "RSSI_DEVICE", "SWITCH", tr)
		if label != "Rssi Device" || omitted {
			t.Fatalf("got (%q, %v), want (%q, false)", label, omitted, "Rssi Device")
		}
	})

	t.Run("nil translator → title-cased fallback, not omitted", func(t *testing.T) {
		t.Parallel()
		ch := newCh()
		label, omitted := TranslatedDataPointLabel(ch, "STATE", "SWITCH", nil)
		if label != "State" || omitted {
			t.Fatalf("got (%q, %v), want (%q, false)", label, omitted, "State")
		}
	})
}
