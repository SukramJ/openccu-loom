// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// UISchemaAdapter assembles a channel's rendering schema from the
// available CCU metadata sources: the device registry (for current
// values and OCCU paramset descriptions), the translation archive
// (for localised labels), the easymode archive (for grouping,
// ordering, presets, conditional visibility), and the receiver
// profile catalogue.
//
// The adapter is stateless — a single instance can serve every
// central in the registry.
type UISchemaAdapter struct {
	registry     *central.Registry
	writer       *client.ValueWriter
	translations *ccudata.Translations
	easymode     *ccudata.Easymode
	profiles     *ccudata.ProfileStore
}

// NewUISchemaAdapter wires the adapter. Any of the data sources may
// be nil — the schema degrades gracefully (no labels, no groups, no
// profile). The writer is only needed for LINK paramsets (to reach
// the backend's GetLinkParamset); VALUES / MASTER stay backed by the
// channel's registered data points.
func NewUISchemaAdapter(
	r *central.Registry,
	w *client.ValueWriter,
	t *ccudata.Translations,
	e *ccudata.Easymode,
	p *ccudata.ProfileStore,
) *UISchemaAdapter {
	return &UISchemaAdapter{registry: r, writer: w, translations: t, easymode: e, profiles: p}
}

// UISchema implements [interfaces.UISchemaService].
func (a *UISchemaAdapter) UISchema(ctx context.Context, req hmapi.UISchemaRequest) (*hmapi.UISchema, error) { //nolint:funlen // single-purpose UI schema assembly with many paramset branches
	address, channelNo, paramset, peer, locale := req.Address, req.Channel, req.Paramset, req.Peer, req.Locale
	dev, ch := a.lookupChannel(address, channelNo)
	if dev == nil || ch == nil {
		return nil, hmapi.ErrUISchemaNotFound
	}

	paramsetKey, err := normalizeParamsetKey(paramset)
	if err != nil {
		return nil, err
	}
	if paramsetKey == hmenum.ParamsetKeyLink {
		return a.buildLinkSchema(ctx, dev, ch, peer, locale)
	}

	channelType := a.channelTypeOf(dev, ch)
	schema := &hmapi.UISchema{
		Channel: hmapi.UISchemaChannel{
			Address:  ch.Address,
			Number:   ch.Number,
			Type:     channelType,
			Device:   dev.Address,
			Label:    a.channelLabel(locale, channelType),
			Paramset: string(paramsetKey),
		},
		ModelDescription: dev.ModelLabel,
		DeviceIcon:       dev.ModelIcon,
	}

	// Pull easymode metadata for the channel type if available.
	var (
		meta  *ccudata.SenderTypeMetadata
		cmeta *ccudata.ChannelMetadata
	)
	if a.easymode != nil {
		if c, ok := a.easymode.ChannelMetadata[channelType]; ok {
			cmeta = &c
			if s, ok := c.SenderTypes["_MASTER"]; ok {
				meta = &s
			}
		}
	}
	_ = cmeta

	// Build the parameter table from the channel's data points.
	schema.Parameters = a.buildParameters(ch, paramsetKey, meta, locale, channelType, req.Expert)

	// Sort the parameters by parameter_order when provided, otherwise
	// alphabetically for a stable render.
	a.applyOrder(schema.Parameters, meta)
	if meta != nil && len(meta.ParameterOrder) > 0 {
		schema.ParameterOrder = append([]string(nil), meta.ParameterOrder...)
	}

	// Groups — three-tier fallback chain mirroring
	// /grouping.py
	//   1. Semantic groups from easymode parameter_groups.
	//   2. "Settings" section using parameter_order only (when the
	//      extractor shipped no group breakdown for this type).
	//   3. Pattern-based heuristic (TEMPERATURE_*, _TIME_*, TRANSMIT_*
	//      and friends) plus an "Other Settings" bucket.
	schema.Groups = a.buildGroups(locale, meta, schema.Parameters)
	if meta != nil {
		for _, cv := range meta.ConditionalVisibility {
			rule := hmapi.UISchemaVisibility{
				Trigger:      cv.Trigger,
				TriggerValue: cv.TriggerValue,
			}
			if len(cv.Show) > 0 {
				rule.Show = append([]string(nil), cv.Show...)
			}
			if len(cv.Hide) > 0 {
				rule.Hide = append([]string(nil), cv.Hide...)
			}
			schema.Visibility = append(schema.Visibility, rule)
		}
	}

	// Cross-validations — filtered by the sender-type's rule-id list.
	// Only rules referenced by CrossValidationRuleIDs are emitted;
	// when the list is empty no constraints are sent at all. This
	// mirrors form_schema.py:397-415 which checks st_meta.cross_validation_rule_ids
	// before building the constraint list.
	if a.easymode != nil && meta != nil && len(meta.CrossValidationRuleIDs) > 0 {
		allowedIDs := make(map[string]struct{}, len(meta.CrossValidationRuleIDs))
		for _, id := range meta.CrossValidationRuleIDs {
			allowedIDs[id] = struct{}{}
		}
		for i := range a.easymode.CrossValidations.Rules {
			rule := &a.easymode.CrossValidations.Rules[i]
			if _, ok := allowedIDs[rule.ID]; !ok {
				continue
			}
			schema.CrossValidations = append(schema.CrossValidations, hmapi.UISchemaCrossValidation{
				ID:              rule.ID,
				Rule:            rule.Rule,
				ParamA:          rule.ParamA,
				ParamB:          rule.ParamB,
				Param:           rule.Param,
				MinParam:        rule.MinParam,
				MaxParam:        rule.MaxParam,
				AppliesToParams: append([]string(nil), rule.AppliesToParams...),
				Error:           a.errorLabel(locale, rule.ErrorKey),
			})
		}
	}

	// MASTER profiles — synthesised from the easymode MasterProfile
	// definition (TCL PROFILES_MAP blocks). Rendered by the SPA's
	// ProfileSelector exactly like LINK profiles. The archives
	// currently ship no MASTER profiles for any device, so this
	// branch is infrastructure that activates automatically once the
	// upstream data includes the PROFILES_MAP entries.
	if paramsetKey == hmenum.ParamsetKeyMaster && cmeta != nil && cmeta.MasterProfile != nil {
		if p := a.synthesiseMasterProfile(locale, channelType, cmeta.MasterProfile, schema.Parameters); p != nil {
			schema.Profile = p
		}
	}

	// Subset groups.
	// Resolves SubsetDef → UISchemaSubsetGroup, matches current values
	// against each option to set current_option_id when the values
	// agree exactly. Like MASTER profiles this is infrastructure that
	// activates once the easymode extractor populates the field.
	if paramsetKey == hmenum.ParamsetKeyMaster && meta != nil && len(meta.Subsets) > 0 {
		schema.SubsetGroups = a.buildSubsetGroups(locale, meta.Subsets, schema.Parameters)
	}

	return schema, nil
}

