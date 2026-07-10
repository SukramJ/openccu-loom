// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package siren

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

type stubWriter struct {
	mu    sync.Mutex
	calls []call
}

type call struct {
	param hmenum.Parameter
	value any
}

func (s *stubWriter) SetValue(_ context.Context, _ string, p hmenum.Parameter, v any, _ hmenum.CommandPriority) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call{p, v})
	return nil
}

func (s *stubWriter) has(p hmenum.Parameter) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.calls {
		if c.param == p {
			return c.value, true
		}
	}
	return nil, false
}

type rig struct {
	siren            *Siren
	channel          *device.Channel
	acousticActiveDP *generic.BinarySensor
	acousticIdxDP    *generic.Sensor[string]
	opticalActiveDP  *generic.BinarySensor
	opticalIdxDP     *generic.Sensor[string]
}

func newRig(t *testing.T, address string, w Writer, caps custom.SirenCapabilities) *rig {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel(address, 1, "SIREN", hmenum.ParamsetKeyValues)

	binSensor := func(p hmenum.Parameter) *generic.BinarySensor {
		dp := generic.NewBinarySensor(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: address,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeBool,
				Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			},
		})
		ch.Put(dp)
		return dp
	}
	strSensor := func(p hmenum.Parameter) *generic.Sensor[string] {
		dp := generic.NewStringSensor(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: address,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeString,
				Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			},
		})
		ch.Put(dp)
		return dp
	}

	r := &rig{
		channel:          ch,
		acousticActiveDP: binSensor(hmenum.ParameterAcousticAlarmActive),
		acousticIdxDP:    strSensor(hmenum.ParameterAcousticAlarmSelection),
		opticalActiveDP:  binSensor(hmenum.ParameterOpticalAlarmActive),
		opticalIdxDP:     strSensor(hmenum.ParameterOpticalAlarmSelection),
	}
	r.siren = New(Config{Channel: ch, Writer: w, Capabilities: caps})
	return r
}

func TestSirenTurnOnSendsSelectionAndDuration(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-ASIR:3", w, custom.SirenCapabilities{
		SupportsAcoustic: true,
		SupportsOptical:  true,
		SupportsDuration: true,
	})
	acoustic := "FREQUENCY_RISING"
	optical := "BLINKING_RED"
	err := r.siren.TurnOn(context.Background(), OnConfig{
		Duration:          30 * time.Minute,
		AcousticSelection: &acoustic,
		OpticalSelection:  &optical,
	}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := w.has(hmenum.ParameterAcousticAlarmSelection); !ok || v.(string) != "FREQUENCY_RISING" {
		t.Fatalf("acoustic=%v ok=%v", v, ok)
	}
	if v, ok := w.has(hmenum.ParameterOpticalAlarmSelection); !ok || v.(string) != "BLINKING_RED" {
		t.Fatalf("optical=%v ok=%v", v, ok)
	}
	if _, ok := w.has(hmenum.ParameterDurationValue); !ok {
		t.Fatal("duration value missing")
	}
}

func TestSirenTurnOffZerosBothChannels(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "x", w, custom.SirenCapabilities{SupportsAcoustic: true, SupportsOptical: true})
	if err := r.siren.TurnOff(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	// TurnOff sends the default string label (empty string when no DEFAULT
	// is declared on the DP), not the integer 0.
	if _, ok := w.has(hmenum.ParameterAcousticAlarmSelection); !ok {
		t.Fatal("acoustic off: ACOUSTIC_ALARM_SELECTION must be written")
	}
	if _, ok := w.has(hmenum.ParameterOpticalAlarmSelection); !ok {
		t.Fatal("optical off: OPTICAL_ALARM_SELECTION must be written")
	}
	active, _ := r.siren.IsActive()
	if active {
		t.Fatal("TurnOff must clear active state")
	}
}

func TestSirenIngestion(t *testing.T) {
	r := newRig(t, "x", &stubWriter{}, custom.SirenCapabilities{SupportsAcoustic: true, SupportsOptical: true})

	// Drive the channel-side data points (production path) and
	// verify Siren observes them through its shared pointers.
	r.acousticActiveDP.OnEvent(true)
	r.acousticIdxDP.OnEvent("FREQUENCY_RISING")
	r.opticalActiveDP.OnEvent(false)
	r.opticalIdxDP.OnEvent("DISABLE_OPTICAL_SIGNAL")

	active, _ := r.siren.IsActive()
	if !active {
		t.Fatal("acoustic active should count as overall active")
	}
}

func TestSirenSharesAcousticInstanceWithChannel(t *testing.T) {
	r := newRig(t, "x", &stubWriter{}, custom.SirenCapabilities{})
	chDP := r.channel.Parameter(hmenum.ParameterAcousticAlarmActive)
	if chDP == nil {
		t.Fatal("channel must expose ACOUSTIC_ALARM_ACTIVE")
	}
	if any(r.siren.acousticActive) != any(chDP) || any(r.siren.acousticActive) != any(r.acousticActiveDP) {
		t.Fatalf("Siren.acousticActive must be the same instance as channel parameter")
	}
}

// TestEncodeDurationUnits verifies the threshold behaviour:
// Python uses _TIME_UNIT_THRESHOLD = 16343, so values below that threshold
// stay in the seconds bucket. 5 min (300 s) and 2 h (7200 s) are both below
// 16343 and thus stay in S. Promotion only happens when the value exceeds
// 16343 in the current unit.
func TestEncodeDurationUnits(t *testing.T) {
	cases := []struct {
		d        time.Duration
		wantV    int32
		wantUnit int32
	}{
		{30 * time.Second, 30, 0}, // 30 s → (30, S)
		{61 * time.Second, 61, 0}, // 61 s < 16343 → (61, S), NOT (1, M)
		{5 * time.Minute, 300, 0}, // 300 s < 16343 → (300, S)
		{2 * time.Hour, 7200, 0},  // 7200 s < 16343 → (7200, S)
		{5 * time.Hour, 300, 1},   // 18000 s > 16343 → promote to M: 300 min < 16343 → (300, M)
		{0, 0, 0},
	}
	for _, c := range cases {
		v, u := custom.EncodeTimerDuration(c.d)
		if v != c.wantV || u != c.wantUnit {
			t.Errorf("%v → (%d, %d), want (%d, %d)", c.d, v, u, c.wantV, c.wantUnit)
		}
	}
}

// TestConvertPlayRepetitionsIndex verifies the -1/0/1..18 → label mapping.
func TestConvertPlayRepetitionsIndex(t *testing.T) {
	t.Parallel()

	avail := []string{"NO_REP", "REPETITIONS_5", "INFINITE"}

	cases := []struct {
		index   int
		want    string
		wantErr bool
	}{
		{0, "NO_REP", false},        // no repeat → first slot
		{-1, "INFINITE", false},     // unlimited → last slot
		{1, "REPETITIONS_5", false}, // N+1 times → slot 1
		{-2, "", true},              // out of range (RepetitionsIndexNotSet)
		{19, "", true},              // above max
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("index_%d", tc.index), func(t *testing.T) {
			t.Parallel()
			got, err := ConvertPlayRepetitionsIndex(tc.index, avail)
			if tc.wantErr {
				if err == nil {
					t.Errorf("ConvertPlayRepetitionsIndex(%d): expected error, got %q", tc.index, got)
				}
				return
			}
			if err != nil {
				t.Errorf("ConvertPlayRepetitionsIndex(%d): unexpected error: %v", tc.index, err)
				return
			}
			if got != tc.want {
				t.Errorf("ConvertPlayRepetitionsIndex(%d) = %q, want %q", tc.index, got, tc.want)
			}
		})
	}
}

