// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/SukramJ/go-fabric-ui/fabricui"
	"github.com/SukramJ/go-fabric/eligibility"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	matterendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"
)

// The item fields of the selection page: the schema's columns, keyed as
// the page reads them back from [fabricui.Item].Fields.
const (
	ExposureFieldCentral   = "central"
	ExposureFieldDevice    = "device"
	ExposureFieldChannel   = "channel"
	ExposureFieldKind      = "kind"
	ExposureFieldKey       = "key"
	ExposureFieldParameter = "parameter"
)

// The verdict sources the provider reports in [fabricui.Item].Source: a
// row the operator decided on, or the classifier's rule alone.
const (
	ExposureSourceRule     = "rule"
	ExposureSourceOverride = "override"
)

// ExposureStore is the allowlist the provider reads and writes — the
// subset of [matterendpoint.Store] it needs.
type ExposureStore interface {
	ListExposures(ctx context.Context, centralName string) ([]matterendpoint.ExposureRecord, error)
	UpsertExposure(ctx context.Context, rec matterendpoint.ExposureRecord) error
}

// ChannelTypedParameterLabeler resolves a parameter's operator-facing
// label for its channel type (`POWER` on `ENERGIE_METER_TRANSMITTER`
// reads differently from the bare parameter table). Empty when no
// translation exists.
type ChannelTypedParameterLabeler interface {
	ChannelTypedParameterLabel(channelType, parameter string) string
}

// ExposureProviderConfig is what the provider is built from. Every field
// is read on each call, so a candidate walk that reads the live registry
// surfaces freshly discovered devices on the next request.
type ExposureProviderConfig struct {
	// Candidates walks the model: one [Candidate] per exposable
	// (channel, dp_kind, dp_key); see [CollectCandidates].
	Candidates func(ctx context.Context) []Candidate
	// Store is the operator's allowlist.
	Store ExposureStore
	// Labels resolves the parameter column; nil leaves the title-cased
	// parameter name.
	Labels ChannelTypedParameterLabeler
	// Translate renders the column headers in the daemon's locale from
	// the `node.exposure.column.*` catalogue keys; nil shows the keys.
	Translate func(key string) string
	// Actor names the authenticated principal an Apply records; nil or an
	// empty answer records "anonymous".
	Actor func(ctx context.Context) string
	// Audit receives one entry per applied batch; nil records nothing.
	Audit audit.Recorder
}

// ExposureProvider is loom's side of go-fabric-ui's selection page
// (go-fabric-ui's provider guide, docs/provider in that repository, at
// v0.1.1): the exposable CCU channels
// as the candidate walk classifies them, merged with the operator's
// allowlist. The verdicts are independent of each other — enabling one
// row never changes another row's eligibility — so a preview is the
// touched rows with the proposed state and the unchanged verdict.
type ExposureProvider struct {
	cfg ExposureProviderConfig
}

// NewExposureProvider builds a provider over cfg.
func NewExposureProvider(cfg ExposureProviderConfig) *ExposureProvider {
	return &ExposureProvider{cfg: cfg}
}

// Schema implements [fabricui.ExposureProvider]: the five source-key
// columns and the parameter label, grouped by device. The central and the
// kind carry a value filter; a fleet of one CCU shows a one-entry filter,
// which is cheaper than a schema that depends on the registry.
func (p *ExposureProvider) Schema(context.Context) (fabricui.Schema, error) {
	return fabricui.Schema{
		Columns: []fabricui.Column{
			{Key: ExposureFieldCentral, Label: p.label("node.exposure.column.central"), Filter: true},
			{Key: ExposureFieldDevice, Label: p.label("node.exposure.column.device")},
			{Key: ExposureFieldChannel, Label: p.label("node.exposure.column.channel")},
			{Key: ExposureFieldKind, Label: p.label("node.exposure.column.kind"), Filter: true},
			{Key: ExposureFieldKey, Label: p.label("node.exposure.column.key")},
			{Key: ExposureFieldParameter, Label: p.label("node.exposure.column.parameter")},
		},
		Groups: true,
	}, nil
}

