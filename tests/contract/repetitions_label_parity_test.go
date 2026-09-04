// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/light"
	"github.com/SukramJ/openccu-loom/internal/model/custom/textdisplay"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// repetitionsLabelDeviceList is the REPETITIONS VALUE_LIST both captured
// devices that carry the parameter advertise — HmIP-MP3P (channels 2, 6, 7,
// 8) and HmIP-WRCD (channel 3). Sixteen entries, the numbered run stopping at
// REPETITIONS_014 — the list the firmware builds for the VALUES paramset, and
// the ceiling [custom.MaxRepetitions] now carries.
var repetitionsLabelDeviceList = []string{
	"NO_REPETITION",
	"REPETITIONS_001", "REPETITIONS_002", "REPETITIONS_003", "REPETITIONS_004",
	"REPETITIONS_005", "REPETITIONS_006", "REPETITIONS_007", "REPETITIONS_008",
	"REPETITIONS_009", "REPETITIONS_010", "REPETITIONS_011", "REPETITIONS_012",
	"REPETITIONS_013", "REPETITIONS_014",
	"INFINITE_REPETITIONS",
}

// TestRepetitionsLabelIsOneRuleAcrossCustomProfiles pins the two custom
// profiles that turn a numeric repetition count into a REPETITIONS wire
// label against each other.
//
// The count reaches the domain two ways and used to be converted twice: the
// sound-player LED's turn_on carried its own formatter, and the text
// display's write_with_sound carried a second one that differed in its
// out-of-range verdict — one returned an error the caller swallowed into
// NO_REPETITION, the other returned an empty string that silently dropped
// the parameter from the write. Two formatters mean the same operator input
// can reach two devices as two different labels.
//
// The check drives the same count through BOTH production seams — the
// cdp-invoke plane for the LED, the service registry the MQTT command topic
// invokes for the text display — and compares what actually reached the
// wire. It never compares a profile against a literal: the expectation for
// one profile is the other profile's wire write.
//
// A pure differential would be green if both profiles wrote nothing, so a
// second, absolute assertion anchors the agreed label to device reality:
// for every count the captured devices can express, the agreed label must
// be a member of their advertised VALUE_LIST.
func TestRepetitionsLabelIsOneRuleAcrossCustomProfiles(t *testing.T) {
	t.Parallel()

	const prio = hmenum.CommandPriorityHigh
	ctx := context.Background()

	// Counts the label grammar can express and both captured devices
	// advertise. The two now coincide: [custom.MaxRepetitions] is the
	// device list's own ceiling, so counts above 14 are rejected by both
	// profiles rather than formatted into a label no device offers.
	deviceExpressible := func(n int) bool { return n == -1 || (n >= 0 && n <= 14) }

	for n := -3; n <= 20; n++ {
		t.Run(fmt.Sprintf("count=%d", n), func(t *testing.T) {
			t.Parallel()

			// Seam 1: SPA / REST / WebSocket / MQTT cdp-invoke → the
			// LED's own atomic turn_on.
			ledWriter := &repetitionsLabelWriter{}
			ledReg, ledAddr := repetitionsLabelLEDRegistry(t, ledWriter)
			ledErr := adapter.NewCustomDPDispatcher(ledReg).InvokeCustomDP(
				ctx, ledAddr, "LEVEL", "turn_on",
				maps.Clone(map[string]any{"repetitions": n}), prio, "contract",
			)

			// Seam 2: the Home Assistant MQTT command topic lands on the
			// text display's service registry.
			tdWriter := &repetitionsLabelWriter{}
			td := textdisplay.New("SDV0001:1", tdWriter)
			tdErr := td.Invoke(ctx, "write_with_sound",
				maps.Clone(map[string]any{"id": 1, "repeat": n}), prio)

			ledLabel, ledWrote := ledWriter.repetitions()
			tdLabel, tdWrote := tdWriter.repetitions()

			if ledWrote != tdWrote {
				t.Fatalf("count %d: the LED %s and the text display %s — one profile still carries its own conversion rule (LED err=%v, text-display err=%v)",
					n, repetitionsLabelVerdict(ledWrote, ledLabel), repetitionsLabelVerdict(tdWrote, tdLabel), ledErr, tdErr)
			}
			if ledLabel != tdLabel {
				t.Fatalf("count %d reached the wire as %q from the LED and %q from the text display — the two profiles disagree on the label",
					n, ledLabel, tdLabel)
			}
			if ledWrote != (ledErr == nil) {
				t.Errorf("count %d: LED wrote=%v but returned err=%v — a rejected count must not reach the wire and an accepted one must",
					n, ledWrote, ledErr)
			}
			if tdWrote != (tdErr == nil) {
				t.Errorf("count %d: text display wrote=%v but returned err=%v", n, tdWrote, tdErr)
			}

			// Absolute anchor: agreement on a label no device offers is
			// still wrong. For every count the captured devices can
			// express, the agreed label has to be one of their entries.
			if deviceExpressible(n) {
				if !ledWrote {
					t.Fatalf("count %d is expressible on every captured device (VALUE_LIST %v) but neither profile wrote REPETITIONS",
						n, repetitionsLabelDeviceList)
				}
				if !slices.Contains(repetitionsLabelDeviceList, ledLabel) {
					t.Fatalf("count %d produced %q, which is not an entry of the REPETITIONS VALUE_LIST the captured HmIP-MP3P and HmIP-WRCD advertise (%v)",
						n, ledLabel, repetitionsLabelDeviceList)
				}
				// The text display validates the label against the
				// device list when it holds one; a label the devices
				// offer must survive that validator.
				listed := textdisplay.New("SDV0002:1", &repetitionsLabelWriter{})
				listed.SetAvailableRepetitions(repetitionsLabelDeviceList)
				if err := listed.Invoke(ctx, "write_with_sound",
					map[string]any{"id": 1, "repeat": n}, prio); err != nil {
					t.Fatalf("count %d: a text display holding the captured VALUE_LIST rejected its own label %q: %v", n, ledLabel, err)
				}
			}
		})
	}
}