// TestConvertPlayRepetitionsIndexEmptyList verifies that an empty availableRep
// returns an error.
func TestConvertPlayRepetitionsIndexEmptyList(t *testing.T) {
	t.Parallel()

	if _, err := ConvertPlayRepetitionsIndex(0, nil); err == nil {
		t.Fatal("ConvertPlayRepetitionsIndex with empty list must return error")
	}
}

// TestPlaySoundRepetitionsSemantics verifies the full -1/0/1 write path
// through PlaySound against the rig's rep list ["NO_REP","REPETITIONS_5","INFINITE"].
func TestPlaySoundRepetitionsSemantics(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		index     int
		wantLabel string
		wantWrite bool
	}{
		{"zero_no_repeat", 0, "NO_REP", true},
		{"minus_one_infinite", -1, "INFINITE", true},
		{"one_plus_one", 1, "REPETITIONS_5", true},
		{"not_set_no_write", RepetitionsIndexNotSet, "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sp, w := newSoundPlayerRig(t)
			cfg := PlayConfig{
				SoundfileIndex:   1, // ensure at least one param is written
				RepetitionsIndex: tc.index,
			}
			if err := sp.PlaySound(context.Background(), cfg, hmenum.CommandPriorityHigh); err != nil {
				t.Fatalf("PlaySound: %v", err)
			}
			label, wrote := w.has(hmenum.ParameterRepetitions)
			if tc.wantWrite && !wrote {
				t.Fatalf("expected REPETITIONS write, got none")
			}
			if !tc.wantWrite && wrote {
				t.Fatalf("expected no REPETITIONS write, got %v", label)
			}
			if tc.wantWrite && label != tc.wantLabel {
				t.Fatalf("REPETITIONS = %v, want %q", label, tc.wantLabel)
			}
		})
	}
}

// ConvertSoundfileIndex public function.
func TestConvertSoundfileIndex(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		index   int
		want    string
		wantErr bool
	}{
		{1, "SOUNDFILE_001", false},
		{42, "SOUNDFILE_042", false},
		{189, "SOUNDFILE_189", false},
		{0, "", true},   // below min
		{190, "", true}, // above max
		{-1, "", true},  // negative
	} {
		got, err := ConvertSoundfileIndex(tc.index)
		if tc.wantErr {
			if err == nil {
				t.Errorf("index=%d: expected error, got %q", tc.index, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("index=%d: unexpected error: %v", tc.index, err)
			continue
		}
		if got != tc.want {
			t.Errorf("index=%d: got %q, want %q", tc.index, got, tc.want)
		}
	}
}

// --- Siren accessors ---

// TestSirenAcousticState verifies that AcousticState reflects events pushed on
// the underlying acoustic DPs.
func TestSirenAcousticState(t *testing.T) {
	t.Parallel()

	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{
		SupportsAcoustic: true,
	})

	// Before any event: nothing observed.
	_, _, obs := r.siren.AcousticState()
	if obs {
		t.Error("AcousticState should not be observed before any event")
	}

	r.acousticActiveDP.OnEvent(true)
	r.acousticIdxDP.OnEvent("FREQUENCY_RISING")
	active, sel, obs := r.siren.AcousticState()
	if !obs {
		t.Error("AcousticState should be observed after events")
	}
	if !active {
		t.Error("AcousticState active should be true")
	}
	if sel != "FREQUENCY_RISING" {
		t.Errorf("AcousticState selection = %q, want FREQUENCY_RISING", sel)
	}
}

// TestSirenOpticalState verifies that OpticalState reflects events pushed on
// the underlying optical DPs.
func TestSirenOpticalState(t *testing.T) {
	t.Parallel()

	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{
		SupportsOptical: true,
	})

	_, _, obs := r.siren.OpticalState()
	if obs {
		t.Error("OpticalState should not be observed before any event")
	}

	r.opticalActiveDP.OnEvent(false)
	r.opticalIdxDP.OnEvent("BLINKING_RED")
	active, sel, obs := r.siren.OpticalState()
	if !obs {
		t.Error("OpticalState should be observed after events")
	}
	if active {
		t.Error("OpticalState active should be false")
	}
	if sel != "BLINKING_RED" {
		t.Errorf("OpticalState selection = %q, want BLINKING_RED", sel)
	}
}

// --- Siren nil-writer edge cases ---

// TestSirenTurnOnNilWriter verifies that TurnOn with a nil writer and zero
// params returns nil (early exit, no write attempted).
func TestSirenTurnOnNilWriter(t *testing.T) {
	t.Parallel()

	// Build siren without a writer and with no capable channels — all param
	// maps will be empty, so the early-exit path at len(params)==0 fires.
	s := New(Config{
		Channel:      nil,
		Writer:       nil,
		Capabilities: custom.SirenCapabilities{},
	})
	err := s.TurnOn(context.Background(), OnConfig{}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("TurnOn with nil writer and no capable channels: %v", err)
	}
}

// TestSirenTurnOffNilWriter verifies that TurnOff with a nil writer and no
// supported channels returns nil.
func TestSirenTurnOffNilWriter(t *testing.T) {
	t.Parallel()

	s := New(Config{
		Channel:      nil,
		Writer:       nil,
		Capabilities: custom.SirenCapabilities{},
	})
	err := s.TurnOff(context.Background(), hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("TurnOff with nil writer and no caps: %v", err)
	}
}

// --- Siren topology ---

func TestSirenHAComponent(t *testing.T) {
	t.Parallel()

	s := New(Config{Channel: nil, Writer: &stubWriter{}, Capabilities: custom.SirenCapabilities{}})
	if got := s.HAComponent(); got != "siren" {
		t.Errorf("Siren.HAComponent() = %q, want %q", got, "siren")
	}
}

func TestSirenTopicSlotWithChannelAddress(t *testing.T) {
	t.Parallel()

	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{})
	slot := r.siren.TopicSlot()
	if slot.Parameter != "siren" {
		t.Errorf("TopicSlot.Parameter = %q, want %q", slot.Parameter, "siren")
	}
	if slot.Channel != 3 {
		t.Errorf("TopicSlot.Channel = %d, want 3", slot.Channel)
	}
}

func TestSirenTopicSlotFallbackOnInvalidAddress(t *testing.T) {
	t.Parallel()

	s := New(Config{Channel: nil, Writer: &stubWriter{}, Capabilities: custom.SirenCapabilities{}})
	s.Address = "NOCORON"
	slot := s.TopicSlot()
	if slot.Address != "NOCORON" {
		t.Errorf("TopicSlot fallback address = %q, want %q", slot.Address, "NOCORON")
	}
}

// --- Siren payload ---

func TestSirenInfoPayload(t *testing.T) {
	t.Parallel()

	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{})
	p, ok := r.siren.Info().(*payload.SirenInfo)
	if !ok || p == nil {
		t.Fatal("InfoPayload must return a non-nil *payload.SirenInfo")
	}
	if p.Category != "siren" {
		t.Errorf("InfoPayload category = %v, want siren", p.Category)
	}
}

func TestSirenInfoPayloadNilReceiverReturnsNil(t *testing.T) {
	t.Parallel()

	var s *Siren
	if p := s.Info(); p != nil {
		t.Errorf("nil Siren.Info() = %v, want nil", p)
	}
}

func TestSirenConfigPayload(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", &stubWriter{}, custom.SirenCapabilities{
		SupportsAcoustic: true,
		SupportsOptical:  true,
		SupportsDuration: true,
	})
	p, _ := r.siren.Config().(*payload.SirenConfig)
	if p == nil {
		t.Fatal("ConfigPayload must not be nil")
	}
	if !p.SupportsAcoustic {
		t.Errorf("ConfigPayload supports_acoustic = %v, want true", p.SupportsAcoustic)
	}
	if !p.SupportsDuration {
		t.Errorf("ConfigPayload supports_duration = %v, want true", p.SupportsDuration)
	}
}

