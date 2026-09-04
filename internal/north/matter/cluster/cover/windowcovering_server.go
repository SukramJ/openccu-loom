// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package cover contains the Matter WindowCovering cluster server (0x0102)
// for the position-aware lift feature profile. It is instantiated by the
// rich-model cover types (Cover, Blind, Garage) and mounted onto their
// Matter endpoint projections.
package cover

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
)

// ClusterRevision is the WindowCovering cluster revision this server
// implements. Matched against matter.js HEAD window-covering.element.ts.
const ClusterRevision uint16 = 8

// Config carries the initial state and type-specific attributes for a
// [WindowCoveringServer].
type Config struct {
	// Type is the Matter WindowCovering Type attribute value (enum8).
	Type uint8
	// EndProductType is the Matter WindowCovering EndProductType
	// attribute value (enum8).
	EndProductType uint8
	// FeatureMap is the Matter FeatureMap bitmask for this server
	// instance. Callers set lift / tilt / position-aware bits according
	// to what the device supports.
	FeatureMap uint32
	// InitialPositionPercent100ths is the starting value for
	// CurrentPositionLiftPercent100ths and
	// TargetPositionLiftPercent100ths (0 = fully open, 10000 = fully
	// closed, per Matter §5.3.6.11).
	InitialPositionPercent100ths uint16
}

// WindowCoveringServer implements
// [github.com/SukramJ/openccu-loom/pkg/interfaces.MatterClusterServer]
// for the Matter WindowCovering cluster (0x0102), position-aware lift
// feature profile. State is self-contained; commands update internal
// fields and return Success so that commissioning tools and controller
// apps receive a well-formed cluster without requiring a live CCU
// backend.
type WindowCoveringServer struct {
	mu sync.RWMutex

	wcType         uint8
	endProductType uint8
	featureMap     uint32
	wcMode         uint8 // Mode attribute (0x0017), RW VM, constraint max 15.

	currentPositionPercent100ths uint16
	targetPositionPercent100ths  uint16
	operationalStatus            uint8
}

// NewWindowCoveringServer constructs a [WindowCoveringServer] from cfg.
func NewWindowCoveringServer(cfg Config) *WindowCoveringServer {
	return &WindowCoveringServer{
		wcType:                       cfg.Type,
		endProductType:               cfg.EndProductType,
		featureMap:                   cfg.FeatureMap,
		currentPositionPercent100ths: cfg.InitialPositionPercent100ths,
		targetPositionPercent100ths:  cfg.InitialPositionPercent100ths,
		operationalStatus:            0,
	}
}

// MatterClusterID returns the WindowCovering cluster ID (0x0102).
func (s *WindowCoveringServer) MatterClusterID() uint32 {
	return wire.WindowCoveringClusterID
}

// MatterRead resolves mandatory WindowCovering attributes for the
// position-aware lift profile.
func (s *WindowCoveringServer) MatterRead(attrID uint32) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	switch attrID {
	case wire.WindowCoveringAttrType:
		return s.wcType, true
	case wire.WindowCoveringAttrConfigStatus:
		// 0x05 = Operational | LiftPositionAware.
		return uint8(0x05), true
	case wire.WindowCoveringAttrCurrentPositionLiftPercentage:
		// Deprecated uint8 field (0x0008); return scaled-down value for
		// backward compatibility with Matter 1.0 controllers.
		return uint8((s.currentPositionPercent100ths / 100) & 0xFF), true // value bounded 0-100 by design
	case wire.WindowCoveringAttrOperationalStatus:
		return s.operationalStatus, true
	case wire.WindowCoveringAttrTargetPositionLiftPercent100ths:
		return s.targetPositionPercent100ths, true
	case wire.WindowCoveringAttrEndProductType:
		return s.endProductType, true
	case wire.WindowCoveringAttrCurrentPositionLiftPercent100ths:
		return s.currentPositionPercent100ths, true
	case wire.WindowCoveringAttrMode:
		return s.wcMode, true
	case wire.WindowCoveringAttrSafetyStatus:
		return uint16(0), true
	default:
		return nil, false
	}
}

// windowCoveringConstraintErr is a typed [im.StatusCodeError] for
// writes that violate the "max 10000" constraint on percent100ths attributes.
// Mirrors matter.js window-covering-cluster.element.ts:72,76 constraint.
type windowCoveringConstraintErr struct{ msg string }

func (e windowCoveringConstraintErr) Error() string                 { return e.msg }
func (windowCoveringConstraintErr) MatterStatusCode() im.StatusCode { return im.StatusConstraintError }

// Compile-time assertion.
var _ im.StatusCodeError = windowCoveringConstraintErr{}

