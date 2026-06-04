// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configui

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// --- Widget --- ------------------------------------------------------

func TestDetermineWidgetBool(t *testing.T) {
	if got := DetermineWidget(hmproto.ParameterData{Type: hmenum.ParameterTypeBool}); got != WidgetToggle {
		t.Fatalf("bool=%s want %s", got, WidgetToggle)
	}
}

func TestDetermineWidgetIntegerSliderVsNumber(t *testing.T) {
	small := hmproto.ParameterData{
		Type: hmenum.ParameterTypeInteger,
		Min:  json.RawMessage("0"),
		Max:  json.RawMessage("10"),
	}
	if got := DetermineWidget(small); got != WidgetSliderWithInput {
		t.Fatalf("range=10 → %s want slider", got)
	}
	big := hmproto.ParameterData{
		Type: hmenum.ParameterTypeInteger,
		Min:  json.RawMessage("0"),
		Max:  json.RawMessage("100"),
	}
	if got := DetermineWidget(big); got != WidgetNumberInput {
		t.Fatalf("range=100 → %s want number_input", got)
	}
	noBounds := hmproto.ParameterData{Type: hmenum.ParameterTypeInteger}
	if got := DetermineWidget(noBounds); got != WidgetNumberInput {
		t.Fatalf("no bounds → %s want number_input", got)
	}
}

func TestDetermineWidgetFloatSliderVsNumber(t *testing.T) {
	small := hmproto.ParameterData{
		Type: hmenum.ParameterTypeFloat,
		Min:  json.RawMessage("0"),
		Max:  json.RawMessage("100"),
	}
	if got := DetermineWidget(small); got != WidgetSliderWithInput {
		t.Fatalf("range=100 → %s want slider (boundary)", got)
	}
	big := hmproto.ParameterData{
		Type: hmenum.ParameterTypeFloat,
		Min:  json.RawMessage("0"),
		Max:  json.RawMessage("1000"),
	}
	if got := DetermineWidget(big); got != WidgetNumberInput {
		t.Fatalf("range=1000 → %s want number_input", got)
	}
}

func TestDetermineWidgetEnumRadioVsDropdown(t *testing.T) {
	small := hmproto.ParameterData{
		Type:      hmenum.ParameterTypeEnum,
		ValueList: []string{"A", "B", "C", "D"},
	}
	if got := DetermineWidget(small); got != WidgetRadioGroup {
		t.Fatalf("enum=4 → %s want radio_group", got)
	}
	big := hmproto.ParameterData{
		Type:      hmenum.ParameterTypeEnum,
		ValueList: []string{"A", "B", "C", "D", "E"},
	}
	if got := DetermineWidget(big); got != WidgetDropdown {
		t.Fatalf("enum=5 → %s want dropdown", got)
	}
}

func TestDetermineWidgetStringAndAction(t *testing.T) {
	if got := DetermineWidget(hmproto.ParameterData{Type: hmenum.ParameterTypeString}); got != WidgetTextInput {
		t.Fatalf("string=%s want text_input", got)
	}
	if got := DetermineWidget(hmproto.ParameterData{Type: hmenum.ParameterTypeAction}); got != WidgetButton {
		t.Fatalf("action=%s want button", got)
	}
}

func TestDetermineWidgetUnknownDefaultsReadOnly(t *testing.T) {
	got := DetermineWidget(hmproto.ParameterData{Type: hmenum.ParameterTypeEmpty})
	if got != WidgetReadOnly {
		t.Fatalf("empty=%s want read_only", got)
	}
}

// --- Session --- -----------------------------------------------------

func sampleDescriptions() map[string]hmproto.ParameterData {
	return map[string]hmproto.ParameterData{
		"TEMPERATURE_OFFSET": {Type: hmenum.ParameterTypeFloat, Default: json.RawMessage("0")},
		"DECAL":              {Type: hmenum.ParameterTypeBool, Default: json.RawMessage("false")},
	}
}

func TestSessionInitiallyClean(t *testing.T) {
	s := NewSession(sampleDescriptions(), map[string]any{"TEMPERATURE_OFFSET": 0.5, "DECAL": false})
	if s.IsDirty() {
		t.Fatal("fresh session must not be dirty")
	}
	if s.CanUndo() || s.CanRedo() {
		t.Fatal("fresh session must have empty stacks")
	}
	if len(s.Changes()) != 0 {
		t.Fatalf("Changes=%+v want empty", s.Changes())
	}
}

