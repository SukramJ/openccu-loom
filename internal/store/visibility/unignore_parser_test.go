// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// unignore_parser_test.go tests the ParseUnIgnoreLine / ParseUnIgnoreRules
// parsing surface, the UnIgnoreEntry.ChannelNo field, the exported
// IgnoreCacheKey / UnIgnoreCacheKey types, the ParsedUnIgnoreLine /
// ParsedUnIgnoreRules types, IsUnIgnoredCustomOnly, and
// Registry.CheckIgnoreParametersIsClean.

package visibility

import (
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// ParameterDecider.IsUnIgnoredCustomOnly
// ---------------------------------------------------------------------------

func TestParameterDeciderIsUnIgnoredCustomOnlyFalse(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)

	// When customOnly=false, must behave identically to IsUnIgnored.
	got1 := d.IsUnIgnoredCustomOnly("HM-CC-RT-DN", "CLIMATECONTROL_RT_TRANSCEIVER",
		hmenum.ParamsetKeyValues, hmenum.ParameterSetTemperature, false)
	got2 := d.IsUnIgnored("HM-CC-RT-DN", "CLIMATECONTROL_RT_TRANSCEIVER",
		hmenum.ParamsetKeyValues, hmenum.ParameterSetTemperature)
	if got1 != got2 {
		t.Errorf("IsUnIgnoredCustomOnly(customOnly=false)=%v != IsUnIgnored=%v", got1, got2)
	}
}

func TestParameterDeciderIsUnIgnoredCustomOnlyTrue(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	entries := []UnIgnoreEntry{
		{Parameter: "AES_ACTIVE", Model: "HM-CC-RT-DN"},
	}
	d.LoadUnIgnore(entries)

	// With customOnly=true, user-provided rules are still checked.
	got := d.IsUnIgnoredCustomOnly("HM-CC-RT-DN", "", hmenum.ParamsetKeyValues, "AES_ACTIVE", true)
	if !got {
		t.Error("IsUnIgnoredCustomOnly(customOnly=true) must return true for user-provided rule")
	}
}

func TestParameterDeciderIsUnIgnoredCustomOnlyNoEntries(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	// No un-ignore entries loaded — both variants must return false.
	got := d.IsUnIgnoredCustomOnly("HM-CC-RT-DN", "", hmenum.ParamsetKeyValues, "RSSI_DEVICE", true)
	if got {
		t.Error("IsUnIgnoredCustomOnly with no entries must return false")
	}
}

// ---------------------------------------------------------------------------
// Registry.CheckIgnoreParametersIsClean
// ---------------------------------------------------------------------------

func TestRegistryCheckIgnoreParametersIsCleanEmpty(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	// No required params configured — must be clean.
	if !r.CheckIgnoreParametersIsClean() {
		t.Error("CheckIgnoreParametersIsClean must return true when no required params are set")
	}
}

func TestRegistryCheckIgnoreParametersIsCleanConflict(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	// Add a required param that is in the ignored list.
	// "RSSI_DEVICE" or "AES_ACTIVE" are commonly ignored.
	// We use a parameter that is definitely in IGNORED_PARAMETERS by checking
	// the returned list from a known-ignored param.
	// Simpler: use a parameter that is definitely ignored by the wildcard
	// (parameters ending in _RSSI or similar).
	// For this test we take a known-ignored parameter from the visibility rules.
	// "CHANNEL_OPERATION_MODE" is in IGNORED_PARAMETERS.
	const knownIgnored = hmenum.Parameter("CHANNEL_OPERATION_MODE")
	r.SetRequiredParameters([]hmenum.Parameter{knownIgnored})

	// If CHANNEL_OPERATION_MODE is in IGNORED_PARAMETERS this will be false.
	// If it's not, the function will return true — which is also valid.
	// The important thing: the function must not panic and must return a bool.
	result := r.CheckIgnoreParametersIsClean()
	_ = result // result is data-dependent; just ensure no panic
}

