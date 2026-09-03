// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// buildLinkSchema assembles a UI schema for the LINK paramset of one
// (channel, peer) pair. Unlike VALUES / MASTER the LINK paramset is
// **not** cached in the channel's data-point registry — the CCU
// keeps one LINK paramset per connected peer, and there can be many
// peers. We fetch description + values on-demand through the backend.
//
// Port of the LINK branch in
// (form_schema.py:generate with paramset_key=LINK).
func (a *UISchemaAdapter) buildLinkSchema( //nolint:funlen // single-purpose link schema assembly with many paramset branches
	ctx context.Context,
	dev *device.Device,
	ch *device.Channel,
	peer, locale string,
) (*hmapi.UISchema, error) {
	if peer == "" {
		return nil, errors.New("ui-schema: LINK paramset requires peer query parameter")
	}
	if a.writer == nil {
		return nil, errors.New("ui-schema: LINK paramset requires wired value writer")
	}
	c := a.findCentralFor(dev.Address)
	if c == nil {
		return nil, hmapi.ErrUISchemaNotFound
	}
	backend, ok := a.writer.Backend(c.Name(), hmtypes.ParseWireInterfaceID(dev.InterfaceID))
	if !ok {
		return nil, fmt.Errorf("ui-schema: no backend for %s/%s", c.Name(), dev.InterfaceID)
	}
	desc, err := backend.GetLinkParamsetDescription(ctx, ch.Address, peer)
	if err != nil {
		return nil, fmt.Errorf("ui-schema: link descriptor: %w", err)
	}
	values, err := backend.GetLinkParamset(ctx, ch.Address, peer)
	if err != nil {
		// Values are a nice-to-have — the descriptor alone is enough
		// to render the form with its defaults.
		values = map[string]any{}
	}

	channelType := a.channelTypeOf(dev, ch)
	schema := &hmapi.UISchema{
		Channel: hmapi.UISchemaChannel{
			Address:  ch.Address,
			Number:   ch.Number,
			Type:     channelType,
			Device:   dev.Address,
			Label:    a.channelLabel(locale, channelType),
			Paramset: "LINK",
		},
	}

	// LINK parameters are almost never translated verbatim, so we
	// relax the require-translation filter that MASTER applies.
	params := make([]hmapi.UISchemaParameter, 0, len(desc))
	for name := range desc {
		pd := desc[name]
		if !paramShouldRender(name, pd) {
			continue
		}
		entry := hmapi.UISchemaParameter{
			Name:       name,
			Label:      a.parameterLabel(locale, channelType, name),
			Help:       a.parameterHelp(locale, name),
			Type:       string(pd.Type),
			Unit:       pd.Unit,
			Min:        cloneRaw(pd.Min),
			Max:        cloneRaw(pd.Max),
			Default:    cloneRaw(pd.Default),
			Control:    pd.Control,
			Operations: hmapi.UISchemaParameterOps{Read: pd.IsReadable(), Write: pd.IsWritable(), Event: pd.IsEvent(), Determine: pd.IsDeterminable()},
			Flags:      hmapi.UISchemaParameterFlags{Visible: pd.IsVisible(), Internal: pd.IsInternal(), Service: pd.IsService()},
		}
		if entry.Label == "" {
			entry.Label = humanizeRaw(name)
		}
		if len(pd.ValueList) > 0 {
			entry.ValueList = a.valueList(locale, channelType, name, pd.ValueList)
		}
		if raw, ok := values[name]; ok {
			entry.Value = raw
			entry.Observed = true
		}
		enrichLinkParameter(&entry, pd.Special, locale)
		params = append(params, entry)
	}
	schema.Parameters = params
	a.applyOrder(schema.Parameters, nil)

	// Groups — LINK paramsets use SHORT/LONG/COMMON sub-sections
	// derived from the keypress classification, so the SPA can render
	// labelled blocks. Falls back to the generic pattern builder when
	// no parameters carry keypress metadata.
	if groups := buildKeypressGroups(locale, schema.Parameters); len(groups) > 0 {
		schema.Groups = groups
	} else {
		schema.Groups = a.buildGroups(locale, nil, schema.Parameters)
	}

	// Profile catalogue for the LINK paramset — this is the
	// canonical use-case and [ProfileSelector] thrives on it. We
	// always filter to the concrete sender channel type so the
	// picker never offers variants that don't apply to this link.
	// When the current values match a profile, its ID is returned
	// as ActiveProfileID so the SPA can pre-select it.
	if a.profiles != nil {
		if raw, ok := a.profiles.Resolve(channelType); ok {
			senderType := a.peerChannelType(peer)
			filtered, defs, resolvedSender, err := filterProfileDocBySender(raw, senderType)
			if err != nil {
				return nil, fmt.Errorf("ui-schema: filter link profile: %w", err)
			}
			profileRaw := filtered
			exposedSender := resolvedSender
			if profileRaw == nil {
				// No sender-specific subset — expose the full document
				// so the SPA still shows *something*; happens when the
				// archive has no entry for this pair (even after alias
				// resolution). SenderType stays blank so the SPA falls
				// back to listing all senders.
				profileRaw = raw
				exposedSender = ""
			}
			schema.Profile = &hmapi.UISchemaProfile{
				ReceiverType:    channelType,
				SenderType:      exposedSender,
				ActiveProfileID: matchActiveProfile(defs, values),
				Raw:             profileRaw,
			}
		}
	}
	return schema, nil
}