func TestSessionSetTracksUndoAndDirty(t *testing.T) {
	s := NewSession(sampleDescriptions(), map[string]any{"TEMPERATURE_OFFSET": 0.5})
	s.Set("TEMPERATURE_OFFSET", 1.5)
	if !s.IsDirty() {
		t.Fatal("after Set must be dirty")
	}
	if !s.CanUndo() {
		t.Fatal("after Set must allow undo")
	}
	if got := s.Changes(); len(got) != 1 || got["TEMPERATURE_OFFSET"] != 1.5 {
		t.Fatalf("Changes=%+v", got)
	}
}

func TestSessionSetSameValueIsNoOp(t *testing.T) {
	s := NewSession(sampleDescriptions(), map[string]any{"TEMPERATURE_OFFSET": 0.5})
	s.Set("TEMPERATURE_OFFSET", 0.5)
	if s.IsDirty() || s.CanUndo() {
		t.Fatal("Set with identical value must not push undo entry")
	}
}

func TestSessionUndoRedoRoundTrip(t *testing.T) {
	s := NewSession(sampleDescriptions(), map[string]any{"TEMPERATURE_OFFSET": 0.5})
	s.Set("TEMPERATURE_OFFSET", 1.5)
	if !s.Undo() {
		t.Fatal("Undo must succeed")
	}
	if s.IsDirty() {
		t.Fatal("after Undo must be clean again")
	}
	if !s.CanRedo() {
		t.Fatal("Redo must be available after Undo")
	}
	if !s.Redo() {
		t.Fatal("Redo must succeed")
	}
	if !s.IsDirty() || s.CanRedo() {
		t.Fatal("after Redo: dirty + redo-stack drained")
	}
	if got := s.CurrentValue("TEMPERATURE_OFFSET"); got != 1.5 {
		t.Fatalf("after Redo current=%v want 1.5", got)
	}
}

func TestSessionSetClearsRedoStack(t *testing.T) {
	s := NewSession(sampleDescriptions(), map[string]any{"TEMPERATURE_OFFSET": 0.5})
	s.Set("TEMPERATURE_OFFSET", 1.5)
	s.Undo()
	if !s.CanRedo() {
		t.Fatal("redo must be available")
	}
	s.Set("TEMPERATURE_OFFSET", 2.5)
	if s.CanRedo() {
		t.Fatal("Set after Undo must clear the redo stack")
	}
}

func TestSessionDiscardRevertsAll(t *testing.T) {
	s := NewSession(sampleDescriptions(), map[string]any{"TEMPERATURE_OFFSET": 0.5, "DECAL": false})
	s.Set("TEMPERATURE_OFFSET", 1.5)
	s.Set("DECAL", true)
	s.Discard()
	if s.IsDirty() {
		t.Fatal("after Discard must be clean")
	}
	if s.CanUndo() || s.CanRedo() {
		t.Fatal("after Discard stacks must be empty")
	}
	if s.CurrentValue("TEMPERATURE_OFFSET") != 0.5 || s.CurrentValue("DECAL") != false {
		t.Fatalf("after Discard values not restored: %v %v", s.CurrentValue("TEMPERATURE_OFFSET"), s.CurrentValue("DECAL"))
	}
}

func TestSessionResetToDefaultsUsesDescriptors(t *testing.T) {
	s := NewSession(sampleDescriptions(), map[string]any{"TEMPERATURE_OFFSET": 1.5, "DECAL": true})
	s.ResetToDefaults()
	// JSON unmarshals "0" as float64; "false" as bool. We accept any
	// numeric float so tests stay portable.
	if got := s.CurrentValue("TEMPERATURE_OFFSET"); got != float64(0) {
		t.Fatalf("TEMPERATURE_OFFSET=%v want 0", got)
	}
	if got := s.CurrentValue("DECAL"); got != false {
		t.Fatalf("DECAL=%v want false", got)
	}
	// Each reset is recorded — undo brings the originals back.
	for s.CanUndo() {
		s.Undo()
	}
	if got := s.CurrentValue("TEMPERATURE_OFFSET"); got != 1.5 {
		t.Fatalf("after Undo TEMPERATURE_OFFSET=%v want 1.5 (original)", got)
	}
}

func TestSessionChangesOnlyShowsDelta(t *testing.T) {
	s := NewSession(sampleDescriptions(), map[string]any{"TEMPERATURE_OFFSET": 0.5, "DECAL": false})
	s.Set("DECAL", true)
	got := s.Changes()
	if len(got) != 1 {
		t.Fatalf("Changes=%v want one entry", got)
	}
	if got["DECAL"] != true {
		t.Fatalf("DECAL=%v want true", got["DECAL"])
	}
}