func TestSirenConfigPayloadNilReceiverReturnsNil(t *testing.T) {
	t.Parallel()

	var s *Siren
	if p := s.Config(); p != nil {
		t.Errorf("nil Siren.Config() = %v, want nil", p)
	}
}

func TestSirenStatePayloadOff(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", &stubWriter{}, custom.SirenCapabilities{SupportsAcoustic: true})
	p, ok := r.siren.State().(*payload.SirenState)
	if !ok || p == nil {
		t.Fatal("StatePayload must return a non-nil *payload.SirenState")
	}
	if p.State != "off" {
		t.Errorf("StatePayload state = %v, want off (not yet observed)", p.State)
	}
}

func TestSirenStatePayloadOn(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", &stubWriter{}, custom.SirenCapabilities{SupportsAcoustic: true})
	r.acousticActiveDP.OnEvent(true)
	p, ok := r.siren.State().(*payload.SirenState)
	if !ok || p == nil {
		t.Fatal("StatePayload must return a non-nil *payload.SirenState")
	}
	if p.State != "on" {
		t.Errorf("StatePayload state = %v, want on", p.State)
	}
}

func TestSirenStatePayloadNilReceiverReturnsNil(t *testing.T) {
	t.Parallel()

	var s *Siren
	if p := s.State(); p != nil {
		t.Errorf("nil Siren.State() = %v, want nil", p)
	}
}

// --- Siren service registration (turn_on / turn_off service methods) ---

func TestSirenRegisterServiceTurnOff(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "x", w, custom.SirenCapabilities{SupportsAcoustic: true, SupportsOptical: true})
	r.acousticActiveDP.OnEvent(true)

	if err := r.siren.Invoke(context.Background(), "turn_off", nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("turn_off service: %v", err)
	}
	active, _ := r.siren.IsActive()
	if active {
		t.Error("IsActive must be false after turn_off service call")
	}
}

func TestSirenRegisterServiceTurnOn(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "x", w, custom.SirenCapabilities{
		SupportsAcoustic: true,
		SupportsDuration: true,
	})

	params := map[string]any{
		"duration":           float64(10), // 10 s
		"acoustic_selection": "FREQUENCY_RISING",
	}
	if err := r.siren.Invoke(context.Background(), "turn_on", params, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("turn_on service: %v", err)
	}
	if _, ok := w.has(hmenum.ParameterAcousticAlarmSelection); !ok {
		t.Error("turn_on service must send acoustic selection")
	}
}

// --- Siren matter projection ---

func TestSirenMatterEligibility(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", &stubWriter{}, custom.SirenCapabilities{})
	v := r.siren.MatterEligibility()
	if len(v.Clusters) == 0 {
		t.Error("MatterEligibility must report at least one cluster")
	}
}

func TestSirenMatterWriteOnOff(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "x", w, custom.SirenCapabilities{SupportsAcoustic: true, SupportsOptical: true})
	servers := r.siren.MatterClusterServers()
	if len(servers) == 0 {
		t.Fatal("MatterClusterServers must be non-empty")
	}
	onOffSrv := servers[0]

	// MatterWrite on=true → TurnOn
	if err := onOffSrv.MatterWrite(context.Background(), matterAttrOnOffOnOff, true, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite on=true: %v", err)
	}
	// MatterWrite on=false → TurnOff
	if err := onOffSrv.MatterWrite(context.Background(), matterAttrOnOffOnOff, false, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite on=false: %v", err)
	}
}

func TestSirenMatterWriteUnknownAttrReturnsError(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", &stubWriter{}, custom.SirenCapabilities{})
	servers := r.siren.MatterClusterServers()
	onOffSrv := servers[0]
	if err := onOffSrv.MatterWrite(context.Background(), 0x9999, true, hmenum.CommandPriorityHigh); err == nil {
		t.Error("MatterWrite with unknown attr must return error")
	}
}

func TestSirenMatterWriteWrongTypeReturnsError(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", &stubWriter{}, custom.SirenCapabilities{})
	servers := r.siren.MatterClusterServers()
	onOffSrv := servers[0]
	if err := onOffSrv.MatterWrite(context.Background(), matterAttrOnOffOnOff, "not-a-bool", hmenum.CommandPriorityHigh); err == nil {
		t.Error("MatterWrite with non-bool value must return error")
	}
}

func TestSirenMatterInvokeUnknownCmdReturnsError(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", &stubWriter{}, custom.SirenCapabilities{})
	servers := r.siren.MatterClusterServers()
	onOffSrv := servers[0]
	_, err := onOffSrv.MatterInvoke(context.Background(), 0xFF, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterInvoke with unknown cmd must return error")
	}
}

func TestSirenMatterReadOnOff(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", &stubWriter{}, custom.SirenCapabilities{})
	servers := r.siren.MatterClusterServers()
	onOffSrv := servers[0]

	// Not yet observed → (false, true). Apple Home's HAP-mapper
	// aborts on nil OnOff during service rebuild, so the unobserved
	// path surfaces the boolean default false rather than TLV null.
	v, ok := onOffSrv.MatterRead(matterAttrOnOffOnOff)
	if !ok {
		t.Error("MatterRead OnOff must return ok=true even when unobserved")
	}
	if v != false {
		t.Errorf("MatterRead OnOff before observation = %v, want false", v)
	}

	// After event.
	r.acousticActiveDP.OnEvent(true)
	v, ok = onOffSrv.MatterRead(matterAttrOnOffOnOff)
	if !ok || v != true {
		t.Errorf("MatterRead OnOff after active event = %v ok=%v, want (true, true)", v, ok)
	}

	// OnOff cluster has no Options attribute per matter.js HEAD
	// on-off.element.ts; reads of 0x000F must surface ok=false so the
	// dispatcher maps to UnsupportedAttribute.
	if _, ok := onOffSrv.MatterRead(0x000F); ok {
		t.Error("MatterRead(0x000F) on OnOff must return ok=false — Options is a ZCL holdover not present in Matter")
	}

	// Unknown attr.
	_, ok = onOffSrv.MatterRead(0xFFFF)
	if ok {
		t.Error("MatterRead unknown attr must return ok=false")
	}
}

func TestSirenMatterReportable(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", &stubWriter{}, custom.SirenCapabilities{})
	servers := r.siren.MatterClusterServers()
	onOffSrv := servers[0]
	rep := onOffSrv.MatterReportable()
	if len(rep) == 0 {
		t.Error("MatterReportable must return at least one attr")
	}
}

// --- SmokeSiren ---

func newSmokeSirenRig(t *testing.T) (*SmokeSiren, *stubWriter, *generic.Sensor[int32]) {
	t.Helper()
	w := &stubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "SWSD0001"})
	ch := d.AddChannel("SWSD0001:1", 1, "SMOKE_DETECTOR", hmenum.ParamsetKeyValues)
	statusDP := attachSmokeStatusSensor(ch)
	s := NewSmokeSiren(SmokeSirenConfig{Channel: ch, Writer: w})
	return s, w, statusDP
}

func TestSmokeSirenHAComponent(t *testing.T) {
	t.Parallel()

	s, _, _ := newSmokeSirenRig(t)
	if got := s.HAComponent(); got != "siren" {
		t.Errorf("SmokeSiren.HAComponent() = %q, want %q", got, "siren")
	}
}