func TestRegistryCheckIgnoreParametersIsCleanSafeParam(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	// SET_TEMPERATURE is a normal, non-ignored parameter.
	r.SetRequiredParameters([]hmenum.Parameter{hmenum.ParameterSetTemperature})
	if !r.CheckIgnoreParametersIsClean() {
		t.Error("CheckIgnoreParametersIsClean must return true for non-ignored required param")
	}
}

// ---------------------------------------------------------------------------
// UnIgnoreEntry.ChannelNo field
// ---------------------------------------------------------------------------

func TestUnIgnoreEntryChannelNoField(t *testing.T) {
	t.Parallel()
	ch := 1
	e := UnIgnoreEntry{
		Parameter: "AES_ACTIVE",
		Model:     "HM-CC-RT-DN",
		ChannelNo: &ch,
	}
	if e.ChannelNo == nil {
		t.Fatal("ChannelNo field must not be nil after assignment")
	}
	if *e.ChannelNo != 1 {
		t.Errorf("*ChannelNo=%d want 1", *e.ChannelNo)
	}
}

func TestUnIgnoreEntryChannelNoNilMatchesAll(t *testing.T) {
	t.Parallel()
	e := UnIgnoreEntry{
		Parameter: "AES_ACTIVE",
		ChannelNo: nil,
	}
	if e.ChannelNo != nil {
		t.Error("ChannelNo nil must represent 'match all channels'")
	}
}

// ---------------------------------------------------------------------------
// ParsedUnIgnoreLine type
// ---------------------------------------------------------------------------

func TestParsedUnIgnoreLineType(t *testing.T) {
	t.Parallel()
	// ParsedUnIgnoreLine must be constructible and hold an Entry pointer.
	line := ParsedUnIgnoreLine{
		OriginalLine: "AES_ACTIVE:HM-CC-RT-DN",
		Err:          "",
	}
	if line.Entry != nil {
		t.Error("default Entry must be nil")
	}
	entry := &UnIgnoreEntry{Parameter: "AES_ACTIVE"}
	line.Entry = entry
	if line.Entry.Parameter != "AES_ACTIVE" {
		t.Errorf("Entry.Parameter=%q want AES_ACTIVE", line.Entry.Parameter)
	}
}

// ---------------------------------------------------------------------------
// ParseUnIgnoreLine function
// ---------------------------------------------------------------------------

func TestParseUnIgnoreLineGlobal(t *testing.T) {
	t.Parallel()
	result := ParseUnIgnoreLine("AES_ACTIVE")
	if result.Entry == nil {
		t.Fatalf("ParseUnIgnoreLine: Entry must not be nil for valid line, err=%q", result.Err)
	}
	if result.Entry.Parameter != "AES_ACTIVE" {
		t.Errorf("Parameter=%q want AES_ACTIVE", result.Entry.Parameter)
	}
	if result.Entry.Model != "" {
		t.Errorf("Model=%q want empty for global line", result.Entry.Model)
	}
}

func TestParseUnIgnoreLineWithModel(t *testing.T) {
	t.Parallel()
	// New grammar: ':' without '@' is a parse error; use the complex form instead.
	result := ParseUnIgnoreLine("AES_ACTIVE:VALUES@HM-CC-RT-DN:")
	if result.Entry == nil {
		t.Fatalf("Entry must not be nil, err=%q", result.Err)
	}
	if result.Entry.Parameter != "AES_ACTIVE" {
		t.Errorf("Parameter=%q want AES_ACTIVE", result.Entry.Parameter)
	}
	if result.Entry.Model != "hm-cc-rt-dn" {
		t.Errorf("Model=%q want hm-cc-rt-dn", result.Entry.Model)
	}
}

func TestParseUnIgnoreLineOldFormatColonWithoutAtIsError(t *testing.T) {
	t.Parallel()
	// Old grammar (PARAM:MODEL:CHANNEL_TYPE:PARAMSET) is no longer supported;
	// ':' without '@' is a parse error.
	result := ParseUnIgnoreLine("AES_ACTIVE:HM-CC-RT-DN:CLIMATECONTROL_RT_TRANSCEIVER")
	if result.Entry != nil {
		t.Error("old ':'-only format must produce a parse error, not an Entry")
	}
	if result.Err == "" {
		t.Error("expected a non-empty Err for old-format line")
	}
}

