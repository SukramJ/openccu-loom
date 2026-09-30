// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/parameter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// configRepairAuditNote labels the audit row of a repair rewrite so the
// change-log tells it apart from an operator's own paramset edit.
const configRepairAuditNote = "config repair: full rewrite of the stored MASTER configuration"

// ConfigRepairDomain implements [interfaces.DeviceConfigRepairService]:
// rebuilding a device's stored MASTER configuration from the channel's own
// live paramset description.
//
// Everything is read live from the backend — the description and the stored
// paramset — because the point of the repair is to act on what the interface
// process holds now, not on what the daemon cached at boot. The rewrite goes
// straight to the backend as one full putParamset per channel instead of
// through [device.Channel.SetMany]: the model may leave out parameters the
// description carries (week-profile slots, parameters without read access),
// and a partial write would leave exactly those invalid values in place.
//
// The repair deliberately bypasses the visibility gate and the edit locks.
// It is admin maintenance that rewrites a channel's own stored configuration
// with the values that store already holds, corrected only where the
// description refuses them; it discloses nothing beyond the corrections it
// reports, and holding a lock against it would only block the recovery of a
// channel whose store is already rejecting ordinary edits.
type ConfigRepairDomain struct {
	registry  *central.Registry
	paramsets *ParamsetsDomain
	audit     audit.Recorder
}

// NewConfigRepairDomain constructs the adapter. rec may be nil, in which
// case rewrites are not recorded in the change-log.
func NewConfigRepairDomain(r *central.Registry, w *client.ValueWriter, rec audit.Recorder) *ConfigRepairDomain {
	// The embedded paramsets domain is used for backend resolution and the
	// post-write model refresh only; it carries neither a gate nor an audit
	// recorder, so nothing is checked or recorded twice.
	return &ConfigRepairDomain{registry: r, paramsets: NewParamsetsDomain(r, w), audit: rec}
}

var _ interfaces.DeviceConfigRepairService = (*ConfigRepairDomain)(nil)

// RepairDeviceConfig implements [interfaces.DeviceConfigRepairService]. An
// address no central models answers [hmerr.ErrDescriptionNotFound]; every
// other problem is a per-channel outcome.
func (c *ConfigRepairDomain) RepairDeviceConfig(
	ctx context.Context, deviceAddress string, channels []string, dryRun bool,
) ([]interfaces.ConfigRepairOutcome, error) {
	u, dev := c.findDevice(deviceAddress)
	if dev == nil {
		return nil, fmt.Errorf("%w: device %s is not modelled on any central", hmerr.ErrDescriptionNotFound, deviceAddress)
	}
	targets := channels
	if len(targets) == 0 {
		targets = masterBearingChannels(u, dev)
	}
	out := make([]interfaces.ConfigRepairOutcome, 0, len(targets))
	for _, ch := range targets {
		out = append(out, c.repairChannel(ctx, u.Name(), dev, ch, dryRun))
	}
	return out, nil
}

// findDevice returns the first central, in name-sorted registry order, whose
// model holds the device — the resolution every unscoped paramset path uses.
func (c *ConfigRepairDomain) findDevice(deviceAddress string) (*central.Unit, *device.Device) {
	if c.registry == nil {
		return nil, nil
	}
	for _, u := range c.registry.List() {
		if dev, ok := u.ModelRegistry.Get(deviceAddress); ok && dev != nil {
			return u, dev
		}
	}
	return nil, nil
}

// masterBearingChannels lists the channels of dev that carry a MASTER
// paramset, device level first.
//
// The device level is included when the model holds the device-root
// pseudo-channel: the ingest pipeline creates it only after the device
// address answered a non-empty MASTER description. A real channel is included
// when its cached device description lists MASTER in PARAMSETS; a channel
// without a cached description falls back to whether the model holds any
// MASTER data point for it.
func masterBearingChannels(u *central.Unit, dev *device.Device) []string {
	var out []string
	if dev.RootChannel() != nil {
		out = append(out, dev.Address)
	}
	iface := hmtypes.ParseWireInterfaceID(dev.InterfaceID)
	for _, ch := range dev.Channels() {
		if ch.Number == device.ChannelNumberDevice {
			continue
		}
		if u != nil && u.DescRegistry != nil {
			if desc, ok := u.DescRegistry.Get(iface, ch.Address); ok {
				if slices.Contains(desc.Paramsets, string(hmenum.ParamsetKeyMaster)) {
					out = append(out, ch.Address)
				}
				continue
			}
		}
		if ch.MasterLen() > 0 {
			out = append(out, ch.Address)
		}
	}
	return out
}

// belongsToDevice reports whether address is the device itself or one of
// its modelled channels.
func belongsToDevice(dev *device.Device, address string) bool {
	if address == dev.Address {
		return true
	}
	return deviceAddressOf(address) == dev.Address && dev.Channel(address) != nil
}