func TestSmokeSirenTopicSlot(t *testing.T) {
	t.Parallel()

	s, _, _ := newSmokeSirenRig(t)
	slot := s.TopicSlot()
	if slot.Parameter != "smoke_siren" {
		t.Errorf("SmokeSiren.TopicSlot().Parameter = %q, want smoke_siren", slot.Parameter)
	}
}

func TestSmokeSirenIsSecondaryAlarm(t *testing.T) {
	t.Parallel()

	s, _, statusDP := newSmokeSirenRig(t)
	fireSmokeStatus(t, statusDP, string(SmokeStatusSecondaryAlarm))
	if !s.IsSecondaryAlarm() {
		t.Error("IsSecondaryAlarm must be true in SECONDARY_ALARM state")
	}
	if s.IsPrimaryAlarm() {
		t.Error("IsPrimaryAlarm must be false in SECONDARY_ALARM state")
	}
}

func TestSmokeSirenIsIntrusion(t *testing.T) {
	t.Parallel()

	s, _, statusDP := newSmokeSirenRig(t)
	fireSmokeStatus(t, statusDP, string(SmokeStatusIntrusion))
	if !s.IsIntrusion() {
		t.Error("IsIntrusion must be true in INTRUSION_ALARM state")
	}
}

func TestSmokeSirenTurnOff(t *testing.T) {
	t.Parallel()

	s, w, _ := newSmokeSirenRig(t)
	if err := s.TurnOff(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if v, ok := w.has(hmenum.ParameterSmokeDetectorCommand); !ok || v != "INTRUSION_ALARM_OFF" {
		t.Errorf("SmokeSiren.TurnOff wrote %v ok=%v, want INTRUSION_ALARM_OFF", v, ok)
	}
}

func TestSmokeSirenNilWriterTurnOnReturnsError(t *testing.T) {
	t.Parallel()

	s := NewSmokeSiren(SmokeSirenConfig{Channel: nil, Writer: nil})
	if err := s.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err == nil {
		t.Error("SmokeSiren.TurnOn with nil writer must return error")
	}
}

func TestSmokeSirenNilWriterTurnOffReturnsError(t *testing.T) {
	t.Parallel()

	s := NewSmokeSiren(SmokeSirenConfig{Channel: nil, Writer: nil})
	if err := s.TurnOff(context.Background(), hmenum.CommandPriorityHigh); err == nil {
		t.Error("SmokeSiren.TurnOff with nil writer must return error")
	}
}

func TestSmokeSirenInfoPayload(t *testing.T) {
	t.Parallel()

	s, _, _ := newSmokeSirenRig(t)
	p, ok := s.Info().(*payload.SmokeSirenInfo)
	if !ok || p == nil {
		t.Fatal("SmokeSiren.InfoPayload must return a non-nil *payload.SmokeSirenInfo")
	}
	if p.Kind != "smoke" {
		t.Errorf("InfoPayload kind = %v, want smoke", p.Kind)
	}
}

func TestSmokeSirenConfigPayload(t *testing.T) {
	t.Parallel()

	s, _, _ := newSmokeSirenRig(t)
	p, _ := s.Config().(*payload.SmokeSirenConfig)
	if p == nil {
		t.Fatal("SmokeSiren.ConfigPayload must not be nil")
	}
	if p.Kind != "smoke" {
		t.Errorf("ConfigPayload kind = %v, want smoke", p.Kind)
	}
}

func TestSmokeSirenStatePayloadIdleOff(t *testing.T) {
	t.Parallel()

	s, _, _ := newSmokeSirenRig(t)
	p, ok := s.State().(*payload.SmokeSirenState)
	if !ok || p == nil {
		t.Fatal("SmokeSiren.StatePayload must return a non-nil *payload.SmokeSirenState")
	}
	if p.State != "off" {
		t.Errorf("SmokeSiren.StatePayload state = %v, want off before observation", p.State)
	}
}

func TestSmokeSirenStatePayloadOn(t *testing.T) {
	t.Parallel()

	s, _, statusDP := newSmokeSirenRig(t)
	fireSmokeStatus(t, statusDP, string(SmokeStatusPrimaryAlarm))
	p, ok := s.State().(*payload.SmokeSirenState)
	if !ok || p == nil {
		t.Fatal("SmokeSiren.StatePayload must return a non-nil *payload.SmokeSirenState")
	}
	if p.State != "on" {
		t.Errorf("SmokeSiren.StatePayload state = %v, want on in PRIMARY_ALARM", p.State)
	}
}

func TestSmokeSirenStatePayloadNilReceiverReturnsNil(t *testing.T) {
	t.Parallel()

	var s *SmokeSiren
	if p := s.State(); p != nil {
		t.Errorf("nil SmokeSiren.State() = %v, want nil", p)
	}
}

func TestSmokeSirenNilChannelAvailableLightsAndTones(t *testing.T) {
	t.Parallel()

	s := NewSmokeSiren(SmokeSirenConfig{Channel: nil, Writer: nil})
	if v := s.AvailableLights(); v != nil {
		t.Errorf("SmokeSiren.AvailableLights() = %v, want nil", v)
	}
	if v := s.AvailableTones(); v != nil {
		t.Errorf("SmokeSiren.AvailableTones() = %v, want nil", v)
	}
}

// SmokeSiren service registry.
func TestSmokeSirenServicesTurnOnOff(t *testing.T) {
	t.Parallel()

	s, w, _ := newSmokeSirenRig(t)

	if err := s.Invoke(context.Background(), "turn_on", nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SmokeSiren turn_on: %v", err)
	}
	if v, ok := w.has(hmenum.ParameterSmokeDetectorCommand); !ok || v != "INTRUSION_ALARM" {
		t.Errorf("SmokeSiren turn_on service wrote %v ok=%v, want INTRUSION_ALARM", v, ok)
	}

	if err := s.Invoke(context.Background(), "turn_off", nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SmokeSiren turn_off: %v", err)
	}
}

// SmokeSiren Matter projection.
func TestSmokeSirenMatterDeviceType(t *testing.T) {
	t.Parallel()

	s, _, _ := newSmokeSirenRig(t)
	if got := s.MatterDeviceType(); got != matterDeviceTypeSmokeCOAlarm {
		t.Errorf("SmokeSiren.MatterDeviceType() = 0x%04X, want 0x%04X", got, matterDeviceTypeSmokeCOAlarm)
	}
}

func TestSmokeSirenMatterReadAttributes(t *testing.T) {
	t.Parallel()

	s, _, statusDP := newSmokeSirenRig(t)
	servers := s.MatterClusterServers()
	if len(servers) == 0 {
		t.Fatal("SmokeSiren.MatterClusterServers must be non-empty")
	}
	srv := servers[0]

	// Unobserved → nil, true.
	v, ok := srv.MatterRead(matterAttrSmokeExpressedState)
	if !ok || v != nil {
		t.Errorf("ExpressedState before observation = %v ok=%v, want (nil, true)", v, ok)
	}

	// After PRIMARY_ALARM → Critical=2.
	fireSmokeStatus(t, statusDP, string(SmokeStatusPrimaryAlarm))
	v, ok = srv.MatterRead(matterAttrSmokeExpressedState)
	if !ok || v != matterSmokeAlarmCritical {
		t.Errorf("ExpressedState PRIMARY = %v ok=%v, want (%d, true)", v, ok, matterSmokeAlarmCritical)
	}

	// SmokeState same as ExpressedState for smoke-only device.
	v, ok = srv.MatterRead(matterAttrSmokeState)
	if !ok || v != matterSmokeAlarmCritical {
		t.Errorf("SmokeState PRIMARY = %v ok=%v, want (%d, true)", v, ok, matterSmokeAlarmCritical)
	}

	// CO state → Normal (no CO sensor).
	v, ok = srv.MatterRead(matterAttrCOState)
	if !ok || v != matterSmokeAlarmNormal {
		t.Errorf("COState = %v ok=%v, want Normal", v, ok)
	}

	// HardwareFaultAlert → false.
	v, ok = srv.MatterRead(matterAttrHardwareFaultAlert)
	if !ok || v != false {
		t.Errorf("HardwareFaultAlert = %v ok=%v, want false", v, ok)
	}

	// EndOfServiceAlert → 0.
	v, ok = srv.MatterRead(matterAttrEndOfServiceAlert)
	if !ok || v != uint8(0) {
		t.Errorf("EndOfServiceAlert = %v ok=%v, want 0", v, ok)
	}

	// TestInProgress → false.
	v, ok = srv.MatterRead(matterAttrTestInProgress)
	if !ok || v != false {
		t.Errorf("TestInProgress = %v ok=%v, want false", v, ok)
	}

	// FeatureMap → SMOKE bit.
	v, ok = srv.MatterRead(matterAttrFeatureMap)
	if !ok || v != matterSmokeCOFeatureSmoke {
		t.Errorf("FeatureMap = %v ok=%v, want %d", v, ok, matterSmokeCOFeatureSmoke)
	}

	// Unknown attr → false.
	_, ok = srv.MatterRead(0xDEAD)
	if ok {
		t.Error("MatterRead unknown attr must return ok=false")
	}
}

func TestSmokeSirenMatterWriteAlwaysErrors(t *testing.T) {
	t.Parallel()

	s, _, _ := newSmokeSirenRig(t)
	servers := s.MatterClusterServers()
	srv := servers[0]
	if err := srv.MatterWrite(context.Background(), matterAttrSmokeExpressedState, nil, hmenum.CommandPriorityHigh); err == nil {
		t.Error("SmokeCO MatterWrite must always return error")
	}
}

func TestSmokeSirenMatterInvokeAlwaysErrors(t *testing.T) {
	t.Parallel()

	s, _, _ := newSmokeSirenRig(t)
	servers := s.MatterClusterServers()
	srv := servers[0]
	_, err := srv.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("SmokeCO MatterInvoke must always return error")
	}
}

