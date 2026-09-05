// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge

import (
	"github.com/SukramJ/openccu-loom/internal/model/generic"
)

// Compile-time guard: the consolidated button group — the shape the
// topology assembler mounts as ep.Measurement on every GenericSwitch
// endpoint — must satisfy the reassemble wiring assertion in
// [matterSwitchSubscribable]. The two sides live in different modules'
// worth of code and a signature drift on either would silently stop
// matching, so the assertion is worth keeping.
//
// It lives in a test file because the bridge package is host-free: the
// button group belongs to the daemon's device model, and importing it
// from production code would drag the whole model layer back into the
// Matter subtree's dependency closure. A `_test.go` file never enters
// `go list -deps`, so the drift check costs the closure nothing. The
// behavioural counterpart — that a press actually reaches a
// commissioner — is pinned by
// tests/contract/wiring_pins/matter_switch_press_wiring_test.go.
var _ matterSwitchSubscribable = (*generic.ButtonGroup)(nil)
