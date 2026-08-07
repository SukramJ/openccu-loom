// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/parameter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// VisibilityGate decides whether a parameter is visible and therefore
// writable. A nil gate is a no-op (all parameters are allowed).
//
// The concrete *visibility.Registry from internal/store/visibility
// satisfies this interface. It is defined here so that the adapter
// layer does not have to import the full visibility package at every
// call site; callers can pass a *visibility.Registry directly.
type VisibilityGate interface {
	// IsAllowed is the wildcard-channel variant (channel number unknown).
	// Callers that know the concrete channel number should use
	// [VisibilityGate.IsAllowedForChannel] for more precise MASTER
	// channel-whitelist filtering.
	IsAllowed(model, channelType string, paramset hmenum.ParamsetKey, p hmenum.Parameter) bool

	// IsAllowedForChannel is like [IsAllowed] but accepts the concrete
	// channel number so the MASTER channel-whitelist gating can make a
	// precise decision. Callers that have the channel number available
	// MUST use this method instead of [IsAllowed].
	IsAllowedForChannel(model, channelType string, channelNo int, paramset hmenum.ParamsetKey, p hmenum.Parameter) bool
}

// ParamsetsDomain implements handlers.ParamsetService by routing
// through the client layer's ValueWriter: the registered backend
// answers `GetParamset` / `PutParamset`.
//
// When a VisibilityGate is configured via [SetVisibilityGate], every
// write (PutParamset, PutLinkParamset) checks each parameter name
// against the gate before forwarding the request. Hidden parameters
// cause the entire write to be rejected with [hmerr.ErrParameterHidden].
type ParamsetsDomain struct {
	registry *central.Registry
	writer   *client.ValueWriter
	audit    audit.Recorder
	gate     VisibilityGate
}

// NewParamsetsDomain constructs the adapter.
func NewParamsetsDomain(r *central.Registry, w *client.ValueWriter) *ParamsetsDomain {
	return &ParamsetsDomain{registry: r, writer: w, audit: audit.NoopRecorder()}
}

// SetAuditRecorder rewires the audit recorder. Returns the receiver
// so call sites can chain (`adapter.NewParamsetsDomain(...).SetAuditRecorder(rec)`).
func (p *ParamsetsDomain) SetAuditRecorder(rec audit.Recorder) *ParamsetsDomain {
	if rec == nil {
		rec = audit.NoopRecorder()
	}
	p.audit = rec
	return p
}

// SetVisibilityGate wires a gate that is consulted on every write.
// Nil disables the gate (no-op — all parameters are allowed). Returns
// the receiver for call-site chaining.
func (p *ParamsetsDomain) SetVisibilityGate(g VisibilityGate) *ParamsetsDomain {
	p.gate = g
	return p
}

// ErrNoParamsetBackend is returned when the owning central has no
// registered backend for the device's interface.
var ErrNoParamsetBackend = errors.New("paramsets: no backend for device")

// GetParamset implements handlers.ParamsetService.
//
// For VALUES the channel event stream keeps data points fresh; we
// return the cached snapshot immediately. For MASTER the snapshot is
// seeded at hydration time and may be stale — if the cache is empty
// we trigger [device.Channel.Refresh] first so the caller sees current
// CCU state. When the channel is not found in the model registry (e.g.
// diagnostic tooling on an unknown address) the call falls through to
// the direct backend round-trip.
func (p *ParamsetsDomain) GetParamset(ctx context.Context, deviceAddress string, key hmenum.ParamsetKey) (map[string]any, error) {
	ch := p.resolveChannel(deviceAddress)
	if ch != nil {
		snapshot := ch.GetAll(key)
		if len(snapshot) > 0 {
			// Channel has observed values — return the cached view.
			return paramValueMapToAny(snapshot), nil
		}
		// Empty snapshot: either MASTER was never seeded (CUxD / failed
		// hydration) or this is the first read. Trigger a live fetch and
		// push the values into the data points before returning.
		if err := ch.Refresh(ctx, key); err == nil {
			snapshot = ch.GetAll(key)
			if len(snapshot) > 0 {
				return paramValueMapToAny(snapshot), nil
			}
		}
		// Refresh failed or returned nothing — fall through to the
		// backend direct call so the caller still gets a result.
	}
	b, err := p.resolve(deviceAddress)
	if err != nil {
		return nil, err
	}
	return b.GetParamset(ctx, deviceAddress, key)
}