func TestSessionChangedParametersCarriesFromTo(t *testing.T) {
	s := NewSession(sampleDescriptions(), map[string]any{"TEMPERATURE_OFFSET": 0.5})
	s.Set("TEMPERATURE_OFFSET", 1.5)
	got := s.ChangedParameters()
	if len(got) != 1 {
		t.Fatalf("changed=%+v", got)
	}
	if got[0].Parameter != "TEMPERATURE_OFFSET" || got[0].From != 0.5 || got[0].To != 1.5 {
		t.Fatalf("change=%+v", got[0])
	}
}

// --- LabelResolver --- ---------------------------------------------

type stubProvider struct {
	entries map[string]string
}

func (p *stubProvider) ParameterTranslation(parameter, channelType, locale string) string {
	if t, ok := p.entries[parameter+"|"+channelType+"|"+locale]; ok {
		return t
	}
	return ""
}

func TestHumanizeUppercaseSnakeCase(t *testing.T) {
	cases := map[string]string{
		"TEMPERATURE_OFFSET":  "Temperature Offset",
		"LEVEL":               "Level",
		"":                    "",
		"BURST_LIMIT_WARNING": "Burst Limit Warning",
	}
	for in, want := range cases {
		if got := Humanize(in); got != want {
			t.Fatalf("Humanize(%q)=%q want %q", in, got, want)
		}
	}
}

func TestLabelResolverFallsBackToHumanize(t *testing.T) {
	r := NewLabelResolver(nil, "de")
	if got := r.Resolve("TEMPERATURE_OFFSET", ""); got != "Temperature Offset" {
		t.Fatalf("nil provider must fall back: got %q", got)
	}
	if r.HasTranslation("TEMPERATURE_OFFSET", "") {
		t.Fatal("nil provider must report no translation")
	}
	if r.Locale() != "de" {
		t.Fatalf("locale=%q want de", r.Locale())
	}
}

func TestLabelResolverPrefersChannelTypeMatch(t *testing.T) {
	provider := &stubProvider{
		entries: map[string]string{
			"TEMPERATURE_OFFSET|CLIMATE_CONTROL|de": "Klima-Offset",
			"TEMPERATURE_OFFSET||de":                "Allgemein-Offset",
		},
	}
	r := NewLabelResolver(provider, "de")
	if got := r.Resolve("TEMPERATURE_OFFSET", "CLIMATE_CONTROL"); got != "Klima-Offset" {
		t.Fatalf("channel-specific=%q want Klima-Offset", got)
	}
	if got := r.Resolve("TEMPERATURE_OFFSET", ""); got != "Allgemein-Offset" {
		t.Fatalf("generic=%q want Allgemein-Offset", got)
	}
	if got := r.Resolve("UNKNOWN_PARAM", "CLIMATE_CONTROL"); got != "Unknown Param" {
		t.Fatalf("missing=%q want fallback", got)
	}
}

func TestLabelResolverHasTranslationFallsThroughToGeneric(t *testing.T) {
	provider := &stubProvider{
		entries: map[string]string{
			"FOO||en": "Foo Label",
		},
	}
	r := NewLabelResolver(provider, "en")
	// Channel-specific lookup fails, generic lookup succeeds.
	if !r.HasTranslation("FOO", "ANY_CHANNEL") {
		t.Fatal("HasTranslation must fall through to the parameter-only lookup")
	}
	if r.HasTranslation("BAR", "") {
		t.Fatal("BAR has no translation in either form")
	}
}

func TestLabelResolverDefaultLocale(t *testing.T) {
	r := NewLabelResolver(nil, "")
	if r.Locale() != DefaultLocale {
		t.Fatalf("default locale=%q want %q", r.Locale(), DefaultLocale)
	}
}

// --- Generator --- ---------------------------------------------------

