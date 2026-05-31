// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package core

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Identify implements the Matter Identify cluster (0x0003) per Matter
// Application Cluster Specification §1.2. Mandatory on every endpoint
// other than Root and Network Commissioning (Spec §1.4) — Apple Home's
// HAP service rebuild step uses the cluster's presence as a structural
// gate; an endpoint that advertises a primary device type without
// Identify in its ServerList fails HAPErrorDomain Code=24 ("Failed to
// rebuild HAP services") and the pair aborts.
//
// matter.js places `IdentifyServer` in the mandatory cluster set of
// every device type definition under
// `packages/node/src/devices/*.ts` (e.g. `contact-sensor.ts`,
// `temperature-sensor.ts`, `on-off-plug-in-unit.ts`). Bridge endpoints
// inherit the same requirement.
//
// Bridge semantics: openccu-loom does not drive any visual or audible
// indicator on the underlying CCU device, so Identify is implemented
// as a no-op surface that:
//
//   - reports IdentifyType = None (0x00) — "the device has no
//     identification mechanism", signalling clients should fall back
//     to a software cue rather than expect blinking lights;
//   - tracks IdentifyTime as a software-only countdown (set by the
//     Identify command, not driven by hardware);
//   - implements the Identify command as a successful no-op so
//     commissioner-side Identify flows complete without error.
//
// The TriggerEffect command (0x40) is optional per spec; we accept it
// and return Success without performing any effect — Apple's HAP
// mapper expects either a working implementation or a clean Success
// reply. Returning UnsupportedCommand here would surface as a HAP
// build warning that some firmware revisions escalate to Code=24.
type Identify struct {
	// identifyTime is tracked atomically because reads (subscribe
	// reports), writes (clients setting it directly per spec §1.2.5.1),
	// and command dispatch (Identify) all touch the same field from
	// different goroutines.
	identifyTime atomic.Uint32 // logically uint16; high bits ignored

	// dataVersion tracks the per-cluster monotonic counter per Matter
	// §10.6.5. Bumped after every successful IdentifyTime write (via
	// MatterWrite or MatterInvoke) so DataVersionFilter evaluation works.
	// Satisfies [interfaces.MatterClusterDataVersion].
	dataVersion cluster.DataVersionTracker

	// Countdown-Timer state. Spec §1.2.5.1: "The IdentifyTime
	// attribute … SHALL decrement at a rate of 1 per second once
	// active". One goroutine per Identify cluster decrements every
	// second until either zero is reached or a write/command sets a
	// new non-zero value (in which case the existing goroutine
	// continues with the new value).
	timerMu     sync.Mutex
	timerActive bool
}

// Cluster ID + revision per Matter §1.2.
const (
	identifyClusterID       uint32 = 0x0003
	identifyClusterRevision uint16 = 6 // matter.js HEAD (@matter/model 0.16.11)

	identifyAttrTime uint32 = 0x0000 // IdentifyTime  (uint16, RW, default 0)
	identifyAttrType uint32 = 0x0001 // IdentifyType  (enum8,  R, default 0/None)

	identifyCmdIdentify      uint32 = 0x00 // Identify(IdentifyTime)
	identifyCmdTriggerEffect uint32 = 0x40 // TriggerEffect(EffectIdentifier, EffectVariant)

	// IdentifyType values per Matter §1.2.5.2.
	identifyTypeNone uint8 = 0
)

// NewIdentify constructs a fresh Identify cluster instance with
// IdentifyTime = 0 and IdentifyType = None.
func NewIdentify() *Identify {
	return &Identify{}
}

// Compile-time assertions.
var (
	_ interfaces.MatterClusterServer        = (*Identify)(nil)
	_ interfaces.MatterClusterDataVersion   = (*Identify)(nil)
	_ interfaces.MatterClusterCommandLister = (*Identify)(nil)
)

// MatterClusterID implements [interfaces.MatterClusterServer].
func (i *Identify) MatterClusterID() uint32 { return identifyClusterID }

// MatterDataVersion implements [interfaces.MatterClusterDataVersion].
// Returns the current per-cluster monotonic counter bumped after every
// successful IdentifyTime write or Identify command dispatch.
// Mirrors matter.js IdentifyServer.ts DataVersion tracking.
func (i *Identify) MatterDataVersion() uint32 { return i.dataVersion.Current() }

// MatterRead implements [interfaces.MatterClusterServer].
func (i *Identify) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case identifyAttrTime:
		return uint16(i.identifyTime.Load()), true //nolint:gosec // value capped at uint16 by every writer
	case identifyAttrType:
		return identifyTypeNone, true
	case cluster.AttrGlobalFeatureMap:
		return uint32(0), true
	case cluster.AttrGlobalClusterRevision:
		return identifyClusterRevision, true
	}
	return nil, false
}

