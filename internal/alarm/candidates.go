// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package alarm

import (
	"slices"
	"sort"

	"github.com/SukramJ/openccu-loom/internal/model/custom/cdpkind"
	lightcdp "github.com/SukramJ/openccu-loom/internal/model/custom/light"
	sirencdp "github.com/SukramJ/openccu-loom/internal/model/custom/siren"
	switchcdp "github.com/SukramJ/openccu-loom/internal/model/custom/switch"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// OutputCandidate is one channel whose custom data point can back at
// least one device-backed alarm output class. The candidate list is
// derived from the live domain model — the same custom-DP types the
// runtime deviceResolver (resolver.go) accepts — so an enrollment
// picked from it always resolves to a driver.
type OutputCandidate struct {
	Central        string
	DeviceAddress  string
	DeviceName     string
	Model          string
	ChannelAddress string
	ChannelNo      int
	ChannelName    string
	// ChannelType is the raw wire channel type (e.g.
	// ALARM_SWITCH_VIRTUAL_RECEIVER) — the translation key prefix for
	// localised ENUM value labels.
	ChannelType string
	// Rooms / Functions are the channel's CCU room and function
	// assignments so pickers can filter and label candidates without a
	// second lookup.
	Rooms     []string
	Functions []string
	// Classes are the device-backed output classes this channel can
	// carry, in the canonical class order.
	Classes []hmenum.AlarmOutputClass
	// Kind is the stable custom-DP kind string (cdpkind.Of) for
	// display purposes.
	Kind string
	// AvailableTones / AvailableLights / AvailableSoundfiles are the
	// device's ENUM label lists (acoustic tones and optical patterns
	// for sirens, soundfiles for MP3 players) so pickers can offer
	// real device values instead of free text.
	AvailableTones      []string
	AvailableLights     []string
	AvailableSoundfiles []string
	// Dimmable reports level support for the alarm-light class.
	Dimmable bool
}

// candidateClassOrder is the canonical presentation order of the
// device-backed classes. Notification and sysvar-mirror outputs are
// not device-backed and never appear in candidate sets.
var candidateClassOrder = []hmenum.AlarmOutputClass{
	hmenum.AlarmOutputClassAcousticSiren,
	hmenum.AlarmOutputClassOpticalSiren,
	hmenum.AlarmOutputClassSwitchedSiren,
	hmenum.AlarmOutputClassSmokeSounder,
	hmenum.AlarmOutputClassAlarmLight,
	hmenum.AlarmOutputClassChirp,
}

// DeviceBackedOutputClass reports whether the class enrolls a real CCU
// channel that must resolve to an output driver. Notification rides
// the event bus and the sysvar mirror writes a system variable; both
// carry a channel address as identity only.
func DeviceBackedOutputClass(class hmenum.AlarmOutputClass) bool {
	return slices.Contains(candidateClassOrder, class)
}

// outputCandidateFor derives the device-backed classes (plus picker
// extras) a channel can carry from its custom data point. It mirrors
// the runtime deviceResolver's acceptance and narrows it by the DP's
// own capability flags: the resolver hands out a siren driver for any
// siren channel, but a siren without acoustic support can never sound,
// so it is not an acoustic candidate. The switched-siren class
// additionally requires device-side auto-off (the ON_TIME parameter) —
// without it the actuator cannot self-terminate when the daemon dies,
// which is the class's core safety property.
func outputCandidateFor(ch *device.Channel) (OutputCandidate, bool) {
	var cand OutputCandidate
	switch v := ch.CustomDataPoint().(type) {
	case *sirencdp.Siren:
		if v.Capabilities.SupportsAcoustic {
			cand.Classes = append(cand.Classes,
				hmenum.AlarmOutputClassAcousticSiren, hmenum.AlarmOutputClassChirp)
		}
		if v.Capabilities.SupportsOptical {
			cand.Classes = append(cand.Classes, hmenum.AlarmOutputClassOpticalSiren)
		}
		cand.AvailableTones = v.AvailableTones()
		cand.AvailableLights = v.AvailableLights()
	case *sirencdp.SmokeSiren:
		cand.Classes = append(cand.Classes, hmenum.AlarmOutputClassSmokeSounder)
	case *sirencdp.SoundPlayer:
		cand.Classes = append(cand.Classes, hmenum.AlarmOutputClassChirp)
		cand.AvailableSoundfiles = v.AvailableSoundfiles()
	case *switchcdp.Switch:
		cand.Classes = append(cand.Classes, hmenum.AlarmOutputClassAlarmLight)
		if channelHasOnTime(ch) {
			cand.Classes = append(cand.Classes, hmenum.AlarmOutputClassSwitchedSiren)
		}
	case *lightcdp.Light:
		cand.Classes = append(cand.Classes, hmenum.AlarmOutputClassAlarmLight)
		if channelHasOnTime(ch) {
			cand.Classes = append(cand.Classes, hmenum.AlarmOutputClassSwitchedSiren)
		}
		cand.Dimmable = v.Capabilities.Dimmable
	default:
		return OutputCandidate{}, false
	}
	if len(cand.Classes) == 0 {
		return OutputCandidate{}, false
	}
	slices.SortFunc(cand.Classes, func(a, b hmenum.AlarmOutputClass) int {
		return slices.Index(candidateClassOrder, a) - slices.Index(candidateClassOrder, b)
	})
	cand.Kind = cdpkind.Of(ch.CustomDataPoint())
	return cand, true
}

// channelHasOnTime reports whether the channel's VALUES paramset
// carries the ON_TIME parameter, i.e. supports device-side auto-off.
func channelHasOnTime(ch *device.Channel) bool {
	return ch.Parameter(hmenum.ParameterOnTime) != nil
}

// OutputCandidates enumerates every channel across all centrals that
// can back a device-backed alarm output class, optionally filtered to
// one class (empty class returns all). Ordered by central, device
// address, channel number.
func (s *Service) OutputCandidates(class hmenum.AlarmOutputClass) []OutputCandidate {
	var out []OutputCandidate
	for _, u := range s.reg.List() {
		centralName := u.Name()
		for _, d := range u.QueryFacade().ModelDevices() {
			for _, ch := range d.Channels() {
				cand, ok := outputCandidateFor(ch)
				if !ok {
					continue
				}
				if class != "" && !slices.Contains(cand.Classes, class) {
					continue
				}
				cand.Central = centralName
				cand.DeviceAddress = d.Address
				cand.DeviceName = d.Name()
				cand.Model = d.Model
				cand.ChannelAddress = ch.Address
				cand.ChannelNo = ch.Number
				cand.ChannelName = ch.NameData().ChannelName
				cand.ChannelType = ch.Type
				cand.Rooms = ch.Rooms()
				cand.Functions = ch.Functions()
				out = append(out, cand)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Central != out[j].Central {
			return out[i].Central < out[j].Central
		}
		if out[i].DeviceAddress != out[j].DeviceAddress {
			return out[i].DeviceAddress < out[j].DeviceAddress
		}
		return out[i].ChannelNo < out[j].ChannelNo
	})
	return out
}

// remoteKeyParams is the press-parameter dispatch set of the intent
// router (intents.go onEvent): remote-key bindings only ever fire on
// these parameters, so only channels emitting them are candidates.
var remoteKeyParams = []hmenum.Parameter{
	hmenum.ParameterPressShort,
	hmenum.ParameterPressLong,
}

// RemoteKeyCandidate is one channel that emits the key-press events
// remote-key code bindings dispatch on — a physical remote-control or
// wall-button key. Virtual remote channels are excluded (they would
// flood the picker); binding one remains possible via the raw-JSON
// expert path.
type RemoteKeyCandidate struct {
	Central        string
	DeviceAddress  string
	DeviceName     string
	Model          string
	ChannelAddress string
	ChannelNo      int
	ChannelName    string
	// Parameters are the press parameters this key offers, in the
	// router's dispatch order (PRESS_SHORT before PRESS_LONG).
	Parameters []string
}

// RemoteKeyCandidates enumerates every channel across all centrals
// that emits a press parameter the intent router routes remote-key
// bindings on. Ordered by central, device address, channel number.
func (s *Service) RemoteKeyCandidates() []RemoteKeyCandidate {
	var out []RemoteKeyCandidate
	for _, u := range s.reg.List() {
		centralName := u.Name()
		for _, d := range u.QueryFacade().ModelDevices() {
			if d.IsVirtualRemote() {
				continue
			}
			for _, ch := range d.Channels() {
				// Press parameters are ordinary VALUES data points —
				// the pipeline materialises every wire parameter as a
				// DP (device_pipeline.go), so presence in the VALUES
				// paramset is the authoritative key-channel signal.
				var params []string
				for _, p := range remoteKeyParams {
					if ch.Parameter(p) != nil {
						params = append(params, string(p))
					}
				}
				if len(params) == 0 {
					continue
				}
				out = append(out, RemoteKeyCandidate{
					Central:        centralName,
					DeviceAddress:  d.Address,
					DeviceName:     d.Name(),
					Model:          d.Model,
					ChannelAddress: ch.Address,
					ChannelNo:      ch.Number,
					ChannelName:    ch.NameData().ChannelName,
					Parameters:     params,
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Central != out[j].Central {
			return out[i].Central < out[j].Central
		}
		if out[i].DeviceAddress != out[j].DeviceAddress {
			return out[i].DeviceAddress < out[j].DeviceAddress
		}
		return out[i].ChannelNo < out[j].ChannelNo
	})
	return out
}

// OutputTargetEligible reports whether the enrolled target channel can
// back the given class. known=false means the central or channel is
// not currently resolvable — callers must treat that as eligible
// (soft validation: a CCU that is down or still booting must never
// block a config save; the runtime fault journal remains the safety
// net for those rows). Non-device-backed classes are always eligible.
func (s *Service) OutputTargetEligible(centralName, channelAddress string, class hmenum.AlarmOutputClass) (eligible, known bool) {
	if !DeviceBackedOutputClass(class) {
		return true, true
	}
	u, ok := s.reg.Get(centralName)
	if !ok {
		return true, false
	}
	ch := u.GetChannel(channelAddress)
	if ch == nil {
		return true, false
	}
	cand, ok := outputCandidateFor(ch)
	if !ok {
		return false, true
	}
	return slices.Contains(cand.Classes, class), true
}
