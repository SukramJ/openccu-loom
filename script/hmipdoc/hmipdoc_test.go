// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// sampleDoc mirrors the layout pdftotext -layout produces for the documentation:
// form feeds between pages, a heading per subsection, and parameter rows whose
// access column continues on the next line.
const sampleDoc = `based on crRFD version: 9.9.9
Documentation creation date: 01.02.2026
` + "\f" + `1. Device DIM_V1 (Dimmer, old firmware)

 1.1. Supported device types

  • HmIP-DIM (ID: 1, from version 0.0.0 to version 1.0.0)


 1.2. Channel types
     Channel number               Channel type

     0                            MAINTENANCE

     1                            DIMMER_VIRTUAL_RECEIVER


 1.3. Channels

  1.3.1. Channel 0 - MAINTENANCE

    1.3.1.1. Channel state parameters

  1.3.2. Channel 1 - DIMMER_VIRTUAL_RECEIVER

    1.3.2.1. Channel state parameters
` + "\f" + `2. Device DIM_V2 (Dimmer -
current firmware)

 2.1. Supported device types

  • HmIP-DIM (ID: 1, from version 1.0.1)


 2.2. Channel types
     Channel number               Channel type

     0                            MAINTENANCE_BAT_EL

     1                            DIMMER_TRANSMITTER

     2                            DIMMER_VIRTUAL_RECEIVER

     3                            DIMMER_VIRTUAL_RECEIVER


 2.3. Channels

  2.3.1. Channel 0 - MAINTENANCE_BAT_EL

    2.3.1.1. Channel state parameters

        Logical    Physical   Access            Default             Possible Values

        2.3.1.1.1. Parameter OPERATING_VOLTAGE

        FLOAT      integer    read              0.0        Min:      Max:
                              event                        0.0       25.2

  2.3.2. Channel 1 - DIMMER_TRANSMITTER

    2.3.2.1. Channel state parameters

        2.3.2.1.1. Parameter LEVEL

        FLOAT      integer    read              0.0        Min:      Max:
                              event                        0.0       1.0

  2.3.3. Channel 2 - DIMMER_VIRTUAL_RECEIVER

    2.3.3.1. Channel state parameters

        2.3.3.1.1. Parameter LEVEL

        FLOAT      integer    read              0.0        Min:      Max:
                              write                        0.0       1.0

    2.3.3.2. Channel configuration parameters

        2.3.3.2.1. Parameter RAMP_TIME

        FLOAT      integer    read              0.0        Min:      Max:
                              write                        0.0       1.0

    2.3.3.3. Channel roles

        2.3.3.3.1. Parameter ROLE_ONLY

  2.3.4. Channel 3 - DIMMER_VIRTUAL_RECEIVER

    2.3.4.1. Channel state parameters
`

func TestParseReadsSectionsOfTheSampleDocument(t *testing.T) {
	sections, errs := parse(sampleDoc)
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	if len(sections) != 2 {
		t.Fatalf("sections = %d, want 2", len(sections))
	}
	current := sections[1]
	if got := current.reference(); got != "§2 DIM_V2, p. 3" {
		t.Errorf("reference = %q", got)
	}
	if current.description != "Dimmer -" {
		t.Errorf("description = %q", current.description)
	}
	if current.channels[1] != "DIMMER_TRANSMITTER" || current.channels[3] != "DIMMER_VIRTUAL_RECEIVER" {
		t.Errorf("channels = %v", current.channels)
	}
	if !current.hasParameter(2, "LEVEL") {
		t.Error("state parameter LEVEL on channel 2 not read")
	}
	if !current.hasParameter(2, "RAMP_TIME") {
		t.Error("configuration parameter RAMP_TIME on channel 2 not read")
	}
	if current.hasParameter(3, "LEVEL") {
		t.Error("LEVEL leaked onto channel 3")
	}
	if current.hasParameter(2, "ROLE_ONLY") {
		t.Error("a parameter under the roles subsection counts as documented")
	}
}

