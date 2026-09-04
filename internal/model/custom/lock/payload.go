// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package lock

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Compile-time guarantee that *Lock satisfies the universal Source
// contract and the HA-Discovery payload builder contract (ADR 0010).
var (
	_ payload.Source                    = (*Lock)(nil)
	_ payload.HADiscoveryPayloadBuilder = (*Lock)(nil)
)

// Info returns identity-level fields for a Lock.
func (l *Lock) Info() payload.InfoPayload {
	if l == nil {
		return nil
	}
	return &payload.LockInfo{
		Address:   l.Address,
		Key:       l.key.String(),
		Category:  "lock",
		Kind:      kindName(l.Kind),
		SubDPKeys: subDPKeysAsStrings(l.SubDataPointKeys()),
	}
}

// Config returns the lock capability configuration.
func (l *Lock) Config() payload.ConfigPayload {
	if l == nil {
		return nil
	}
	return &payload.LockConfig{
		SupportsOpen: l.Capabilities.SupportsOpen,
	}
}

// State returns the live lock state in HA-friendly semantic keys.
//
// All keys the discovery payload references (lock_state, direction) are
// emitted unconditionally — HA's `value_template` filters (`{{
// value_json.lock_state }}`) log a warning the moment they resolve to
// `undefined`, so a fresh thermostat with no observed state would otherwise
// spam the operator's HA log on every `state_topic` publish. Pre-event values
// map to "UNLOCKED" / "" (empty direction) — matches HA's lock-default state.
func (l *Lock) State() payload.StatePayload {
	if l == nil {
		return nil
	}
	st := &payload.LockState{
		StateUncertain: l.StateUncertain(),
		IsJammed:       l.IsJammed(),
	}
	if s, ok := l.LockState(); ok {
		st.LockState = string(s)
		st.IsLocked = s == StateLocked
	} else {
		// Default to UNLOCKED so HA does not warn about a missing
		// `lock_state` key on every retained-discovery rebroadcast
		// before the CCU has reported the actual state. The lock
		// will publish the real value on the next wire event.
		st.LockState = string(StateUnlocked)
		st.IsLocked = false
	}
	if d, ok := l.Direction(); ok {
		st.Direction = string(d)
		st.IsLocking = d == DirectionLocking
		st.IsUnlocking = d == DirectionUnlock
	} else {
		st.Direction = ""
		st.IsLocking = false
		st.IsUnlocking = false
	}
	return st
}

// serviceLockCommand is the service method that carries the command
// Home Assistant multiplexes onto a lock entity's single
// `command_topic`. It exists for the button-lock kind, whose wire slot
// is GLOBAL_BUTTON_LOCK in the MASTER paramset: a MASTER parameter
// faults on setValue with XML-RPC -5, so the command has to travel
// through the domain operation that writes it via put_paramset instead
// of through a wire-parameter topic.
const serviceLockCommand = "lock_command"

// argLockCommand is the scalar-argument key [serviceLockCommand]
// expects. The MQTT bridge wraps a bare payload under it before the
// invoke reaches the handler.
const argLockCommand = "command"

// The command tokens advertised as payload_lock / payload_unlock /
// payload_open. Spelled as words rather than as wire values so a payload
// that lands on the wrong topic cannot be mistaken for a level or a
// boolean.
const (
	commandTokenLock   = "LOCK"
	commandTokenUnlock = "UNLOCK"
	commandTokenOpen   = "OPEN"
)

// registerServices wires the lock operations onto the embedded
// ServiceRegistry. Service-method names mirror
// service_method_names for lock custom DPs (lock, unlock, open).
func (l *Lock) registerServices() {
	l.RegisterService("lock", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return l.Lock(ctx, priority)
	})
	l.RegisterService("unlock", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return l.Unlock(ctx, priority)
	})
	if l.Capabilities.SupportsOpen {
		l.RegisterService("open", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
			return l.Open(ctx, priority)
		})
	}
	l.RegisterServiceWithArg(serviceLockCommand, argLockCommand, l.invokeLockCommand)
}

// invokeLockCommand routes one of the [commandTokenLock] /
// [commandTokenUnlock] / [commandTokenOpen] tokens onto the matching
// operation.
//
// Dispatch goes back through the registry rather than calling
// l.Lock / l.Unlock / l.Open directly so the token resolves to whatever
// the registry holds for this device — including the absence of "open"
// on a kind that does not support it, which then answers with the
// unknown-method error instead of silently doing nothing.
func (l *Lock) invokeLockCommand(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
	raw, err := payload.ParamString(params, argLockCommand)
	if err != nil {
		return err
	}
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case commandTokenLock:
		return l.Invoke(ctx, "lock", nil, priority)
	case commandTokenUnlock:
		return l.Invoke(ctx, "unlock", nil, priority)
	case commandTokenOpen:
		return l.Invoke(ctx, "open", nil, priority)
	}
	return fmt.Errorf("%w: %s=%q", payload.ErrServiceInvalidParam, argLockCommand, raw)
}