// EnrichLinkParameter applies the link-metadata
// classification to p and fills the Link* fields of the schema
// parameter. Port of the enrich_link_metadata branch in
// 's FormBuilder.
func enrichLinkParameter(p *hmapi.UISchemaParameter, special json.RawMessage, locale string) {
	meta := ClassifyLinkParameter(p.Name)
	p.Category = string(meta.Category)
	p.KeypressGroup = string(meta.KeypressGroup)
	p.DisplayAsPercent = meta.DisplayAsPercent
	p.HiddenByDefault = meta.HiddenByDefault
	if meta.TimePairID != "" {
		p.TimePairID = meta.TimePairID
	}
	if meta.TimeSelectorType != "" {
		p.TimeSelectorType = string(meta.TimeSelectorType)
		for _, lp := range GetTimePresets(meta.TimeSelectorType, locale) {
			p.TimePresets = append(p.TimePresets, hmapi.UISchemaTimePreset{
				Base:   lp.Base,
				Factor: lp.Factor,
				Label:  lp.Label,
			})
		}
	}
	// "Last known level" is declared, not inferred from the range.
	//
	// This used to read `MAX > 1.0`, on the claim that a device carrying the
	// feature reports the extended bound. It does not: the firmware declares
	// these parameters max="1.0" and carries the sentinel as a separate
	// SPECIAL member — `<special_value id="OLD_LEVEL" value="1.005"/>`,
	// src/devicetypes/rftypes/rf_d.xml:608 — which the paramset description
	// exports as its own field. A write of 1.005 is accepted because the
	// firmware widens an internal effective_max while leaving max alone, and
	// that is the mechanism the old test mistook for a raised bound. Measured
	// against the descriptor corpus: every dimmer offering the feature reports
	// MAX 1.0 with SPECIAL {"OLD_LEVEL": 1.005}, so the branch never fired.
	//
	// Nothing was missing in practice, because the classification already
	// grants the feature to every level parameter (see LinkParamCategoryLevel).
	// The declared SPECIAL is kept as a second, descriptor-driven route so a
	// parameter the classification does not recognise is still served by what
	// its own device says. It is deliberately not gated on DisplayAsPercent:
	// a parameter declaring OLD_LEVEL is a level parameter by the device's own
	// account, whatever our classification made of its name.
	p.HasLastValue = meta.HasLastValue || declaresSpecialValue(special, "OLD_LEVEL")
	// Steer the keypress sub-group via GroupID so the SPA can render
	// SHORT/LONG blocks without a second classification pass.
	switch meta.KeypressGroup {
	case KeypressGroupShort:
		p.GroupID = "keypress.short"
	case KeypressGroupLong:
		p.GroupID = "keypress.long"
	case KeypressGroupCommon:
		p.GroupID = "keypress.common"
	}
}

// buildKeypressGroups emits up to three groups (short, long, common)
// populated from the classification's GroupID assignment. Empty
// groups are omitted so the SPA does not render stub headings.
func buildKeypressGroups(locale string, params []hmapi.UISchemaParameter) []hmapi.UISchemaGroup {
	order := []struct {
		id string
		en string
		de string
	}{
		{"keypress.common", "General", "Allgemein"},
		{"keypress.short", "Short keypress", "Kurzer Tastendruck"},
		{"keypress.long", "Long keypress", "Langer Tastendruck"},
	}
	members := make(map[string][]string, len(order))
	hit := false
	for i := range params {
		p := &params[i]
		if p.GroupID == "" {
			continue
		}
		members[p.GroupID] = append(members[p.GroupID], p.Name)
		hit = true
	}
	if !hit {
		return nil
	}
	out := make([]hmapi.UISchemaGroup, 0, len(order))
	for _, g := range order {
		if len(members[g.id]) == 0 {
			continue
		}
		label := g.en
		if locale == "de" {
			label = g.de
		}
		out = append(out, hmapi.UISchemaGroup{ID: g.id, Label: label, Parameters: members[g.id]})
	}
	return out
}

// rawFloatGreaterThan reports whether the JSON-encoded `raw` decodes
// to a float strictly greater than `threshold`. Non-numeric payloads
// return false.
func rawFloatGreaterThan(raw []byte, threshold float64) bool {
	if len(raw) == 0 {
		return false
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return false
	}
	return f > threshold
}

// findCentralFor returns the central that owns deviceAddress.
func (a *UISchemaAdapter) findCentralFor(deviceAddress string) *central.Unit {
	if a.registry == nil {
		return nil
	}
	for _, u := range a.registry.List() {
		if _, ok := u.ModelRegistry.Get(deviceAddress); ok {
			return u
		}
	}
	return nil
}

// _ guard against unused import when LINK is the only consumer of
// hmenum from this file.
var _ = hmenum.ParamsetKeyLink

// declaresSpecialValue reports whether a parameter's SPECIAL field carries the
// named member. The CCU exports SPECIAL as an object keyed by the member's id
// — {"OLD_LEVEL": 1.005} — so membership is a key lookup, and a parameter that
// declares none is simply absent.
func declaresSpecialValue(special json.RawMessage, id string) bool {
	if len(special) == 0 {
		return false
	}
	var members map[string]any
	if err := json.Unmarshal(special, &members); err != nil {
		return false
	}
	_, ok := members[id]
	return ok
}