// repetitionsLabelVerdict renders a profile's outcome for the failure text.
func repetitionsLabelVerdict(wrote bool, label string) string {
	if wrote {
		return fmt.Sprintf("wrote REPETITIONS=%q", label)
	}
	return "wrote no REPETITIONS"
}

// repetitionsLabelWriter records the REPETITIONS value that reached the wire,
// through either write shape a custom profile may pick.
type repetitionsLabelWriter struct {
	mu     sync.Mutex
	values []string
}

func (w *repetitionsLabelWriter) SetValue(
	_ context.Context, _ string, p hmenum.Parameter, v any, _ hmenum.CommandPriority,
) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if p == hmenum.ParameterRepetitions {
		w.values = append(w.values, fmt.Sprint(v))
	}
	return nil
}

func (w *repetitionsLabelWriter) PutParamset(
	_ context.Context, _ string, _ hmenum.ParamsetKey, values map[string]any, _ hmenum.CommandPriority,
) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if v, ok := values[string(hmenum.ParameterRepetitions)]; ok {
		w.values = append(w.values, fmt.Sprint(v))
	}
	return nil
}

// repetitions returns the REPETITIONS value written and whether one was
// written at all. Repeated writes of the same value collapse; differing
// values are joined so the failure text shows the drift.
func (w *repetitionsLabelWriter) repetitions() (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.values) == 0 {
		return "", false
	}
	out := append([]string(nil), w.values...)
	sort.Strings(out)
	return strings.Join(slices.Compact(out), "|"), true
}

// repetitionsLabelLEDRegistry builds a central registry holding one
// HmIP-MP3P-shaped status LED, constructed through its real constructor and
// reachable through the cdp-invoke plane.
func repetitionsLabelLEDRegistry(t *testing.T, w *repetitionsLabelWriter) (registry *central.Registry, address string) {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu-rep"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	const addr = "MP3P0001"
	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     addr,
		Model:       "HmIP-MP3P",
	})
	chAddr := addr + ":6"
	ch := dev.AddChannel(chAddr, 6, "DIMMER", hmenum.ParamsetKeyValues)
	ch.Put(generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	}))
	ch.Put(generic.NewStringSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterRepetitions),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			ValueList:  repetitionsLabelDeviceList,
		},
		Writer: w,
	}))

	dp := light.NewSoundPlayerLED(light.Config{
		Channel:      ch,
		Writer:       w,
		Capabilities: custom.LightCapabilities{Dimmable: true},
	})
	ch.SetCustomDataPoint(dp)
	c.ModelRegistry.Put(dev)
	return reg, addr
}
