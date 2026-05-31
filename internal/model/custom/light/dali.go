// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// DRGDaliLight is the HmIP-DRG-DALI 48-channel DALI bus driver. Each channel
// exposes the standard dimmer surface (LEVEL) plus the optional
// COLOR_TEMPERATURE and EFFECT inputs DALI fixtures may surface.
//
// Functionally a DALI light is RGBWLight minus HUE/SATURATION (DALI does
// not carry RGB). To keep the API focussed and the type hierarchy shallow we
// compose [ColorTempLight] directly and re-use its KELVIN handling; effects
// are layered on top via the optional EFFECT parameter.
type DRGDaliLight struct {
	*ColorTempLight

	// effect is the optional EFFECT action-select data point for DALI effect
	// selection. It is write-only and addressed by String label from the
	// channel's VALUE_LIST. nil when the channel does not carry EFFECT.
	effect *generic.ActionSelect
}

// NewDRGDaliLight constructs a DALI light with the configured Kelvin bounds.
// When the channel carries an EFFECT parameter its VALUE_LIST is used to
// populate the effect surface via [SetEffect].
func NewDRGDaliLight(cfg Config, minK, maxK int32) *DRGDaliLight {
	l := &DRGDaliLight{
		ColorTempLight: NewColorTempLight(cfg, minK, maxK),
		effect:         custom.ActionSelectField(cfg.Channel, hmenum.ParameterEffect),
	}
	return l
}

// NamePostfix reports the entity-name suffix ("color_temp").
func (l *DRGDaliLight) NamePostfix() string { return "color_temp" }

// SetEffect sends an effect by its String label (e.g. "Off", "Flash") to the
// DALI channel. Returns nil without writing when the channel carries no EFFECT
// parameter — older DALI fixtures without software effect support.
func (l *DRGDaliLight) SetEffect(ctx context.Context, label string, priority hmenum.CommandPriority) error {
	if l.effect == nil {
		return nil
	}
	if label == "" {
		return fmt.Errorf("dali: effect label must not be empty")
	}
	if err := l.effect.TriggerLabel(custom.EnsureContext(ctx), label, priority); err != nil {
		return fmt.Errorf("dali: SET EFFECT: %w", err)
	}
	return nil
}