// repairChannel runs the reads, the plan and — outside a dry run — the write
// for one channel.
func (c *ConfigRepairDomain) repairChannel(
	ctx context.Context, centralName string, dev *device.Device, address string, dryRun bool,
) interfaces.ConfigRepairOutcome {
	failed := func(msg string) interfaces.ConfigRepairOutcome {
		return interfaces.ConfigRepairOutcome{
			Channel: address, Status: interfaces.RepairFailed, Error: msg,
			Corrections: []interfaces.ConfigRepairCorrection{}, Foreign: []string{},
		}
	}
	if !belongsToDevice(dev, address) {
		return failed(fmt.Sprintf("channel %s does not belong to device %s", address, dev.Address))
	}
	b, err := c.paramsets.resolveOn(centralName, address)
	if err != nil {
		return failed(err.Error())
	}
	descs, err := b.GetParamsetDescription(ctx, address, hmenum.ParamsetKeyMaster)
	if err != nil {
		return failed(fmt.Sprintf("read MASTER description: %v", err))
	}
	stored, err := b.GetParamset(ctx, address, hmenum.ParamsetKeyMaster)
	if err != nil {
		return failed(fmt.Sprintf("read stored MASTER paramset: %v", err))
	}

	plan := planConfigRepair(descs, stored)
	outcome := interfaces.ConfigRepairOutcome{
		Channel:     address,
		Corrections: plan.corrections,
		Foreign:     plan.foreign,
	}
	hasForeign := len(plan.foreign) > 0
	switch {
	case len(plan.corrections) == 0 && !hasForeign:
		// A full rewrite costs radio airtime on every channel it touches;
		// a store that already matches its description is left alone.
		outcome.Status = interfaces.RepairClean
		return outcome
	case dryRun:
		outcome.Status = interfaces.RepairWouldRepair
		if hasForeign {
			outcome.Status = interfaces.RepairForeignParameters
		}
		return outcome
	}
	if len(plan.write) == 0 {
		// Nothing usable to send: an empty putParamset changes nothing, and
		// no paramset write can remove foreign entries.
		if hasForeign {
			outcome.Status = interfaces.RepairForeignParameters
			return outcome
		}
		outcome.Status = interfaces.RepairFailed
		outcome.Error = "no stored parameter has a usable value to write"
		return outcome
	}

	if err := b.PutParamset(ctx, address, hmenum.ParamsetKeyMaster, plan.write,
		hmenum.CommandPriorityHigh, hmenum.CommandRxModeUnset); err != nil {
		// A store holding foreign entries may keep rejecting every write;
		// that fault is the expected answer, reported with the foreign list
		// rather than as a plain failure.
		outcome.Status = interfaces.RepairFailed
		if hasForeign {
			outcome.Status = interfaces.RepairForeignParameters
		}
		outcome.Error = err.Error()
		return outcome
	}
	readBack, readErr := b.GetParamset(ctx, address, hmenum.ParamsetKeyMaster)
	if readErr == nil {
		c.paramsets.applyStoredValuesOn(centralName, address, hmenum.ParamsetKeyMaster, readBack)
	}
	outcome.Result = paramsetWriteReport(plan.write, descs, readBack, readErr)
	outcome.Status = interfaces.RepairRepaired
	if hasForeign {
		outcome.Status = interfaces.RepairForeignParameters
	}
	c.recordRepair(address, stored, plan.write)
	return outcome
}

// recordRepair appends one change-log row for a written channel.
func (c *ConfigRepairDomain) recordRepair(address string, before, after map[string]any) {
	if c.audit == nil {
		return
	}
	_, channelNo, _ := hmtypes.SplitChannelAddress(address)
	c.audit.Record(audit.Entry{
		Action:        audit.ActionParamsetWrite,
		DeviceAddress: deviceAddressOf(address),
		ChannelNo:     channelNo,
		Paramset:      string(hmenum.ParamsetKeyMaster),
		Changes:       auditChanges(before, after),
		Note:          configRepairAuditNote,
	})
}

// configRepairPlan is the outcome of comparing a stored paramset against its
// description: the full paramset to write, the corrections it carries and
// the stored names the description does not know.
type configRepairPlan struct {
	write       map[string]any
	corrections []interfaces.ConfigRepairCorrection
	foreign     []string
}

// planConfigRepair builds the rewrite of one channel. The write set is every
// parameter present in both the description and the store that the
// description marks writable; read-only entries are never sent, whatever
// they hold. Corrections and foreign names are sorted by parameter name.
func planConfigRepair(descs map[string]hmproto.ParameterData, stored map[string]any) configRepairPlan {
	plan := configRepairPlan{
		write:       map[string]any{},
		corrections: []interfaces.ConfigRepairCorrection{},
		foreign:     []string{},
	}
	names := make([]string, 0, len(stored))
	for name := range stored {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		raw := stored[name]
		desc, ok := descs[name]
		if !ok {
			plan.foreign = append(plan.foreign, name)
			continue
		}
		if !desc.IsWritable() {
			continue
		}
		value, keep, reason := repairValue(desc, raw)
		if reason != "" {
			var corrected any
			if keep {
				corrected = value
			}
			plan.corrections = append(plan.corrections, interfaces.ConfigRepairCorrection{
				Parameter: name, Stored: raw, Corrected: corrected, Reason: reason,
			})
		}
		if keep {
			plan.write[name] = value
		}
	}
	return plan
}

