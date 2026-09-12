// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package lock

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	hacatalog "github.com/SukramJ/go-ha-catalog"
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
	hamodel "github.com/SukramJ/go-hamqtt/model"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Compile-time guarantee that *Lock satisfies the universal Source
// contract and the HA-Discovery payload builder contract (ADR 0010).
var (
	_ payload.Source                   = (*Lock)(nil)
	_ payload.HADiscoveryEntityBuilder = (*Lock)(nil)
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

// HADiscoveryEntity describes the lock on the shared model. HA's lock
// platform uses a single command_topic with payload_lock / payload_unlock —
// not separate lock and unlock topics.
//
// The command surface is Kind-aware: IP locks write LOCK_TARGET_LEVEL (the
// ENUM labels [ipTargetLocked] / [ipTargetUnlocked] / [ipTargetOpen]) and RF
// locks write STATE (false/true), both real VALUES parameters a wire-parameter
// binding reaches. Button locks have no such parameter — their slot is
// GLOBAL_BUTTON_LOCK in MASTER — so their command travels on the
// [serviceLockCommand] method topic instead, which is the write path that
// reaches put_paramset. State reads from the aggregate.
func (l *Lock) HADiscoveryEntity() hamodel.Entity {
	if l == nil {
		return nil
	}
	entity := &lockEntity{CustomEntity: payload.CustomEntity{
		Basic: hamodel.Basic{
			EntityKey:      l.TopicSlot().Parameter,
			EntityPlatform: hacatalog.PlatformLock,
			Description: hamodel.Description{
				// lock_state is the HA lifecycle string the aggregate emits.
				ValueTemplate: "{{ value_json.lock_state }}",
				// optimistic=false — without this HA defaults to true and shows
				// the lock as locked / unlocked before the CCU echo arrives.
				// Critical for door locks, where a brief connection drop would
				// otherwise leave HA showing the wrong state.
				Optimistic: hamodel.Ptr(false),
			},
			Binds: []hamodel.Binding{{
				Role: hamodel.RoleState, Mode: hamodel.Read,
				Slot: payload.CustomSlot(l.TopicSlot()),
			}},
		},
	}}

	fields := hadiscovery.LockFields{
		// HA lifecycle string tokens — match what StatePayload.lock_state
		// emits.
		StateLocked:    "LOCKED",
		StateUnlocked:  "UNLOCKED",
		StateJammed:    "JAMMED",
		StateUnlocking: "UNLOCKING",
		StateLocking:   "LOCKING",
	}
	switch l.Kind {
	case KindRF:
		// RF locks expose a bool STATE. The advertised payloads render the
		// same constants [Lock.sendRF] writes, so a command from Home
		// Assistant and one from the daemon reach the CCU as the same wire
		// value — see [rfStateLocked].
		entity.Binds = append(entity.Binds, hamodel.Binding{
			Role: hamodel.RoleCommand, Mode: hamodel.Write,
			Slot: payload.WireSlot(string(hmenum.ParameterState)),
		})
		fields.PayloadLock = strconv.FormatBool(rfStateLocked)
		fields.PayloadUnlock = strconv.FormatBool(rfStateUnlocked)
	case KindButton:
		// A button lock's slot is GLOBAL_BUTTON_LOCK in the MASTER paramset
		// (see [Lock.writeButtonParam]), so no wire-parameter binding can
		// carry it: the VALUES setValue such a topic produces faults, and the
		// parameter it named does not exist on the channel at all.
		entity.method = serviceLockCommand
		fields.PayloadLock = commandTokenLock
		fields.PayloadUnlock = commandTokenUnlock
	default: // KindIP
		// HmIP locks use the LOCK_TARGET_LEVEL ENUM. The advertised payloads
		// are the same labels [Lock.sendIP] writes, so a command originating
		// in Home Assistant and one originating in the daemon reach the CCU in
		// the same form. Labels also make the payload independent of the
		// VALUE_LIST order, which no code on this path can see.
		entity.Binds = append(entity.Binds, hamodel.Binding{
			Role: hamodel.RoleCommand, Mode: hamodel.Write,
			Slot: payload.WireSlot("LOCK_TARGET_LEVEL"),
		})
		fields.PayloadLock = ipTargetLocked
		fields.PayloadUnlock = ipTargetUnlocked
	}
	// Door-opener (HmIP-DLD) — only IP locks expose the short-time unlock
	// action, via LOCK_TARGET_LEVEL. RF and button locks have no open action.
	if l.Capabilities.SupportsOpen && l.Kind == KindIP {
		fields.PayloadOpen = ipTargetOpen
	}
	entity.Fields = fields
	return entity
}

// lockEntity is the lock's platform vocabulary plus the one command shape the
// model cannot express: a button lock is written through a named action, and
// the render pipeline only wires a method up on its own for an entity that
// declares exactly one — a lock declares lock, unlock and open.
type lockEntity struct {
	payload.CustomEntity

	// method is the named action the command topic points at, empty for a
	// lock whose command is a wire-parameter binding.
	method string
}

// BuildDiscovery implements [hadiscovery.Builder].
func (e *lockEntity) BuildDiscovery(ctx hadiscovery.Context, comp *hadiscovery.Component) error {
	if err := e.CustomEntity.BuildDiscovery(ctx, comp); err != nil {
		return err
	}
	if e.method != "" {
		comp.CommandTopic = e.MethodTopic(ctx, e.method)
	}
	return nil
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