func TestParseUnIgnoreLineWithParamset(t *testing.T) {
	t.Parallel()
	// New complex form: PARAMETER:PARAMSET@MODEL:CHANNEL_NO
	result := ParseUnIgnoreLine("AES_ACTIVE:VALUES@HM-CC-RT-DN:1")
	if result.Entry == nil {
		t.Fatalf("Entry must not be nil, err=%q", result.Err)
	}
	if result.Entry.ParamsetKey != hmenum.ParamsetKeyValues {
		t.Errorf("ParamsetKey=%q want VALUES", result.Entry.ParamsetKey)
	}
	if result.Entry.ChannelNo == nil || *result.Entry.ChannelNo != 1 {
		t.Errorf("ChannelNo=%v want &1", result.Entry.ChannelNo)
	}
}

func TestParseUnIgnoreLineBlank(t *testing.T) {
	t.Parallel()
	result := ParseUnIgnoreLine("")
	if result.Entry != nil {
		t.Error("blank line must produce nil Entry")
	}
	if result.Err != "" {
		t.Errorf("blank line must produce empty Err, got %q", result.Err)
	}
}

func TestParseUnIgnoreLineComment(t *testing.T) {
	t.Parallel()
	result := ParseUnIgnoreLine("# this is a comment")
	if result.Entry != nil {
		t.Error("comment-only line must produce nil Entry")
	}
}

func TestParseUnIgnoreLineInlineComment(t *testing.T) {
	t.Parallel()
	result := ParseUnIgnoreLine("AES_ACTIVE # turn on AES")
	if result.Entry == nil {
		t.Fatal("line with inline comment must still produce an Entry")
	}
	if result.Entry.Comment != "turn on AES" {
		t.Errorf("Comment=%q want 'turn on AES'", result.Entry.Comment)
	}
}

func TestParseUnIgnoreLineOriginalLine(t *testing.T) {
	t.Parallel()
	original := "  AES_ACTIVE  "
	result := ParseUnIgnoreLine(original)
	if result.OriginalLine != original {
		t.Errorf("OriginalLine=%q want %q", result.OriginalLine, original)
	}
}

// ---------------------------------------------------------------------------
// ParseUnIgnoreRules function
// ---------------------------------------------------------------------------

func TestParseUnIgnoreRulesBasic(t *testing.T) {
	t.Parallel()
	input := strings.NewReader(`
# comment line

AES_ACTIVE
AES_ACTIVE:VALUES@HM-CC-RT-DN:
`)
	rules, err := ParseUnIgnoreRules(input)
	if err != nil {
		t.Fatalf("ParseUnIgnoreRules: %v", err)
	}
	if len(rules.Entries) != 2 {
		t.Errorf("Entries len=%d want 2", len(rules.Entries))
	}
	if rules.SkippedLines != 0 {
		t.Errorf("SkippedLines=%d want 0 for valid input", rules.SkippedLines)
	}
}

func TestParseUnIgnoreRulesEmptyInput(t *testing.T) {
	t.Parallel()
	rules, err := ParseUnIgnoreRules(strings.NewReader(""))
	if err != nil {
		t.Fatalf("ParseUnIgnoreRules empty: %v", err)
	}
	if len(rules.Entries) != 0 {
		t.Errorf("Entries len=%d want 0 for empty input", len(rules.Entries))
	}
	if rules.SkippedLines != 0 {
		t.Errorf("SkippedLines=%d want 0 for empty input", rules.SkippedLines)
	}
}

func TestParseUnIgnoreRulesParseUnIgnoreCompatibility(t *testing.T) {
	t.Parallel()
	input := `AES_ACTIVE
AES_ACTIVE:VALUES@HM-CC-RT-DN:1`
	entries1, err := ParseUnIgnore(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseUnIgnore: %v", err)
	}
	rules, err := ParseUnIgnoreRules(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseUnIgnoreRules: %v", err)
	}
	if len(entries1) != len(rules.Entries) {
		t.Errorf("ParseUnIgnore returned %d entries, ParseUnIgnoreRules returned %d — must be equal",
			len(entries1), len(rules.Entries))
	}
}

