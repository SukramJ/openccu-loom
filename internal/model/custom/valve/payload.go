// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package valve

import (
	"context"
	"time"

	hacatalog "github.com/SukramJ/go-ha-catalog"
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
	hamodel "github.com/SukramJ/go-hamqtt/model"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Compile-time guarantees that the valve-domain custom data points
// satisfy the universal Source contract and the HA-Discovery payload
// builder contract (ADR 0010). ADR-0007 step 5.
//
// Irrigation inherits the ServiceRegistry write-half from its embedded
// *generic.Switch (which promotes turn_on, turn_off, set). Modulating
// inherits from its embedded *generic.Float (which promotes set_value).
// Both types register additional service methods below.
var (
	_ payload.Source                   = (*Irrigation)(nil)
	_ payload.Source                   = (*Modulating)(nil)
	_ payload.HADiscoveryEntityBuilder = (*Irrigation)(nil)
	_ payload.HADiscoveryEntityBuilder = (*Modulating)(nil)
)

// --- Irrigation ---

// Info returns identity-level fields for an Irrigation valve.
func (v *Irrigation) Info() payload.InfoPayload {
	if v == nil {
		return nil
	}
	return &payload.IrrigationValveInfo{
		Address:  v.Address(),
		Key:      v.DataPointKey().String(),
		Category: "valve",
		Kind:     "irrigation",
	}
}

// Config returns the irrigation valve static configuration.
func (v *Irrigation) Config() payload.ConfigPayload {
	if v == nil {
		return nil
	}
	return &payload.IrrigationValveConfig{
		Kind: "irrigation",
	}
}

// State returns the live irrigation valve state.
//
// `is_open` is emitted unconditionally — HA's
// `value_template={{ value_json.is_open }}` filter logs a warning on
// every retained-state rebroadcast where the key is missing, before
// the CCU has reported the actual state. Defaults to `false`
// (closed) until the first wire event arrives.
func (v *Irrigation) State() payload.StatePayload {
	if v == nil {
		return nil
	}
	open, observed := v.IsOpen()
	if !observed {
		open = false
	}
	return &payload.IrrigationValveState{IsOpen: open}
}

// registerIrrigationServices registers the irrigation-specific service
// methods on top of the generic ones inherited from *generic.Switch.
func (v *Irrigation) registerIrrigationServices() {
	v.RegisterService("open", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		var dur time.Duration
		if d, err := payload.ParamFloat64(params, "duration"); err == nil {
			dur = time.Duration(d * float64(time.Second))
		}
		return v.Open(ctx, dur, priority)
	})
	v.RegisterService("close", func(ctx context.Context, _ map[string]any, priority hmenum.CommandPriority) error {
		return v.Close(ctx, priority)
	})
}

// --- Modulating ---

// Info returns identity-level fields for a Modulating valve.
func (v *Modulating) Info() payload.InfoPayload {
	if v == nil {
		return nil
	}
	return &payload.ModulatingValveInfo{
		Address:  v.Address(),
		Key:      v.DataPointKey().String(),
		Category: "valve",
		Kind:     "modulating",
	}
}

// Config returns the modulating valve static configuration.
func (v *Modulating) Config() payload.ConfigPayload {
	if v == nil {
		return nil
	}
	return &payload.ModulatingValveConfig{
		Kind: "modulating",
	}
}

// State returns the live modulating valve state.
//
// `current_level_pct` is emitted unconditionally — HA's
// `value_template={{ value_json.current_level_pct }}` filter logs a
// warning on every retained-state rebroadcast where the key is
// missing, before the CCU has reported the actual level. Defaults
// to 0 (closed) until the first wire event arrives.
func (v *Modulating) State() payload.StatePayload {
	if v == nil {
		return nil
	}
	st := &payload.ModulatingValveState{}
	if pos, ok := v.Level(); ok {
		lvl := pos.Level()
		st.CurrentLevel = &lvl
		st.CurrentLevelPct = lvl * 100
	} else {
		st.CurrentLevel = nil
		st.CurrentLevelPct = 0.0
	}
	return st
}