func (p *ExposureProvider) label(key string) string {
	if p.cfg.Translate == nil {
		return key
	}
	return p.cfg.Translate(key)
}

// List implements [fabricui.ExposureProvider]: every candidate, with the
// allowlist row's state where one exists, ordered central → device →
// channel → kind → key.
func (p *ExposureProvider) List(ctx context.Context) ([]fabricui.Item, error) {
	items, _, err := p.items(ctx)
	return items, err
}

// listing is the merged list, an index by item key and the allowlist rows
// by source key.
type listing struct {
	items []fabricui.Item
	index map[string]int
	rows  map[matterendpoint.SourceKey]matterendpoint.ExposureRecord
}

// items is the merged list plus an index by key.
func (p *ExposureProvider) items(ctx context.Context) ([]fabricui.Item, map[string]int, error) {
	l, err := p.list(ctx)
	if err != nil {
		return nil, nil, err
	}
	return l.items, l.index, nil
}

func (p *ExposureProvider) list(ctx context.Context) (listing, error) {
	if p.cfg.Candidates == nil || p.cfg.Store == nil {
		return listing{}, errors.New("matteradapter: exposure provider has no candidate walk or no store")
	}
	candidates := p.cfg.Candidates(ctx)
	rows, err := p.cfg.Store.ListExposures(ctx, "")
	if err != nil {
		return listing{}, err
	}
	byKey := make(map[matterendpoint.SourceKey]matterendpoint.ExposureRecord, len(rows))
	for i := range rows {
		byKey[rows[i].Key] = rows[i]
	}
	sort.SliceStable(candidates, func(i, j int) bool { return lessSourceKey(candidates[i].Key, candidates[j].Key) })
	out := make([]fabricui.Item, 0, len(candidates))
	index := make(map[string]int, len(candidates))
	for i := range candidates {
		c := &candidates[i]
		it := p.item(c)
		if row, ok := byKey[c.Key]; ok {
			it.Enabled = row.Enabled
			if row.FriendlyName != "" {
				it.Name = row.FriendlyName
			}
			it.Source = ExposureSourceOverride
		}
		if _, dup := index[it.Key]; dup {
			continue
		}
		index[it.Key] = len(out)
		out = append(out, it)
	}
	return listing{items: out, index: index, rows: byKey}, nil
}

// item renders one candidate without its allowlist state.
func (p *ExposureProvider) item(c *Candidate) fabricui.Item {
	name := c.DisplayName
	if name == "" {
		name = c.Key.DeviceAddress
	}
	return fabricui.Item{
		Key:   c.Key.String(),
		Name:  name,
		Group: name,
		Fields: map[string]string{
			ExposureFieldCentral:   c.Key.CentralName,
			ExposureFieldDevice:    c.Key.DeviceAddress,
			ExposureFieldChannel:   strconv.Itoa(c.Key.ChannelNo),
			ExposureFieldKind:      string(c.Key.DPKind),
			ExposureFieldKey:       c.Key.DPKey,
			ExposureFieldParameter: p.parameterLabel(c.ChannelType, c.Key.DPKey),
		},
		Verdict: c.Verdict,
		Source:  ExposureSourceRule,
	}
}

// parameterLabel is the channel-typed translation when one exists, else
// the title-cased parameter — the same chain the device views render.
func (p *ExposureProvider) parameterLabel(channelType, parameter string) string {
	if p.cfg.Labels != nil {
		if s := p.cfg.Labels.ChannelTypedParameterLabel(channelType, parameter); s != "" {
			return s
		}
	}
	return naming.TitleCaseParameter(parameter)
}

// check refuses a batch the provider cannot apply: a key it does not
// list, or enabling a row the classifier calls unmappable. It returns the
// listed items with the index the updates resolve through.
func (p *ExposureProvider) check(ctx context.Context, updates []fabricui.Update) (listing, error) {
	l, err := p.list(ctx)
	if err != nil {
		return listing{}, err
	}
	for _, u := range updates {
		i, ok := l.index[u.Key]
		if !ok {
			return listing{}, fmt.Errorf("%w: %q", fabricui.ErrUnknownItem, u.Key)
		}
		if u.Enabled && l.items[i].Verdict.State == eligibility.StateUnmappable {
			return listing{}, fmt.Errorf("%w: %q cannot be exposed: %s", fabricui.ErrInvalidUpdate, u.Key, l.items[i].Verdict.Reason)
		}
	}
	return l, nil
}