func TestGenerateProducesOneSectionWithSortedParams(t *testing.T) {
	in := GenerateInput{
		ChannelAddress: "0001ABCD:1",
		ChannelType:    "CLIMATE_TRANSCEIVER",
		Descriptions: map[string]hmproto.ParameterData{
			"TEMPERATURE_OFFSET": {
				Type:       hmenum.ParameterTypeFloat,
				Min:        json.RawMessage("-3.5"),
				Max:        json.RawMessage("3.5"),
				Default:    json.RawMessage("0"),
				Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
				Unit:       "°C",
			},
			"BOOST_MODE": {
				Type:       hmenum.ParameterTypeBool,
				Default:    json.RawMessage("false"),
				Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
			},
		},
		CurrentValues: map[string]any{
			"TEMPERATURE_OFFSET": 1.5,
			"BOOST_MODE":         false,
		},
	}
	s := Generate(in)
	if len(s.Sections) != 1 {
		t.Fatalf("sections=%d want 1", len(s.Sections))
	}
	params := s.Sections[0].Parameters
	if len(params) != 2 {
		t.Fatalf("params=%d want 2", len(params))
	}
	// Sorted alphabetically: BOOST_MODE before TEMPERATURE_OFFSET.
	if params[0].ID != "BOOST_MODE" || params[1].ID != "TEMPERATURE_OFFSET" {
		t.Fatalf("order=%s,%s want BOOST_MODE,TEMPERATURE_OFFSET", params[0].ID, params[1].ID)
	}
	if s.TotalParameters != 2 || s.WritableParameters != 2 {
		t.Fatalf("counts: total=%d writable=%d", s.TotalParameters, s.WritableParameters)
	}
	if s.Sections[0].Parameters[0].Widget != string(WidgetToggle) {
		t.Fatalf("BOOST_MODE widget=%q want toggle", s.Sections[0].Parameters[0].Widget)
	}
	if s.Sections[0].Parameters[1].Widget != string(WidgetSliderWithInput) {
		t.Fatalf("TEMPERATURE_OFFSET widget=%q want slider_with_input", s.Sections[0].Parameters[1].Widget)
	}
}