// HADiscoveryEntity describes an Irrigation valve on the shared model.
//
// Irrigation is a binary open/close device. The open and close service
// methods are distinct, but HA's valve platform sends both payloads to one
// command_topic, so no single named action can carry them — the write goes to
// the STATE wire parameter, which accepts exactly the two values. State comes
// from the aggregate's is_open flag.
func (v *Irrigation) HADiscoveryEntity() hamodel.Entity {
	if v == nil {
		return nil
	}
	return &payload.CustomEntity{
		Basic: hamodel.Basic{
			EntityKey:      v.TopicSlot().Parameter,
			EntityPlatform: hacatalog.PlatformValve,
			Description: hamodel.Description{
				// device_class drives the HA icon (water-droplet) and semantic
				// classification.
				DeviceClass: "water",
				// Render the HA-canonical state strings ("open" / "closed")
				// directly: the bare `{{ value_json.is_open }}` form returns
				// Python's `True`/`False` (capitalised) and matches no
				// state_open / state_closed permutation. HA logs `Payload
				// received … is not one of [open, closed, opening, closing],
				// got: False` until the explicit branch emits a matching
				// string.
				ValueTemplate: "{% if value_json.is_open %}open{% else %}closed{% endif %}",
				Optimistic:    hamodel.Ptr(false),
			},
			Binds: []hamodel.Binding{
				{
					Role: hamodel.RoleState, Mode: hamodel.Read,
					Slot: payload.CustomSlot(v.TopicSlot()),
				},
				{
					Role: hamodel.RoleCommand, Mode: hamodel.Write,
					Slot: payload.WireSlot("STATE"),
				},
			},
		},
		Fields: hadiscovery.ValveFields{
			PayloadOpen:  "true",
			PayloadClose: "false",
			// Irrigation is binary — no position reporting.
			ReportsPosition: hadiscovery.Ptr(false),
			StateOpen:       "open",
			StateClosed:     "closed",
		},
	}
}

// HADiscoveryEntity describes a Modulating valve on the shared model.
//
// set_level is a distinct service method and the valve's only write, so the
// render pipeline wires it up as the command topic on its own — see
// [Modulating.Methods]. State comes from the aggregate's current_level_pct
// (0..100), which is why the valve reports position where an irrigation valve
// does not.
func (v *Modulating) HADiscoveryEntity() hamodel.Entity {
	if v == nil {
		return nil
	}
	return &modulatingEntity{CustomEntity: payload.CustomEntity{
		Basic: hamodel.Basic{
			EntityKey:      v.TopicSlot().Parameter,
			EntityPlatform: hacatalog.PlatformValve,
			Description: hamodel.Description{
				// device_class drives the HA icon — water-droplet for
				// irrigation valves; modulating water-flow regulators inherit
				// the same classification.
				DeviceClass:   "water",
				ValueTemplate: "{{ value_json.current_level_pct }}",
				Optimistic:    hamodel.Ptr(false),
			},
			Binds: []hamodel.Binding{{
				Role: hamodel.RoleState, Mode: hamodel.Read,
				Slot: payload.CustomSlot(v.TopicSlot()),
			}},
		},
		Fields: hadiscovery.ValveFields{
			// Modulating valves report position.
			ReportsPosition: hadiscovery.Ptr(true),
		},
	}}
}

// modulatingEntity carries the one key the model has no field for: HA sends a
// 0..100 percentage and the service method takes the 0..1 fraction the CCU
// speaks.
type modulatingEntity struct {
	payload.CustomEntity
}

// Methods implements [hamodel.Invoker]: set_level is the valve's single write,
// so declaring it is what makes the render pipeline point `command_topic` at
// its method topic instead of at a wire parameter no single value could drive.
func (*modulatingEntity) Methods() []string { return []string{"set_level"} }

// BuildDiscovery implements [hadiscovery.Builder].
func (e *modulatingEntity) BuildDiscovery(ctx hadiscovery.Context, comp *hadiscovery.Component) error {
	if err := e.CustomEntity.BuildDiscovery(ctx, comp); err != nil {
		return err
	}
	comp.CommandTemplate = "{{ (value | float / 100) }}"
	return nil
}

// registerModulatingServices registers the modulating valve service
// methods on top of the generic ones inherited from *generic.Float.
func (v *Modulating) registerModulatingServices() {
	v.RegisterServiceWithArg("set_level", "level", func(ctx context.Context, params map[string]any, priority hmenum.CommandPriority) error {
		f, err := payload.ParamFloat64(params, "level")
		if err != nil {
			return err
		}
		return v.SetLevel(ctx, f, priority)
	})
}
