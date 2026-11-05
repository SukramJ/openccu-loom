// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/model/calculated"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// undocumentedPrefixes limits the "known models without any documented model"
// section to the families the documentation covers.
var undocumentedPrefixes = []string{"hmip", "elv"}

// catalog is what the documentation is compared against.
type catalog struct {
	registry *custom.Registry
	// voltageIgnored reports whether the daemon suppresses OPERATING_VOLTAGE on
	// channel 0 of the model.
	voltageIgnored func(model string) bool
	// hasBatteryEntry reports whether the battery table has a key for the model,
	// including entries whose cell type is unknown.
	hasBatteryEntry func(model string) bool
	ruleModels      []string
	batteryModels   []string
}

// productionCatalog builds the catalogue as the daemon does: the default
// profile registry, and a visibility registry carrying its required parameters
// (cmd/openccu-loom/daemon_infra.go).
func productionCatalog() catalog {
	registry := custom.DefaultRegistry()
	vis := visibility.NewRegistry()
	vis.SetRequiredParameters(registry.RequiredParameters())

	rules := visibility.UnIgnoreParameterModels()
	for _, models := range visibility.IgnoreParametersByDeviceLower() {
		rules = slices.AppendSeq(rules, maps.Keys(models))
	}

	return catalog{
		registry: registry,
		voltageIgnored: func(model string) bool {
			return vis.Parameter().IsParameterIgnored(model, "", 0, hmenum.ParamsetKeyValues, hmenum.ParameterOperatingVoltage)
		},
		hasBatteryEntry: func(model string) bool {
			// An unknown cell type answers false too, but with the entry's
			// config; only a model without a key leaves the type empty.
			cfg, _ := calculated.LookupBatteryConfig(model)
			return cfg.Battery != ""
		},
		ruleModels:    rules,
		batteryModels: calculated.BatteryModels(),
	}
}

// fieldKey groups one profile field across every channel group it applies to.
type fieldKey struct {
	profile, role, field, parameter string
}

// fieldResult is one channel group's answer for a profile field.
type fieldResult struct {
	model   string
	channel *int
	found   bool
}

// typeCombination is the pair of channel types one channel group binds.
type typeCombination struct {
	primary, state string
}

// missingChannel is a configured channel the model does not have.
type missingChannel struct {
	model, profile, role string
	channel              int
}

// analysis collects the findings of the comparison.
type analysis struct {
	models        int
	missing       []missingChannel
	fields        map[fieldKey][]fieldResult
	combinations  map[string]map[typeCombination][]string
	uncovered     [][]string
	noConfig      [][]string
	withoutSchema [][]string
	prefixed      [][]string
}

// analyse compares the catalogue's profiles with the current section of every
// documented model. Channel numbers follow the materializer: the group number is
// base plus primary offset, and without a primary channel no group is assigned
// (internal/model/custom/materialize.go, addChannelGroupsToDevice).
func analyse(latest map[string]*section, cat catalog) *analysis {
	a := &analysis{
		models:       len(latest),
		fields:       map[fieldKey][]fieldResult{},
		combinations: map[string]map[typeCombination][]string{},
	}
	for _, model := range slices.Sorted(maps.Keys(latest)) {
		s := latest[model]
		profiles := cat.registry.GetConfigs(model)
		if len(profiles) == 0 {
			a.noConfig = append(a.noConfig, []string{model, s.description, channelTypeSummary(s), s.reference()})
			continue
		}
		if matched := prefixMatches(model, profiles); matched != "" {
			a.prefixed = append(a.prefixed, []string{model, matched, profileChannels(profiles), s.reference()})
		}

		covered := map[int]struct{}{}
		for _, p := range profiles {
			a.addProfile(model, s, p, covered)
		}
		if receivers := uncoveredReceivers(s, covered); receivers != "" {
			a.uncovered = append(a.uncovered, []string{model, profileNames(profiles), receivers, s.reference()})
		}
	}
	return a
}