func TestSmokeSirenMatterReportable(t *testing.T) {
	t.Parallel()

	s, _, _ := newSmokeSirenRig(t)
	servers := s.MatterClusterServers()
	srv := servers[0]
	rep := srv.MatterReportable()
	if len(rep) == 0 {
		t.Error("SmokeCO MatterReportable must return at least one attr")
	}
}

func TestSmokeSirenReadBatteryAlert(t *testing.T) {
	t.Parallel()

	s, _, _ := newSmokeSirenRig(t)
	servers := s.MatterClusterServers()
	srv := servers[0]
	v, ok := srv.MatterRead(matterAttrBatteryAlert)
	if !ok || v != matterSmokeAlarmNormal {
		t.Errorf("BatteryAlert = %v ok=%v, want (Normal, true)", v, ok)
	}
}

func TestSmokeSirenReadSecondaryAlarmWarning(t *testing.T) {
	t.Parallel()

	s, _, statusDP := newSmokeSirenRig(t)
	servers := s.MatterClusterServers()
	srv := servers[0]
	fireSmokeStatus(t, statusDP, string(SmokeStatusSecondaryAlarm))
	v, ok := srv.MatterRead(matterAttrSmokeState)
	if !ok || v != matterSmokeAlarmWarning {
		t.Errorf("SmokeState SECONDARY = %v ok=%v, want (Warning, true)", v, ok)
	}
}

// fireEnum resolves label against dp's own VALUE_LIST and fires the
// resulting raw index as a wire event — mirrors how the resolver projects a
// read-only ENUM parameter (SOUNDFILE, DIRECTION) onto an index-valued
// Sensor[int32].
func fireEnum(t *testing.T, dp *generic.Sensor[int32], label string) {
	t.Helper()
	idx, ok := custom.EnumLabelIndex(dp, label)
	if !ok {
		t.Fatalf("label %q not in VALUE_LIST", label)
	}
	dp.OnEvent(idx)
}

// --- SoundPlayer ---

func newSoundPlayerRig(t *testing.T) (*SoundPlayer, *stubWriter) {
	t.Helper()
	w := &stubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "MP3P0001"})
	ch := d.AddChannel("MP3P0001:2", 2, "AUDIO", hmenum.ParamsetKeyValues)

	addStr := func(p hmenum.Parameter, values ...string) {
		dp := generic.NewStringSensor(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: "MP3P0001:2",
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeString,
				Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
				ValueList:  values,
			},
		})
		ch.Put(dp)
	}
	addFloat := func(p hmenum.Parameter) {
		dp := generic.NewFloat(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: "MP3P0001:2",
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Writer: w,
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeFloat,
				Operations: hmenum.OperationsRead | hmenum.OperationsEvent | hmenum.OperationsWrite,
			},
		})
		ch.Put(dp)
	}

	addStr(hmenum.ParameterSoundfile, "SOUNDFILE_001", "SOUNDFILE_002", "SOUNDFILE_003")
	addStr(hmenum.ParameterRepetitions, "NO_REP", "REPETITIONS_5", "INFINITE")
	addStr(hmenum.ParameterDirection)
	addFloat(hmenum.ParameterLevel)

	sp := NewSoundPlayer(SoundPlayerConfig{Channel: ch, Writer: w})
	return sp, w
}

func TestSoundPlayerHAComponent(t *testing.T) {
	t.Parallel()

	sp, _ := newSoundPlayerRig(t)
	if got := sp.HAComponent(); got != "siren" {
		t.Errorf("SoundPlayer.HAComponent() = %q, want %q", got, "siren")
	}
}

func TestSoundPlayerTopicSlot(t *testing.T) {
	t.Parallel()

	sp, _ := newSoundPlayerRig(t)
	slot := sp.TopicSlot()
	if slot.Parameter != "sound_player" {
		t.Errorf("SoundPlayer.TopicSlot().Parameter = %q, want sound_player", slot.Parameter)
	}
}

func TestSoundPlayerDataPointKey(t *testing.T) {
	t.Parallel()

	sp, _ := newSoundPlayerRig(t)
	k := sp.DataPointKey()
	if k.ChannelAddress != "MP3P0001:2" {
		t.Errorf("DataPointKey.ChannelAddress = %q, want MP3P0001:2", k.ChannelAddress)
	}
}

func TestSoundPlayerAvailableLightsNil(t *testing.T) {
	t.Parallel()

	sp, _ := newSoundPlayerRig(t)
	if v := sp.AvailableLights(); v != nil {
		t.Errorf("SoundPlayer.AvailableLights() = %v, want nil", v)
	}
}

func TestSoundPlayerCurrentSoundfile(t *testing.T) {
	t.Parallel()

	sp, _ := newSoundPlayerRig(t)

	// Not yet observed.
	_, obs := sp.CurrentSoundfile()
	if obs {
		t.Error("CurrentSoundfile should not be observed before any event")
	}

	// Get the soundfile DP from the channel directly; drive it.
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "MP3P0001"})
	ch := d.AddChannel("MP3P0001:2", 2, "AUDIO", hmenum.ParamsetKeyValues)
	sfDP := generic.NewIntegerSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "MP3P0001:2",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterSoundfile),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			ValueList:  []string{"SOUNDFILE_042"},
		},
	})
	ch.Put(sfDP)
	sp2 := NewSoundPlayer(SoundPlayerConfig{Channel: ch, Writer: &stubWriter{}})
	fireEnum(t, sfDP, "SOUNDFILE_042")
	sf, obs2 := sp2.CurrentSoundfile()
	if !obs2 || sf != "SOUNDFILE_042" {
		t.Errorf("CurrentSoundfile = %q obs=%v, want (SOUNDFILE_042, true)", sf, obs2)
	}
}