// PutParamset implements handlers.ParamsetService.
//
// The write is routed through [device.Channel.SetMany] so the model is
// the single source of truth for every write. The VisibilityGate (F1)
// runs BEFORE the channel is touched as defense-in-depth — a hidden
// parameter rejects the entire write. On success the adapter re-fetches
// the authoritative values and pushes them back through the channel's
// data points so the SPA always sees post-write state.
//
// LINK paramsets are NOT routed here; they use the legacy backend path
// in [PutLinkParamset]. Adding a LinkPair aggregate to the model is a
// Phase-A.3 follow-up.
//
// When the channel is not found in the model registry (diagnostic
// tooling, unknown address), the call falls through to a direct backend
// round-trip.
func (p *ParamsetsDomain) PutParamset(ctx context.Context, deviceAddress string, key hmenum.ParamsetKey, values map[string]any) error {
	// VisibilityGate runs first — before the channel is touched.
	if err := p.checkVisibility(deviceAddress, key, values); err != nil {
		return err
	}
	b, err := p.resolve(deviceAddress)
	if err != nil {
		return err
	}
	// Capture before-state for the audit log (best-effort).
	before, _ := b.GetParamset(ctx, deviceAddress, key)

	ch := p.resolveChannel(deviceAddress)
	if ch != nil {
		// Route through the model: Channel.SetMany validates + dispatches.
		paramValues, convErr := anyMapToParamValues(values)
		if convErr != nil {
			return fmt.Errorf("paramsets: convert values: %w", convErr)
		}
		opts := device.SetOptions{
			Validate:   true,
			Optimistic: true,
			Priority:   hmenum.CommandPriorityHigh,
			Source:     "rest:paramset.put",
		}
		if err := ch.SetMany(ctx, key, paramValues, opts); err != nil {
			return err
		}
	} else {
		// No channel in model — validate min/max before the direct backend call
		// so invalid values are rejected early with a clear error instead of a
		// CCU-side XML-RPC fault.
		if validErr := validateParamsetValues(ctx, b, deviceAddress, key, values); validErr != nil {
			return validErr
		}
		if err := b.PutParamset(ctx, deviceAddress, key, values, hmenum.CommandRxModeUnset); err != nil {
			return err
		}
	}
	p.refreshAfterPut(ctx, b, deviceAddress, key)
	p.recordParamsetWrite(deviceAddress, string(key), before, values)
	return nil
}

// auditChanges renders a paramset write as audit rows, recording the
// name of every written parameter and the value of every parameter whose
// value is not itself a credential.
//
// The values matter: an audit row that says only "CODE_ID was written"
// answers far less than "the heating curve went from 21 to 24". But a
// write payload can carry a secret — CODE_ID is the access code of a
// keypad or lock channel — and the audit log is append-only with a
// 90-day retention, so persisting that value hands the code to every
// operator dump. The sibling data-point write path already recorded
// names only for exactly this reason; this keeps the useful half.
func auditChanges(before, after map[string]any) []audit.Change {
	changes := make([]audit.Change, 0, len(after))
	for name, v := range after {
		change := audit.Change{Parameter: name}
		if hmenum.IsSecretBearingParameter(hmenum.Parameter(name)) {
			change.Before = auditRedacted(before[name])
			change.After = auditRedactedMask
		} else {
			change.Before = before[name]
			change.After = v
		}
		changes = append(changes, change)
	}
	return changes
}

// auditRedactedMask replaces a credential value in an audit row. It is a
// constant string rather than nil so a reader can tell "the value was
// withheld" from "there was no previous value".
const auditRedactedMask = "***"

// auditRedacted masks a previous value, preserving the nil case so an
// initial write still reads as "had no value before".
func auditRedacted(before any) any {
	if before == nil {
		return nil
	}
	return auditRedactedMask
}