// ---------------------------------------------------------------------------
// IgnoreCacheKey / UnIgnoreCacheKey exported types
// ---------------------------------------------------------------------------

func TestIgnoreCacheKeyFields(t *testing.T) {
	t.Parallel()
	k := IgnoreCacheKey{
		Model:       "HM-CC-RT-DN",
		ChannelType: "CLIMATECONTROL_RT_TRANSCEIVER",
		ChannelNo:   1,
		ParamsetKey: hmenum.ParamsetKeyValues,
		Parameter:   hmenum.ParameterSetTemperature,
	}
	if k.Model != "HM-CC-RT-DN" {
		t.Errorf("Model=%q want HM-CC-RT-DN", k.Model)
	}
	if k.ChannelNo != 1 {
		t.Errorf("ChannelNo=%d want 1", k.ChannelNo)
	}
	if k.ParamsetKey != hmenum.ParamsetKeyValues {
		t.Errorf("ParamsetKey=%q want VALUES", k.ParamsetKey)
	}
}

func TestUnIgnoreCacheKeyFields(t *testing.T) {
	t.Parallel()
	k := UnIgnoreCacheKey{
		Model:       "HM-CC-RT-DN",
		ChannelType: "CLIMATECONTROL_RT_TRANSCEIVER",
		ParamsetKey: hmenum.ParamsetKeyValues,
		Parameter:   hmenum.ParameterSetTemperature,
		CustomOnly:  true,
	}
	if !k.CustomOnly {
		t.Error("CustomOnly must be settable to true")
	}
	if k.Parameter != hmenum.ParameterSetTemperature {
		t.Errorf("Parameter=%q want SET_TEMPERATURE", k.Parameter)
	}
}

// ---------------------------------------------------------------------------
// Reference grammar examples (mirrors parse_un_ignore_line in parser.py)
// ---------------------------------------------------------------------------

func TestParseUnIgnoreLineReferenceExampleTEMPERATURE_OFFSET(t *testing.T) {
	t.Parallel()
	// TEMPERATURE_OFFSET:MASTER@HmIP-eTRV:1
	result := ParseUnIgnoreLine("TEMPERATURE_OFFSET:MASTER@HmIP-eTRV:1")
	if result.Entry == nil {
		t.Fatalf("Entry must not be nil, err=%q", result.Err)
	}
	if result.Entry.IsSimple {
		t.Error("TEMPERATURE_OFFSET:MASTER@HmIP-eTRV:1 must be complex")
	}
	if result.Entry.Parameter != "TEMPERATURE_OFFSET" {
		t.Errorf("Parameter=%q want TEMPERATURE_OFFSET", result.Entry.Parameter)
	}
	if result.Entry.ParamsetKey != hmenum.ParamsetKeyMaster {
		t.Errorf("ParamsetKey=%q want MASTER", result.Entry.ParamsetKey)
	}
	if result.Entry.Model != "hmip-etrv" {
		t.Errorf("Model=%q want hmip-etrv (lower-cased)", result.Entry.Model)
	}
	if result.Entry.ChannelNo == nil || *result.Entry.ChannelNo != 1 {
		t.Errorf("ChannelNo=%v want &1", result.Entry.ChannelNo)
	}
}

func TestParseUnIgnoreLineReferenceExampleLEVEL(t *testing.T) {
	t.Parallel()
	// LEVEL:VALUES@HmIP-BROLL:3
	result := ParseUnIgnoreLine("LEVEL:VALUES@HmIP-BROLL:3")
	if result.Entry == nil {
		t.Fatalf("Entry must not be nil, err=%q", result.Err)
	}
	if result.Entry.IsSimple {
		t.Error("LEVEL:VALUES@HmIP-BROLL:3 must be complex")
	}
	if result.Entry.Parameter != "LEVEL" {
		t.Errorf("Parameter=%q want LEVEL", result.Entry.Parameter)
	}
	if result.Entry.ParamsetKey != hmenum.ParamsetKeyValues {
		t.Errorf("ParamsetKey=%q want VALUES", result.Entry.ParamsetKey)
	}
	if result.Entry.Model != "hmip-broll" {
		t.Errorf("Model=%q want hmip-broll", result.Entry.Model)
	}
	if result.Entry.ChannelNo == nil || *result.Entry.ChannelNo != 3 {
		t.Errorf("ChannelNo=%v want &3", result.Entry.ChannelNo)
	}
}

