// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light

import (
	"context"
	"errors"
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

	// hue / saturation are the optional HUE / SATURATION data points. Most
	// DALI ballasts are colour-temperature only, but the IPDRGDALI schema
	// maps both fields, and the CCU's UNIVERSAL_LIGHT_RECEIVER channel type
	// carries them on RGB-capable DALI fixtures — mirrors the reference
	// CustomDpIpDrgDaliLight, which resolves the same pair (hue_field /
	// saturation_field on CombinedHsColorField). nil on a channel that
	// carries neither.
	hue        *generic.Integer
	saturation *generic.Float
}

// NewDRGDaliLight constructs a DALI light with the configured Kelvin bounds.
// When the channel carries an EFFECT parameter its VALUE_LIST is used to
// populate the effect surface via [SetEffect].
func NewDRGDaliLight(cfg Config, minK, maxK int32) *DRGDaliLight {
	l := &DRGDaliLight{
		ColorTempLight: NewColorTempLight(cfg, minK, maxK),
		effect:         custom.ActionSelectField(custom.ResolveSlotOr(cfg.Channel, cfg.Group, hmenum.FieldEffect, hmenum.ParameterEffect)),
		hue:            custom.IntegerField(custom.ResolveSlotOr(cfg.Channel, cfg.Group, hmenum.FieldHue, hmenum.ParameterHue)),
		saturation:     custom.FloatField(custom.ResolveSlotOr(cfg.Channel, cfg.Group, hmenum.FieldSaturation, hmenum.ParameterSaturation)),
	}
	if l.hue != nil {
		_ = l.hue.OnConfirmedUpdate(func(_, _ int32) { l.dataVersion.Bump() })
	}
	if l.saturation != nil {
		_ = l.saturation.OnConfirmedUpdate(func(_, _ float64) { l.dataVersion.Bump() })
	}
	return l
}

// HSColor returns the hue (0..360) and saturation (0..1) of a DALI fixture
// that carries both axes, and whether both have been observed. ok is false
// when the channel carries neither parameter (colour-temperature-only DALI
// ballasts) or either axis has not yet reported a value.
func (l *DRGDaliLight) HSColor() (hue int32, saturation float64, ok bool) {
	if l.hue == nil || l.saturation == nil {
		return 0, 0, false
	}
	h, hOK := l.hue.Value()
	s, sOK := l.saturation.Value()
	if !hOK || !sOK {
		return 0, 0, false
	}
	return h, s, true
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
		return errors.New("dali: effect label must not be empty")
	}
	if err := l.effect.TriggerLabel(custom.EnsureContext(ctx), label, priority); err != nil {
		return fmt.Errorf("dali: SET EFFECT: %w", err)
	}
	return nil
}