// TestParseFailsLoudlyOnChangedLayout pins the promise of the tool: each change
// below would silently shrink the report if the counts were not checked against
// the raw text.
func TestParseFailsLoudlyOnChangedLayout(t *testing.T) {
	tests := []struct {
		name, old, replacement, want string
	}{
		{
			name:        "device header no longer parses",
			old:         "2. Device DIM_V2 (Dimmer -",
			replacement: "2. Device DIM V2 Dimmer -",
			want:        "device headers: 2 in text, 1 parsed",
		},
		{
			name:        "supported type line no longer parses",
			old:         "• HmIP-DIM (ID: 1, from version 1.0.1)",
			replacement: "• HmIP-DIM (ID: 1, since 1.0.1)",
			want:        "supported types: 2 in text, 1 parsed",
		},
		{
			name:        "channel table row no longer parses",
			old:         "     3                            DIMMER_VIRTUAL_RECEIVER\n",
			replacement: "     3 DIMMER_VIRTUAL_RECEIVER\n",
			want:        "§2 DIM_V2, p. 3: channel table differs from channel sections",
		},
		{
			name:        "parameter row no longer parses",
			old:         "FLOAT      integer    read              0.0        Min:      Max:\n                              event                        0.0       1.0",
			replacement: "DECIMAL    integer    read              0.0        Min:      Max:\n                              event                        0.0       1.0",
			want:        "1 parameters without a readable table row",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(sampleDoc, tt.old) {
				t.Fatalf("anchor %q not in sample", tt.old)
			}
			_, errs := parse(strings.Replace(sampleDoc, tt.old, tt.replacement, 1))
			if !slices.Contains(errs, tt.want) {
				t.Errorf("errors = %v, want %q", errs, tt.want)
			}
		})
	}
}

func TestLatestSectionsTakesOnlyTheOpenEndedRange(t *testing.T) {
	sections, errs := parse(sampleDoc)
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	latest, errs := latestSections(sections)
	if len(errs) > 0 {
		t.Fatalf("latest errors: %v", errs)
	}
	if got := latest["HmIP-DIM"]; got != sections[1] {
		t.Errorf("latest section = %v, want the open-ended one", got.reference())
	}
}

func TestLatestSectionsRejectsAnUndeterminedCurrentSection(t *testing.T) {
	closed := newSection(1, "A", "", 1)
	closed.models = []supportedModel{{name: "HmIP-CLOSED", until: "1.0.0"}}
	first := newSection(2, "B", "", 2)
	first.models = []supportedModel{{name: "HmIP-TWICE"}}
	second := newSection(3, "C", "", 3)
	second.models = []supportedModel{{name: "HmIP-TWICE"}}

	_, errs := latestSections([]*section{closed, first, second})
	want := []string{
		"HmIP-TWICE: open-ended firmware range in §2 B, p. 2 and §3 C, p. 3",
		"HmIP-CLOSED: no open-ended firmware range",
	}
	if !slices.Equal(errs, want) {
		t.Errorf("errors = %v, want %v", errs, want)
	}
}

func TestLatestSectionsAcceptsAModelListedTwiceInOneSection(t *testing.T) {
	s := newSection(1, "A", "", 1)
	s.models = []supportedModel{{name: "HM-CCU2"}, {name: "HM-CCU2"}}
	if _, errs := latestSections([]*section{s}); len(errs) > 0 {
		t.Errorf("errors = %v, want none", errs)
	}
}

// dimmerProfile mirrors the shape of the IPDimmer schema: state channel one
// below the base, secondaries above it.
func dimmerProfile(deviceType string, channel int) custom.Profile {
	return custom.Profile{
		Name:         "IPDimmer",
		DeviceType:   deviceType,
		ProductGroup: hmenum.ProductGroupHmIP,
		Category:     hmenum.DataPointCategoryLight,
		Channels:     []custom.ChannelRoleAssignment{{Channel: channel, Role: custom.ChannelRolePrimary}},
		Config: &custom.ProfileConfig{
			ProfileType: "IPDimmer",
			ChannelGroup: custom.ChannelGroupConfig{
				PrimaryChannelSet:  true,
				SecondaryChannels:  []int{1},
				StateChannelOffset: new(-1),
				Fields: map[hmenum.Field]custom.FieldValue{
					hmenum.FieldLevel:         custom.Bare(hmenum.ParameterLevel),
					hmenum.FieldRampTimeValue: custom.Bare(hmenum.ParameterRampTime),
					hmenum.FieldOnTimeValue:   custom.Bare(hmenum.ParameterOnTime),
				},
			},
		},
	}
}