func TestGenerateAppliesLabelResolver(t *testing.T) {
	provider := &stubProvider{
		entries: map[string]string{
			"TEMPERATURE_OFFSET||en": "Temperature Offset (translated)",
		},
	}
	in := GenerateInput{
		ChannelAddress: "addr:1",
		LabelResolver:  NewLabelResolver(provider, "en"),
		Descriptions: map[string]hmproto.ParameterData{
			"TEMPERATURE_OFFSET": {Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
		},
	}
	got := Generate(in).Sections[0].Parameters[0].Label
	if got != "Temperature Offset (translated)" {
		t.Fatalf("label=%q want translated", got)
	}
}

func TestGenerateMarksModifiedWhenCurrentDiffersFromDefault(t *testing.T) {
	in := GenerateInput{
		Descriptions: map[string]hmproto.ParameterData{
			"BOOST_MODE": {
				Type:       hmenum.ParameterTypeBool,
				Default:    json.RawMessage("false"),
				Operations: hmenum.OperationsWrite,
			},
		},
		CurrentValues: map[string]any{"BOOST_MODE": true},
	}
	p := Generate(in).Sections[0].Parameters[0]
	if !p.Modified {
		t.Fatal("BOOST_MODE current=true vs default=false must be modified")
	}
}

func TestGenerateDoesNotMarkModifiedWhenAtDefault(t *testing.T) {
	in := GenerateInput{
		Descriptions: map[string]hmproto.ParameterData{
			"BOOST_MODE": {
				Type:       hmenum.ParameterTypeBool,
				Default:    json.RawMessage("false"),
				Operations: hmenum.OperationsWrite,
			},
		},
		CurrentValues: map[string]any{"BOOST_MODE": false},
	}
	p := Generate(in).Sections[0].Parameters[0]
	if p.Modified {
		t.Fatal("BOOST_MODE current=false vs default=false must not be modified")
	}
}

func TestGrouperPutsParametersIntoCuratedSections(t *testing.T) {
	g := NewParameterGrouper(nil)
	got := g.Group([]string{
		"TEMPERATURE_OFFSET", "BOOST_TIME", "DISPLAY_BRIGHTNESS", "RANDOM_PARAM",
	})
	idsInOrder := make([]string, 0, len(got))
	for _, group := range got {
		idsInOrder = append(idsInOrder, group.ID)
	}
	// Curated order: temperature → timing? no — BOOST_TIME hits timing
	// pattern via _TIME_, but BOOST also hits the "boost" group.
	// Because temperature comes first, then timing, then display:
	// expected order has temperature, timing, display, other.
	if len(got) < 3 {
		t.Fatalf("expected at least 3 groups, got=%v", idsInOrder)
	}
	// "other" must be last.
	if got[len(got)-1].ID != "other" {
		t.Fatalf("last group=%s want other", got[len(got)-1].ID)
	}
	// RANDOM_PARAM must be in "other".
	if slices.Contains(got[len(got)-1].Parameters, "RANDOM_PARAM") {
		return
	}
	t.Fatalf("RANDOM_PARAM not in other group: %+v", got[len(got)-1])
}

func TestGrouperEmptyInputReturnsNil(t *testing.T) {
	g := NewParameterGrouper(nil)
	if got := g.Group(nil); got != nil {
		t.Fatalf("nil input → %v want nil", got)
	}
}

func TestGrouperSkipsEmptyCuratedSections(t *testing.T) {
	g := NewParameterGrouper(nil)
	got := g.Group([]string{"TEMPERATURE_OFFSET"})
	if len(got) != 1 {
		t.Fatalf("len=%d want 1 (only temperature non-empty)", len(got))
	}
	if got[0].ID != "temperature" {
		t.Fatalf("id=%s want temperature", got[0].ID)
	}
}

func TestGrouperFirstMatchWins(t *testing.T) {
	g := NewParameterGrouper(nil)
	// BOOST_TIME_OFFSET matches timing (_TIME_*) and boost (^BOOST_*).
	// The grouper visits definitions in declaration order — timing
	// comes before boost, so the parameter lands in timing.
	got := g.Group([]string{"BOOST_TIME_OFFSET"})
	if len(got) != 1 {
		t.Fatalf("len=%d want 1", len(got))
	}
	if got[0].ID != "timing" {
		t.Fatalf("id=%s want timing (first-match-wins; boost comes later)", got[0].ID)
	}
}

func TestGrouperCustomDefinitionsOverrideCurated(t *testing.T) {
	g := NewParameterGrouper([]GroupDefinition{
		{ID: "boost", Title: "Boost First", Patterns: []string{`^BOOST_.*`}},
	})
	got := g.Group([]string{"BOOST_TIME"})
	if len(got) != 1 || got[0].ID != "boost" {
		t.Fatalf("custom-only=%+v want single boost", got)
	}
}

type fakeUILabels map[string]map[string]string

func (f fakeUILabels) UILabel(key, locale string) string {
	if l, ok := f[key]; ok {
		return l[locale]
	}
	return ""
}

func TestGroupForChannelUsesMetadataGroupsWhenAvailable(t *testing.T) {
	g := NewParameterGrouper(nil)
	got := g.GroupForChannel(
		[]string{"TEMPERATURE_OFFSET", "BOOST_DURATION", "RANDOM_X"},
		GroupChannelOptions{
			Groups: []MetadataGroup{
				{ID: "auto-mode", LabelKey: "grp.auto", Parameters: []string{"BOOST_DURATION"}},
				{ID: "comfort", LabelKey: "grp.comfort", Parameters: []string{"TEMPERATURE_OFFSET"}},
			},
			UILabels: fakeUILabels{
				"grp.auto":    {"en": "Auto Mode"},
				"grp.comfort": {"en": "Comfort", "de": "Komfort"},
			},
			Locale: "de",
		},
	)
	if len(got) != 3 {
		t.Fatalf("len=%d want 3 (2 metadata + 1 ungrouped)", len(got))
	}
	if got[0].ID != "auto-mode" || got[0].Title != "Auto Mode" || got[0].Parameters[0] != "BOOST_DURATION" {
		t.Fatalf("group[0]=%+v want auto-mode(Auto Mode → en fallback)", got[0])
	}
	if got[1].ID != "comfort" || got[1].Title != "Komfort" {
		t.Fatalf("group[1]=%+v want comfort(Komfort)", got[1])
	}
	if got[2].ID != otherGroupID || got[2].Parameters[0] != "RANDOM_X" {
		t.Fatalf("ungrouped=%+v want other(RANDOM_X)", got[2])
	}
}

func TestGroupForChannelMissingLabelFallsBackToOtherTitle(t *testing.T) {
	g := NewParameterGrouper(nil)
	got := g.GroupForChannel(
		[]string{"P1"},
		GroupChannelOptions{
			Groups: []MetadataGroup{{ID: "g", LabelKey: "no-such-key", Parameters: []string{"P1"}}},
			// No UILabels provided.
		},
	)
	if len(got) != 1 || got[0].Title != otherGroupTitle {
		t.Fatalf("got=%+v want one group titled %q", got, otherGroupTitle)
	}
}

// TestGroupForChannelInlineLabelFallback verifies that the Stage-3/4 inline
// Label map on MetadataGroup is used when LabelKey resolution yields nothing.
func TestGroupForChannelInlineLabelFallback(t *testing.T) {
	g := NewParameterGrouper(nil)

	// Stage 3: locale match.
	got := g.GroupForChannel(
		[]string{"P1"},
		GroupChannelOptions{
			Groups: []MetadataGroup{{
				ID:         "grp",
				LabelKey:   "",
				Label:      map[string]string{"de": "Automatik", "en": "Auto"},
				Parameters: []string{"P1"},
			}},
			Locale: "de",
		},
	)
	if len(got) != 1 || got[0].Title != "Automatik" {
		t.Fatalf("stage-3 (locale): got=%+v want title %q", got, "Automatik")
	}

	// Stage 4: English fallback when requested locale not in map.
	got = g.GroupForChannel(
		[]string{"P1"},
		GroupChannelOptions{
			Groups: []MetadataGroup{{
				ID:         "grp",
				LabelKey:   "",
				Label:      map[string]string{"en": "Auto"},
				Parameters: []string{"P1"},
			}},
			Locale: "fr",
		},
	)
	if len(got) != 1 || got[0].Title != "Auto" {
		t.Fatalf("stage-4 (en fallback): got=%+v want title %q", got, "Auto")
	}

	// UILabel-table wins over inline Label when LabelKey resolves.
	got = g.GroupForChannel(
		[]string{"P1"},
		GroupChannelOptions{
			Groups: []MetadataGroup{{
				ID:         "grp",
				LabelKey:   "auto_mode",
				Label:      map[string]string{"en": "Inline"},
				Parameters: []string{"P1"},
			}},
			UILabels: fakeUILabels{"auto_mode": {"en": "TableLabel"}},
			Locale:   "en",
		},
	)
	if len(got) != 1 || got[0].Title != "TableLabel" {
		t.Fatalf("table-wins: got=%+v want title %q", got, "TableLabel")
	}
}

func TestGroupForChannelMetadataPreservesDefinitionOrderAndDeduplicates(t *testing.T) {
	g := NewParameterGrouper(nil)
	got := g.GroupForChannel(
		[]string{"A", "B", "C"},
		GroupChannelOptions{
			Groups: []MetadataGroup{
				// First metadata group claims A and B.
				{ID: "g1", Parameters: []string{"A", "B"}},
				// Second tries to also claim B — must be skipped (no double-counting).
				{ID: "g2", Parameters: []string{"B", "C"}},
			},
		},
	)
	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}
	if got[0].ID != "g1" || len(got[0].Parameters) != 2 {
		t.Fatalf("g1=%+v want [A B]", got[0])
	}
	if got[1].ID != "g2" || len(got[1].Parameters) != 1 || got[1].Parameters[0] != "C" {
		t.Fatalf("g2=%+v want [C] (B already claimed)", got[1])
	}
}