// MatterWrite accepts Mode (0x0017) writes; all other attributes are
// controlled via commands. Mode is RW with constraint max 15 per
// matter.js window-covering-cluster.element.ts:79.
func (s *WindowCoveringServer) MatterWrite(_ context.Context, attrID uint32, value any) error {
	if attrID != wire.WindowCoveringAttrMode {
		return fmt.Errorf("windowcovering: attribute 0x%04X is not writable", attrID)
	}
	v, ok := cluster.AsUint8(value)
	if !ok {
		return fmt.Errorf("windowcovering: Mode: expected numeric, got %T", value)
	}
	// Constraint max 15 per matter.js window-covering-cluster.element.ts:79.
	if v > 15 {
		return windowCoveringConstraintErr{fmt.Sprintf("windowcovering: Mode %d exceeds constraint max 15", v)}
	}
	s.mu.Lock()
	s.wcMode = v
	s.mu.Unlock()
	return nil
}

// MatterInvoke handles the mandatory WindowCovering commands. All four
// commands accept the request and update internal state; they return
// Success without forwarding to a CCU backend. Callers that need live-CCU
// control use the rich-model projections in
// internal/model/custom/cover/matter.go instead.
func (s *WindowCoveringServer) MatterInvoke(_ context.Context, cmdID uint32, fields any) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch cmdID {
	case wire.WindowCoveringCmdUpOrOpen:
		s.targetPositionPercent100ths = 0
		s.currentPositionPercent100ths = 0
		s.operationalStatus = 0
		return nil, nil
	case wire.WindowCoveringCmdDownOrClose:
		s.targetPositionPercent100ths = 10000
		s.currentPositionPercent100ths = 10000
		s.operationalStatus = 0
		return nil, nil
	case wire.WindowCoveringCmdStopMotion:
		s.operationalStatus = 0
		return nil, nil
	case wire.WindowCoveringCmdGoToLiftPercentage:
		pct, err := extractPercent100ths(fields)
		if err != nil {
			return nil, err
		}
		s.targetPositionPercent100ths = pct
		s.currentPositionPercent100ths = pct
		s.operationalStatus = 0
		return nil, nil
	default:
		return nil, fmt.Errorf("windowcovering: unknown command 0x%02X", cmdID)
	}
}

// MatterReportable lists the attributes that change at runtime and
// require Matter subscription reports.
func (s *WindowCoveringServer) MatterReportable() []uint32 {
	return []uint32{
		wire.WindowCoveringAttrCurrentPositionLiftPercent100ths,
		wire.WindowCoveringAttrOperationalStatus,
	}
}

// MatterAttributes lists every attribute the server implements via
// MatterRead.
func (s *WindowCoveringServer) MatterAttributes() []uint32 {
	return []uint32{
		wire.WindowCoveringAttrType,
		wire.WindowCoveringAttrConfigStatus,
		wire.WindowCoveringAttrCurrentPositionLiftPercentage,
		wire.WindowCoveringAttrOperationalStatus,
		wire.WindowCoveringAttrTargetPositionLiftPercent100ths,
		wire.WindowCoveringAttrEndProductType,
		wire.WindowCoveringAttrCurrentPositionLiftPercent100ths,
		wire.WindowCoveringAttrMode,
		wire.WindowCoveringAttrSafetyStatus,
	}
}

// extractPercent100ths pulls a uint16 percent100ths value from the
// GoToLiftPercentage command fields. The bridge may deliver a bare
// uint16 or a map with a "percent" key. Values > 10000 are rejected
// with ConstraintError per matter.js window-covering-cluster.element.ts:72
// constraint "max 10000" on liftPercent100thsValue.
func extractPercent100ths(fields any) (uint16, error) {
	var pct uint16
	switch v := fields.(type) {
	case uint16:
		pct = v
	case map[string]any:
		raw, ok := v["percent"]
		if !ok {
			return 0, errors.New("windowcovering: GoToLiftPercentage missing percent field")
		}
		var castOK bool
		pct, castOK = raw.(uint16)
		if !castOK {
			return 0, fmt.Errorf("windowcovering: GoToLiftPercentage percent expected uint16, got %T", raw)
		}
	default:
		return 0, fmt.Errorf("windowcovering: GoToLiftPercentage expected uint16 or map[string]any, got %T", fields)
	}
	// Constraint max 10000 per matter.js window-covering-cluster.element.ts:72.
	if pct > 10000 {
		return 0, windowCoveringConstraintErr{fmt.Sprintf("windowcovering: liftPercent100thsValue %d exceeds constraint max 10000", pct)}
	}
	return pct, nil
}
