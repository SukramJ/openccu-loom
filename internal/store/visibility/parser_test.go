// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package visibility

import (
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestParseUnIgnoreVariants(t *testing.T) {
	src := strings.NewReader(`
# comment line
LEVEL
TEMPERATURE_OFFSET:MASTER@HmIP-eTRV:1
LEVEL:VALUES@HmIP-BROLL:3  # inline comment
PARTY_TIME_END:VALUES@HmIP-eTRV:0
   :MISSING_PARAM
`)
	entries, err := ParseUnIgnore(src)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(entries); got != 4 {
		t.Fatalf("expected 4 entries, got %d", got)
	}
	// LEVEL — simple entry.
	if entries[0].Parameter != hmenum.Parameter("LEVEL") {
		t.Errorf("entry[0]=%+v", entries[0])
	}
	if !entries[0].IsSimple {
		t.Errorf("entry[0] must be simple; IsSimple=%v", entries[0].IsSimple)
	}
	// TEMPERATURE_OFFSET:MASTER@HmIP-eTRV:1 — complex MASTER entry.
	if entries[1].Model != "hmip-etrv" {
		t.Errorf("entry[1].Model=%q want hmip-etrv", entries[1].Model)
	}
	if entries[1].ParamsetKey != hmenum.ParamsetKeyMaster {
		t.Errorf("entry[1].ParamsetKey=%q want MASTER", entries[1].ParamsetKey)
	}
	if entries[1].ChannelNo == nil || *entries[1].ChannelNo != 1 {
		t.Errorf("entry[1].ChannelNo=%v want &1", entries[1].ChannelNo)
	}
	// LEVEL:VALUES@HmIP-BROLL:3 — complex VALUES entry with inline comment.
	if entries[2].Comment != "inline comment" {
		t.Errorf("entry[2].Comment=%q want 'inline comment'", entries[2].Comment)
	}
	if entries[2].Model != "hmip-broll" {
		t.Errorf("entry[2].Model=%q want hmip-broll", entries[2].Model)
	}
	// PARTY_TIME_END:VALUES@HmIP-eTRV:0 — complex VALUES entry.
	if entries[3].ParamsetKey != hmenum.ParamsetKeyValues {
		t.Errorf("entry[3].ParamsetKey=%q want VALUES", entries[3].ParamsetKey)
	}
}

func TestParameterDeciderUnIgnoreOverridesGlobalHide(t *testing.T) {
	rules := NewRules()
	rules.HideGlobal(hmenum.ParameterPartyTemperature)
	d := NewParameterDecider(rules)

	if !d.IsParameterIgnored("HmIP-eTRV", "TRANSCEIVER", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterPartyTemperature) {
		t.Fatal("PartyTemperature must be ignored by default")
	}
	d.LoadUnIgnore([]UnIgnoreEntry{
		{Parameter: hmenum.ParameterPartyTemperature, Model: "HmIP-eTRV"},
	})
	if d.IsParameterIgnored("HmIP-eTRV", "TRANSCEIVER", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterPartyTemperature) {
		t.Fatal("un_ignore override must re-enable PartyTemperature")
	}
	// Cache must be respected on second call.
	if d.IsParameterIgnored("HmIP-eTRV", "TRANSCEIVER", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterPartyTemperature) {
		t.Fatal("cached lookup must agree with first call")
	}
	// Different model must not match.
	if !d.IsParameterIgnored("HmIP-RGBW", "TRANSCEIVER", channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterPartyTemperature) {
		t.Fatal("un_ignore must not leak across models")
	}
}

func TestModelValidatorRelevantMasterPrefixes(t *testing.T) {
	v := NewModelValidator()
	if !v.IsRelevantParamset("HmIP-Foo", hmenum.ParamsetKeyMaster) {
		t.Fatal("default allow-list (empty) must accept every model")
	}
	v.SetRelevantMasterPrefixes([]string{"HmIP-eTRV", "HmIP-WTH"})
	if !v.IsRelevantParamset("HmIP-eTRV-CL", hmenum.ParamsetKeyMaster) {
		t.Fatal("prefix HmIP-eTRV must match HmIP-eTRV-CL")
	}
	if v.IsRelevantParamset("HmIP-RGBW", hmenum.ParamsetKeyMaster) {
		t.Fatal("HmIP-RGBW must not be relevant for MASTER under restricted allow-list")
	}
	if !v.IsRelevantParamset("HmIP-RGBW", hmenum.ParamsetKeyValues) {
		t.Fatal("VALUES paramsets are always relevant")
	}
}

func TestRegistryIsAllowedRespectsModelAndParameter(t *testing.T) {
	r := NewRegistry()
	r.Model().IgnoreModel("HmIP-Internal")
	if r.IsAllowed("HmIP-Internal", "X", hmenum.ParamsetKeyValues, hmenum.ParameterLevel) {
		t.Fatal("ignored model must not be allowed")
	}
	if !r.IsAllowed("HmIP-eTRV", "TRANSCEIVER", hmenum.ParamsetKeyValues, hmenum.ParameterLevel) {
		t.Fatal("regular VALUES parameter must be allowed")
	}
}