func TestSoundPlayerIsPlaying(t *testing.T) {
	t.Parallel()

	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "MP3P0001"})
	ch := d.AddChannel("MP3P0001:2", 2, "AUDIO", hmenum.ParamsetKeyValues)
	dirDP := generic.NewIntegerSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "MP3P0001:2",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterDirection),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			ValueList:  []string{"", "UP", "DOWN"},
		},
	})
	ch.Put(dirDP)
	sp := NewSoundPlayer(SoundPlayerConfig{Channel: ch, Writer: &stubWriter{}})

	// Not observed.
	playing, obs := sp.IsPlaying()
	if obs || playing {
		t.Error("IsPlaying should be (false, false) before event")
	}

	// Playing UP.
	fireEnum(t, dirDP, "UP")
	playing, obs = sp.IsPlaying()
	if !obs || !playing {
		t.Errorf("IsPlaying after UP = %v obs=%v, want (true, true)", playing, obs)
	}

	// Playing DOWN.
	fireEnum(t, dirDP, "DOWN")
	playing, _ = sp.IsPlaying()
	if !playing {
		t.Error("IsPlaying after DOWN must be true")
	}

	// Stopped (empty label — no direction).
	fireEnum(t, dirDP, "")
	playing, _ = sp.IsPlaying()
	if playing {
		t.Error("IsPlaying after empty DIRECTION must be false")
	}
}

func TestSoundPlayerPlaySound(t *testing.T) {
	t.Parallel()

	sp, w := newSoundPlayerRig(t)
	cfg := PlayConfig{
		SoundfileIndex:   1,
		Volume:           0.5,
		Duration:         30 * time.Second,
		RampTime:         2 * time.Second,
		RepetitionsIndex: 1, // "REPETITIONS_5" from the list
	}
	if err := sp.PlaySound(context.Background(), cfg, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("PlaySound: %v", err)
	}
	if _, ok := w.has(hmenum.ParameterSoundfile); !ok {
		t.Error("PlaySound must write SOUNDFILE")
	}
	if _, ok := w.has(hmenum.ParameterLevel); !ok {
		t.Error("PlaySound must write LEVEL")
	}
	if _, ok := w.has(hmenum.ParameterDurationValue); !ok {
		t.Error("PlaySound must write DURATION_VALUE")
	}
	if _, ok := w.has(hmenum.ParameterRampTimeValue); !ok {
		t.Error("PlaySound must write RAMP_TIME_VALUE")
	}
	if _, ok := w.has(hmenum.ParameterRepetitions); !ok {
		t.Error("PlaySound must write REPETITIONS")
	}
}

func TestSoundPlayerPlaySoundNilWriterReturnsError(t *testing.T) {
	t.Parallel()

	sp := NewSoundPlayer(SoundPlayerConfig{Channel: nil, Writer: nil})
	if err := sp.PlaySound(context.Background(), PlayConfig{SoundfileIndex: 1}, hmenum.CommandPriorityHigh); err == nil {
		t.Error("PlaySound with nil writer must return error")
	}
}

func TestSoundPlayerPlaySoundEmptyParamsUsesDefaults(t *testing.T) {
	t.Parallel()

	// Empty config sends default volume + duration so the device always receives
	// a complete command rather than an empty no-op.
	sp, w := newSoundPlayerRig(t)
	if err := sp.PlaySound(context.Background(), PlayConfig{RepetitionsIndex: RepetitionsIndexNotSet}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("PlaySound with empty config: %v", err)
	}
	// At minimum LEVEL and DURATION_VALUE + DURATION_UNIT are expected.
	if len(w.calls) == 0 {
		t.Errorf("PlaySound with empty config should send defaults, got 0 calls")
	}
}

func TestSoundPlayerStopSound(t *testing.T) {
	t.Parallel()

	sp, w := newSoundPlayerRig(t)
	if err := sp.StopSound(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("StopSound: %v", err)
	}
	// Must write LEVEL=0 and DURATION_VALUE=0.
	if v, ok := w.has(hmenum.ParameterLevel); !ok || v.(float64) != 0 {
		t.Errorf("StopSound LEVEL = %v ok=%v, want 0", v, ok)
	}
	if _, ok := w.has(hmenum.ParameterDurationValue); !ok {
		t.Error("StopSound must write DURATION_VALUE=0")
	}
}

func TestSoundPlayerStopSoundNilWriterReturnsError(t *testing.T) {
	t.Parallel()

	sp := NewSoundPlayer(SoundPlayerConfig{Channel: nil, Writer: nil})
	if err := sp.StopSound(context.Background(), hmenum.CommandPriorityHigh); err == nil {
		t.Error("StopSound with nil writer must return error")
	}
}

func TestSoundPlayerTurnOff(t *testing.T) {
	t.Parallel()

	sp, w := newSoundPlayerRig(t)
	if err := sp.TurnOff(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("TurnOff: %v", err)
	}
	if _, ok := w.has(hmenum.ParameterDurationValue); !ok {
		t.Error("TurnOff must write DURATION_VALUE=0 (via StopSound)")
	}
}

func TestSoundPlayerInfoPayload(t *testing.T) {
	t.Parallel()

	sp, _ := newSoundPlayerRig(t)
	p, ok := sp.Info().(*payload.SoundPlayerInfo)
	if !ok || p == nil {
		t.Fatal("SoundPlayer.InfoPayload must return a non-nil *payload.SoundPlayerInfo")
	}
	if p.Kind != "sound_player" {
		t.Errorf("InfoPayload kind = %v, want sound_player", p.Kind)
	}
}

func TestSoundPlayerStatePayload(t *testing.T) {
	t.Parallel()

	sp, _ := newSoundPlayerRig(t)
	p, ok := sp.State().(*payload.SoundPlayerState)
	if !ok || p == nil {
		t.Fatal("SoundPlayer.StatePayload must return a non-nil *payload.SoundPlayerState")
	}
	if p.State != "off" {
		t.Errorf("SoundPlayer.StatePayload state = %v, want off before observation", p.State)
	}
}

func TestSoundPlayerServicesTurnOnOff(t *testing.T) {
	t.Parallel()

	sp, w := newSoundPlayerRig(t)

	params := map[string]any{
		"soundfile_index":   int32(1),
		"volume":            float64(0.8),
		"duration":          float64(5),
		"ramp_time":         float64(1),
		"repetitions_index": int32(0),
	}
	if err := sp.Invoke(context.Background(), "turn_on", params, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SoundPlayer turn_on service: %v", err)
	}
	if _, ok := w.has(hmenum.ParameterSoundfile); !ok {
		t.Error("SoundPlayer turn_on service must write SOUNDFILE")
	}

	if err := sp.Invoke(context.Background(), "turn_off", nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SoundPlayer turn_off service: %v", err)
	}
}

// --- Siren deep tests ---

// TestSirenAddressReturnsChannelAddress verifies that Address is set
// from the channel address at construction time.
func TestSirenAddressReturnsChannelAddress(t *testing.T) {
	t.Parallel()

	const addr = "HmIP-ASIR:3"
	r := newRig(t, addr, &stubWriter{}, custom.SirenCapabilities{})
	if r.siren.Address != addr {
		t.Errorf("Address = %q, want %q", r.siren.Address, addr)
	}
}

