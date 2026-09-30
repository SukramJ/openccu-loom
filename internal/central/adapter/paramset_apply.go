// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// ParamsetDescriptionIndex is the slice of the persisted paramset-description
// store the multi-channel apply needs: one channel's stored description, and
// every description stored for a central. *sqlite.ParamsetStore satisfies it.
type ParamsetDescriptionIndex interface {
	Get(ctx context.Context, centralName, ifaceID, channelAddress string, psKey hmenum.ParamsetKey) (sqlite.ParamsetRecord, error)
	ListByCentral(ctx context.Context, centralName string) ([]sqlite.ParamsetRecord, error)
}

// ParamsetApplyDomain implements [interfaces.ParamsetApplyService]: applying
// one channel's MASTER values to other channels of the same central.
//
// Eligibility is identity of the full stored MASTER description, compared as
// the parsed structure the description store holds. Neither channel type nor
// device model equality is a substitute: two channels of one channel type
// routinely carry different MASTER parameter sets across device types and
// firmware versions, and a value written to a channel whose description does
// not carry it poisons that channel's configuration store. Writes go through
// [ParamsetsDomain.PutParamsetOn], so every target gets the same coercion,
// model refresh, audit row and read-back report as a single-channel write.
type ParamsetApplyDomain struct {
	registry     *central.Registry
	descriptions ParamsetDescriptionIndex
	paramsets    *ParamsetsDomain
}

// NewParamsetApplyDomain constructs the adapter.
func NewParamsetApplyDomain(
	r *central.Registry, descriptions ParamsetDescriptionIndex, paramsets *ParamsetsDomain,
) *ParamsetApplyDomain {
	return &ParamsetApplyDomain{registry: r, descriptions: descriptions, paramsets: paramsets}
}

var _ interfaces.ParamsetApplyService = (*ParamsetApplyDomain)(nil)

// applySource is the resolved owner of the source channel.
type applySource struct {
	central     string
	interfaceID string
	description hmproto.Paramset
}

// ApplyTargets implements [interfaces.ParamsetApplyService]. A source without
// a stored MASTER description answers [hmerr.ErrDescriptionNotFound]; without
// it there is nothing to compare a target against.
func (p *ParamsetApplyDomain) ApplyTargets(ctx context.Context, sourceChannel string) ([]interfaces.ParamsetApplyTarget, error) {
	src, err := p.resolveSource(ctx, sourceChannel)
	if err != nil {
		return nil, err
	}
	records, err := p.descriptions.ListByCentral(ctx, src.central)
	if err != nil {
		return nil, fmt.Errorf("paramset apply: list stored descriptions of %s: %w", src.central, err)
	}
	unit := p.unit(src.central)
	out := []interfaces.ParamsetApplyTarget{}
	for _, rec := range records {
		if rec.ParamsetKey != hmenum.ParamsetKeyMaster ||
			rec.InterfaceID != src.interfaceID ||
			rec.ChannelAddress == sourceChannel ||
			!sameDescription(src.description, rec.Paramset) {
			continue
		}
		out = append(out, applyTargetFor(unit, rec))
	}
	return out, nil
}

// ApplyToChannels implements [interfaces.ParamsetApplyService]. Every problem
// is a per-target outcome, including a source whose description cannot be
// loaded: the source is re-resolved for each target, so such a source refuses
// every target with the reason rather than failing the request.
func (p *ParamsetApplyDomain) ApplyToChannels(
	ctx context.Context, sourceChannel string, values map[string]any, targets []string, dryRun bool,
) ([]interfaces.ParamsetApplyOutcome, error) {
	out := make([]interfaces.ParamsetApplyOutcome, 0, len(targets))
	for _, target := range targets {
		out = append(out, p.applyOne(ctx, sourceChannel, values, target, dryRun))
	}
	return out, nil
}