func testCatalog(t *testing.T, profiles ...custom.Profile) catalog {
	t.Helper()
	registry := custom.NewRegistry()
	for _, p := range profiles {
		if err := registry.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	return catalog{
		registry:        registry,
		voltageIgnored:  func(string) bool { return false },
		hasBatteryEntry: func(string) bool { return false },
	}
}

func sampleLatest(t *testing.T) (latest map[string]*section, sections []*section) {
	t.Helper()
	sections, errs := parse(sampleDoc)
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	latest, errs = latestSections(sections)
	if len(errs) > 0 {
		t.Fatalf("latest errors: %v", errs)
	}
	return latest, sections
}

// TestAnalyseSeparatesCorrectFromWrongChannelBinding is the check's reason to
// exist: the same sample document gives a clean result for a profile on the first
// receiver channel and a finding for one on the transmitter channel. A check that
// reported the same in both directions would measure nothing.
func TestAnalyseSeparatesCorrectFromWrongChannelBinding(t *testing.T) {
	latest, _ := sampleLatest(t)

	correct := analyse(latest, testCatalog(t, dimmerProfile("hmip-dim", 2)))
	if got := correct.combinations["IPDimmer"][typeCombination{primary: "DIMMER_VIRTUAL_RECEIVER", state: "DIMMER_TRANSMITTER"}]; len(got) != 1 {
		t.Errorf("correct binding: combinations = %v", correct.combinations)
	}
	if len(correct.missing) != 0 || len(correct.uncovered) != 0 {
		t.Errorf("correct binding: missing = %v, uncovered = %v", correct.missing, correct.uncovered)
	}

	wrong := analyse(latest, testCatalog(t, dimmerProfile("hmip-dim", 1)))
	if got := wrong.combinations["IPDimmer"][typeCombination{primary: "DIMMER_TRANSMITTER", state: "MAINTENANCE_BAT_EL"}]; len(got) != 1 {
		t.Errorf("wrong binding: combinations = %v", wrong.combinations)
	}
	if len(wrong.uncovered) != 1 || wrong.uncovered[0][2] != "3: DIMMER_VIRTUAL_RECEIVER" {
		t.Errorf("wrong binding: uncovered = %v", wrong.uncovered)
	}
}

func TestAnalyseReportsChannelsTheModelDoesNotHave(t *testing.T) {
	latest, _ := sampleLatest(t)
	a := analyse(latest, testCatalog(t, dimmerProfile("hmip-dim", 3)))
	want := []missingChannel{{model: "HmIP-DIM", profile: "IPDimmer", role: "secondary", channel: 4, reason: "no such channel"}}
	if !slices.Equal(a.missing, want) {
		t.Errorf("missing = %v, want %v", a.missing, want)
	}
}

// TestAnalyseSkipsTheGroupOfAPrimaryTheRoleDoesNotAccept pins the materializer's
// rule in the report: the transmitter is not an accepted primary, so the group does
// not exist and its secondary channel does not count as covered.
func TestAnalyseSkipsTheGroupOfAPrimaryTheRoleDoesNotAccept(t *testing.T) {
	latest, _ := sampleLatest(t)
	profile := dimmerProfile("hmip-dim", 1)
	profile.Config.ChannelGroup.PrimaryChannelTypes = []string{"DIMMER_VIRTUAL_RECEIVER"}

	a := analyse(latest, testCatalog(t, profile))

	want := []missingChannel{{model: "HmIP-DIM", profile: "IPDimmer", role: "primary", channel: 1, reason: "type DIMMER_TRANSMITTER not accepted"}}
	if !slices.Equal(a.missing, want) {
		t.Errorf("missing = %v, want %v", a.missing, want)
	}
	if len(a.combinations["IPDimmer"]) != 0 {
		t.Errorf("combinations = %v, want none", a.combinations)
	}
	if len(a.uncovered) != 1 || !strings.Contains(a.uncovered[0][2], "2: DIMMER_VIRTUAL_RECEIVER") {
		t.Errorf("uncovered = %v, want the receivers listed", a.uncovered)
	}
}

func TestAnalyseReportsFieldsOnlyWhereTheParameterIsMissing(t *testing.T) {
	latest, _ := sampleLatest(t)
	rows := analyse(latest, testCatalog(t, dimmerProfile("hmip-dim", 2))).fieldRows()
	want := [][]string{{"IPDimmer", "fields", string(hmenum.FieldOnTimeValue), "ON_TIME", "0/1", "HmIP-DIM:2"}}
	if !slices.EqualFunc(rows, want, slices.Equal[[]string]) {
		t.Errorf("field rows = %v, want %v", rows, want)
	}
}

func TestAnalyseNamesPrefixHitsAndLeavesExactMatchesOut(t *testing.T) {
	latest, _ := sampleLatest(t)

	exact := analyse(latest, testCatalog(t, dimmerProfile("hmip-dim", 2)))
	if len(exact.prefixed) != 0 {
		t.Errorf("exact match reported as prefix: %v", exact.prefixed)
	}

	prefix := analyse(latest, testCatalog(t, dimmerProfile("hmip-di", 2)))
	if len(prefix.prefixed) != 1 || prefix.prefixed[0][1] != "light: hmip-di" {
		t.Errorf("prefixed = %v", prefix.prefixed)
	}
}

func TestAnalyseListsModelsWithoutAProfile(t *testing.T) {
	latest, _ := sampleLatest(t)
	a := analyse(latest, testCatalog(t))
	want := "DIMMER_TRANSMITTER×1, DIMMER_VIRTUAL_RECEIVER×2"
	if len(a.noConfig) != 1 || a.noConfig[0][2] != want {
		t.Errorf("noConfig = %v, want channel types %q", a.noConfig, want)
	}
}

func TestUndocumentedKeepsOnlyKeysNoDocumentedModelMatches(t *testing.T) {
	_, sections := sampleLatest(t)
	got := undocumented([]string{"hmip-dim", "HmIP-DI", "hmip-gone", "HM-OTHER-FAMILY"}, sections)
	if got != "hmip-gone" {
		t.Errorf("undocumented = %q, want hmip-gone", got)
	}
}

func TestVoltageFindingsFollowTheChannelZeroType(t *testing.T) {
	latest, _ := sampleLatest(t)

	cat := testCatalog(t)
	withoutBattery, visible := voltageFindings(latest, cat)
	if len(withoutBattery) != 1 || withoutBattery[0][0] != "HmIP-DIM" || len(visible) != 0 {
		t.Errorf("no battery entry: without = %v, visible = %v", withoutBattery, visible)
	}

	cat.hasBatteryEntry = func(string) bool { return true }
	withoutBattery, visible = voltageFindings(latest, cat)
	if len(withoutBattery) != 0 || len(visible) != 0 {
		t.Errorf("battery entry: without = %v, visible = %v", withoutBattery, visible)
	}

	mains := map[string]*section{"HmIP-MAINS": newSection(1, "M", "", 1)}
	mains["HmIP-MAINS"].channels[0] = "MAINTENANCE"
	mains["HmIP-MAINS"].stateParameters[0] = map[string]struct{}{"OPERATING_VOLTAGE": {}}
	if _, visible = voltageFindings(mains, cat); len(visible) != 1 {
		t.Errorf("mains, not ignored: visible = %v", visible)
	}
	cat.voltageIgnored = func(string) bool { return true }
	if _, visible = voltageFindings(mains, cat); len(visible) != 0 {
		t.Errorf("mains, ignored: visible = %v", visible)
	}
}

func TestBuildReportWithholdsTheReportOnParseErrors(t *testing.T) {
	broken := strings.Replace(sampleDoc, "• HmIP-DIM (ID: 1, from version 1.0.1)", "• HmIP-DIM (ID: 1, since 1.0.1)", 1)
	content, errs := buildReport(broken, testCatalog(t))
	if content != "" || len(errs) == 0 {
		t.Errorf("content = %q, errs = %v", content, errs)
	}
}

func TestBuildReportNamesTheEditionAndEverySection(t *testing.T) {
	content, errs := buildReport(sampleDoc, testCatalog(t, dimmerProfile("hmip-dim", 2)))
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	for _, want := range []string{
		"- PDF: crRFD 9.9.9, created 01.02.2026",
		"- Parsed: 2 sections, 1 models",
		"## 1. Coverage", "## 2. ", "## 3. ", "## 4. ", "## 5. ", "## 6. ", "## 7. ", "## 8. ", "## 9. ",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("report lacks %q", want)
		}
	}
}