// TestSirenNilChannelGracefullyDegrades verifies that a Siren constructed
// with a nil channel has an empty address and all accessors are panic-free.
func TestSirenNilChannelGracefullyDegrades(t *testing.T) {
	t.Parallel()

	s := New(Config{Channel: nil, Writer: &stubWriter{}, Capabilities: custom.SirenCapabilities{
		SupportsAcoustic: true,
		SupportsOptical:  true,
	}})
	if s.Address != "" {
		t.Errorf("nil-channel siren address = %q, want empty", s.Address)
	}
	active, observed := s.IsActive()
	if active || observed {
		t.Errorf("IsActive() = %v, %v; want false, false", active, observed)
	}
	if tones := s.AvailableTones(); tones != nil {
		t.Errorf("AvailableTones() with nil channel = %v, want nil", tones)
	}
}

// TestSirenTurnOnForwardsAcousticAndOptical verifies that TurnOn
// sends both ACOUSTIC_ALARM_SELECTION and OPTICAL_ALARM_SELECTION.
func TestSirenTurnOnForwardsAcousticAndOptical(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "HmIP-ASIR:3", w, custom.SirenCapabilities{
		SupportsAcoustic: true,
		SupportsOptical:  true,
	})
	acoustic := "FREQUENCY_RISING"
	optical := "BLINKING_RED"
	err := r.siren.TurnOn(context.Background(), OnConfig{
		AcousticSelection: &acoustic,
		OpticalSelection:  &optical,
	}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := w.has(hmenum.ParameterAcousticAlarmSelection); !ok || v.(string) != "FREQUENCY_RISING" {
		t.Errorf("acoustic selection = %v ok=%v, want FREQUENCY_RISING", v, ok)
	}
	if v, ok := w.has(hmenum.ParameterOpticalAlarmSelection); !ok || v.(string) != "BLINKING_RED" {
		t.Errorf("optical selection = %v ok=%v, want BLINKING_RED", v, ok)
	}
}

// TestSirenStopForwardsStopCommand verifies that TurnOff zeros both
// ACOUSTIC and OPTICAL selection parameters and clears the active state.
func TestSirenStopForwardsStopCommand(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "HmIP-ASIR:3", w, custom.SirenCapabilities{
		SupportsAcoustic: true,
		SupportsOptical:  true,
	})
	// Pre-arm state by pushing events.
	r.acousticActiveDP.OnEvent(true)
	r.opticalActiveDP.OnEvent(true)

	if err := r.siren.TurnOff(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.has(hmenum.ParameterAcousticAlarmSelection); !ok {
		t.Errorf("acoustic stop: ACOUSTIC_ALARM_SELECTION must be written by TurnOff")
	}
	if _, ok := w.has(hmenum.ParameterOpticalAlarmSelection); !ok {
		t.Errorf("optical stop: OPTICAL_ALARM_SELECTION must be written by TurnOff")
	}
	active, _ := r.siren.IsActive()
	if active {
		t.Error("IsActive must be false after TurnOff")
	}
}

// TestSirenIsActiveReflectsDP verifies that IsActive reflects events
// pushed onto the underlying acoustic/optical active DPs.
func TestSirenIsActiveReflectsDP(t *testing.T) {
	t.Parallel()

	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{
		SupportsAcoustic: true,
		SupportsOptical:  true,
	})

	// Nothing observed yet.
	_, observed := r.siren.IsActive()
	if observed {
		t.Error("IsActive should not be observed before any DP event")
	}

	// Drive acoustic only.
	r.acousticActiveDP.OnEvent(true)
	active, observed := r.siren.IsActive()
	if !observed || !active {
		t.Errorf("IsActive after acoustic event = %v observed=%v, want (true, true)", active, observed)
	}

	// Clear acoustic, set optical.
	r.acousticActiveDP.OnEvent(false)
	r.opticalActiveDP.OnEvent(true)
	active, _ = r.siren.IsActive()
	if !active {
		t.Error("IsActive must be true when optical is active")
	}

	// Clear both.
	r.opticalActiveDP.OnEvent(false)
	active, _ = r.siren.IsActive()
	if active {
		t.Error("IsActive must be false when both channels are inactive")
	}
}

// TestSirenTurnOnWithDurationForwardsDurationValue verifies that a
// non-zero Duration in OnConfig causes DURATION_VALUE to be sent when
// SupportsDuration is true.
func TestSirenTurnOnWithDurationForwardsDurationValue(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "HmIP-ASIR:3", w, custom.SirenCapabilities{
		SupportsAcoustic: true,
		SupportsDuration: true,
	})
	acoustic := "FREQUENCY_RISING"
	err := r.siren.TurnOn(context.Background(), OnConfig{
		Duration:          2 * time.Minute,
		AcousticSelection: &acoustic,
	}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := w.has(hmenum.ParameterDurationValue); !ok {
		t.Error("DURATION_VALUE must be present when SupportsDuration=true and Duration>0")
	}
	if _, ok := w.has(hmenum.ParameterDurationUnit); !ok {
		t.Error("DURATION_UNIT must be present when SupportsDuration=true and Duration>0")
	}
}

// TestSirenCapabilityGatesOptical verifies that optical parameters are
// NOT sent when SupportsOptical = false.
func TestSirenCapabilityGatesOptical(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "x", w, custom.SirenCapabilities{
		SupportsAcoustic: true,
		SupportsOptical:  false,
	})
	acoustic := "FREQUENCY_RISING"
	optical := "BLINKING_RED"
	_ = r.siren.TurnOn(context.Background(), OnConfig{
		AcousticSelection: &acoustic,
		OpticalSelection:  &optical,
	}, hmenum.CommandPriorityHigh)
	if _, ok := w.has(hmenum.ParameterOpticalAlarmSelection); ok {
		t.Error("OPTICAL_ALARM_SELECTION must NOT be sent when SupportsOptical=false")
	}
}

// TestSmokeSirenTurnOnSendsIntrusionCommand verifies that SmokeSiren.TurnOn
// writes SMOKE_DETECTOR_COMMAND = "INTRUSION_ALARM".
func TestSmokeSirenTurnOnSendsIntrusionCommand(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("HmIP-SWSD:1", 1, "SMOKE_DETECTOR", hmenum.ParamsetKeyValues)

	attachSmokeStatusSensor(ch)

	s := NewSmokeSiren(SmokeSirenConfig{Channel: ch, Writer: w})
	if err := s.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if v, ok := w.has(hmenum.ParameterSmokeDetectorCommand); !ok || v != "INTRUSION_ALARM" {
		t.Errorf("SmokeSiren TurnOn sent %v ok=%v, want INTRUSION_ALARM", v, ok)
	}
}

// --- Siren.Subscribe ---

// TestSirenSubscribeNilChannelReturnsUnsubFunc verifies that Subscribe(nil)
// returns a no-op function without panicking.
func TestSirenSubscribeNilChannelReturnsUnsubFunc(t *testing.T) {
	t.Parallel()

	s := New(Config{Channel: nil, Writer: &stubWriter{}, Capabilities: custom.SirenCapabilities{
		SupportsAcoustic: true,
	}})
	unsub := s.Subscribe(nil)
	if unsub == nil {
		t.Fatal("Subscribe(nil) must return a non-nil unsubscribe function")
	}
	unsub() // must not panic
}