func (a *analysis) addProfile(model string, s *section, p custom.Profile, covered map[int]struct{}) {
	name := string(p.Name)
	if p.Config == nil {
		a.withoutSchema = append(a.withoutSchema, []string{model, name, s.reference()})
		return
	}
	cg := p.Config.ChannelGroup
	for _, base := range p.Channels {
		var primary *int
		groupNo := 0
		if cg.PrimaryChannelSet {
			groupNo = base.Channel + cg.PrimaryChannel
			primary = &groupNo
		}
		group := p.Rebase(groupNo)

		a.addCombination(name, model, s, primary, group.StateChannel)

		a.addRole(model, name, "primary", primary, s, covered)
		a.addRole(model, name, "state", group.StateChannel, s, covered)
		for _, offset := range cg.SecondaryChannels {
			secondary := base.Channel + offset
			a.addRole(model, name, "secondary", &secondary, s, covered)
		}

		a.addFields(model, name, "fields", primary, group.Fields, s)
		for channel, fields := range group.ChannelFields {
			target := &channel
			if channel == custom.AnyChannelOffset {
				target = primary
			}
			a.addFields(model, name, "channel_fields", target, fields, s)
		}
		for channel, fields := range group.FixedChannelFields {
			a.addFields(model, name, "fixed_channel_fields", &channel, fields, s)
		}
		if p.Extended != nil {
			for channel, fields := range p.Extended.FixedChannelFields {
				a.addExtendedFields(model, name, channel, fields, s)
			}
		}
	}
}

func (a *analysis) addCombination(profile, model string, s *section, primary, state *int) {
	if a.combinations[profile] == nil {
		a.combinations[profile] = map[typeCombination][]string{}
	}
	key := typeCombination{primary: channelType(s, primary), state: channelType(s, state)}
	a.combinations[profile][key] = append(a.combinations[profile][key], model)
}

func (a *analysis) addRole(model, profile, role string, channel *int, s *section, covered map[int]struct{}) {
	if channel == nil {
		return
	}
	covered[*channel] = struct{}{}
	if _, ok := s.channels[*channel]; !ok {
		a.missing = append(a.missing, missingChannel{model: model, profile: profile, role: role, channel: *channel})
	}
}

func (a *analysis) addFields(model, profile, role string, channel *int, fields map[hmenum.Field]custom.FieldValue, s *section) {
	for field, value := range fields {
		parameter, _ := custom.ResolveFieldValue(value)
		a.addField(model, profile, role, string(field), string(parameter), channel, s)
	}
}

func (a *analysis) addExtendedFields(model, profile string, channel int, fields map[hmenum.Field]hmenum.Parameter, s *section) {
	for field, parameter := range fields {
		a.addField(model, profile, "extended.fixed_channel_fields", string(field), string(parameter), &channel, s)
	}
}

func (a *analysis) addField(model, profile, role, field, parameter string, channel *int, s *section) {
	key := fieldKey{profile: profile, role: role, field: field, parameter: parameter}
	found := channel != nil && s.hasParameter(*channel, parameter)
	a.fields[key] = append(a.fields[key], fieldResult{model: model, channel: channel, found: found})
}

func channelType(s *section, channel *int) string {
	if channel == nil {
		return "-"
	}
	if name, ok := s.channels[*channel]; ok {
		return name
	}
	return "-"
}

// channelTypeSummary counts a section's channel types, leaving out the ones
// every device has.
func channelTypeSummary(s *section) string {
	counts := map[string]int{}
	for _, name := range s.channels {
		if name != "KEY_TRANSCEIVER" && !strings.HasPrefix(name, "MAINTENANCE") {
			counts[name]++
		}
	}
	parts := make([]string, 0, len(counts))
	for _, name := range slices.Sorted(maps.Keys(counts)) {
		parts = append(parts, fmt.Sprintf("%s×%d", name, counts[name]))
	}
	return strings.Join(parts, ", ")
}

// prefixMatches names the categories in which the model resolves through a
// shorter registered key. Registered device types are stored in lower case
// (internal/model/custom/registry.go, Register), so a resolved profile whose
// device type differs from the lower-cased model is a prefix hit.
func prefixMatches(model string, profiles []custom.Profile) string {
	normalized := strings.ToLower(model)
	matches := map[string]struct{}{}
	for _, p := range profiles {
		if p.DeviceType != normalized {
			matches[string(p.Category)+": "+p.DeviceType] = struct{}{}
		}
	}
	return strings.Join(slices.Sorted(maps.Keys(matches)), ", ")
}

