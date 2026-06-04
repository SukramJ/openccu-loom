// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
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
) (*handlers.UISchema, error) {
	if peer == "" {
		return nil, errors.New("ui-schema: LINK paramset requires peer query parameter")
	}
	if a.writer == nil {
		return nil, errors.New("ui-schema: LINK paramset requires wired value writer")
	}
	c := a.findCentralFor(dev.Address)
	if c == nil {
		return nil, handlers.ErrUISchemaNotFound
	}
	backend, ok := a.writer.Backend(c.Name(), dev.InterfaceID)
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
	schema := &handlers.UISchema{
		Channel: handlers.UISchemaChannel{
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
	params := make([]handlers.UISchemaParameter, 0, len(desc))
	for name := range desc {
		pd := desc[name]
		if !paramShouldRender(name, pd) {
			continue
		}
		entry := handlers.UISchemaParameter{
			Name:       name,
			Label:      a.parameterLabel(locale, channelType, name),
			Help:       a.parameterHelp(locale, name),
			Type:       string(pd.Type),
			Unit:       pd.Unit,
			Min:        cloneRaw(pd.Min),
			Max:        cloneRaw(pd.Max),
			Default:    cloneRaw(pd.Default),
			Control:    pd.Control,
			Operations: handlers.UISchemaParameterOps{Read: pd.IsReadable(), Write: pd.IsWritable(), Event: pd.IsEvent()},
			Flags:      handlers.UISchemaParameterFlags{Visible: pd.IsVisible(), Internal: pd.IsInternal(), Service: pd.IsService()},
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
		enrichLinkParameter(&entry, locale)
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
			schema.Profile = &handlers.UISchemaProfile{
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
func enrichLinkParameter(p *handlers.UISchemaParameter, locale string) {
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
			p.TimePresets = append(p.TimePresets, handlers.UISchemaTimePreset{
				Base:   lp.Base,
				Factor: lp.Factor,
				Label:  lp.Label,
			})
		}
	}
	// the CCU uses the extended range where 1.005 means "last known level").
	if meta.DisplayAsPercent && rawFloatGreaterThan(p.Max, 1.0) {
		p.HasLastValue = true
	} else {
		p.HasLastValue = meta.HasLastValue
	}
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
func buildKeypressGroups(locale string, params []handlers.UISchemaParameter) []handlers.UISchemaGroup {
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
	out := make([]handlers.UISchemaGroup, 0, len(order))
	for _, g := range order {
		if len(members[g.id]) == 0 {
			continue
		}
		label := g.en
		if locale == "de" {
			label = g.de
		}
		out = append(out, handlers.UISchemaGroup{ID: g.id, Label: label, Parameters: members[g.id]})
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