func (p *ParamsetsDomain) recordParamsetWrite(channelAddress, paramset string, before, after map[string]any) {
	if p.audit == nil {
		return
	}
	changes := auditChanges(before, after)
	p.audit.Record(audit.Entry{
		Action:        audit.ActionParamsetWrite,
		DeviceAddress: deviceAddressOf(channelAddress),
		ChannelNo:     channelNumberOf(channelAddress),
		Paramset:      paramset,
		Changes:       changes,
	})
}

// validateParamsetValues fetches the paramset descriptor from the backend and
// checks every supplied value against its min/max range. Any parameter that
// fails validation is collected into a combined error so the caller receives
// the full rejection list in one shot.
//
// The function is a best-effort guard: if the descriptor lookup fails (e.g.
// the CCU is temporarily unreachable) the values pass through unchanged so
// the backend error path handles the failure instead.
func validateParamsetValues(ctx context.Context, b interface {
	GetParamsetDescription(ctx context.Context, address string, key hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error)
}, address string, key hmenum.ParamsetKey, values map[string]any,
) error {
	descs, err := b.GetParamsetDescription(ctx, address, key)
	if err != nil {
		// Descriptor unavailable — skip validation so the backend error path handles the failure.
		return nil //nolint:nilerr // intentional soft-path: descriptor fetch failure is non-fatal
	}
	var errs []error
	for name, rawVal := range values {
		desc, ok := descs[name]
		if !ok {
			continue
		}
		pv, convErr := hmtypes.NewParamValue(rawVal)
		if convErr != nil {
			errs = append(errs, fmt.Errorf("paramsets: %s: %w", name, convErr))
			continue
		}
		if valErr := parameter.Validate(desc, pv); valErr != nil {
			errs = append(errs, fmt.Errorf("paramsets: %s: %w", name, valErr))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %w", hmerr.ErrValidation, errors.Join(errs...))
}

// channelNumberOf parses "DEV:N" → N. Returns 0 when the channel is
// not numeric (e.g. the device level).
func channelNumberOf(channelAddress string) int {
	for i := len(channelAddress) - 1; i >= 0; i-- {
		if channelAddress[i] == ':' {
			n := 0
			for j := i + 1; j < len(channelAddress); j++ {
				c := channelAddress[j]
				if c < '0' || c > '9' {
					return 0
				}
				n = n*10 + int(c-'0')
			}
			return n
		}
	}
	return 0
}

// refreshAfterPut pulls the current paramset values and, when the
// owning channel still holds the data points, forwards them through
// [OnWireValue]. Best effort — a transient read failure here is
// silently ignored because the write itself already succeeded.
func (p *ParamsetsDomain) refreshAfterPut(
	ctx context.Context,
	b paramsetBackend,
	channelAddress string,
	key hmenum.ParamsetKey,
) {
	if p.registry == nil {
		return
	}
	current, err := b.GetParamset(ctx, channelAddress, key)
	if err != nil {
		return
	}
	deviceAddr := deviceAddressOf(channelAddress)
	for _, u := range p.registry.List() {
		dev, ok := u.ModelRegistry.Get(deviceAddr)
		if !ok {
			continue
		}
		ch := dev.Channel(channelAddress)
		if ch == nil {
			continue
		}
		for name, v := range current {
			dp := ch.ParamsetParameter(key, hmenum.Parameter(name))
			if dp == nil {
				continue
			}
			if setter, ok := dp.(interface{ OnWireValue(any) bool }); ok {
				setter.OnWireValue(v)
			}
		}
		return
	}
}

// GetLinkParamset is the LINK equivalent of [GetParamset]. Unlike
// VALUES / MASTER, the CCU identifies LINK paramsets by the peer
// channel address rather than a fixed key string, so the call takes
// the peer explicitly.
func (p *ParamsetsDomain) GetLinkParamset(ctx context.Context, channelAddress, peerAddress string) (map[string]any, error) {
	b, err := p.resolve(channelAddress)
	if err != nil {
		return nil, err
	}
	return b.GetLinkParamset(ctx, channelAddress, peerAddress)
}

// GetLinkFormSchema implements ws.LinkFormSchemaProvider.
//
// Fetches the LINK paramset descriptor for (receiverChannelAddr, senderChannelAddr)
// from the backend and converts each parameter entry to a plain map[string]any
// via JSON round-trip so the WS layer can forward it to the SPA without
// importing hmproto. Mirrors the raw-descriptor path that
// UISchemaAdapter.buildLinkSchema uses but returns the undecorated
// wire shape instead of the enriched UISchema.
func (p *ParamsetsDomain) GetLinkFormSchema(
	ctx context.Context,
	_ string, // interfaceID — reserved; receiver is resolved from the registry
	receiverChannelAddr, senderChannelAddr string,
) (map[string]any, error) {
	b, err := p.resolve(receiverChannelAddr)
	if err != nil {
		return nil, err
	}
	desc, err := b.GetLinkParamsetDescription(ctx, receiverChannelAddr, senderChannelAddr)
	if err != nil {
		return nil, fmt.Errorf("paramsets: link form schema: %w", err)
	}
	// Convert map[string]hmproto.ParameterData → map[string]any via JSON.
	raw, err := json.Marshal(desc)
	if err != nil {
		return nil, fmt.Errorf("paramsets: link form schema encode: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("paramsets: link form schema decode: %w", err)
	}
	return out, nil
}

// PutLinkParamset writes a LINK paramset atomically.
//
// Like [PutParamset] we re-fetch after the write so data points (if
// any) see the authoritative state. Unlike MASTER/VALUES, LINK is
// not cached on the channel, so the refresh only keeps downstream
// re-reads consistent.
//
// When a VisibilityGate is configured, each parameter is checked
// against it before the write is forwarded.
func (p *ParamsetsDomain) PutLinkParamset(
	ctx context.Context, channelAddress, peerAddress string, values map[string]any,
) error {
	if err := p.checkVisibility(channelAddress, hmenum.ParamsetKeyLink, values); err != nil {
		return err
	}
	b, err := p.resolve(channelAddress)
	if err != nil {
		return err
	}
	before, _ := b.GetLinkParamset(ctx, channelAddress, peerAddress)
	if err := b.PutLinkParamset(ctx, channelAddress, peerAddress, values); err != nil {
		return err
	}
	// Touch the LINK paramset once to flush any caches the CCU may
	// be keeping; best effort.
	_, _ = b.GetLinkParamset(ctx, channelAddress, peerAddress)
	if p.audit != nil {
		changes := auditChanges(before, values)
		p.audit.Record(audit.Entry{
			Action:        audit.ActionLinkParamsetWrite,
			DeviceAddress: deviceAddressOf(channelAddress),
			ChannelNo:     channelNumberOf(channelAddress),
			Peer:          peerAddress,
			Changes:       changes,
		})
	}
	return nil
}

// checkVisibility consults the VisibilityGate (if configured) against
// every parameter name in values. Returns [hmerr.ErrParameterHidden]
// on the first hidden parameter found.
//
// When the gate is nil this method is a no-op and returns nil. When
// the device cannot be found in the registry, the check is skipped
// (lenient: unknown devices are not gated so that diagnostic tooling
// can still write arbitrary paramsets).
//
// The channel number is extracted from channelAddress (e.g. "DEV:3" → 3)
// and forwarded to [VisibilityGate.IsAllowedForChannel] for precise MASTER
// channel-whitelist filtering.
func (p *ParamsetsDomain) checkVisibility(channelAddress string, key hmenum.ParamsetKey, values map[string]any) error {
	if p.gate == nil || len(values) == 0 {
		return nil
	}
	model, channelType := p.resolveChannelInfo(channelAddress)
	// When the device cannot be found, skip the gate check. Diagnostic
	// tooling may write to addresses that are not yet in the model
	// registry; blocking them here would break initialisation sequences.
	if model == "" {
		return nil
	}
	channelNo := channelNumberOf(channelAddress)
	for name := range values {
		if !p.gate.IsAllowedForChannel(model, channelType, channelNo, key, hmenum.Parameter(name)) {
			return fmt.Errorf("parameter %q: %w", name, hmerr.ErrParameterHidden)
		}
	}
	return nil
}

// resolveChannelInfo returns the device model string and channel type
// string for the given channel address by walking the central registry.
// Returns ("", "") when the channel cannot be found.
func (p *ParamsetsDomain) resolveChannelInfo(channelAddress string) (model, channelType string) {
	if p.registry == nil {
		return "", ""
	}
	devAddr := deviceAddressOf(channelAddress)
	for _, u := range p.registry.List() {
		dev, ok := u.ModelRegistry.Get(devAddr)
		if !ok {
			continue
		}
		ch := dev.Channel(channelAddress)
		if ch == nil {
			// Device found but no channel — use device model with empty type.
			return dev.Model, ""
		}
		return dev.Model, ch.Type
	}
	return "", ""
}

// resolve maps a device address to the owning central's backend.
// Walks the registry so multi-CCU setups work out of the box.
func (p *ParamsetsDomain) resolve(deviceOrChannel string) (paramsetBackend, error) {
	if p.registry == nil || p.writer == nil {
		return nil, ErrNoParamsetBackend
	}
	addr := deviceAddressOf(deviceOrChannel)
	for _, u := range p.registry.List() {
		dev, ok := u.ModelRegistry.Get(addr)
		if !ok {
			continue
		}
		b, ok := p.writer.Backend(u.Name(), dev.InterfaceID)
		if !ok {
			return nil, fmt.Errorf("%w: %s/%s", ErrNoParamsetBackend, u.Name(), dev.InterfaceID)
		}
		return b, nil
	}
	return nil, fmt.Errorf("%w: device %s", ErrNoParamsetBackend, addr)
}

// resolveChannel walks the registry and returns the [device.Channel]
// for channelAddress, or nil when not found. The lookup is intentionally
// lenient — unknown addresses fall through to the backend path.
func (p *ParamsetsDomain) resolveChannel(channelAddress string) *device.Channel {
	if p.registry == nil {
		return nil
	}
	devAddr := deviceAddressOf(channelAddress)
	for _, u := range p.registry.List() {
		dev, ok := u.ModelRegistry.Get(devAddr)
		if !ok {
			continue
		}
		return dev.Channel(channelAddress)
	}
	return nil
}

// anyMapToParamValues converts a map[string]any (wire format from JSON /
// backend) into the typed map required by [device.Channel.SetMany].
// Returns an error on the first value that [hmtypes.NewParamValue]
// cannot represent.
func anyMapToParamValues(values map[string]any) (map[hmenum.Parameter]hmtypes.ParamValue, error) {
	out := make(map[hmenum.Parameter]hmtypes.ParamValue, len(values))
	for name, v := range values {
		pv, err := hmtypes.NewParamValue(v)
		if err != nil {
			return nil, fmt.Errorf("parameter %q: %w", name, err)
		}
		out[hmenum.Parameter(name)] = pv
	}
	return out, nil
}

// paramValueMapToAny converts a [device.Channel.GetAll] result back
// to map[string]any for callers that expect the wire format.
func paramValueMapToAny(values map[hmenum.Parameter]hmtypes.ParamValue) map[string]any {
	out := make(map[string]any, len(values))
	for p, v := range values {
		out[string(p)] = v.Unwrap()
	}
	return out
}

// paramsetBackend is the minimal backend slice the adapter needs.
// Decoupled from backends.Operations so tests can inject small fakes.
type paramsetBackend interface {
	GetParamset(ctx context.Context, address string, key hmenum.ParamsetKey) (map[string]any, error)
	PutParamset(ctx context.Context, address string, key hmenum.ParamsetKey, values map[string]any, rxMode hmenum.CommandRxMode) error
	GetParamsetDescription(ctx context.Context, address string, key hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error)
	GetLinkParamset(ctx context.Context, channelAddress, peerAddress string) (map[string]any, error)
	PutLinkParamset(ctx context.Context, channelAddress, peerAddress string, values map[string]any) error
	GetLinkParamsetDescription(ctx context.Context, channelAddress, peerAddress string) (map[string]hmproto.ParameterData, error)
}