// buildSubsetGroups resolves easymode subsets into UI-renderable
// groups. Subsets sharing the same member-param set are merged into
// one picker.
func (a *UISchemaAdapter) buildSubsetGroups(
	locale string,
	subsets []ccudata.SubsetDef,
	params []hmapi.UISchemaParameter,
) []hmapi.UISchemaSubsetGroup {
	if len(subsets) == 0 {
		return nil
	}
	// Snapshot of current values for option-matching.
	current := make(map[string]any, len(params))
	for i := range params {
		p := &params[i]
		if p.Observed {
			current[p.Name] = p.Value
		}
	}

	type bucket struct {
		group hmapi.UISchemaSubsetGroup
		key   string
	}
	out := make([]bucket, 0, len(subsets))
	for _, ss := range subsets {
		if len(ss.MemberParams) == 0 {
			continue
		}
		k := joinSorted(ss.MemberParams)
		// Merge into existing bucket if the member set matches.
		idx := -1
		for i := range out {
			if out[i].key == k {
				idx = i
				break
			}
		}
		opts := resolveSubsetOptions(ss)
		if idx == -1 {
			out = append(out, bucket{
				key: k,
				group: hmapi.UISchemaSubsetGroup{
					ID:           "subset_" + ss.MemberParams[0],
					Label:        a.errorLabel(locale, ss.NameKey),
					MemberParams: append([]string(nil), ss.MemberParams...),
					Options:      opts,
				},
			})
		} else {
			out[idx].group.Options = append(out[idx].group.Options, opts...)
		}
	}
	// Active-option detection.
	for i := range out {
		g := &out[i].group
		for j := range g.Options {
			if subsetOptionMatches(g.Options[j].Values, current) {
				id := g.Options[j].ID
				g.CurrentOptionID = &id
				break
			}
		}
	}
	groups := make([]hmapi.UISchemaSubsetGroup, len(out))
	for i := range out {
		groups[i] = out[i].group
	}
	return groups
}