func TestGroupForChannelFallsBackToParameterOrderWhenNoGroups(t *testing.T) {
	g := NewParameterGrouper(nil)
	got := g.GroupForChannel(
		[]string{"P1", "P2", "P3", "EXTRA"},
		GroupChannelOptions{
			ParameterOrder: []string{"P3", "P1", "P2"},
		},
	)
	if len(got) != 1 || got[0].ID != "all" {
		t.Fatalf("got=%+v want single all section", got)
	}
	want := []string{"P3", "P1", "P2", "EXTRA"}
	for i, p := range want {
		if got[0].Parameters[i] != p {
			t.Fatalf("position %d=%s want %s (full=%v)", i, got[0].Parameters[i], p, got[0].Parameters)
		}
	}
}

func TestGroupForChannelNoMetadataFallsBackToPatternBased(t *testing.T) {
	g := NewParameterGrouper(nil)
	parameters := []string{"BOOST_TIME", "RANDOM_X"}
	patternBased := g.Group(parameters)
	metaBased := g.GroupForChannel(parameters, GroupChannelOptions{})
	if len(metaBased) != len(patternBased) {
		t.Fatalf("len mismatch meta=%d pattern=%d", len(metaBased), len(patternBased))
	}
	for i := range patternBased {
		if metaBased[i].ID != patternBased[i].ID {
			t.Fatalf("section[%d] id mismatch meta=%s pattern=%s", i, metaBased[i].ID, patternBased[i].ID)
		}
	}
}