// TestProductionCatalogReadsTheRealTables crosses the seam to the daemon's own
// tables: a catalogue built from empty sources would report every model as
// unconfigured without any test noticing.
func TestProductionCatalogReadsTheRealTables(t *testing.T) {
	cat := productionCatalog()
	if len(cat.registry.GetConfigs("HmIP-BDT")) == 0 {
		t.Error("HmIP-BDT has no profile")
	}
	if !slices.Contains(cat.batteryModels, "HmIP-SWDO") {
		t.Errorf("battery models = %v", cat.batteryModels)
	}
	if !slices.Contains(cat.ruleModels, "HmIP-BDT") {
		t.Errorf("rule models = %v", cat.ruleModels)
	}
	if !cat.hasBatteryEntry("HmIP-SWDO") || cat.hasBatteryEntry("HmIP-BDT") {
		t.Error("hasBatteryEntry does not follow the battery table")
	}
	// HmIP-SWSD has a key with an unknown cell type: a known entry all the same.
	if !cat.hasBatteryEntry("HmIP-SWSD") {
		t.Error("an entry with an unknown cell type must count as an entry")
	}
	if !cat.voltageIgnored("HmIP-BDT") || cat.voltageIgnored("HmIP-SWDO") {
		t.Error("voltageIgnored does not follow the visibility rules")
	}
}