// repairValue decides what the rewrite sends for one stored value. keep is
// false when the parameter has to be left out of the write; reason is empty
// when the stored value is sent unchanged.
//
// The order is: a value that coerces into the descriptor's type and
// validates is kept (a representation change such as a numeric string is
// reported); a numeric value outside MIN/MAX is clamped to the bound it
// violates — a declared SPECIAL value validates and is never clamped; any
// other unusable value falls back to the description's DEFAULT, and is
// dropped when there is no valid DEFAULT.
func repairValue(desc hmproto.ParameterData, raw any) (value any, keep bool, reason string) {
	pv, err := coerceIgnoringRange(desc, raw)
	if err == nil {
		valErr := parameter.Validate(desc, pv)
		if valErr == nil {
			if !storedTypeMatches(desc.Type, raw) {
				return pv.Unwrap(), true, fmt.Sprintf("stored as %T; rewritten as the %s type", raw, desc.Type)
			}
			return pv.Unwrap(), true, ""
		}
		if clamped, bound, ok := clampToRange(desc, pv); ok {
			return clamped, true, fmt.Sprintf("%v; clamped to %s", valErr, bound)
		}
		err = valErr
	}
	if def, ok := descriptionDefault(desc); ok {
		return def, true, fmt.Sprintf("stored value unusable (%v); replaced by the description DEFAULT", err)
	}
	return nil, false, fmt.Sprintf("stored value unusable (%v) and the description has no valid DEFAULT; dropped from the rewrite", err)
}

// coerceIgnoringRange converts raw into the descriptor's type without the
// MIN/MAX check [parameter.Coerce] performs for numeric types, so an
// out-of-range value arrives typed and can be clamped instead of being
// discarded as uncoercible.
func coerceIgnoringRange(desc hmproto.ParameterData, raw any) (hmtypes.ParamValue, error) {
	loose := desc
	if desc.Type == hmenum.ParameterTypeInteger || desc.Type == hmenum.ParameterTypeFloat {
		loose.Min, loose.Max = nil, nil
	}
	return parameter.Coerce(loose, raw)
}

// storedTypeMatches reports whether a stored wire value already has the Go
// shape the descriptor's type decodes to. Integral numbers of any Go numeric
// type count for INTEGER and ENUM, since transports differ in which numeric
// type they decode to.
func storedTypeMatches(t hmenum.ParameterType, raw any) bool {
	switch t {
	case hmenum.ParameterTypeBool, hmenum.ParameterTypeAction:
		_, ok := raw.(bool)
		return ok
	case hmenum.ParameterTypeInteger, hmenum.ParameterTypeEnum, hmenum.ParameterTypeFloat:
		switch raw.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, json.Number:
			return true
		}
		return false
	case hmenum.ParameterTypeString, hmenum.ParameterTypeEmpty, hmenum.ParameterTypeDummy:
		_, ok := raw.(string)
		return ok
	default:
		return false
	}
}

// clampToRange moves a numeric value that violates MIN or MAX onto the bound
// it violates and returns the clamped wire value and the bound's name. It
// reports false for non-numeric types, NaN / infinite floats, and when the
// clamped value still does not validate.
func clampToRange(desc hmproto.ParameterData, pv hmtypes.ParamValue) (value any, bound string, ok bool) {
	var v float64
	switch {
	case desc.Type == hmenum.ParameterTypeInteger && pv.Kind == hmtypes.ValueKindInt:
		v = float64(pv.Int)
	case desc.Type == hmenum.ParameterTypeFloat && pv.Kind == hmtypes.ValueKindFloat:
		v = pv.Float
	default:
		return nil, "", false
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil, "", false
	}
	var target float64
	if lo, has := rawNumber(desc.Min); has && v < lo {
		target, bound = lo, fmt.Sprintf("MIN %g", lo)
	} else if hi, has := rawNumber(desc.Max); has && v > hi {
		target, bound = hi, fmt.Sprintf("MAX %g", hi)
	} else {
		return nil, "", false
	}
	var clamped hmtypes.ParamValue
	if desc.Type == hmenum.ParameterTypeInteger {
		clamped = hmtypes.IntValue(int(target))
	} else {
		clamped = hmtypes.FloatValue(target)
	}
	if parameter.Validate(desc, clamped) != nil {
		return nil, "", false
	}
	return clamped.Unwrap(), bound, true
}

// descriptionDefault returns the descriptor's DEFAULT as a wire value when it
// is present, coerces and validates.
func descriptionDefault(desc hmproto.ParameterData) (any, bool) {
	if len(desc.Default) == 0 {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(desc.Default))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil || raw == nil {
		return nil, false
	}
	pv, err := parameter.Coerce(desc, raw)
	if err != nil {
		return nil, false
	}
	if parameter.Validate(desc, pv) != nil {
		return nil, false
	}
	return pv.Unwrap(), true
}

// rawNumber decodes a numeric MIN / MAX bound; absent or non-numeric bounds
// report false.
func rawNumber(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, false
	}
	f, err := n.Float64()
	if err != nil {
		return 0, false
	}
	return f, true
}
