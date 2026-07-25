// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// translatorLabeler implements ParameterLabeler AND
// device.ParameterTranslator so toDataPointSummary takes the
// TranslatedName / LabelOmitted branch (which the bare ParameterLabeler
// stubs do not exercise). A present key returns (value, true); an
// explicit-empty value models the "primary parameter" marker; an absent
// key returns ("", false).
type translatorLabeler struct {
	entries map[string]string // "<channelType>|<parameter>" → label
}

func (l translatorLabeler) ParameterLabel(string) string { return "" }
func (l translatorLabeler) ChannelTypedParameterLabelOk(channelType, parameter string) (string, bool) {
	v, ok := l.entries[channelType+"|"+parameter]
	return v, ok
}

// chanWithDevice builds a SWITCH channel attached to a named device so
// device.TranslatedDataPointLabel resolves a non-empty channel name.
func chanWithDevice(t *testing.T) *device.Channel {
	t.Helper()
	d := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "000ABC",
		Model:       "HmIP-PSM",
		Name:        "Wohnzimmer",
	})
	return d.AddChannel("000ABC:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
}

// namedChanWithDevice is chanWithDevice with an operator-assigned
// channel name, so the label-omitted collapse resolves to the custom
// channel name instead of the derived `<device>:<no>` form.
func namedChanWithDevice(t *testing.T, channelName string) *device.Channel {
	t.Helper()
	ch := chanWithDevice(t)
	ch.Name = channelName
	return ch
}

// TestToDataPointSummary_TranslatedName pins that the REST data-point
// summary carries the same per-entity name the MQTT discovery builder emits:
// both resolve through device.TranslatedDataPointLabel → naming.EntityDisplayName.
func TestToDataPointSummary_TranslatedName(t *testing.T) {
	t.Parallel()

	t.Run("locale label flows into TranslatedName", func(t *testing.T) {
		t.Parallel()
		dp := newCategorisedDP(t, "000ABC:1", hmenum.ParameterState, generic.KindBinarySensor)
		lab := translatorLabeler{entries: map[string]string{"SWITCH|STATE": "Status"}}
		s := toDataPointSummary(dp, lab, chanWithDevice(t), "")
		if s.TranslatedName != "Status" || s.LabelOmitted {
			t.Fatalf("TranslatedName=%q LabelOmitted=%v, want (%q, false)", s.TranslatedName, s.LabelOmitted, "Status")
		}
	})

	t.Run("primary marker on a derived channel name collapses to empty", func(t *testing.T) {
		t.Parallel()
		dp := newCategorisedDP(t, "000ABC:1", hmenum.ParameterState, generic.KindBinarySensor)
		lab := translatorLabeler{entries: map[string]string{"SWITCH|STATE": ""}}
		s := toDataPointSummary(dp, lab, chanWithDevice(t), "")
		// The derived channel name equals the device name, so the
		// collapsed name reduces to "" — the entity takes the device
		// name alone.
		if s.TranslatedName != "" || !s.LabelOmitted {
			t.Fatalf("TranslatedName=%q LabelOmitted=%v, want (%q, true)", s.TranslatedName, s.LabelOmitted, "")
		}
	})

	t.Run("primary marker on a custom-named channel ships the collapsed name", func(t *testing.T) {
		t.Parallel()
		dp := newCategorisedDP(t, "000ABC:1", hmenum.ParameterState, generic.KindBinarySensor)
		lab := translatorLabeler{entries: map[string]string{"SWITCH|STATE": ""}}
		s := toDataPointSummary(dp, lab, namedChanWithDevice(t, "Relais Status"), "")
		// The collapse names the entity after the channel — the wire
		// carries the composed name so REST consumers never rebuild it.
		if s.TranslatedName != "Relais Status" || !s.LabelOmitted {
			t.Fatalf("TranslatedName=%q LabelOmitted=%v, want (%q, true)",
				s.TranslatedName, s.LabelOmitted, "Relais Status")
		}
	})

	t.Run("primary marker on a multi-channel parameter ships the chN marker", func(t *testing.T) {
		t.Parallel()
		ch1 := chanWithDevice(t)
		d := ch1.Device()
		ch2 := d.AddChannel("000ABC:2", 2, "SWITCH", hmenum.ParamsetKeyValues)
		dp1 := newCategorisedDP(t, "000ABC:1", hmenum.ParameterState, generic.KindBinarySensor)
		dp2 := newCategorisedDP(t, "000ABC:2", hmenum.ParameterState, generic.KindBinarySensor)
		ch1.Put(dp1)
		ch2.Put(dp2)
		lab := translatorLabeler{entries: map[string]string{"SWITCH|STATE": ""}}
		s := toDataPointSummary(dp1, lab, ch1, "")
		// Derived channel names on a multi-channel parameter keep the
		// disambiguation marker in the collapsed name.
		if s.TranslatedName != "ch1" || !s.LabelOmitted {
			t.Fatalf("TranslatedName=%q LabelOmitted=%v, want (%q, true)", s.TranslatedName, s.LabelOmitted, "ch1")
		}
	})

	t.Run("no entry falls back to title-cased parameter", func(t *testing.T) {
		t.Parallel()
		dp := newCategorisedDP(t, "000ABC:1", hmenum.ParameterState, generic.KindBinarySensor)
		lab := translatorLabeler{entries: map[string]string{}}
		s := toDataPointSummary(dp, lab, chanWithDevice(t), "")
		if s.TranslatedName != "State" || s.LabelOmitted {
			t.Fatalf("TranslatedName=%q LabelOmitted=%v, want (%q, false)", s.TranslatedName, s.LabelOmitted, "State")
		}
	})

	t.Run("non-translator labeler leaves the fields empty", func(t *testing.T) {
		t.Parallel()
		dp := newCategorisedDP(t, "000ABC:1", hmenum.ParameterState, generic.KindBinarySensor)
		// nil labels → the TranslatedName branch is skipped entirely.
		s := toDataPointSummary(dp, nil, chanWithDevice(t), "")
		if s.TranslatedName != "" || s.LabelOmitted {
			t.Fatalf("TranslatedName=%q LabelOmitted=%v, want empty/false", s.TranslatedName, s.LabelOmitted)
		}
	})
}
