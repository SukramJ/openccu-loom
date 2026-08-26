// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package switchdev

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Access-authorization enum labels sent on the write-only
// ACCESS_AUTHORIZATION control. The wire values are the ENUM labels the
// CCU exposes in the parameter's VALUE_LIST (DISABLE at index 0, ENABLE
// at index 1) — the string label is sent, matching the HmIP ENUM-write
// convention already used by the lock's LOCK_TARGET_LEVEL path.
const (
	accessAuthorizationDisable = "DISABLE"
	accessAuthorizationEnable  = "ENABLE"
)

// AccessPermission is a per-user access-permission switch for the
// ACCESS_RECEIVER channels of HomematicIP access-control devices
// (HmIP-DLD user channels 2-9, HmIP-FWI channels 1-8). It combines the
// read-only STATE (the current permission, a binary sensor) with the
// write-only ACCESS_AUTHORIZATION control (an ENUM action-select that
// enables/disables the permission) into a single SWITCH-category custom
// data point:
//
//   - the switch value / IsOn mirror STATE;
//   - TurnOn writes ACCESS_AUTHORIZATION = ENABLE;
//   - TurnOff writes ACCESS_AUTHORIZATION = DISABLE.
//
// ACCESS_AUTHORIZATION is globally ignored and only un-ignored on these
// devices (see internal/store/visibility rules); it is consumed hidden
// (NO_CREATE) here so it never surfaces as a bare control alongside the
// switch. This mirrors the newer PERMISSION_STATE switch of door locks
// such as the HmIP-DLP.
type AccessPermission struct {
	custom.BaseDP

	// ServiceRegistry implements the write-half of [payload.Source].
	// Service methods are registered in [NewAccessPermission].
	payload.ServiceRegistry

	Address string

	// key is the composite identifier used by [AccessPermission.DataPointKey]
	// to satisfy [device.AttachableDataPoint]. It is keyed on
	// ACCESS_AUTHORIZATION so the per-channel naming appends a distinguishing
	// ` chN` suffix — the ACCESS_RECEIVER channels otherwise carry only the
	// device name, which would render every user channel with an identical
	// name.
	key hmtypes.DataPointKey

	// stateDp is the read-only STATE binary sensor — the current permission.
	// The switch value / IsOn delegate to it; there is no STATE write path.
	stateDp *generic.BinarySensor

	// authDp is the write-only ACCESS_AUTHORIZATION action-select. It is
	// consumed hidden (NO_CREATE) and driven only through TurnOn / TurnOff.
	authDp *generic.ActionSelect
}

// NewAccessPermission constructs an AccessPermission for the given
// ACCESS_RECEIVER channel. It resolves the read-only STATE binary sensor
// and the write-only ACCESS_AUTHORIZATION action-select, forcing the
// latter to NO_CREATE so it is not exposed separately.
//
// Returns nil when the channel carries neither a STATE binary sensor nor
// the un-ignored ACCESS_AUTHORIZATION control — the materializer treats
// nil as "skip custom-DP registration on this channel".
func NewAccessPermission(ch *device.Channel, group custom.RebasedChannelGroupConfig) *AccessPermission {
	stateDp := custom.BinarySensorField(custom.ResolveSlotOr(ch, group, hmenum.FieldState, hmenum.ParameterState))
	authDp := custom.ActionSelectField(ch, hmenum.ParameterAccessAuthorization)
	if stateDp == nil || authDp == nil {
		return nil
	}

	address := ""
	var key hmtypes.DataPointKey
	if ch != nil {
		address = ch.Address
		var iface string
		if dev := ch.Device(); dev != nil {
			iface = dev.InterfaceID
		}
		key = hmtypes.DataPointKey{
			InterfaceID:    iface,
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterAccessAuthorization),
		}
	}

	ap := &AccessPermission{
		Address: address,
		key:     key,
		stateDp: stateDp,
		authDp:  authDp,
	}
	// Consume the write-only control hidden. The IPAccessPermission profile
	// carries AllowUndefinedGenericDataPoints=true (so unrelated generic DPs
	// on HmIP-FWI survive), which disables the blanket undefined-DP
	// suppression pass — without this explicit mark ACCESS_AUTHORIZATION would
	// leak as a bare control next to the switch.
	authDp.SetForcedUsage(hmenum.DataPointUsageNoCreate)
	ap.registerServices()
	return ap
}

// DataPointKey returns the composite identifier used by the materializer
// to attach this custom DP to its channel. Satisfies
// [device.AttachableDataPoint].
func (a *AccessPermission) DataPointKey() hmtypes.DataPointKey { return a.key }

// Category reports the HA data-point category — clients spawn the entity
// off this value (switch platform).
func (a *AccessPermission) Category() hmenum.DataPointCategory {
	return hmenum.DataPointCategorySwitch
}

// Value returns the current permission (STATE). The second return flags
// whether the value has been observed yet.
func (a *AccessPermission) Value() (on, observed bool) {
	if a.stateDp == nil {
		return false, false
	}
	return a.stateDp.Value()
}

// IsOn mirrors the switch accessor and returns (on, observed).
func (a *AccessPermission) IsOn() (on, observed bool) { return a.Value() }

// IsStateChange reports whether a turn-on / turn-off command materially
// changes the permission. Mirrors is_state_change: a turn-on is a change
// when the value is not already true, a turn-off when not already false;
// an unobserved value always counts as a change so the first command
// reaches the wire.
func (a *AccessPermission) IsStateChange(target bool) bool {
	cur, observed := a.Value()
	if !observed {
		return true
	}
	return cur != target
}

