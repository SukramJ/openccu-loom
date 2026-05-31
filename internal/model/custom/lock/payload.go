// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package lock

import (
	"context"

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
}

// HADiscoveryPayload returns the HA Lock-platform-specific payload
// skeleton. HA lock platform uses a single command_topic with
// payload_lock / payload_unlock — not separate lock/unlock topics.
//
// command_topic is Kind-aware: IP locks write LOCK_TARGET_LEVEL (0/1/2);
// RF locks write STATE (false/true); Button locks write BUTTON_LOCK
// (false/true). Each kind's wire parameter is real — pointing at a
// non-existent parameter causes an XML-RPC fault on every HA command.
//
// Per ADR 0010: lock/unlock multiplexing on one HA command_topic
// → wire-parameter command topic fallback. State reads from aggregated topic.
func (l *Lock) HADiscoveryPayload(ctx payload.HADiscoveryContext) (component string, body map[string]any) {
	if l == nil || ctx == nil {
		return "", nil
	}
	stateTopic := ctx.CustomDPStateTopic()

	var commandTopic, payloadLock, payloadUnlock string
	switch l.Kind {
	case KindRF:
		// RF locks expose a bool STATE: false = locked, true = unlocked.
		commandTopic = ctx.WireParameterCommandTopic("STATE")
		payloadLock = "false"
		payloadUnlock = "true"
	case KindButton:
		// Button locks expose BUTTON_LOCK: false = unlocked, true = locked.
		commandTopic = ctx.WireParameterCommandTopic("BUTTON_LOCK")
		payloadLock = "true"
		payloadUnlock = "false"
	default: // KindIP
		// HmIP locks use LOCK_TARGET_LEVEL ENUM: 0 = lock, 1 = unlock, 2 = open.
		commandTopic = ctx.WireParameterCommandTopic("LOCK_TARGET_LEVEL")
		payloadLock = "0"
		payloadUnlock = "1"
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
	// action via LOCK_TARGET_LEVEL=2. RF/Button locks have no open action.
	if l.Capabilities.SupportsOpen && l.Kind == KindIP {
		body["payload_open"] = "2"
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