// HADiscoveryPayload returns the HA Lock-platform-specific payload
// skeleton. HA lock platform uses a single command_topic with
// payload_lock / payload_unlock — not separate lock/unlock topics.
//
// command_topic is Kind-aware: IP locks write LOCK_TARGET_LEVEL (the
// ENUM labels [ipTargetLocked] / [ipTargetUnlocked] / [ipTargetOpen])
// and RF locks write STATE (false/true), both real VALUES parameters
// reachable by a wire-parameter topic. Button locks have no such
// parameter — their slot is GLOBAL_BUTTON_LOCK in MASTER — so they use
// the [serviceLockCommand] service-method topic instead.
//
// Per ADR 0010: lock/unlock multiplexing on one HA command_topic
// → wire-parameter command topic where a real VALUES parameter carries
// the operation, service-method topic otherwise. State reads from the
// aggregated topic.
func (l *Lock) HADiscoveryPayload(ctx payload.HADiscoveryContext) (component string, body map[string]any) {
	if l == nil || ctx == nil {
		return "", nil
	}
	stateTopic := ctx.CustomDPStateTopic()

	var commandTopic, payloadLock, payloadUnlock string
	switch l.Kind {
	case KindRF:
		// RF locks expose a bool STATE. The advertised payloads render
		// the same constants [Lock.sendRF] writes, so a command from Home
		// Assistant and one from the daemon reach the CCU as the same wire
		// value — see [rfStateLocked].
		commandTopic = ctx.WireParameterCommandTopic(string(hmenum.ParameterState))
		payloadLock = strconv.FormatBool(rfStateLocked)
		payloadUnlock = strconv.FormatBool(rfStateUnlocked)
	case KindButton:
		// A button lock's slot is GLOBAL_BUTTON_LOCK in the MASTER
		// paramset (see [Lock.writeButtonParam]), so no wire-parameter
		// command topic can carry it: the VALUES setValue such a topic
		// produces faults, and the parameter it named does not exist on
		// the channel at all. The command travels through the service
		// method that reaches the put_paramset write path instead.
		commandTopic = ctx.ServiceMethodCommandTopic(serviceLockCommand)
		payloadLock = commandTokenLock
		payloadUnlock = commandTokenUnlock
	default: // KindIP
		// HmIP locks use the LOCK_TARGET_LEVEL ENUM. The advertised
		// payloads are the same labels [Lock.sendIP] writes, so a
		// command originating in Home Assistant and one originating in
		// the daemon reach the CCU in the same form. Labels also make
		// the payload independent of the VALUE_LIST order, which no
		// code on this path can see.
		commandTopic = ctx.WireParameterCommandTopic("LOCK_TARGET_LEVEL")
		payloadLock = ipTargetLocked
		payloadUnlock = ipTargetUnlocked
	}

	body = map[string]any{
		// HA lock: single command_topic, payload_lock/payload_unlock on it.
		"command_topic":  commandTopic,
		"payload_lock":   payloadLock,
		"payload_unlock": payloadUnlock,
		// HA lifecycle string tokens — match what StatePayload.lock_state emits.
		"state_locked":    "LOCKED",
		"state_unlocked":  "UNLOCKED",
		"state_jammed":    "JAMMED",
		"state_unlocking": "UNLOCKING",
		"state_locking":   "LOCKING",
		// State from aggregated topic — lock_state is the HA lifecycle string.
		"state_topic":    stateTopic,
		"value_template": "{{ value_json.lock_state }}",
		// optimistic=false — without this HA defaults to true and
		// shows the lock as locked / unlocked before the CCU echo
		// arrives. Critical for door locks where a brief connection
		// drop would otherwise leave HA showing the wrong state.
		"optimistic": false,
	}
	// Door-opener (HmIP-DLD) — only IP locks expose the short-time unlock
	// action, via LOCK_TARGET_LEVEL. RF/Button locks have no open action.
	if l.Capabilities.SupportsOpen && l.Kind == KindIP {
		body["payload_open"] = ipTargetOpen
	}
	return "lock", body
}

// kindName maps the internal Kind enum to a wire-stable string label.
func kindName(k Kind) string {
	switch k {
	case KindIP:
		return "ip"
	case KindRF:
		return "rf"
	case KindButton:
		return "button"
	}
	return "unknown"
}

// subDPKeysAsStrings returns the wire identifiers of every slot as plain
// strings.
func subDPKeysAsStrings(keys []hmtypes.DataPointKey) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = k.String()
	}
	return out
}