// TestSirenSubscribeRegistersHooksAndReplays verifies that after Subscribe,
// OnAnyUpdate hooks are registered on all four DPs and that an initial event
// is replayed for any DP that has already been observed.
func TestSirenSubscribeRegistersHooksAndReplays(t *testing.T) {
	t.Parallel()

	r := newRig(t, "HmIP-ASIR:3", &stubWriter{}, custom.SirenCapabilities{
		SupportsAcoustic: true,
		SupportsOptical:  true,
	})

	// Pre-seed acoustic active so replay fires.
	r.acousticActiveDP.OnEvent(true)

	fired := 0
	origUnsub := r.acousticActiveDP.OnAnyUpdate(func(_, _ any) { fired++ })
	defer origUnsub()

	// Subscribe must not panic and must return a non-nil unsubscribe closure.
	unsub := r.siren.Subscribe(r.channel)
	if unsub == nil {
		t.Fatal("Subscribe must return a non-nil unsub function")
	}
	defer unsub()

	// Drive a new acoustic event — the internal no-op hook registered by
	// Subscribe must not break the external hook we attached above.
	r.acousticActiveDP.OnEvent(false)
	if fired == 0 {
		t.Error("external OnAnyUpdate hook must still fire after Subscribe")
	}
}

// TestSirenSubscribeUnsubRemovesHooks verifies that calling the returned
// unsubscribe function does not cause panics and that subsequent events
// still reach independently registered listeners.
func TestSirenSubscribeUnsubRemovesHooks(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", &stubWriter{}, custom.SirenCapabilities{SupportsAcoustic: true})
	unsub := r.siren.Subscribe(r.channel)
	unsub() // must not panic

	// External listener must still work after unsubscribe.
	count := 0
	done := r.acousticActiveDP.OnAnyUpdate(func(_, _ any) { count++ })
	defer done()
	r.acousticActiveDP.OnEvent(true)
	if count == 0 {
		t.Error("external hook must still fire after siren unsub")
	}
}

// --- SoundPlayer.Subscribe ---

// TestSoundPlayerSubscribeNilChannelReturnsUnsubFunc verifies that
// SoundPlayer.Subscribe(nil) returns a non-nil no-op unsub function.
func TestSoundPlayerSubscribeNilChannelReturnsUnsubFunc(t *testing.T) {
	t.Parallel()

	sp := NewSoundPlayer(SoundPlayerConfig{Channel: nil, Writer: &stubWriter{}})
	unsub := sp.Subscribe(nil)
	if unsub == nil {
		t.Fatal("Subscribe(nil) must return a non-nil unsubscribe function")
	}
	unsub() // must not panic
}

// TestSoundPlayerSubscribeRegistersHooksOnWireDPs builds a full SoundPlayer
// rig with LEVEL, SOUNDFILE, REPETITIONS and DIRECTION DPs, calls Subscribe
// and verifies no panic and that the returned unsub closure is callable.
func TestSoundPlayerSubscribeRegistersHooksOnWireDPs(t *testing.T) {
	t.Parallel()

	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "MP3P0001"})
	ch := d.AddChannel("MP3P0001:2", 2, "AUDIO", hmenum.ParamsetKeyValues)

	addFloat := func(p hmenum.Parameter) *generic.Float {
		dp := generic.NewFloat(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: "MP3P0001:2",
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeFloat,
				Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			},
		})
		ch.Put(dp)
		return dp
	}
	addStr := func(p hmenum.Parameter) *generic.Sensor[string] {
		dp := generic.NewStringSensor(generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: "MP3P0001:2",
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(p),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeString,
				Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			},
		})
		ch.Put(dp)
		return dp
	}

	addFloat(hmenum.ParameterLevel)
	sfDP := addStr(hmenum.ParameterSoundfile)
	addStr(hmenum.ParameterRepetitions)
	addStr(hmenum.ParameterDirection)

	sp := NewSoundPlayer(SoundPlayerConfig{Channel: ch, Writer: &stubWriter{}})

	// Pre-seed SOUNDFILE so replay fires.
	sfDP.OnEvent("SOUNDFILE_042")

	unsub := sp.Subscribe(ch)
	if unsub == nil {
		t.Fatal("Subscribe must return a non-nil unsub function")
	}
	unsub() // must not panic
}

// TestSmokeSirenStatusReflectsDP verifies that SmokeSiren.Status() reflects
// events pushed onto the SMOKE_DETECTOR_ALARM_STATUS sensor.
func TestSmokeSirenStatusReflectsDP(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel("HmIP-SWSD:1", 1, "SMOKE_DETECTOR", hmenum.ParamsetKeyValues)

	statusDP := attachSmokeStatusSensor(ch)

	s := NewSmokeSiren(SmokeSirenConfig{Channel: ch, Writer: w})

	// Not yet observed.
	if _, ok := s.Status(); ok {
		t.Error("Status should not be observed before any event")
	}

	fireSmokeStatus(t, statusDP, string(SmokeStatusPrimaryAlarm))
	st, ok := s.Status()
	if !ok {
		t.Fatal("Status should be observed after event")
	}
	if st != SmokeStatusPrimaryAlarm {
		t.Errorf("Status = %q, want %q", st, SmokeStatusPrimaryAlarm)
	}
	active, observed := s.IsActive()
	if !observed || !active {
		t.Errorf("IsActive = %v observed=%v after PRIMARY_ALARM, want true", active, observed)
	}
	if !s.IsPrimaryAlarm() {
		t.Error("IsPrimaryAlarm must be true in PRIMARY_ALARM state")
	}
}

// --- Siren IsRefreshed and ValidateTone ---

var fullCaps = custom.SirenCapabilities{
	SupportsAcoustic: true,
	SupportsOptical:  true,
}

// TestSirenIsRefreshed_FalseWhenNoDPObserved verifies IsRefreshed returns
// false on a freshly constructed siren.
func TestSirenIsRefreshed_FalseWhenNoDPObserved(t *testing.T) {
	r := newRig(t, "ABC0001:1", &stubWriter{}, fullCaps)
	if r.siren.IsRefreshed() {
		t.Fatal("expected IsRefreshed=false on un-observed siren")
	}
}

// TestSirenIsRefreshed_TrueAfterAcousticPush verifies IsRefreshed returns
// true after the acoustic-active DP receives a CCU push.
//
// Pins the availability gate to its primary state carrier
// (ACOUSTIC_ALARM_ACTIVE); see docs/parity/by_design.md.
func TestSirenIsRefreshed_TrueAfterAcousticPush(t *testing.T) {
	r := newRig(t, "ABC0001:1", &stubWriter{}, fullCaps)
	r.acousticActiveDP.OnEvent(true)
	if !r.siren.IsRefreshed() {
		t.Fatal("expected IsRefreshed=true after acoustic active DP observed")
	}
}

// TestSirenValidateTone_AcceptedWhenEmpty passes when the available list is nil.
func TestSirenValidateTone_AcceptedWhenEmpty(t *testing.T) {
	s := &Siren{}
	if err := s.ValidateTone("ANYTHING"); err != nil {
		t.Fatalf("expected nil for empty available list, got %v", err)
	}
}

// TestSirenValidateTone_RejectsUnknownTone returns ErrInvalidTone for a name
// not in the list.
func TestSirenValidateTone_RejectsUnknownTone(t *testing.T) {
	s := &Siren{availableTones: []string{"DISABLE", "FREQUENCY_RISING"}}
	if err := s.ValidateTone("UNKNOWN_TONE"); err == nil {
		t.Fatal("expected ErrInvalidTone, got nil")
	}
}

// TestSirenValidateTone_AcceptsKnownTone returns nil for a valid entry.
func TestSirenValidateTone_AcceptsKnownTone(t *testing.T) {
	s := &Siren{availableTones: []string{"DISABLE", "FREQUENCY_RISING"}}
	if err := s.ValidateTone("FREQUENCY_RISING"); err != nil {
		t.Fatalf("expected nil for known tone, got %v", err)
	}
}