func TestGenerateUsesGrouperWhenSet(t *testing.T) {
	in := GenerateInput{
		Grouper: NewParameterGrouper(nil),
		Descriptions: map[string]hmproto.ParameterData{
			"TEMPERATURE_OFFSET": {Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
			"BOOST_DURATION":     {Type: hmenum.ParameterTypeInteger, Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
			"RANDOM_X":           {Type: hmenum.ParameterTypeBool, Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
		},
	}
	s := Generate(in)
	if len(s.Sections) < 2 {
		t.Fatalf("with grouper expected ≥2 sections, got %d", len(s.Sections))
	}
	// Each parameter must appear exactly once across all sections.
	seen := make(map[string]int)
	for _, sec := range s.Sections {
		for _, p := range sec.Parameters {
			seen[p.ID]++
		}
	}
	for _, k := range []string{"TEMPERATURE_OFFSET", "BOOST_DURATION", "RANDOM_X"} {
		if seen[k] != 1 {
			t.Fatalf("%s appears %d times across sections", k, seen[k])
		}
	}
	if s.TotalParameters != 3 {
		t.Fatalf("total=%d want 3 (counts unchanged by grouping)", s.TotalParameters)
	}
}

func TestGenerateEnrichesLinkMetadataWhenFlagSet(t *testing.T) {
	in := GenerateInput{
		EnrichLinkMetadata: true,
		LinkMetadataLocale: "en",
		Descriptions: map[string]hmproto.ParameterData{
			"SHORT_ON_TIME_BASE": {Type: hmenum.ParameterTypeInteger, Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
			"JT_ON":              {Type: hmenum.ParameterTypeInteger, Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
			"LEVEL":              {Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
		},
	}
	s := Generate(in)
	idx := make(map[string]FormParameter)
	for _, sec := range s.Sections {
		for _, p := range sec.Parameters {
			idx[p.ID] = p
		}
	}

	tb := idx["SHORT_ON_TIME_BASE"]
	if tb.Category != "time" {
		t.Errorf("SHORT_ON_TIME_BASE category=%q want time", tb.Category)
	}
	if tb.KeypressGroup != "short" {
		t.Errorf("SHORT_ON_TIME_BASE keypress_group=%q want short", tb.KeypressGroup)
	}
	if len(tb.TimePresets) == 0 {
		t.Errorf("SHORT_ON_TIME_BASE must carry time presets")
	}

	jt := idx["JT_ON"]
	if jt.Category != "jump_target" {
		t.Errorf("JT_ON category=%q want jump_target", jt.Category)
	}
	if !jt.HiddenByDefault {
		t.Errorf("JT_ON must be hidden_by_default")
	}

	lv := idx["LEVEL"]
	if lv.Category != "level" {
		t.Errorf("LEVEL category=%q want level", lv.Category)
	}
	if !lv.DisplayAsPercent {
		t.Errorf("LEVEL must have display_as_percent")
	}
}

func TestGenerateDoesNotEnrichLinkMetadataByDefault(t *testing.T) {
	in := GenerateInput{
		Descriptions: map[string]hmproto.ParameterData{
			"JT_ON": {Type: hmenum.ParameterTypeInteger, Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
		},
	}
	s := Generate(in)
	p := s.Sections[0].Parameters[0]
	if p.Category != "" || p.KeypressGroup != "" {
		t.Errorf("without EnrichLinkMetadata, link fields must be empty; got category=%q keypress=%q", p.Category, p.KeypressGroup)
	}
}

func TestGenerateCountsWritableOnly(t *testing.T) {
	in := GenerateInput{
		Descriptions: map[string]hmproto.ParameterData{
			"READ_ONLY":  {Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead},
			"WRITABLE":   {Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
			"WRITE_ONLY": {Type: hmenum.ParameterTypeAction, Operations: hmenum.OperationsWrite},
		},
	}
	s := Generate(in)
	if s.TotalParameters != 3 {
		t.Fatalf("total=%d want 3", s.TotalParameters)
	}
	if s.WritableParameters != 2 {
		t.Fatalf("writable=%d want 2", s.WritableParameters)
	}
}

// --- L-A6-02: OptionValueTranslator --- ----------------------------------

// TestGenerateOptionLabelsPopulatedWhenTranslatorWired verifies that
// FormParameter.OptionLabels is populated for ENUM parameters when an
// OptionValueTranslator is provided.
func TestGenerateOptionLabelsPopulatedWhenTranslatorWired(t *testing.T) {
	t.Parallel()

	in := GenerateInput{
		Descriptions: map[string]hmproto.ParameterData{
			"MODE": {
				Type:       hmenum.ParameterTypeEnum,
				Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
				ValueList:  []string{"AUTO", "MANUAL", "BOOST"},
			},
		},
		OptionValueTranslator: func(parameter, value, channelType, locale string) string {
			switch value {
			case "AUTO":
				return "Automatic"
			case "MANUAL":
				return "Manual"
			case "BOOST":
				return "Boost"
			}
			return ""
		},
	}
	s := Generate(in)
	if len(s.Sections) == 0 || len(s.Sections[0].Parameters) == 0 {
		t.Fatal("expected at least one parameter in generated schema")
	}
	p := s.Sections[0].Parameters[0]
	if p.OptionLabels == nil {
		t.Fatal("OptionLabels must be populated when translator is wired")
	}
	if p.OptionLabels["AUTO"] != "Automatic" {
		t.Fatalf("OptionLabels[AUTO]=%q want Automatic", p.OptionLabels["AUTO"])
	}
	if p.OptionLabels["MANUAL"] != "Manual" {
		t.Fatalf("OptionLabels[MANUAL]=%q want Manual", p.OptionLabels["MANUAL"])
	}
	if p.OptionLabels["BOOST"] != "Boost" {
		t.Fatalf("OptionLabels[BOOST]=%q want Boost", p.OptionLabels["BOOST"])
	}
}

// TestGenerateOptionLabelsNilWhenNoTranslator verifies that OptionLabels is
// nil (not an empty map) when no OptionValueTranslator is wired.
func TestGenerateOptionLabelsNilWhenNoTranslator(t *testing.T) {
	t.Parallel()

	in := GenerateInput{
		Descriptions: map[string]hmproto.ParameterData{
			"MODE": {
				Type:       hmenum.ParameterTypeEnum,
				Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
				ValueList:  []string{"AUTO", "MANUAL"},
			},
		},
	}
	s := Generate(in)
	if len(s.Sections[0].Parameters) == 0 {
		t.Fatal("expected parameter")
	}
	p := s.Sections[0].Parameters[0]
	if p.OptionLabels != nil {
		t.Fatalf("OptionLabels must be nil when no translator is wired, got %v", p.OptionLabels)
	}
}

// --- L-A6-04: RequireTranslation --- -------------------------------------

// stubTranslationProvider is a minimal TranslationProvider backed by a fixed
// map: only parameters present in the map are considered translated.
type stubTranslationProvider struct {
	translations map[string]string // key: "param" or "channeltype|param"
}

func (p *stubTranslationProvider) ParameterTranslation(parameter, channelType, locale string) string {
	key := parameter
	if channelType != "" {
		key = channelType + "|" + parameter
	}
	return p.translations[key]
}

// TestGenerateRequireTranslationFiltersUntranslated verifies that when
// RequireTranslation=true and a LabelResolver is wired, parameters without a
// CCU translation are excluded from the schema.
func TestGenerateRequireTranslationFiltersUntranslated(t *testing.T) {
	t.Parallel()

	resolver := NewLabelResolver(&stubTranslationProvider{
		translations: map[string]string{
			"TRANSLATED": "Some Label",
		},
	}, "en")

	in := GenerateInput{
		LabelResolver:      resolver,
		RequireTranslation: true,
		Descriptions: map[string]hmproto.ParameterData{
			"TRANSLATED":   {Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
			"UNTRANSLATED": {Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
		},
	}
	s := Generate(in)
	if s.TotalParameters != 1 {
		t.Fatalf("total=%d want 1 (UNTRANSLATED must be filtered)", s.TotalParameters)
	}
	if s.Sections[0].Parameters[0].ID != "TRANSLATED" {
		t.Fatalf("expected TRANSLATED, got %q", s.Sections[0].Parameters[0].ID)
	}
}

// TestGenerateRequireTranslationFalseIncludesAll verifies that
// RequireTranslation=false includes all parameters regardless of translation
// availability.
func TestGenerateRequireTranslationFalseIncludesAll(t *testing.T) {
	t.Parallel()

	resolver := NewLabelResolver(&stubTranslationProvider{
		translations: map[string]string{
			"TRANSLATED": "Some Label",
		},
	}, "en")

	in := GenerateInput{
		LabelResolver:      resolver,
		RequireTranslation: false,
		Descriptions: map[string]hmproto.ParameterData{
			"TRANSLATED":   {Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
			"UNTRANSLATED": {Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
		},
	}
	s := Generate(in)
	if s.TotalParameters != 2 {
		t.Fatalf("total=%d want 2 when RequireTranslation=false", s.TotalParameters)
	}
}