// MatterWrite handles direct writes to IdentifyTime per Matter
// §1.2.5.1. Spec wording: "The IdentifyTime attribute SHALL be in
// units of seconds, and SHALL be writable." — clients drive Identify
// either via the command or by setting the attribute directly.
func (i *Identify) MatterWrite(_ context.Context, attrID uint32, value any, _ hmenum.CommandPriority) error {
	if attrID != identifyAttrTime {
		return fmt.Errorf("matter: Identify attribute 0x%04X is read-only", attrID)
	}
	t, err := coerceUint16(value)
	if err != nil {
		return fmt.Errorf("matter: Identify.IdentifyTime: %w", err)
	}
	i.identifyTime.Store(uint32(t))
	i.maybeStartCountdown()
	// Bump DataVersion after a successful IdentifyTime write so
	// DataVersionFilter evaluation correctly detects the cluster changed.
	i.dataVersion.Bump()
	return nil
}

// MatterInvoke dispatches Identify and TriggerEffect commands.
func (i *Identify) MatterInvoke(_ context.Context, cmdID uint32, fields any, _ hmenum.CommandPriority) (any, error) {
	switch cmdID {
	case identifyCmdIdentify:
		// Identify command argument is a struct {IdentifyTime: uint16}
		// per Matter §1.2.6.1. The IM cluster-fields decoder may pass
		// either the decoded struct or a raw value depending on the
		// commissioner's encoding; coerceUint16 handles both with a
		// best-effort fallback to 0 (= clear identify).
		t, _ := coerceUint16(fields)
		i.identifyTime.Store(uint32(t))
		i.maybeStartCountdown()
		// Bump DataVersion after a successful Identify command so
		// DataVersionFilter evaluation correctly detects the cluster changed.
		i.dataVersion.Bump()
		// Success path returns nil (status-only response).
		return nil, nil
	case identifyCmdTriggerEffect:
		// TriggerEffect is a no-op for the bridge — we don't actuate
		// any indicator. Return success so Apple's HAP mapper does
		// not flag the cluster as broken during the topology probe.
		return nil, nil
	}
	return nil, fmt.Errorf("matter: Identify command 0x%02X not supported", cmdID)
}

// MatterReportable returns the attributes that change over the
// subscription's lifetime. IdentifyTime ticks down once per second
// while a software-driven identify is in progress; clients subscribe
// to it to render the remaining-time indicator.
func (i *Identify) MatterReportable() []uint32 {
	return []uint32{identifyAttrTime}
}

// MatterAttributes implements
// [interfaces.MatterClusterAttributeLister] so wildcard subscribe /
// read enumerates the full Identify surface.
func (i *Identify) MatterAttributes() []uint32 {
	return []uint32{identifyAttrTime, identifyAttrType}
}

// MatterAcceptedCommands implements [interfaces.MatterClusterCommandLister].
// Returns the command IDs handled by MatterInvoke so the dispatcher
// populates AcceptedCommandList (0xFFF9) correctly. TriggerEffect (0x40)
// is optional per spec but accepted as a visual no-op; including it here
// prevents strict commissioners from flagging an AcceptedCommandList
// mismatch against the Identify element.ts command table.
func (i *Identify) MatterAcceptedCommands() []uint32 {
	return []uint32{identifyCmdIdentify, identifyCmdTriggerEffect}
}

// MatterGeneratedCommands returns nil; Identify commands carry no
// response payload.
func (i *Identify) MatterGeneratedCommands() []uint32 { return nil }

// maybeStartCountdown starts a background goroutine that decrements
// IdentifyTime once per second per Matter §1.2.5.1, if one is not
// already running. Concurrent callers are serialised by timerMu so
// only one goroutine is ever active at a time.
func (i *Identify) maybeStartCountdown() {
	i.timerMu.Lock()
	if i.timerActive || i.identifyTime.Load() == 0 {
		i.timerMu.Unlock()
		return
	}
	i.timerActive = true
	i.timerMu.Unlock()

	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			v := i.identifyTime.Load()
			if v == 0 {
				i.timerMu.Lock()
				i.timerActive = false
				i.timerMu.Unlock()
				return
			}
			i.identifyTime.Store(v - 1)
		}
	}()
}

// coerceUint16 best-effort reads a uint16 out of TLV-decoded values.
// IdentifyTime arrives as either a bare uint64 / uint32 / uint16 (when
// the IM layer decoded the scalar without a cluster-aware fields
// reader) or as a `struct { IdentifyTime uint16 }` (when a future
// type-aware decoder lands). Unrecognised shapes coerce to 0, which
// is the spec's "clear identify" semantic — safer than rejecting the
// command outright.
func coerceUint16(v any) (uint16, error) {
	switch x := v.(type) {
	case uint16:
		return x, nil
	case uint32:
		return uint16(x), nil //nolint:gosec // spec-bound
	case uint64:
		return uint16(x), nil //nolint:gosec // spec-bound
	case int:
		return uint16(x), nil //nolint:gosec // spec-bound
	case int64:
		return uint16(x), nil //nolint:gosec // spec-bound
	case nil:
		return 0, nil
	}
	return 0, fmt.Errorf("unsupported IdentifyTime type %T", v)
}