func profileChannels(profiles []custom.Profile) string {
	sets := map[string]struct{}{}
	for _, p := range profiles {
		numbers := make([]string, 0, len(p.Channels))
		for _, c := range p.Channels {
			numbers = append(numbers, strconv.Itoa(c.Channel))
		}
		sets["["+strings.Join(numbers, ",")+"]"] = struct{}{}
	}
	return strings.Join(slices.Sorted(maps.Keys(sets)), " ")
}

func profileNames(profiles []custom.Profile) string {
	names := map[string]struct{}{}
	for _, p := range profiles {
		names[string(p.Name)] = struct{}{}
	}
	return strings.Join(slices.Sorted(maps.Keys(names)), ", ")
}

func uncoveredReceivers(s *section, covered map[int]struct{}) string {
	var parts []string
	for _, channel := range slices.Sorted(maps.Keys(s.channels)) {
		_, isCovered := covered[channel]
		if name := s.channels[channel]; strings.Contains(name, "VIRTUAL_RECEIVER") && !isCovered {
			parts = append(parts, fmt.Sprintf("%d: %s", channel, name))
		}
	}
	return strings.Join(parts, ", ")
}

// undocumented returns the models of the documented families that no documented
// model equals or starts with.
func undocumented(models []string, sections []*section) string {
	var documented []string
	for _, s := range sections {
		for _, m := range s.models {
			documented = append(documented, strings.ToLower(m.name))
		}
	}
	found := map[string]struct{}{}
	for _, model := range models {
		lower := strings.ToLower(model)
		inFamily := slices.ContainsFunc(undocumentedPrefixes, func(prefix string) bool {
			return strings.HasPrefix(lower, prefix)
		})
		matched := slices.ContainsFunc(documented, func(doc string) bool {
			return strings.HasPrefix(doc, lower)
		})
		if inFamily && !matched {
			found[model] = struct{}{}
		}
	}
	return strings.Join(slices.Sorted(maps.Keys(found)), ", ")
}

// voltageFindings compares OPERATING_VOLTAGE on channel 0 with the battery table
// and the visibility rules. That MAINTENANCE_BAT* means battery powered is read
// from the type name only, and the report says so.
func voltageFindings(latest map[string]*section, cat catalog) (withoutBattery, visible [][]string) {
	for _, model := range slices.Sorted(maps.Keys(latest)) {
		s := latest[model]
		if !s.hasParameter(0, string(hmenum.ParameterOperatingVoltage)) {
			continue
		}
		maintenance := s.channels[0]
		row := []string{model, maintenance, s.reference()}
		if strings.HasPrefix(maintenance, "MAINTENANCE_BAT") {
			if !cat.hasBatteryEntry(model) {
				withoutBattery = append(withoutBattery, row)
			}
			continue
		}
		if !cat.voltageIgnored(model) {
			visible = append(visible, row)
		}
	}
	return withoutBattery, visible
}

func writeTable(out *[]string, header []string, rows [][]string) {
	*out = append(*out, "| "+strings.Join(header, " | ")+" |", "|"+strings.Repeat(" --- |", len(header)))
	for _, row := range rows {
		*out = append(*out, "| "+strings.Join(row, " | ")+" |")
	}
	*out = append(*out, "")
}

func (a *analysis) missingRows() [][]string {
	sorted := slices.Clone(a.missing)
	slices.SortFunc(sorted, func(x, y missingChannel) int {
		return cmp.Or(cmp.Compare(x.model, y.model), cmp.Compare(x.profile, y.profile), cmp.Compare(x.role, y.role), cmp.Compare(x.channel, y.channel))
	})
	sorted = slices.Compact(sorted)
	rows := make([][]string, 0, len(sorted))
	for _, m := range sorted {
		rows = append(rows, []string{m.model, m.profile, m.role, strconv.Itoa(m.channel)})
	}
	return rows
}

func (a *analysis) combinationRows() [][]string {
	var rows [][]string
	for _, profile := range slices.Sorted(maps.Keys(a.combinations)) {
		combinations := a.combinations[profile]
		keys := slices.Collect(maps.Keys(combinations))
		slices.SortFunc(keys, func(x, y typeCombination) int {
			return cmp.Or(
				cmp.Compare(len(combinations[y]), len(combinations[x])),
				cmp.Compare(x.primary, y.primary),
				cmp.Compare(x.state, y.state),
			)
		})
		for _, key := range keys {
			models := combinations[key]
			rows = append(rows, []string{profile, key.primary, key.state, strconv.Itoa(len(models)), joinUnique(models)})
		}
	}
	return rows
}