func resolveSubsetOptions(ss ccudata.SubsetDef) []hmapi.UISchemaSubsetOpt {
	if len(ss.Options) > 0 {
		out := make([]hmapi.UISchemaSubsetOpt, 0, len(ss.Options))
		for _, opt := range ss.Options {
			out = append(out, hmapi.UISchemaSubsetOpt{
				ID:     opt.ID,
				Label:  opt.LabelKey,
				Values: opt.Values,
			})
		}
		return out
	}
	// Legacy single-option form: treat the whole def as one option.
	if len(ss.Values) > 0 {
		return []hmapi.UISchemaSubsetOpt{{
			ID:     ss.ID,
			Label:  ss.NameKey,
			Values: ss.Values,
		}}
	}
	return nil
}

func subsetOptionMatches(values, current map[string]any) bool {
	for k, want := range values {
		got, ok := current[k]
		if !ok {
			return false
		}
		if !looseEqual(got, want) {
			return false
		}
	}
	return true
}

func looseEqual(a, b any) bool {
	if a == b {
		return true
	}
	// Numeric tolerance — CCU may return floats where the easymode
	// value is an int.
	af, ok1 := coerceFloat(a)
	bf, ok2 := coerceFloat(b)
	if ok1 && ok2 {
		return af == bf
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func joinSorted(in []string) string {
	cp := append([]string(nil), in...)
	sort.Strings(cp)
	return strings.Join(cp, "|")
}

// synthesiseMasterProfile converts the easymode MasterProfile into a
// UISchemaProfile with the shape the SPA ProfileSelector expects, so
// the component needs no branch for MASTER vs. LINK profiles.
//
// Structure: `{ "_MASTER": { "profiles": [ { id, name:{en,de}, params:
// { <p>: {constraint_type:"fixed", value:…} } }, … ] } }` — i.e. the
// MasterProfileDef's constraints are emitted as fixed values (the
// Go model does not yet carry range / list constraints).
//
// The active profile ID is derived from the current values just like
// matchActiveProfile() does for LINK: highest-specificity profile
// whose fixed values all match current.
func (a *UISchemaAdapter) synthesiseMasterProfile(
	locale, channelType string,
	mp *ccudata.MasterProfile,
	params []hmapi.UISchemaParameter,
) *hmapi.UISchemaProfile {
	if mp == nil || len(mp.Profiles) == 0 {
		return nil
	}
	// Current values → for active-profile matching.
	current := make(map[string]any, len(params))
	for i := range params {
		p := &params[i]
		if p.Observed {
			current[p.Name] = p.Value
		}
	}

	type outProfile struct {
		ID     int                               `json:"id"`
		Name   map[string]string                 `json:"name"`
		Params map[string]profileParamConstraint `json:"params,omitempty"`
	}
	profiles := make([]outProfile, 0, len(mp.Profiles)+1)
	// Profile id=0 ("Expert" fallback) — no params, always available.
	profiles = append(profiles, outProfile{
		ID: 0,
		Name: map[string]string{
			"en": "Expert",
			"de": "Experte",
		},
	})
	defsForMatch := make([]profileDef, 0, len(mp.Profiles))
	for i, def := range mp.Profiles {
		labelEn := a.errorLabel("en", def.LabelKey)
		labelDe := a.errorLabel("de", def.LabelKey)
		if labelEn == "" {
			labelEn = def.ID
		}
		if labelDe == "" {
			labelDe = labelEn
		}
		paramConstraints := make(map[string]profileParamConstraint, len(def.Constraints))
		for _, c := range def.Constraints {
			enc, err := json.Marshal(c.Value)
			if err != nil {
				continue
			}
			paramConstraints[c.Parameter] = profileParamConstraint{
				ConstraintType: "fixed",
				Value:          enc,
			}
		}
		// Positional id starting at 1; the Go model's ID is a string
		// label — we use the index for the numeric contract.
		id := i + 1
		profiles = append(profiles, outProfile{
			ID:     id,
			Name:   map[string]string{"en": labelEn, "de": labelDe},
			Params: paramConstraints,
		})
		defsForMatch = append(defsForMatch, profileDef{
			ID:     id,
			Name:   map[string]string{"en": labelEn, "de": labelDe},
			Params: paramConstraints,
		})
	}
	raw, err := json.Marshal(map[string]any{
		"_MASTER": map[string]any{"profiles": profiles},
	})
	if err != nil {
		return nil
	}
	_ = locale
	_ = channelType
	return &hmapi.UISchemaProfile{
		ReceiverType:    channelType,
		SenderType:      "_MASTER",
		ActiveProfileID: matchActiveProfile(defsForMatch, current),
		Raw:             raw,
	}
}

// buildParameters projects the channel's data points onto the
// renderable parameter list. Applies the same visibility filter the
// CCU WebUI uses
//
//   - FLAGS.VISIBLE must be set
//   - FLAGS.INTERNAL must NOT be set
//   - OPERATIONS must include READ or WRITE
//   - Schedule parameters (N_WP_*, WEEK_PROGRAM*) are excluded
//   - MASTER parameters are hidden when no CCU translation exists
//     (matches the WebUI easymode behaviour — untranslated entries
//     are expert-only and would pollute the view with dozens of raw
//     debug keys)
//
// VALUES keeps the "no translation" exception because runtime state
// must always be visible; operators diagnose with raw names if need
// be.
func (a *UISchemaAdapter) buildParameters(
	ch *device.Channel,
	paramset hmenum.ParamsetKey,
	meta *ccudata.SenderTypeMetadata,
	locale string,
	channelType string,
	expert bool,
) []hmapi.UISchemaParameter {
	dps := ch.ParamsetDataPoints(paramset)
	// Easymode filter: hide MASTER params without a CCU-translated
	// label so the casual view stays readable. The SPA's expert toggle
	// surfaces them again.
	requireTranslation := paramset == hmenum.ParamsetKeyMaster && !expert
	out := make([]hmapi.UISchemaParameter, 0, len(dps))
	for _, dp := range dps {
		pd := dp.ParameterData()
		name := string(dp.Parameter())
		if !paramShouldRender(name, pd) {
			continue
		}
		label := a.parameterLabel(locale, channelType, name)
		if requireTranslation && label == "" {
			continue
		}
		entry := hmapi.UISchemaParameter{
			Name:  name,
			Label: label,
			Help:  a.parameterHelp(locale, name),
			Type:  string(pd.Type),
			// Normalise the unit to the canonical display form (`°C`, `lx`, `%`)
			// regardless of the CCU firmware's spelling (`degree`, `Lux`, `100%`).
			Unit:       generic.CleanupUnit(hmenum.Parameter(name), pd.Unit),
			Min:        cloneRaw(pd.Min),
			Max:        cloneRaw(pd.Max),
			Default:    cloneRaw(pd.Default),
			Control:    pd.Control,
			Operations: hmapi.UISchemaParameterOps{Read: pd.IsReadable(), Write: dpIsWritable(dp, pd), Event: pd.IsEvent(), Determine: pd.IsDeterminable()},
			Flags:      hmapi.UISchemaParameterFlags{Visible: pd.IsVisible(), Internal: pd.IsInternal(), Service: pd.IsService()},
		}
		if len(pd.ValueList) > 0 {
			entry.ValueList = a.valueList(locale, channelType, name, pd.ValueList)
		}
		// Multiplier/DisplayValue project the raw wire value into the unit
		// Unit names, mirroring [DataPointSummary.DisplayValue] /
		// [DataPointSummary.Multiplier] on the REST data-point summary so
		// the config-panel editor and the device list agree. LINK paramset
		// parameters never reach here — UISchema returns into
		// buildLinkSchema before buildParameters runs for that key — and
		// already scale through DisplayAsPercent, which would double-scale
		// if this ran for them too.
		//
		// The multiplier is descriptor metadata, so it is reported whether
		// or not a value has been observed: the editor divides by it before
		// writing, and a parameter the CCU has not pushed yet is still
		// writable. Gating it on observation would send an unobserved
		// dimmer the displayed number as its wire value.
		mult := generic.DefaultMultiplier
		if m, ok := dp.(interface{ Multiplier() float64 }); ok {
			if v := m.Multiplier(); v != 0 && v != generic.DefaultMultiplier {
				mult = v
				entry.Multiplier = v
			}
		}
		if raw, observed := dp.RawValue(); observed {
			entry.Value = raw
			entry.Observed = true
			if dv, dvOK := generic.DisplayValue(raw, mult); dvOK {
				entry.DisplayValue = dv
			}
		}
		if t := dp.ModifiedAt(); !t.IsZero() {
			entry.ModifiedAt = t.UTC().Format("2006-01-02T15:04:05.000Z")
		}
		if meta != nil {
			if g := groupForParam(meta, name); g != "" {
				entry.GroupID = g
			}
			if presetID, ok := meta.OptionPresets[name]; ok {
				entry.Preset = presetID
				expanded := a.expandPresets(locale, presetID)
				entry.Presets = expanded
				// UC5: propagate the allow_custom flag from the archive preset.
				// Only 16 of 82 presets carry AllowCustom=true; for those the
				// SPA renders a free-text input alongside the chips.
				if len(expanded) > 0 && a.easymode != nil {
					if ep, epOK := a.easymode.OptionPresets[presetID]; epOK {
						entry.AllowCustomValue = ep.AllowCustom
					}
				}
			}
			// UC6: attach the subset group id when the parameter is a
			// member of a subset group. SubsetGroupIDs maps parameter name
			// to the group id string.
			if groupID, ok := meta.SubsetGroupIDs[name]; ok && groupID != "" {
				entry.SubsetGroupID = groupID
			}
		}
		out = append(out, entry)
	}
	return out
}

// ParamShouldRender reports whether a parameter should appear in the
// rendered schema. Keeping this as a standalone predicate makes the
// visibility rules easy to unit-test independently.
func paramShouldRender(name string, pd hmproto.ParameterData) bool {
	if !pd.IsVisible() || pd.IsInternal() {
		return false
	}
	if !pd.IsReadable() && !pd.IsWritable() {
		return false
	}
	// Week-program / schedule parameters have their own editor and
	// should not appear in the generic configuration form.
	if strings.HasPrefix(name, "WEEK_PROGRAM") {
		return false
	}
	if isSchedulePattern(name) {
		return false
	}
	return true
}

// isSchedulePattern reports whether `name` matches the CCU's
// `<N>_WP_*` schedule convention (e.g. `1_WP_ENDTIME_SUNDAY_5`).
// Cheaper than a regex: scan until the `_WP_` sentinel.
func isSchedulePattern(name string) bool {
	if len(name) < 5 {
		return false
	}
	i := 0
	for i < len(name) && name[i] >= '0' && name[i] <= '9' {
		i++
	}
	if i == 0 {
		return false
	}
	return strings.HasPrefix(name[i:], "_WP_")
}

// applyOrder reorders params to match the easymode ParameterOrder
// when provided; missing entries are appended alphabetically.
func (a *UISchemaAdapter) applyOrder(params []hmapi.UISchemaParameter, meta *ccudata.SenderTypeMetadata) {
	if meta == nil || len(meta.ParameterOrder) == 0 {
		sort.Slice(params, func(i, j int) bool { return params[i].Name < params[j].Name })
		return
	}
	rank := make(map[string]int, len(meta.ParameterOrder))
	for i, n := range meta.ParameterOrder {
		rank[n] = i
	}
	sort.SliceStable(params, func(i, j int) bool {
		ri, oki := rank[params[i].Name]
		rj, okj := rank[params[j].Name]
		switch {
		case oki && okj:
			return ri < rj
		case oki:
			return true
		case okj:
			return false
		default:
			return params[i].Name < params[j].Name
		}
	})
}

func (a *UISchemaAdapter) lookupChannel(address string, channelNo int) (*device.Device, *device.Channel) {
	if a.registry == nil {
		return nil, nil
	}
	for _, u := range a.registry.List() {
		dev, ok := u.ModelRegistry.Get(address)
		if !ok {
			continue
		}
		chAddr := fmt.Sprintf("%s:%d", address, channelNo)
		ch := dev.Channel(chAddr)
		return dev, ch
	}
	return nil, nil
}

// channelTypeOf returns the effective channel type for metadata
// lookups. Prefers the channel's own Type (set from the CCU's
// listDevices TYPE field during ingest) and falls back to the device
// model for legacy paths that didn't record it yet. The receiver-
// alias table from the embedded profile catalogue is applied so
// user-facing easymode keys resolve even when the CCU names a
// slightly different receiver type (e.g. OPTICAL_SIGNAL_RECEIVER →
// DIMMER_VIRTUAL_RECEIVER).
func (a *UISchemaAdapter) channelTypeOf(dev *device.Device, ch *device.Channel) string {
	t := ch.Type
	if t == "" {
		t = dev.Model
	}
	if a.profiles != nil {
		if alias, ok := a.profiles.Aliases[t]; ok {
			return alias
		}
	}
	return t
}

// channelLabel looks up the localised label for the channel type.
func (a *UISchemaAdapter) channelLabel(locale, channelType string) string {
	if a.translations == nil {
		return ""
	}
	return a.translations.ChannelType(locale, channelType)
}

func (a *UISchemaAdapter) parameterLabel(locale, channelType, parameter string) string {
	if a.translations == nil {
		return ""
	}
	return a.translations.ParameterLabel(locale, channelType, parameter)
}

func (a *UISchemaAdapter) parameterHelp(locale, parameter string) string {
	if a.translations == nil {
		return ""
	}
	return a.translations.ParameterHelpText(locale, parameter)
}

func (a *UISchemaAdapter) groupLabel(locale, key string) string {
	if a.translations == nil || key == "" {
		return key
	}
	return a.translations.UILabel(locale, key)
}

// groupLabelWithFallback resolves a group title using a two-stage chain:
//  1. Translation-table lookup via key (locale, then "en").
//  2. Inline locale→label map from the easymode archive (locale, then "en").
//
// Returns the key itself when neither stage yields a non-empty string.
func (a *UISchemaAdapter) groupLabelWithFallback(locale, key string, inlineLabels map[string]string) string {
	// Stage 1: translation table.
	if a.translations != nil && key != "" {
		if t := a.translations.UILabel(locale, key); t != "" {
			return t
		}
		if locale != "en" {
			if t := a.translations.UILabel("en", key); t != "" {
				return t
			}
		}
	}
	// Stage 2: inline label map from the easymode archive.
	if len(inlineLabels) > 0 {
		if t := inlineLabels[locale]; t != "" {
			return t
		}
		if t := inlineLabels["en"]; t != "" {
			return t
		}
	}
	return key
}

func (a *UISchemaAdapter) errorLabel(locale, key string) string {
	if a.translations == nil || key == "" {
		return key
	}
	return a.translations.UILabel(locale, key)
}

// expandPresets resolves the easymode preset id into the list the SPA
// renders as clickable chips. Returns nil when the id is unknown or
// the preset has no entries — that lets the SPA omit the preset row
// entirely.
func (a *UISchemaAdapter) expandPresets(locale, presetID string) []hmapi.UISchemaPreset {
	if a.easymode == nil || presetID == "" {
		return nil
	}
	preset, ok := a.easymode.OptionPresets[presetID]
	if !ok || len(preset.Options) == 0 {
		return nil
	}
	out := make([]hmapi.UISchemaPreset, 0, len(preset.Options))
	for _, opt := range preset.Options {
		raw, err := json.Marshal(opt.Value)
		if err != nil {
			continue
		}
		label := a.resolvePresetLabel(locale, opt)
		out = append(out, hmapi.UISchemaPreset{
			Label: label,
			Value: raw,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// resolvePresetLabel returns the display label for one preset entry.
// Resolution order:
//  1. UILabel(label_key, locale)  — translation table, requested locale
//  2. UILabel(label_key, "en")    — translation table, English fallback
//  3. opt.Label                   — inline label from the archive
//  4. empty string
func (a *UISchemaAdapter) resolvePresetLabel(locale string, opt ccudata.OptionPresetVal) string {
	if opt.LabelKey != "" && a.translations != nil {
		if t := a.translations.UILabel(locale, opt.LabelKey); t != "" {
			return t
		}
		if locale != "en" {
			if t := a.translations.UILabel("en", opt.LabelKey); t != "" {
				return t
			}
		}
	}
	return opt.Label
}

// valueList resolves one VALUE_LIST into labelled entries.
//
// 1. `<channel_type>|<param>=<value>` (channel-qualified) 2. bare
// `<param>=<value>` (global) 3. `<channel_type>|<param>=<index>` /
// `<param>=<index>` (easymode TCL options are stored as parameter=N because
// the VALUE_LIST strings are not available at extraction time) 4. humanised
// raw value as last resort
//
// The channel-qualified forms piggy-back on
// [ccudata.Translations.ParameterValue] via a synthetic parameter prefix;
// that helper already understands the `<param>=<value>` key shape.
func (a *UISchemaAdapter) valueList(locale, channelType, parameter string, values []string) []hmapi.UISchemaValueListEntry {
	out := make([]hmapi.UISchemaValueListEntry, 0, len(values))
	for i, v := range values {
		out = append(out, hmapi.UISchemaValueListEntry{
			Value: i,
			Key:   v,
			Label: ValueListLabel(a.translations, locale, channelType, parameter, v, i),
		})
	}
	return out
}

// humanizeRaw produces a readable label from an untranslated
// VALUE_LIST entry. Upper-snake-case "HIGH_PRIORITY" becomes "High
// Priority"; pure numeric keys stay as-is.
func humanizeRaw(v string) string {
	if v == "" {
		return ""
	}
	parts := strings.Split(v, "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
	}
	return strings.Join(parts, " ")
}

func groupForParam(meta *ccudata.SenderTypeMetadata, parameter string) string {
	for _, g := range meta.ParameterGroups {
		if slices.Contains(g.Parameters, parameter) {
			return g.ID
		}
	}
	return ""
}

// normalizeParamsetKey accepts "VALUES" / "MASTER" / "LINK" (any
// case) and returns the canonical [hmenum.ParamsetKey]. Unknown
// keys surface as an error the handler converts to HTTP 400.
func normalizeParamsetKey(raw string) (hmenum.ParamsetKey, error) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "", "VALUES":
		return hmenum.ParamsetKeyValues, nil
	case "MASTER":
		return hmenum.ParamsetKeyMaster, nil
	case "LINK":
		return hmenum.ParamsetKeyLink, nil
	}
	return "", fmt.Errorf("ui-schema: unsupported paramset %q", raw)
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}

// dpIsWritable returns the data point's effective write capability.
// Prefers the typed `IsWritable()` method (every `*generic.DataPoint[T]`
// implements it through its embedded base) so the `_SWITCH_DP_TO_SENSOR`
// override surfaces in the UI schema (HmIP-eTRV.LEVEL → write: false
// even though the descriptor lists WRITE). Falls back to the
// descriptor bitmask for DPs that don't expose the typed method
// (test fakes, future families).
func dpIsWritable(dp device.ParameterDataPoint, pd hmproto.ParameterData) bool {
	if w, ok := dp.(interface{ IsWritable() bool }); ok {
		return w.IsWritable()
	}
	return pd.IsWritable()
}