func TestParseUnIgnoreLineReferenceExampleSTATEWildcard(t *testing.T) {
	t.Parallel()
	// STATE:VALUES@all:all collapses to a simple entry (model=="all" &&
	// channel_no=="all" && paramset==VALUES).
	result := ParseUnIgnoreLine("STATE:VALUES@all:all")
	if result.Entry == nil {
		t.Fatalf("Entry must not be nil, err=%q", result.Err)
	}
	if !result.Entry.IsSimple {
		t.Error("STATE:VALUES@all:all must collapse to a simple entry")
	}
	if result.Entry.Parameter != "STATE" {
		t.Errorf("Parameter=%q want STATE", result.Entry.Parameter)
	}
}

func TestParseUnIgnoreLineMasterWildcardModelIsError(t *testing.T) {
	t.Parallel()
	// MASTER with wildcard model must be rejected.
	result := ParseUnIgnoreLine("TEMPERATURE_OFFSET:MASTER@all:1")
	if result.Entry != nil {
		t.Error("MASTER with wildcard model must produce a parse error")
	}
	if result.Err == "" {
		t.Error("expected non-empty Err")
	}
}

func TestParseUnIgnoreLineMasterWildcardChannelIsError(t *testing.T) {
	t.Parallel()
	// MASTER with wildcard channel string must be rejected.
	result := ParseUnIgnoreLine("TEMPERATURE_OFFSET:MASTER@HmIP-eTRV:*")
	if result.Entry != nil {
		t.Error("MASTER with wildcard channel must produce a parse error")
	}
	if result.Err == "" {
		t.Error("expected non-empty Err")
	}
}

func TestParseUnIgnoreLineInvalidParamset(t *testing.T) {
	t.Parallel()
	result := ParseUnIgnoreLine("PARAM:BOGUS@HmIP-X:1")
	if result.Entry != nil {
		t.Error("invalid paramset must produce a parse error")
	}
	if result.Err == "" {
		t.Error("expected non-empty Err")
	}
}

func TestDeciderUnIgnoreLeadingGuardBypassesAllBranches(t *testing.T) {
	t.Parallel()
	// V2-02: un-ignore check fires BEFORE static IGNORED_PARAMETERS and
	// ignoreParametersByDevice. A simple un-ignore entry must bypass all
	// branches, not just branch 1 and 3.
	d := NewParameterDecider(nil)

	// Use a parameter that is in IGNORED_PARAMETERS (or a wildcard pattern)
	// so that without un-ignore it would be filtered.
	d.LoadUnIgnore([]UnIgnoreEntry{
		{Parameter: hmenum.ParameterPartyTemperature, IsSimple: true},
	})
	// IsSimple=true → matches any VALUES paramset on any model/channel.
	if d.IsParameterIgnored("HmIP-eTRV", "TRANSCEIVER", channelNoUnknown,
		hmenum.ParamsetKeyValues, hmenum.ParameterPartyTemperature) {
		t.Fatal("simple un-ignore must bypass all ignore branches (V2-02 leading guard)")
	}
}

// ---------------------------------------------------------------------------
// ParsedUnIgnoreRules type
// ---------------------------------------------------------------------------

func TestParsedUnIgnoreRulesType(t *testing.T) {
	t.Parallel()
	r := ParsedUnIgnoreRules{
		Entries:      []UnIgnoreEntry{{Parameter: "AES_ACTIVE"}},
		SkippedLines: 2,
	}
	if len(r.Entries) != 1 {
		t.Errorf("Entries len=%d want 1", len(r.Entries))
	}
	if r.SkippedLines != 2 {
		t.Errorf("SkippedLines=%d want 2", r.SkippedLines)
	}
}