func (a *analysis) fieldRows() [][]string {
	keys := slices.Collect(maps.Keys(a.fields))
	slices.SortFunc(keys, func(x, y fieldKey) int {
		return cmp.Or(cmp.Compare(x.profile, y.profile), cmp.Compare(x.role, y.role), cmp.Compare(x.field, y.field), cmp.Compare(x.parameter, y.parameter))
	})
	var rows [][]string
	for _, key := range keys {
		results := a.fields[key]
		var failing []string
		for _, r := range results {
			if !r.found {
				failing = append(failing, r.model+":"+channelLabel(r.channel))
			}
		}
		if len(failing) > 0 {
			found := fmt.Sprintf("%d/%d", len(results)-len(failing), len(results))
			rows = append(rows, []string{key.profile, key.role, key.field, key.parameter, found, joinUnique(failing)})
		}
	}
	return rows
}

func channelLabel(channel *int) string {
	if channel == nil {
		return "-"
	}
	return strconv.Itoa(*channel)
}

func joinUnique(values []string) string {
	sorted := slices.Sorted(slices.Values(values))
	return strings.Join(slices.Compact(sorted), ", ")
}

// report renders the comparison as markdown lines.
func report(latest map[string]*section, sections []*section, cat catalog) []string {
	a := analyse(latest, cat)
	out := make([]string, 0, 64)
	out = append(out,
		"## 1. Coverage", "",
		fmt.Sprintf("- Models with a current section: %d", a.models),
		fmt.Sprintf("- With a configuration: %d", a.models-len(a.noConfig)),
		fmt.Sprintf("- Without a configuration: %d", len(a.noConfig)),
		fmt.Sprintf("- Profiles without a schema (the materializer skips them): %d", len(a.withoutSchema)),
		"",
		"## 2. Configured channel does not exist on the model", "",
	)
	writeTable(&out, []string{"Model", "Profile", "Role", "Channel"}, a.missingRows())

	out = append(out, "## 3. Channel types per profile (primary, state)", "", "Rare combinations are worth reading.", "")
	writeTable(&out, []string{"Profile", "Primary type", "State type", "Groups", "Models"}, a.combinationRows())

	out = append(out, "## 4. Profile field whose parameter is not documented on the resolved channel", "",
		"`found` counts the channel groups where it exists. Low values usually mean an optional field.", "")
	writeTable(&out, []string{"Profile", "Role", "Field", "Parameter", "found", "Missing on"}, a.fieldRows())

	out = append(out, "## 5. VIRTUAL_RECEIVER channels outside every channel group", "")
	writeTable(&out, []string{"Model", "Profiles", "Channels", "Source"}, a.uncovered)

	out = append(out, "## 6. Models without a configuration", "", "These get generic data points only.", "")
	writeTable(&out, []string{"Model", "Description", "Channel types", "Source"}, a.noConfig)

	out = append(out, "## 7. Models resolved by prefix", "", "Check these against section 3 and 4.", "")
	writeTable(&out, []string{"Model", "Matched key", "Channels", "Source"}, a.prefixed)

	out = append(out, "## 8. Known models without any documented model", "",
		"Limited to the prefixes "+strings.Join(undocumentedPrefixes, ", ")+".", "")
	writeTable(&out, []string{"Source", "Models"}, [][]string{
		{"custom profile registry", undocumented(cat.registry.DeviceTypes(), sections)},
		{"visibility rules", undocumented(cat.ruleModels, sections)},
		{"battery table", undocumented(cat.batteryModels, sections)},
	})

	withoutBattery, visible := voltageFindings(latest, cat)
	out = append(out, "## 9. OPERATING_VOLTAGE", "",
		"That `MAINTENANCE_BAT*` means battery powered is read from the type name only.", "",
		"### Channel 0 is `MAINTENANCE_BAT*`, no entry in the battery table", "")
	writeTable(&out, []string{"Model", "Channel 0", "Source"}, withoutBattery)
	out = append(out, "### Channel 0 is not `MAINTENANCE_BAT*`, parameter not ignored by the visibility rules", "")
	writeTable(&out, []string{"Model", "Channel 0", "Source"}, visible)
	return out
}