// TurnOn grants the user access permission by writing
// ACCESS_AUTHORIZATION = ENABLE. Gated via [IsStateChange]: when the
// permission is already granted the wire write is suppressed.
func (a *AccessPermission) TurnOn(ctx context.Context, priority hmenum.CommandPriority) error {
	if !a.IsStateChange(true) {
		return nil
	}
	return a.sendAuthorization(ctx, accessAuthorizationEnable, priority)
}

// TurnOff revokes the user access permission by writing
// ACCESS_AUTHORIZATION = DISABLE. Gated via [IsStateChange].
func (a *AccessPermission) TurnOff(ctx context.Context, priority hmenum.CommandPriority) error {
	if !a.IsStateChange(false) {
		return nil
	}
	return a.sendAuthorization(ctx, accessAuthorizationDisable, priority)
}

// sendAuthorization dispatches the ENUM label through the write-only
// ACCESS_AUTHORIZATION action-select. The label is validated against the
// parameter's VALUE_LIST and sent as the wire value (HmIP ENUM-write
// convention).
func (a *AccessPermission) sendAuthorization(ctx context.Context, label string, priority hmenum.CommandPriority) error {
	if a.authDp == nil {
		return generic.ErrNoWriter
	}
	if err := a.authDp.TriggerLabel(custom.EnsureContext(ctx), label, priority); err != nil {
		return fmt.Errorf("access_permission: send ACCESS_AUTHORIZATION=%s: %w", label, err)
	}
	return nil
}

// IsRefreshed reports whether the underlying STATE wire DP has been
// observed at least once.
func (a *AccessPermission) IsRefreshed() bool {
	if a.stateDp == nil {
		return false
	}
	return a.stateDp.IsRefreshed()
}

// SubDataPointKeys returns the wire identifier of the observable STATE
// data point. ACCESS_AUTHORIZATION is write-only and carries no state.
func (a *AccessPermission) SubDataPointKeys() []hmtypes.DataPointKey {
	if a.stateDp == nil {
		return nil
	}
	return []hmtypes.DataPointKey{a.stateDp.DataPointKey()}
}

// Subscribe wires the channel's STATE parameter into the custom DP so
// CCU pushes feed through the EventBridge's publishCustomDPState path.
// The OnAnyUpdate hook is a no-op body — the accessors read the wire DP
// directly — it only needs to exist so the channel records a
// registration the bridge can re-fire on every wire-side change.
// Implements [device.SubscribingDataPoint].
func (a *AccessPermission) Subscribe(ch *device.Channel) func() {
	if ch == nil || a.stateDp == nil {
		return func() {}
	}
	return a.stateDp.OnAnyUpdate(func(_, _ any) {})
}

// Info returns identity-level fields for the switch.
func (a *AccessPermission) Info() payload.InfoPayload {
	if a == nil {
		return nil
	}
	return &payload.SwitchInfo{
		Address:  a.Address,
		Key:      a.key.String(),
		Category: "switch",
	}
}

// Config returns the static switch configuration.
func (a *AccessPermission) Config() payload.ConfigPayload {
	if a == nil {
		return nil
	}
	return &payload.SwitchConfig{Category: "switch"}
}

// State returns the live permission state.
func (a *AccessPermission) State() payload.StatePayload {
	if a == nil {
		return nil
	}
	st := &payload.SwitchState{}
	if on, ok := a.Value(); ok {
		st.IsOn = &on
	}
	return st
}

// serviceAccessPermission is the service method that carries the
// grant/revoke command Home Assistant multiplexes onto a switch entity's
// single `command_topic`.
//
// It cannot be a wire-parameter topic: the writable slot is
// ACCESS_AUTHORIZATION, a write-only ENUM this custom DP consumes hidden
// (no_create), and the readable slot STATE takes no writes at all.
const serviceAccessPermission = "set_permission"

// argAccessPermission is the scalar-argument key
// [serviceAccessPermission] expects. The MQTT bridge wraps a bare
// payload under it before the invoke reaches the handler.
const argAccessPermission = "granted"

// registerServices registers the turn_on / turn_off service methods on
// top of the embedded ServiceRegistry.
func (a *AccessPermission) registerServices() {
	a.RegisterService("turn_on", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return a.TurnOn(ctx, priority)
	})
	a.RegisterService("turn_off", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return a.TurnOff(ctx, priority)
	})
	a.RegisterServiceWithArg(serviceAccessPermission, argAccessPermission,
		func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
			granted, err := payload.ParamBool(params, argAccessPermission)
			if err != nil {
				return err
			}
			if granted {
				return a.TurnOn(ctx, priority)
			}
			return a.TurnOff(ctx, priority)
		})
}

// HADiscoveryPayload returns the HA Switch-platform payload for a
// per-user access permission.
//
// Both constituent wire data points are invisible on their own — STATE
// is suppressed on a custom-DP channel and ACCESS_AUTHORIZATION is
// forced to no_create — so without this builder the permission has no HA
// entity at all. State therefore reads from the custom-DP aggregate
// topic and the command goes to [serviceAccessPermission].
func (a *AccessPermission) HADiscoveryPayload(ctx payload.HADiscoveryContext) (component string, body map[string]any) {
	if a == nil || ctx == nil {
		return "", nil
	}
	body = map[string]any{
		"command_topic": ctx.ServiceMethodCommandTopic(serviceAccessPermission),
		"payload_on":    "true",
		"payload_off":   "false",
		"state_topic":   ctx.CustomDPStateTopic(),
		// The aggregate omits is_on until STATE has been observed; the
		// `is defined` guard keeps HA from logging a template error on the
		// retained pre-observation payload.
		"value_template": `{% if value_json.is_on is defined %}{{ value_json.is_on | lower }}{% endif %}`,
		"state_on":       "true",
		"state_off":      "false",
		// The CCU confirms the grant on STATE; HA must not flip the entity
		// locally before that echo arrives.
		"optimistic": false,
	}
	return "switch", body
}
