// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import "context"

// ProgramDpButton wraps a [*Program] and exposes it as a press-only
// Button. Modelled after
// (model/hub/button.py), which invokes execute_program when the button
// is pressed.
//
// All Program methods remain reachable through the embedded pointer.
type ProgramDpButton struct {
	*Program
}

// Available reports whether the button is ready for interaction. In Go this
// returns true only when the program has been observed and is currently
// active.
func (b *ProgramDpButton) Available() bool {
	active, ok := b.Active()
	return ok && active
}

// Press executes the wrapped program. It is a thin alias for
// [Program.Execute] and mirrors press.
func (b *ProgramDpButton) Press(ctx context.Context) error {
	return b.Execute(ctx)
}

// ProgramDpSwitch wraps a [*Program] and exposes it as an
// Enable/disable switch. Modelled after
// (model/hub/switch.py:32). The [Value] method reports the current
// is_active state; [TurnOn] and [TurnOff] call [Program.SetEnabled].
//
// All Program methods remain reachable through the embedded pointer.
type ProgramDpSwitch struct {
	*Program
}

// Value returns the current active state of the program. Returns false when
// the state has not yet been observed.
func (sw *ProgramDpSwitch) Value() bool {
	active, _ := sw.Active()
	return active
}

// TurnOn enables the program via [Program.SetEnabled].
func (sw *ProgramDpSwitch) TurnOn(ctx context.Context) error {
	return sw.SetEnabled(ctx, true)
}

// TurnOff disables the program via [Program.SetEnabled].
func (sw *ProgramDpSwitch) TurnOff(ctx context.Context) error {
	return sw.SetEnabled(ctx, false)
}