// applyOne runs the gates and the write for one target. The source is
// re-resolved per target on purpose: descriptions are refreshed while a batch
// runs (a firmware update, a re-pair), and identity must hold at the moment of
// each write, not at the moment the batch started.
func (p *ParamsetApplyDomain) applyOne(
	ctx context.Context, sourceChannel string, values map[string]any, target string, dryRun bool,
) interfaces.ParamsetApplyOutcome {
	refused := func(reason string) interfaces.ParamsetApplyOutcome {
		return interfaces.ParamsetApplyOutcome{Address: target, Status: interfaces.ApplyRefused, Reason: reason}
	}
	if target == sourceChannel {
		return refused("target is the source channel")
	}
	src, err := p.resolveSource(ctx, sourceChannel)
	if err != nil {
		return refused(err.Error())
	}
	rec, err := p.descriptions.Get(ctx, src.central, src.interfaceID, target, hmenum.ParamsetKeyMaster)
	if err != nil {
		if errors.Is(err, sqlite.ErrParamsetNotFound) {
			return refused(fmt.Sprintf("no stored MASTER description for %s on %s/%s", target, src.central, src.interfaceID))
		}
		return refused(fmt.Sprintf("stored MASTER description of %s unreadable: %v", target, err))
	}
	if !sameDescription(src.description, rec.Paramset) {
		return refused("MASTER description differs from the source channel's")
	}
	if _, err := coerceAgainstDescriptions(rec.Paramset, values); err != nil {
		return refused(err.Error())
	}
	// Run the visibility gate in the dry run too, so a dry run predicts
	// exactly what the live write will do — the write path checks it
	// again, but a dry run that skips it would answer would_apply for a
	// value the write then refuses.
	if err := p.paramsets.checkVisibilityOn(src.central, target, hmenum.ParamsetKeyMaster, values); err != nil {
		return refused(err.Error())
	}
	// A modelled target is written through Channel.SetMany, which accepts
	// only parameters the model holds a MASTER data point for — a subset
	// of the stored description (profile-owned slots and filtered
	// parameters are deliberately absent). Predict that gate here, so the
	// dry run never promises a write SetMany would refuse.
	if ch := p.paramsets.resolveChannelOn(src.central, target); ch != nil {
		for name := range values {
			if channelParameterFor(ch, hmenum.ParamsetKeyMaster, hmenum.Parameter(name)) == nil {
				return refused(fmt.Sprintf("parameter %s is not writable on this channel's model paramset", name))
			}
		}
	}
	if dryRun {
		return interfaces.ParamsetApplyOutcome{Address: target, Status: interfaces.ApplyWouldApply}
	}
	report, err := p.paramsets.PutParamsetOn(ctx, src.central, target, hmenum.ParamsetKeyMaster, values)
	if err != nil {
		if isWriteRejection(err) {
			return refused(err.Error())
		}
		return interfaces.ParamsetApplyOutcome{Address: target, Status: interfaces.ApplyFailed, Reason: err.Error()}
	}
	return interfaces.ParamsetApplyOutcome{Address: target, Status: interfaces.ApplyApplied, Result: report}
}

// isWriteRejection reports whether a write error is a pre-write refusal of
// the request — nothing reached the CCU — rather than an upstream failure.
// The set matches the rejections the REST write surface answers 400 for.
func isWriteRejection(err error) bool {
	return errors.Is(err, hmerr.ErrValidation) ||
		errors.Is(err, hmerr.ErrParameterHidden) ||
		errors.Is(err, device.ErrValidation) ||
		errors.Is(err, device.ErrUnknownParameter) ||
		errors.Is(err, device.ErrParameterNotWritable) ||
		errors.Is(err, device.ErrChannelOperationLocked)
}

// sameDescription is the eligibility gate: deep equality of two parsed stored
// descriptions, parameter by parameter and field by field, including the raw
// MIN/MAX/DEFAULT/SPECIAL bytes. Both sides are decoded by the same store
// from the same wire encoding, so equal wire descriptions compare equal.
func sameDescription(a, b hmproto.Paramset) bool {
	return reflect.DeepEqual(a, b)
}

// resolveSource finds the central and interface owning the source channel and
// loads its stored MASTER description. The owner is the first central, in
// name-sorted registry order, whose model holds the source's device — the same
// resolution the single-channel write path uses.
func (p *ParamsetApplyDomain) resolveSource(ctx context.Context, sourceChannel string) (applySource, error) {
	if p.registry == nil {
		return applySource{}, fmt.Errorf("%w: %s", hmerr.ErrDescriptionNotFound, sourceChannel)
	}
	devAddr := deviceAddressOf(sourceChannel)
	for _, u := range p.registry.List() {
		dev, ok := u.ModelRegistry.Get(devAddr)
		if !ok {
			continue
		}
		rec, err := p.descriptions.Get(ctx, u.Name(), dev.InterfaceID, sourceChannel, hmenum.ParamsetKeyMaster)
		if errors.Is(err, sqlite.ErrParamsetNotFound) {
			return applySource{}, fmt.Errorf("%w: no stored MASTER description for %s", hmerr.ErrDescriptionNotFound, sourceChannel)
		}
		if err != nil {
			return applySource{}, fmt.Errorf("paramset apply: load MASTER description of %s: %w", sourceChannel, err)
		}
		return applySource{central: u.Name(), interfaceID: dev.InterfaceID, description: rec.Paramset}, nil
	}
	return applySource{}, fmt.Errorf("%w: channel %s is not modelled on any central", hmerr.ErrDescriptionNotFound, sourceChannel)
}

func (p *ParamsetApplyDomain) unit(centralName string) *central.Unit {
	if p.registry == nil {
		return nil
	}
	u, ok := p.registry.Get(centralName)
	if !ok {
		return nil
	}
	return u
}

// applyTargetFor projects a stored record onto the target DTO, enriched from
// the model where it holds the channel. A channel the model does not hold is
// still eligible — eligibility is the stored description — and is listed by
// address alone.
func applyTargetFor(u *central.Unit, rec sqlite.ParamsetRecord) interfaces.ParamsetApplyTarget {
	t := interfaces.ParamsetApplyTarget{Address: rec.ChannelAddress, InterfaceID: rec.InterfaceID}
	if u == nil {
		return t
	}
	dev, ok := u.ModelRegistry.Get(deviceAddressOf(rec.ChannelAddress))
	if !ok || dev == nil {
		return t
	}
	t.DeviceAddress = dev.Address
	t.DeviceName = dev.Name()
	t.DeviceModel = dev.Model
	if ch := dev.Channel(rec.ChannelAddress); ch != nil {
		t.Name = ch.Name()
	}
	return t
}
