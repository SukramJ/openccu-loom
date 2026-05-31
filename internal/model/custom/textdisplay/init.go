// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package textdisplay

import (
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// DataPointKey satisfies [device.AttachableDataPoint] so the materializer can
// call [device.Channel.SetCustomDataPoint] on a *TextDisplay after
// construction. The key is built from the channel address, VALUES paramset
// and DISPLAY_DATA_COMMIT parameter — the commit pulse is the canonical
// identity of this multi-parameter DP.
func (t *TextDisplay) DataPointKey() hmtypes.DataPointKey { return t.key }

// init registers the [Constructor] for the IPTextDisplay profile on
// the process-wide [custom.DefaultRegistry]. D.12.
//
// The constructor is intentionally thin: it delegates all behavioural
// logic to [New] and maps the channel's installed writer (via
// [device.Channel.Writer]) to the TextDisplay.Writer field.
// A nil writer is valid at construction time — the field may be set
// later once the pipeline wires the channel.
//
// Runtime capability lists (available_icons, available_sounds) are
// captured from the channel's VALUES paramset — this mirrors
// (text_display.py:108-111: `_dp_display_data_icon.values`
// `_dp_acoustic_notification_selection.values`). The static defaults
// from [defaultIcons] / [defaultSounds] are already seeded by [New];
// the channel values take precedence when present.
func init() {
	custom.DefaultRegistry().MustRegisterConstructor(
		hmenum.DeviceProfileIPTextDisplay,
		func(ch *device.Channel, _ custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
			td := New(ch.Address, ch.Writer())
			if dev := ch.Device(); dev != nil {
				td.key = hmtypes.DataPointKey{
					InterfaceID:    dev.InterfaceID,
					ChannelAddress: ch.Address,
					ParamsetKey:    hmenum.ParamsetKeyValues,
					Parameter:      string(hmenum.ParameterDisplayDataCommit),
				}
			} else {
				td.key = hmtypes.DataPointKey{
					ChannelAddress: ch.Address,
					ParamsetKey:    hmenum.ParamsetKeyValues,
					Parameter:      string(hmenum.ParameterDisplayDataCommit),
				}
			}
			// Populate capability lists from the runtime paramset so that
			// StatePayload reflects actual device options rather than the
			// static fallback. Mirrors siren/siren.go:101-106.
			if dp := ch.Parameter(hmenum.ParameterDisplayDataIcon); dp != nil {
				td.SetAvailableIcons(dp.ParameterData().ValueList)
			}
			if dp := ch.Parameter(hmenum.ParameterAcousticNotificationSelection); dp != nil {
				td.SetAvailableSounds(dp.ParameterData().ValueList)
			}
			if dp := ch.Parameter(hmenum.ParameterRepetitions); dp != nil {
				td.SetAvailableRepetitions(dp.ParameterData().ValueList)
			}
			// Wire the BURST_LIMIT_WARNING binary sensor when the channel
			// exposes the parameter so Write/WriteWithSound can emit a
			// log warning before dispatching writes.
			if raw := ch.Parameter(hmenum.ParameterBurstLimitWarning); raw != nil {
				if bs, ok := raw.(*generic.BinarySensor); ok {
					td.SetBurstLimitWarningDP(bs)
				}
			}
			return td, nil
		},
	)
}