// Preview implements [fabricui.ExposureProvider]: the touched items with
// the proposed state. Nothing is written.
func (p *ExposureProvider) Preview(ctx context.Context, updates []fabricui.Update) ([]fabricui.Item, error) {
	l, err := p.check(ctx, updates)
	if err != nil {
		return nil, err
	}
	out := make([]fabricui.Item, 0, len(updates))
	seen := make(map[string]bool, len(updates))
	for _, u := range updates {
		if seen[u.Key] {
			continue
		}
		seen[u.Key] = true
		it := l.items[l.index[u.Key]]
		it.Fields = cloneFields(it.Fields)
		it.Enabled = u.Enabled
		if u.Name != nil {
			if n := strings.TrimSpace(*u.Name); n != "" {
				it.Name = n
			}
		}
		it.Source = ExposureSourceOverride
		out = append(out, it)
	}
	return out, nil
}

// Apply implements [fabricui.ExposureProvider]: one allowlist row per
// update, written after the whole batch passed [ExposureProvider.check],
// then one audit entry for the batch. The handler reassembles the node's
// endpoints afterwards (Options.Reassemble), so the change is served
// before the response.
func (p *ExposureProvider) Apply(ctx context.Context, updates []fabricui.Update) (int, error) {
	l, err := p.check(ctx, updates)
	if err != nil {
		return 0, err
	}
	actor := p.actor(ctx)
	applied := 0
	for _, u := range updates {
		key := sourceKeyOf(l.items[l.index[u.Key]])
		rec := matterendpoint.ExposureRecord{
			Key:     key,
			Enabled: u.Enabled,
			// A row the operator named before keeps its name unless the
			// update renames it.
			FriendlyName: l.rows[key].FriendlyName,
			Actor:        actor,
		}
		if u.Name != nil {
			rec.FriendlyName = strings.TrimSpace(*u.Name)
		}
		if err := p.cfg.Store.UpsertExposure(ctx, rec); err != nil {
			return applied, err
		}
		applied++
	}
	if p.cfg.Audit != nil && applied > 0 {
		p.cfg.Audit.Record(audit.Entry{
			User:   actor,
			Action: audit.ActionMatterExposureBulk,
			Note:   fmt.Sprintf("applied=%d", applied),
		})
	}
	return applied, nil
}

func (p *ExposureProvider) actor(ctx context.Context) string {
	if p.cfg.Actor != nil {
		if s := p.cfg.Actor(ctx); s != "" {
			return s
		}
	}
	return "anonymous"
}

// sourceKeyOf rebuilds the store key from the item's fields; the fields
// are the key's own five parts, so this is a projection, not a parse.
func sourceKeyOf(it fabricui.Item) matterendpoint.SourceKey {
	ch, _ := strconv.Atoi(it.Fields[ExposureFieldChannel])
	return matterendpoint.SourceKey{
		CentralName:   it.Fields[ExposureFieldCentral],
		DeviceAddress: it.Fields[ExposureFieldDevice],
		ChannelNo:     ch,
		DPKind:        matterendpoint.DPKind(it.Fields[ExposureFieldKind]),
		DPKey:         it.Fields[ExposureFieldKey],
	}
}

func lessSourceKey(a, b matterendpoint.SourceKey) bool {
	if a.CentralName != b.CentralName {
		return a.CentralName < b.CentralName
	}
	if a.DeviceAddress != b.DeviceAddress {
		return a.DeviceAddress < b.DeviceAddress
	}
	if a.ChannelNo != b.ChannelNo {
		return a.ChannelNo < b.ChannelNo
	}
	if a.DPKind != b.DPKind {
		return a.DPKind < b.DPKind
	}
	return a.DPKey < b.DPKey
}

func cloneFields(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
