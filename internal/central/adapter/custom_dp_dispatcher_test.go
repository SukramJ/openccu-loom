// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/climate"
	"github.com/SukramJ/openccu-loom/internal/model/custom/cover"
	"github.com/SukramJ/openccu-loom/internal/model/custom/light"
	"github.com/SukramJ/openccu-loom/internal/model/custom/lock"
	"github.com/SukramJ/openccu-loom/internal/model/custom/siren"
	switchdev "github.com/SukramJ/openccu-loom/internal/model/custom/switch"
	"github.com/SukramJ/openccu-loom/internal/model/custom/textdisplay"
	"github.com/SukramJ/openccu-loom/internal/model/custom/valve"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ============================================================
// Test infrastructure
// ============================================================

// dispatchWriter records SetValue calls. Satisfies generic.Writer and
// generic.ParamsetWriter so the model types actually wire up.
type dispatchWriter struct {
	mu     sync.Mutex
	sets   []setCall
	puts   []putCall
	setErr error
	putErr error
}

type setCall struct {
	addr  string
	param hmenum.Parameter
	value any
}

type putCall struct {
	addr   string
	values map[string]any
}

func (w *dispatchWriter) SetValue(
	_ context.Context, addr string, p hmenum.Parameter, v any, _ hmenum.CommandPriority,
) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.setErr != nil {
		return w.setErr
	}
	w.sets = append(w.sets, setCall{addr, p, v})
	return nil
}

func (w *dispatchWriter) PutParamset(
	_ context.Context, addr string, _ hmenum.ParamsetKey, values map[string]any, _ hmenum.CommandPriority,
) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.putErr != nil {
		return w.putErr
	}
	cp := make(map[string]any, len(values))
	maps.Copy(cp, values)
	w.puts = append(w.puts, putCall{addr, cp})
	return nil
}

func (w *dispatchWriter) lastSet() (setCall, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.sets) == 0 {
		return setCall{}, false
	}
	return w.sets[len(w.sets)-1], true
}

func (w *dispatchWriter) callCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.sets) + len(w.puts)
}

// floatDP creates a minimal *generic.Float for the given address and parameter.
func floatDP(address string, param hmenum.Parameter, w generic.Writer) *generic.Float {
	return generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
}

func intDP(address string, param hmenum.Parameter, w generic.Writer) *generic.Integer {
	return generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
}

func selectDP(address string, param hmenum.Parameter, w generic.Writer, valueList []string) *generic.Select {
	return generic.NewSelect(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			ValueList:  valueList,
		},
		Writer: w,
	})
}

// testRegistry builds a central registry with one device whose channels hold
// the given custom DP.
func testRegistry(t *testing.T, deviceAddr, dpName string, dp device.AttachableDataPoint) *central.Registry {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     deviceAddr,
		Model:       "TestDevice",
	})

	chAddr := deviceAddr + ":1"
	ch := dev.AddChannel(chAddr, 1, "TEST", hmenum.ParamsetKeyValues)
	_ = dpName
	ch.SetCustomDataPoint(dp)
	c.ModelRegistry.Put(dev)
	return reg
}

// ============================================================
// Carrier fakes for non-embeddable types
// ============================================================

// fakeClimateDP is an AttachableDataPoint + climateCarrier backed by a
// *generic.Float so it satisfies both the channel interface and the
// dispatcher's carrier pattern.
type fakeClimateDP struct {
	*generic.Float
	c *climate.Climate
}

func (f *fakeClimateDP) ClimateDP() *climate.Climate { return f.c }

// fakeLockDP is a carrier for *lock.Lock.
type fakeLockDP struct {
	*generic.Switch
	l *lock.Lock
}

func (f *fakeLockDP) LockDP() *lock.Lock { return f.l }

// fakeSirenDP is a carrier for *siren.Siren.
type fakeSirenDP struct {
	*generic.Switch
	s *siren.Siren
}

func (f *fakeSirenDP) SirenDP() *siren.Siren { return f.s }

// fakeTextDisplayDP is a carrier for *textdisplay.TextDisplay.
type fakeTextDisplayDP struct {
	*generic.Switch
	t *textdisplay.TextDisplay
}

func (f *fakeTextDisplayDP) TextDisplayDP() *textdisplay.TextDisplay { return f.t }

// ============================================================
// Audit spy
// ============================================================

type spyAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (s *spyAudit) Record(e audit.Entry) {
	s.mu.Lock()
	s.entries = append(s.entries, e)
	s.mu.Unlock()
}

func (s *spyAudit) List(_ int) []audit.Entry { return nil }

func (s *spyAudit) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// ============================================================
// Helper: build dispatcher + channel with a custom DP
// ============================================================

func buildDispatcher(t *testing.T, deviceAddr, dpName string, dp device.AttachableDataPoint) (
	*CustomDPDispatcher, *spyAudit,
) {
	t.Helper()
	reg := testRegistry(t, deviceAddr, dpName, dp)
	spy := &spyAudit{}
	d := NewCustomDPDispatcher(reg).SetAuditRecorder(spy)
	return d, spy
}

// ============================================================
// Helpers to build concrete custom DPs
// ============================================================

func buildLightDP(t *testing.T, addr string, w *dispatchWriter) *light.Light {
	t.Helper()
	dev := device.New(device.Config{Address: addr, InterfaceID: "test"})
	ch := dev.AddChannel(addr+":1", 1, "DIMMER", hmenum.ParamsetKeyValues)
	levelDP := floatDP(addr+":1", hmenum.ParameterLevel, w)
	ch.Put(levelDP)
	levelDP.OnEvent(0.8) // seed a non-zero level so TurnOn has a lastLevel
	return light.New(light.Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{Dimmable: true}})
}

func buildColorLightDP(t *testing.T, addr string, w *dispatchWriter) *light.ColorLight {
	t.Helper()
	dev := device.New(device.Config{Address: addr, InterfaceID: "test"})
	ch := dev.AddChannel(addr+":1", 1, "DIMMER", hmenum.ParamsetKeyValues)
	ch.Put(floatDP(addr+":1", hmenum.ParameterLevel, w))
	ch.Put(intDP(addr+":1", hmenum.ParameterHue, w))
	ch.Put(floatDP(addr+":1", hmenum.ParameterSaturation, w))
	return light.NewColorLight(light.Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{Dimmable: true}})
}

func buildColorTempLightDP(t *testing.T, addr string, w *dispatchWriter) *light.ColorTempLight {
	t.Helper()
	dev := device.New(device.Config{Address: addr, InterfaceID: "test"})
	ch := dev.AddChannel(addr+":1", 1, "CT", hmenum.ParamsetKeyValues)
	ch.Put(floatDP(addr+":1", hmenum.ParameterLevel, w))
	ch.Put(intDP(addr+":1", hmenum.ParameterColorTemperature, w))
	return light.NewColorTempLight(light.Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{Dimmable: true}}, 2000, 6500)
}

func buildFixedColorLightDP(t *testing.T, addr string, w *dispatchWriter) *light.FixedColorLight {
	t.Helper()
	dev := device.New(device.Config{Address: addr, InterfaceID: "test"})
	ch := dev.AddChannel(addr+":1", 1, "FC", hmenum.ParamsetKeyValues)
	ch.Put(floatDP(addr+":1", hmenum.ParameterLevel, w))
	ch.Put(selectDP(addr+":1", hmenum.ParameterColor, w,
		[]string{"BLACK", "RED", "GREEN", "YELLOW", "BLUE", "PURPLE", "TURQUOISE", "WHITE"}))
	return light.NewFixedColorLight(light.Config{Channel: ch, Writer: w, Capabilities: custom.LightCapabilities{}})
}

func buildCoverDP(t *testing.T, addr string, w *dispatchWriter) *cover.Cover {
	t.Helper()
	dev := device.New(device.Config{Address: addr, InterfaceID: "test"})
	ch := dev.AddChannel(addr+":1", 1, "COVER", hmenum.ParamsetKeyValues)
	ch.Put(floatDP(addr+":1", hmenum.ParameterLevel, w))
	return cover.New(cover.Config{Channel: ch, Writer: w, Capabilities: custom.CoverCapabilities{SupportsStop: true}})
}

func buildBlindDP(t *testing.T, addr string, w *dispatchWriter) *cover.Blind {
	t.Helper()
	dev := device.New(device.Config{Address: addr, InterfaceID: "test"})
	ch := dev.AddChannel(addr+":1", 1, "BLIND", hmenum.ParamsetKeyValues)
	ch.Put(floatDP(addr+":1", hmenum.ParameterLevel, w))
	ch.Put(floatDP(addr+":1", hmenum.ParameterLevel2, w))
	return cover.NewBlind(cover.BlindConfig{Channel: ch, Writer: w, Capabilities: custom.CoverCapabilities{SupportsStop: true}})
}

func buildClimateDP(t *testing.T, addr string, w *dispatchWriter) (*climate.Climate, *fakeClimateDP) {
	t.Helper()
	dev := device.New(device.Config{Address: addr, InterfaceID: "test"})
	ch := dev.AddChannel(addr+":1", 1, "THERMOSTAT", hmenum.ParamsetKeyValues)
	setpointDP := floatDP(addr+":1", hmenum.ParameterSetPointTemperature, w)
	ch.Put(setpointDP)

	caps := custom.ClimateCapabilities{
		SupportsBoost:   true,
		SupportsProfile: true,
		SupportsAway:    true,
		SupportsHeat:    true,
		SupportsAuto:    true,
		MinTemperature:  4.5,
		MaxTemperature:  30.5,
	}
	c := climate.New(climate.Config{Channel: ch, Writer: w, Capabilities: caps, Kind: climate.KindIP})
	carrier := &fakeClimateDP{
		Float: floatDP(addr+":1", hmenum.ParameterSetPointTemperature, w),
		c:     c,
	}
	return c, carrier
}

func buildLockDP(addr string, w *dispatchWriter) (*lock.Lock, *fakeLockDP) {
	dev := device.New(device.Config{Address: addr, InterfaceID: "test"})
	ch := dev.AddChannel(addr+":1", 1, "LOCK", hmenum.ParamsetKeyValues)
	caps := custom.LockCapabilities{SupportsOpen: true}
	l := lock.New(lock.Config{Channel: ch, Writer: w, Capabilities: caps, Kind: lock.KindRF})
	sw := custom.NewStateSwitch(addr+":1", "", w)
	carrier := &fakeLockDP{Switch: sw, l: l}
	return l, carrier
}

func buildSirenDP(addr string, w *dispatchWriter) (*siren.Siren, *fakeSirenDP) {
	dev := device.New(device.Config{Address: addr, InterfaceID: "test"})
	ch := dev.AddChannel(addr+":1", 1, "SIREN", hmenum.ParamsetKeyValues)

	caps := custom.SirenCapabilities{SupportsAcoustic: true, SupportsOptical: true, SupportsDuration: true}
	s := siren.New(siren.Config{Channel: ch, Writer: w, Capabilities: caps})
	sw := custom.NewStateSwitch(addr+":1", "", w)
	carrier := &fakeSirenDP{Switch: sw, s: s}
	return s, carrier
}

func buildTextDisplayDP(addr string, w *dispatchWriter) (*textdisplay.TextDisplay, *fakeTextDisplayDP) {
	t := textdisplay.New(addr+":1", w)
	sw := custom.NewStateSwitch(addr+":1", "", w)
	carrier := &fakeTextDisplayDP{Switch: sw, t: t}
	return t, carrier
}

func buildIrrigationDP(addr string, w *dispatchWriter) *valve.Irrigation {
	chAddr := addr + ":1"
	dev := device.New(device.Config{Address: addr, InterfaceID: "test"})
	ch := dev.AddChannel(chAddr, 1, "SWITCH", hmenum.ParamsetKeyValues)
	dp := generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
	ch.Put(dp)
	return valve.NewIrrigation(ch)
}

func buildModulatingValveDP(addr string, w *dispatchWriter) *valve.Modulating {
	chAddr := addr + ":1"
	dev := device.New(device.Config{Address: addr, InterfaceID: "test"})
	ch := dev.AddChannel(chAddr, 1, "SWITCH", hmenum.ParamsetKeyValues)
	dp := generic.NewFloat(generic.Spec{
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
	})
	ch.Put(dp)
	return valve.NewModulating(ch)
}

func buildSwitchDP(addr string, w *dispatchWriter) *switchdev.Switch {
	chAddr := addr + ":1"
	dev := device.New(device.Config{Address: addr, InterfaceID: "test"})
	ch := dev.AddChannel(chAddr, 1, "SWITCH", hmenum.ParamsetKeyValues)
	dp := generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
	ch.Put(dp)
	return switchdev.New(ch)
}

// ============================================================
// Tests: Light
// ============================================================

func TestDispatchLight_TurnOn(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildLightDP(t, "LIGHT001", w)
	// buildLightDP seeded LEVEL=0.8 (on). Push LEVEL=0 so the
	// subsequent turn_on actually crosses an on/off boundary —
	// otherwise IsStateChangeFull suppresses the wire write because
	// the light is already on at the lastLevel.
	if d := l.Float; d != nil {
		d.OnEvent(0)
	}
	disp, spy := buildDispatcher(t, "LIGHT001", "LEVEL", l)

	if err := disp.InvokeCustomDP(context.Background(), "LIGHT001", "LEVEL", "turn_on", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("turn_on: %v", err)
	}
	if w.callCount() == 0 {
		t.Fatal("expected at least one write call")
	}
	if spy.count() != 1 {
		t.Fatalf("expected 1 audit entry, got %d", spy.count())
	}
}

func TestDispatchLight_TurnOff(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildLightDP(t, "LIGHT002", w)
	disp, _ := buildDispatcher(t, "LIGHT002", "LEVEL", l)

	if err := disp.InvokeCustomDP(context.Background(), "LIGHT002", "LEVEL", "turn_off", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("turn_off: %v", err)
	}
	s, ok := w.lastSet()
	if !ok {
		t.Fatal("expected SetValue call")
	}
	if s.value != 0.0 {
		t.Fatalf("expected level=0, got %v", s.value)
	}
}

func TestDispatchLight_SetBrightness(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildLightDP(t, "LIGHT003", w)
	disp, _ := buildDispatcher(t, "LIGHT003", "LEVEL", l)

	params := map[string]any{"brightness": 0.5}
	if err := disp.InvokeCustomDP(context.Background(), "LIGHT003", "LEVEL", "set_brightness", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_brightness: %v", err)
	}
	s, ok := w.lastSet()
	if !ok {
		t.Fatal("expected SetValue call")
	}
	if s.value != 0.5 {
		t.Fatalf("expected level=0.5, got %v", s.value)
	}
}

func TestDispatchLight_SetBrightness_MissingParam(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildLightDP(t, "LIGHT004", w)
	disp, _ := buildDispatcher(t, "LIGHT004", "LEVEL", l)

	err := disp.InvokeCustomDP(context.Background(), "LIGHT004", "LEVEL", "set_brightness", nil, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, hmapi.ErrBadParam) {
		t.Fatalf("expected ErrBadParam, got %v", err)
	}
}

func TestDispatchLight_SetOnTime(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildLightDP(t, "LIGHT007", w)
	disp, spy := buildDispatcher(t, "LIGHT007", "LEVEL", l)

	params := map[string]any{"duration": "30s"}
	if err := disp.InvokeCustomDP(context.Background(), "LIGHT007", "LEVEL", "set_on_time", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_on_time: %v", err)
	}
	if w.callCount() == 0 {
		t.Fatal("expected ON_TIME write calls")
	}
	if spy.count() != 1 {
		t.Fatalf("expected 1 audit entry, got %d", spy.count())
	}
}

func TestDispatchLight_SetOnTime_MissingDuration(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildLightDP(t, "LIGHT008", w)
	disp, _ := buildDispatcher(t, "LIGHT008", "LEVEL", l)

	err := disp.InvokeCustomDP(context.Background(), "LIGHT008", "LEVEL", "set_on_time", nil, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, hmapi.ErrBadParam) {
		t.Fatalf("expected ErrBadParam, got %v", err)
	}
}

func TestDispatchLight_SetColorOnPlainLight_ReturnsUnknownOp(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildLightDP(t, "LIGHT005", w)
	disp, _ := buildDispatcher(t, "LIGHT005", "LEVEL", l)

	err := disp.InvokeCustomDP(context.Background(), "LIGHT005", "LEVEL", "set_color", nil, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, hmapi.ErrUnknownOperation) {
		t.Fatalf("expected ErrUnknownOperation, got %v", err)
	}
}

func TestDispatchLight_UnknownOp(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildLightDP(t, "LIGHT006", w)
	disp, _ := buildDispatcher(t, "LIGHT006", "LEVEL", l)

	err := disp.InvokeCustomDP(context.Background(), "LIGHT006", "LEVEL", "frobnicate", nil, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, hmapi.ErrUnknownOperation) {
		t.Fatalf("expected ErrUnknownOperation, got %v", err)
	}
}

// ============================================================
// Tests: ColorLight
// ============================================================

func TestDispatchColorLight_SetColor(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildColorLightDP(t, "CLGT001", w)
	disp, _ := buildDispatcher(t, "CLGT001", "LEVEL", l)

	params := map[string]any{"hue": float64(120), "saturation": 1.0}
	if err := disp.InvokeCustomDP(context.Background(), "CLGT001", "LEVEL", "set_color", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_color: %v", err)
	}
	if w.callCount() == 0 {
		t.Fatal("expected write calls")
	}
}

func TestDispatchColorLight_SetColorMissingHue(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildColorLightDP(t, "CLGT002", w)
	disp, _ := buildDispatcher(t, "CLGT002", "LEVEL", l)

	err := disp.InvokeCustomDP(context.Background(), "CLGT002", "LEVEL", "set_color",
		map[string]any{"saturation": 1.0}, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, hmapi.ErrBadParam) {
		t.Fatalf("expected ErrBadParam, got %v", err)
	}
}

// ============================================================
// Tests: ColorTempLight
// ============================================================

func TestDispatchColorTempLight_SetKelvin(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildColorTempLightDP(t, "CTLG001", w)
	disp, _ := buildDispatcher(t, "CTLG001", "LEVEL", l)

	params := map[string]any{"kelvin": float64(4000)}
	if err := disp.InvokeCustomDP(context.Background(), "CTLG001", "LEVEL", "set_color_temperature", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_color_temperature: %v", err)
	}
}

func TestDispatchColorTempLight_SetColorReturnsUnknownOp(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildColorTempLightDP(t, "CTLG002", w)
	disp, _ := buildDispatcher(t, "CTLG002", "LEVEL", l)

	err := disp.InvokeCustomDP(context.Background(), "CTLG002", "LEVEL", "set_color", nil, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, hmapi.ErrUnknownOperation) {
		t.Fatalf("expected ErrUnknownOperation, got %v", err)
	}
}

// ============================================================
// Tests: FixedColorLight
// ============================================================

func TestDispatchFixedColorLight_SetColorBySlot(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildFixedColorLightDP(t, "FCLG001", w)
	disp, _ := buildDispatcher(t, "FCLG001", "LEVEL", l)

	params := map[string]any{"slot": float64(2)} // green
	if err := disp.InvokeCustomDP(context.Background(), "FCLG001", "LEVEL", "set_color", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_color by slot: %v", err)
	}
}

func TestDispatchFixedColorLight_SetColorByHS(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	l := buildFixedColorLightDP(t, "FCLG002", w)
	disp, _ := buildDispatcher(t, "FCLG002", "LEVEL", l)

	params := map[string]any{"hue": float64(240), "saturation": 1.0} // blue
	if err := disp.InvokeCustomDP(context.Background(), "FCLG002", "LEVEL", "set_color", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_color by hs: %v", err)
	}
}

// ============================================================
// Tests: Climate (via carrier)
// ============================================================

func TestDispatchClimate_SetTemperature(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildClimateDP(t, "CLM001", w)
	disp, spy := buildDispatcher(t, "CLM001", "SET_POINT_TEMPERATURE", carrier)

	params := map[string]any{"temperature": 21.5}
	if err := disp.InvokeCustomDP(context.Background(), "CLM001", "SET_POINT_TEMPERATURE", "set_temperature", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_temperature: %v", err)
	}
	if spy.count() != 1 {
		t.Fatalf("expected 1 audit entry, got %d", spy.count())
	}
}

func TestDispatchClimate_EnableBoost(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildClimateDP(t, "CLM002", w)
	disp, _ := buildDispatcher(t, "CLM002", "SET_POINT_TEMPERATURE", carrier)

	if err := disp.InvokeCustomDP(context.Background(), "CLM002", "SET_POINT_TEMPERATURE", "enable_boost", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("enable_boost: %v", err)
	}
}

func TestDispatchClimate_DisableBoost(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildClimateDP(t, "CLM003", w)
	disp, _ := buildDispatcher(t, "CLM003", "SET_POINT_TEMPERATURE", carrier)

	if err := disp.InvokeCustomDP(context.Background(), "CLM003", "SET_POINT_TEMPERATURE", "disable_boost", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("disable_boost: %v", err)
	}
}

func TestDispatchClimate_SetMode(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildClimateDP(t, "CLM004", w)
	disp, _ := buildDispatcher(t, "CLM004", "SET_POINT_TEMPERATURE", carrier)

	params := map[string]any{"mode": "auto"}
	if err := disp.InvokeCustomDP(context.Background(), "CLM004", "SET_POINT_TEMPERATURE", "set_mode", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_mode: %v", err)
	}
}

func TestDispatchClimate_SetProfile(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildClimateDP(t, "CLM005", w)
	disp, _ := buildDispatcher(t, "CLM005", "SET_POINT_TEMPERATURE", carrier)

	params := map[string]any{"profile": "week_program_1"}
	if err := disp.InvokeCustomDP(context.Background(), "CLM005", "SET_POINT_TEMPERATURE", "set_profile", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_profile: %v", err)
	}
}

func TestDispatchClimate_EnableAway(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildClimateDP(t, "CLM006", w)
	disp, _ := buildDispatcher(t, "CLM006", "SET_POINT_TEMPERATURE", carrier)

	until := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	params := map[string]any{"until": until, "temperature": 15.0}
	if err := disp.InvokeCustomDP(context.Background(), "CLM006", "SET_POINT_TEMPERATURE", "enable_away", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("enable_away: %v", err)
	}
}

func TestDispatchClimate_EnableAwayByCalendar(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildClimateDP(t, "CLM010", w)
	disp, spy := buildDispatcher(t, "CLM010", "SET_POINT_TEMPERATURE", carrier)

	end := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	params := map[string]any{"end": end, "away_temperature": 15.0}
	if err := disp.InvokeCustomDP(context.Background(), "CLM010", "SET_POINT_TEMPERATURE", "enable_away_by_calendar", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("enable_away_by_calendar: %v", err)
	}
	if spy.count() != 1 {
		t.Fatalf("expected 1 audit entry, got %d", spy.count())
	}
}

func TestDispatchClimate_EnableAwayByCalendar_MissingTemp(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildClimateDP(t, "CLM011", w)
	disp, _ := buildDispatcher(t, "CLM011", "SET_POINT_TEMPERATURE", carrier)

	end := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	err := disp.InvokeCustomDP(context.Background(), "CLM011", "SET_POINT_TEMPERATURE", "enable_away_by_calendar", map[string]any{"end": end}, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, hmapi.ErrBadParam) {
		t.Fatalf("expected ErrBadParam, got %v", err)
	}
}

func TestDispatchClimate_EnableAwayByDurationHours(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildClimateDP(t, "CLM012", w)
	disp, spy := buildDispatcher(t, "CLM012", "SET_POINT_TEMPERATURE", carrier)

	params := map[string]any{"hours": 6.0, "away_temperature": 16.0}
	if err := disp.InvokeCustomDP(context.Background(), "CLM012", "SET_POINT_TEMPERATURE", "enable_away_by_duration", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("enable_away_by_duration (hours): %v", err)
	}
	if spy.count() != 1 {
		t.Fatalf("expected 1 audit entry, got %d", spy.count())
	}
}

func TestDispatchClimate_EnableAwayByDurationSeconds(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildClimateDP(t, "CLM013", w)
	disp, _ := buildDispatcher(t, "CLM013", "SET_POINT_TEMPERATURE", carrier)

	params := map[string]any{"duration_seconds": 3600.0, "away_temperature": 16.0}
	if err := disp.InvokeCustomDP(context.Background(), "CLM013", "SET_POINT_TEMPERATURE", "enable_away_by_duration", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("enable_away_by_duration (seconds): %v", err)
	}
}

func TestDispatchClimate_EnableAwayByDuration_MissingDuration(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildClimateDP(t, "CLM014", w)
	disp, _ := buildDispatcher(t, "CLM014", "SET_POINT_TEMPERATURE", carrier)

	err := disp.InvokeCustomDP(context.Background(), "CLM014", "SET_POINT_TEMPERATURE", "enable_away_by_duration", map[string]any{"away_temperature": 16.0}, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, hmapi.ErrBadParam) {
		t.Fatalf("expected ErrBadParam, got %v", err)
	}
}

func TestDispatchClimate_DisableAway(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildClimateDP(t, "CLM007", w)
	disp, _ := buildDispatcher(t, "CLM007", "SET_POINT_TEMPERATURE", carrier)

	if err := disp.InvokeCustomDP(context.Background(), "CLM007", "SET_POINT_TEMPERATURE", "disable_away", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("disable_away: %v", err)
	}
}

func TestDispatchClimate_UnknownOp(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildClimateDP(t, "CLM008", w)
	disp, _ := buildDispatcher(t, "CLM008", "SET_POINT_TEMPERATURE", carrier)

	err := disp.InvokeCustomDP(context.Background(), "CLM008", "SET_POINT_TEMPERATURE", "magic", nil, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, hmapi.ErrUnknownOperation) {
		t.Fatalf("expected ErrUnknownOperation, got %v", err)
	}
}

// ============================================================
// Tests: Cover
// ============================================================

func TestDispatchCover_Open(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	c := buildCoverDP(t, "CVR001", w)
	disp, _ := buildDispatcher(t, "CVR001", "LEVEL", c)

	if err := disp.InvokeCustomDP(context.Background(), "CVR001", "LEVEL", "open", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("open: %v", err)
	}
	s, ok := w.lastSet()
	if !ok {
		t.Fatal("expected SetValue call")
	}
	if s.value != 1.0 {
		t.Fatalf("expected position=1.0, got %v", s.value)
	}
}

func TestDispatchCover_Close(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	c := buildCoverDP(t, "CVR002", w)
	disp, _ := buildDispatcher(t, "CVR002", "LEVEL", c)

	if err := disp.InvokeCustomDP(context.Background(), "CVR002", "LEVEL", "close", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestDispatchCover_SetPosition(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	c := buildCoverDP(t, "CVR003", w)
	disp, _ := buildDispatcher(t, "CVR003", "LEVEL", c)

	params := map[string]any{"position": 0.6}
	if err := disp.InvokeCustomDP(context.Background(), "CVR003", "LEVEL", "set_position", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_position: %v", err)
	}
}

func TestDispatchCover_Stop(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	c := buildCoverDP(t, "CVR004", w)
	disp, _ := buildDispatcher(t, "CVR004", "LEVEL", c)

	if err := disp.InvokeCustomDP(context.Background(), "CVR004", "LEVEL", "stop", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func TestDispatchCover_SetTiltOnPlainCoverReturnsUnknownOp(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	c := buildCoverDP(t, "CVR005", w)
	disp, _ := buildDispatcher(t, "CVR005", "LEVEL", c)

	err := disp.InvokeCustomDP(context.Background(), "CVR005", "LEVEL", "set_tilt", map[string]any{"tilt": 0.5}, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, hmapi.ErrUnknownOperation) {
		t.Fatalf("expected ErrUnknownOperation, got %v", err)
	}
}

// ============================================================
// Tests: Blind (extends Cover)
// ============================================================

func TestDispatchBlind_SetTilt(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	b := buildBlindDP(t, "BLD001", w)
	disp, _ := buildDispatcher(t, "BLD001", "LEVEL", b)

	params := map[string]any{"tilt": 0.5}
	if err := disp.InvokeCustomDP(context.Background(), "BLD001", "LEVEL", "set_tilt", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_tilt: %v", err)
	}
}

func TestDispatchBlind_OpenRoutesToCover(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	b := buildBlindDP(t, "BLD002", w)
	disp, _ := buildDispatcher(t, "BLD002", "LEVEL", b)

	if err := disp.InvokeCustomDP(context.Background(), "BLD002", "LEVEL", "open", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("open on blind: %v", err)
	}
}

func TestDispatchBlind_SetCombined(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	b := buildBlindDP(t, "BLD003", w)
	disp, spy := buildDispatcher(t, "BLD003", "LEVEL", b)

	params := map[string]any{"level": 1.0, "tilt": 0.5}
	if err := disp.InvokeCustomDP(context.Background(), "BLD003", "LEVEL", "set_combined", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_combined: %v", err)
	}
	if w.callCount() == 0 {
		t.Fatal("expected write calls")
	}
	if spy.count() != 1 {
		t.Fatalf("expected 1 audit entry, got %d", spy.count())
	}
}

func TestDispatchBlind_SetCombined_MissingTilt(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	b := buildBlindDP(t, "BLD004", w)
	disp, _ := buildDispatcher(t, "BLD004", "LEVEL", b)

	err := disp.InvokeCustomDP(context.Background(), "BLD004", "LEVEL", "set_combined", map[string]any{"level": 1.0}, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, hmapi.ErrBadParam) {
		t.Fatalf("expected ErrBadParam, got %v", err)
	}
}

// ============================================================
// Tests: Lock (via carrier)
// ============================================================

func TestDispatchLock_Lock(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildLockDP("LCK001", w)
	disp, spy := buildDispatcher(t, "LCK001", "STATE", carrier)

	if err := disp.InvokeCustomDP(context.Background(), "LCK001", "STATE", "lock", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("lock: %v", err)
	}
	if spy.count() != 1 {
		t.Fatalf("expected 1 audit entry, got %d", spy.count())
	}
}

func TestDispatchLock_Unlock(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildLockDP("LCK002", w)
	disp, _ := buildDispatcher(t, "LCK002", "STATE", carrier)

	if err := disp.InvokeCustomDP(context.Background(), "LCK002", "STATE", "unlock", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("unlock: %v", err)
	}
}

func TestDispatchLock_Open(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildLockDP("LCK003", w)
	disp, _ := buildDispatcher(t, "LCK003", "STATE", carrier)

	if err := disp.InvokeCustomDP(context.Background(), "LCK003", "STATE", "open", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("open: %v", err)
	}
}

func TestDispatchLock_UnknownOp(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildLockDP("LCK004", w)
	disp, _ := buildDispatcher(t, "LCK004", "STATE", carrier)

	err := disp.InvokeCustomDP(context.Background(), "LCK004", "STATE", "disassemble", nil, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, hmapi.ErrUnknownOperation) {
		t.Fatalf("expected ErrUnknownOperation, got %v", err)
	}
}

// ============================================================
// Tests: Siren (via carrier)
// ============================================================

func TestDispatchSiren_TurnOn(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildSirenDP("SRN001", w)
	disp, spy := buildDispatcher(t, "SRN001", "STATE", carrier)

	params := map[string]any{"acoustic": "FREQUENCY_RISING"}
	if err := disp.InvokeCustomDP(context.Background(), "SRN001", "STATE", "turn_on", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("turn_on siren: %v", err)
	}
	if spy.count() != 1 {
		t.Fatalf("expected 1 audit entry, got %d", spy.count())
	}
}

func TestDispatchSiren_TurnOff(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildSirenDP("SRN002", w)
	disp, _ := buildDispatcher(t, "SRN002", "STATE", carrier)

	if err := disp.InvokeCustomDP(context.Background(), "SRN002", "STATE", "turn_off", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("turn_off siren: %v", err)
	}
}

func TestDispatchSiren_TurnOnWithDuration(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildSirenDP("SRN003", w)
	disp, _ := buildDispatcher(t, "SRN003", "STATE", carrier)

	params := map[string]any{"duration": "5s", "acoustic": "FREQUENCY_RISING"}
	if err := disp.InvokeCustomDP(context.Background(), "SRN003", "STATE", "turn_on", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("turn_on with duration: %v", err)
	}
}

func TestDispatchSiren_TurnOnWithDurationSeconds(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildSirenDP("SRN004", w)
	disp, _ := buildDispatcher(t, "SRN004", "STATE", carrier)

	params := map[string]any{"duration_seconds": 5.0, "optical": "BLINKING_ALTERNATELY_REPEATING"}
	if err := disp.InvokeCustomDP(context.Background(), "SRN004", "STATE", "turn_on", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("turn_on with duration_seconds: %v", err)
	}
}

func TestDispatchSiren_Stop(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildSirenDP("SRN005", w)
	disp, _ := buildDispatcher(t, "SRN005", "STATE", carrier)

	if err := disp.InvokeCustomDP(context.Background(), "SRN005", "STATE", "stop", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("stop siren: %v", err)
	}
}

// ============================================================
// Tests: TextDisplay (via carrier)
// ============================================================

func TestDispatchTextDisplay_Write(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildTextDisplayDP("TXT001", w)
	disp, spy := buildDispatcher(t, "TXT001", "STATE", carrier)

	params := map[string]any{"id": float64(1), "text": "hello"}
	if err := disp.InvokeCustomDP(context.Background(), "TXT001", "STATE", "write", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if spy.count() != 1 {
		t.Fatalf("expected 1 audit entry, got %d", spy.count())
	}
}

func TestDispatchTextDisplay_Clear(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildTextDisplayDP("TXT002", w)
	disp, _ := buildDispatcher(t, "TXT002", "STATE", carrier)

	params := map[string]any{"id": float64(2)}
	if err := disp.InvokeCustomDP(context.Background(), "TXT002", "STATE", "clear", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("clear: %v", err)
	}
}

func TestDispatchTextDisplay_MissingID(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildTextDisplayDP("TXT003", w)
	disp, _ := buildDispatcher(t, "TXT003", "STATE", carrier)

	err := disp.InvokeCustomDP(context.Background(), "TXT003", "STATE", "write", map[string]any{"text": "hi"}, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, hmapi.ErrBadParam) {
		t.Fatalf("expected ErrBadParam, got %v", err)
	}
}

func TestDispatchTextDisplay_SendText(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildTextDisplayDP("TXT004", w)
	disp, spy := buildDispatcher(t, "TXT004", "STATE", carrier)

	params := map[string]any{"id": float64(1), "text": "hello"}
	if err := disp.InvokeCustomDP(context.Background(), "TXT004", "STATE", "send_text", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("send_text: %v", err)
	}
	if spy.count() != 1 {
		t.Fatalf("expected 1 audit entry, got %d", spy.count())
	}
}

func TestDispatchTextDisplay_ClearText(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildTextDisplayDP("TXT005", w)
	disp, _ := buildDispatcher(t, "TXT005", "STATE", carrier)

	params := map[string]any{"id": float64(2)}
	if err := disp.InvokeCustomDP(context.Background(), "TXT005", "STATE", "clear_text", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("clear_text: %v", err)
	}
}

func TestDispatchTextDisplay_Commit(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	_, carrier := buildTextDisplayDP("TXT006", w)
	disp, _ := buildDispatcher(t, "TXT006", "STATE", carrier)

	if err := disp.InvokeCustomDP(context.Background(), "TXT006", "STATE", "commit", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if w.callCount() == 0 {
		t.Fatal("expected DISPLAY_DATA_COMMIT write")
	}
}

// ============================================================
// Tests: Irrigation valve
// ============================================================

func TestDispatchIrrigation_Open(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	v := buildIrrigationDP("VLV001", w)
	disp, spy := buildDispatcher(t, "VLV001", "STATE", v)

	if err := disp.InvokeCustomDP(context.Background(), "VLV001", "STATE", "open", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("open: %v", err)
	}
	if spy.count() != 1 {
		t.Fatalf("expected 1 audit entry, got %d", spy.count())
	}
}

func TestDispatchIrrigation_OpenWithDuration(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	v := buildIrrigationDP("VLV002", w)
	disp, _ := buildDispatcher(t, "VLV002", "STATE", v)

	params := map[string]any{"duration": "2m"}
	if err := disp.InvokeCustomDP(context.Background(), "VLV002", "STATE", "open", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("open with duration: %v", err)
	}
}

func TestDispatchIrrigation_SetOnTime(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	v := buildIrrigationDP("VLV005", w)
	disp, spy := buildDispatcher(t, "VLV005", "STATE", v)

	params := map[string]any{"duration": "2m"}
	if err := disp.InvokeCustomDP(context.Background(), "VLV005", "STATE", "set_on_time", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_on_time: %v", err)
	}
	if spy.count() != 1 {
		t.Fatalf("expected 1 audit entry, got %d", spy.count())
	}
}

func TestDispatchIrrigation_SetOnTime_MissingDuration(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	v := buildIrrigationDP("VLV006", w)
	disp, _ := buildDispatcher(t, "VLV006", "STATE", v)

	err := disp.InvokeCustomDP(context.Background(), "VLV006", "STATE", "set_on_time", nil, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, hmapi.ErrBadParam) {
		t.Fatalf("expected ErrBadParam, got %v", err)
	}
}

func TestDispatchIrrigation_Close(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	v := buildIrrigationDP("VLV003", w)
	disp, _ := buildDispatcher(t, "VLV003", "STATE", v)

	if err := disp.InvokeCustomDP(context.Background(), "VLV003", "STATE", "close", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestDispatchIrrigation_SetLevelReturnsUnknownOp(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	v := buildIrrigationDP("VLV004", w)
	disp, _ := buildDispatcher(t, "VLV004", "STATE", v)

	err := disp.InvokeCustomDP(context.Background(), "VLV004", "STATE", "set_level", map[string]any{"level": 0.5}, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, hmapi.ErrUnknownOperation) {
		t.Fatalf("expected ErrUnknownOperation, got %v", err)
	}
}

// ============================================================
// Tests: Modulating valve
// ============================================================

func TestDispatchModulating_SetLevel(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	v := buildModulatingValveDP("MOD001", w)
	disp, spy := buildDispatcher(t, "MOD001", "LEVEL", v)

	params := map[string]any{"level": 0.75}
	if err := disp.InvokeCustomDP(context.Background(), "MOD001", "LEVEL", "set_level", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_level: %v", err)
	}
	if spy.count() != 1 {
		t.Fatalf("expected 1 audit entry, got %d", spy.count())
	}
}

func TestDispatchModulating_Open(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	v := buildModulatingValveDP("MOD002", w)
	disp, _ := buildDispatcher(t, "MOD002", "LEVEL", v)

	if err := disp.InvokeCustomDP(context.Background(), "MOD002", "LEVEL", "open", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("open: %v", err)
	}
}

func TestDispatchModulating_Close(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	v := buildModulatingValveDP("MOD003", w)
	disp, _ := buildDispatcher(t, "MOD003", "LEVEL", v)

	if err := disp.InvokeCustomDP(context.Background(), "MOD003", "LEVEL", "close", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// ============================================================
// Tests: Switch
// ============================================================

func TestDispatchSwitch_TurnOn(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	s := buildSwitchDP("SW001", w)
	disp, spy := buildDispatcher(t, "SW001", "STATE", s)

	if err := disp.InvokeCustomDP(context.Background(), "SW001", "STATE", "turn_on", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("turn_on: %v", err)
	}
	if spy.count() != 1 {
		t.Fatalf("expected 1 audit entry, got %d", spy.count())
	}
	sc, ok := w.lastSet()
	if !ok {
		t.Fatal("expected SetValue call")
	}
	if sc.value != true {
		t.Fatalf("expected value=true, got %v", sc.value)
	}
}

func TestDispatchSwitch_TurnOff(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	s := buildSwitchDP("SW002", w)
	disp, _ := buildDispatcher(t, "SW002", "STATE", s)

	if err := disp.InvokeCustomDP(context.Background(), "SW002", "STATE", "turn_off", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("turn_off: %v", err)
	}
	sc, ok := w.lastSet()
	if !ok {
		t.Fatal("expected SetValue call")
	}
	if sc.value != false {
		t.Fatalf("expected value=false, got %v", sc.value)
	}
}

func TestDispatchSwitch_TurnOnFor(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	s := buildSwitchDP("SW003", w)
	disp, _ := buildDispatcher(t, "SW003", "STATE", s)

	params := map[string]any{"duration": "10s"}
	if err := disp.InvokeCustomDP(context.Background(), "SW003", "STATE", "turn_on_for", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("turn_on_for: %v", err)
	}
}

func TestDispatchSwitch_SetOnTime(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	s := buildSwitchDP("SW006", w)
	disp, spy := buildDispatcher(t, "SW006", "STATE", s)

	params := map[string]any{"duration": "10s"}
	if err := disp.InvokeCustomDP(context.Background(), "SW006", "STATE", "set_on_time", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_on_time: %v", err)
	}
	if spy.count() != 1 {
		t.Fatalf("expected 1 audit entry, got %d", spy.count())
	}
}

func TestDispatchSwitch_Toggle(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	s := buildSwitchDP("SW004", w)
	disp, _ := buildDispatcher(t, "SW004", "STATE", s)

	// First toggle: off → on
	if err := disp.InvokeCustomDP(context.Background(), "SW004", "STATE", "toggle", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("toggle: %v", err)
	}
}

func TestDispatchSwitch_TurnOnForMissingDuration(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	s := buildSwitchDP("SW005", w)
	disp, _ := buildDispatcher(t, "SW005", "STATE", s)

	err := disp.InvokeCustomDP(context.Background(), "SW005", "STATE", "turn_on_for", nil, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, hmapi.ErrBadParam) {
		t.Fatalf("expected ErrBadParam, got %v", err)
	}
}

// ============================================================
// Tests: Error handling
// ============================================================

func TestDispatch_DeviceNotFound(t *testing.T) {
	t.Parallel()
	c, _ := central.New(central.Config{Name: "ccu-01"})
	reg := central.NewRegistry()
	_ = reg.Register(c)
	disp := NewCustomDPDispatcher(reg)

	err := disp.InvokeCustomDP(context.Background(), "UNKNOWN999", "STATE", "turn_on", nil, hmenum.CommandPriorityHigh, "test")
	if err == nil {
		t.Fatal("expected error for unknown device")
	}
}

func TestDispatch_DPNotFound(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	s := buildSwitchDP("SW010", w)
	disp, _ := buildDispatcher(t, "SW010", "STATE", s)

	err := disp.InvokeCustomDP(context.Background(), "SW010", "NONEXISTENT", "turn_on", nil, hmenum.CommandPriorityHigh, "test")
	if err == nil {
		t.Fatal("expected error for unknown DP name")
	}
}

func TestDispatch_AuditFiresOnSuccess(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	s := buildSwitchDP("SW020", w)
	spy := &spyAudit{}
	reg := testRegistry(t, "SW020", "STATE", s)
	disp := NewCustomDPDispatcher(reg).SetAuditRecorder(spy)

	if err := disp.InvokeCustomDP(context.Background(), "SW020", "STATE", "turn_on", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spy.count() != 1 {
		t.Fatalf("expected 1 audit entry, got %d", spy.count())
	}
}

func TestDispatch_AuditDoesNotFireOnError(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	s := buildSwitchDP("SW021", w)
	spy := &spyAudit{}
	reg := testRegistry(t, "SW021", "STATE", s)
	disp := NewCustomDPDispatcher(reg).SetAuditRecorder(spy)

	// Unknown operation → should not fire audit.
	_ = disp.InvokeCustomDP(context.Background(), "SW021", "STATE", "unknown_op", nil, hmenum.CommandPriorityHigh, "test")
	if spy.count() != 0 {
		t.Fatalf("expected 0 audit entries on error, got %d", spy.count())
	}
}

func TestDispatch_UnsupportedDPType(t *testing.T) {
	t.Parallel()
	// A raw generic.Float stored as custom DP has no matching dispatcher.
	w := &dispatchWriter{}
	f := floatDP("UNSUP001:1", hmenum.ParameterLevel, w)
	reg := testRegistry(t, "UNSUP001", "LEVEL", f)
	disp := NewCustomDPDispatcher(reg)

	err := disp.InvokeCustomDP(context.Background(), "UNSUP001", "LEVEL", "turn_on", nil, hmenum.CommandPriorityHigh, "test")
	if err == nil {
		t.Fatal("expected error for unsupported DP type")
	}
}

// ============================================================
// Tests: Param helpers
// ============================================================

func TestParamFloat_Bounds(t *testing.T) {
	t.Parallel()
	_, err := paramFloat(map[string]any{"v": 1.5}, "v", 1)
	if !errors.Is(err, hmapi.ErrBadParam) {
		t.Fatalf("expected ErrBadParam for out-of-range, got %v", err)
	}
}

func TestParamFloat_Missing(t *testing.T) {
	t.Parallel()
	_, err := paramFloat(map[string]any{}, "v", 1)
	if !errors.Is(err, hmapi.ErrBadParam) {
		t.Fatalf("expected ErrBadParam for missing key, got %v", err)
	}
}

func TestParamInt32_WrongType(t *testing.T) {
	t.Parallel()
	_, err := paramInt32(map[string]any{"v": "not-a-number"}, "v")
	if !errors.Is(err, hmapi.ErrBadParam) {
		t.Fatalf("expected ErrBadParam, got %v", err)
	}
}

func TestAnyToDuration_String(t *testing.T) {
	t.Parallel()
	d, err := anyToDuration("5s")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != 5*time.Second {
		t.Fatalf("expected 5s, got %v", d)
	}
}

func TestAnyToDuration_Milliseconds(t *testing.T) {
	t.Parallel()
	d, err := anyToDuration(float64(2000))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != 2*time.Second {
		t.Fatalf("expected 2s, got %v", d)
	}
}

// ============================================================
// EffectLight builder
// ============================================================

func buildEffectLightDP(t *testing.T, addr string, w *dispatchWriter, effects []string) *light.EffectLight {
	t.Helper()
	dev := device.New(device.Config{Address: addr, InterfaceID: "test"})
	chAddr := addr + ":1"
	ch := dev.AddChannel(chAddr, 1, "COLOR_DIMMER", hmenum.ParamsetKeyValues)

	// LEVEL
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
	// HUE
	ch.Put(generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterHue),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	}))
	// SATURATION
	ch.Put(generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterSaturation),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	}))
	// PROGRAM (with value list for effects)
	ch.Put(generic.NewInteger(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterProgram),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			ValueList:  effects,
		},
		Writer: w,
	}))
	return light.NewEffectLight(light.Config{
		Channel:      ch,
		Writer:       w,
		Capabilities: custom.LightCapabilities{Dimmable: true, SupportsColor: true},
	})
}

// buildRGBWLightDP builds an RGBWLight in RGB mode (supports color and effects).
func buildRGBWLightDP(t *testing.T, addr string, w *dispatchWriter) *light.RGBWLight {
	t.Helper()
	dev := device.New(device.Config{Address: addr, InterfaceID: "test"})
	chAddr := addr + ":1"
	ch := dev.AddChannel(chAddr, 1, "RGBW", hmenum.ParamsetKeyValues)
	// LEVEL
	ch.Put(generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: chAddr, ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterLevel)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	}))
	// HUE
	ch.Put(generic.NewInteger(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: chAddr, ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterHue)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	}))
	// SATURATION
	ch.Put(generic.NewFloat(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: chAddr, ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterSaturation)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	}))
	// COLOR_TEMPERATURE
	ch.Put(generic.NewInteger(generic.Spec{
		Key:        hmtypes.DataPointKey{ChannelAddress: chAddr, ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterColorTemperature)},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent},
		Writer:     w,
	}))
	// EFFECT
	ch.Put(generic.NewActionSelect(generic.Spec{
		Key: hmtypes.DataPointKey{ChannelAddress: chAddr, ParamsetKey: hmenum.ParamsetKeyValues, Parameter: string(hmenum.ParameterEffect)},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsWrite,
			ValueList:  []string{"OFF", "EFFECT1", "EFFECT2"},
		},
		Writer: w,
	}))
	// DEVICE_OPERATION_MODE string sensor — seeded to "RGBW" so all capabilities are active.
	modeSensor := generic.NewStringSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterDeviceOperationMode),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeString,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
	// Seed the sensor with a value so RGBWLight.Subscribe replays it.
	modeSensor.OnEvent("RGBW")
	ch.Put(modeSensor)

	rl := light.NewRGBWLight(light.Config{
		Channel:      ch,
		Writer:       w,
		Capabilities: custom.LightCapabilities{Dimmable: true, SupportsColor: true, SupportsColorTemp: true},
	})
	// Subscribe wires DEVICE_OPERATION_MODE → mode and replays the current value.
	unsub := rl.Subscribe(ch)
	t.Cleanup(unsub)
	return rl
}

// ============================================================
// EffectLight dispatcher tests
// ============================================================

// TestDispatchEffectLight_SetEffectByIndex exercises the index path.
func TestDispatchEffectLight_SetEffectByIndex(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	effects := []string{"Off", "Slow", "Fast"}
	el := buildEffectLightDP(t, "EFF001", w, effects)
	disp, _ := buildDispatcher(t, "EFF001", "LEVEL", el)

	params := map[string]any{"index": float64(1)}
	if err := disp.InvokeCustomDP(context.Background(), "EFF001", "LEVEL", "set_effect", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_effect by index: %v", err)
	}
	if w.callCount() == 0 {
		t.Fatal("expected write calls")
	}
}

// TestDispatchEffectLight_SetEffectByLabel exercises the label path.
func TestDispatchEffectLight_SetEffectByLabel(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	effects := []string{"Off", "Slow", "Fast"}
	el := buildEffectLightDP(t, "EFF002", w, effects)
	disp, _ := buildDispatcher(t, "EFF002", "LEVEL", el)

	params := map[string]any{"label": "Slow"}
	if err := disp.InvokeCustomDP(context.Background(), "EFF002", "LEVEL", "set_effect", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_effect by label: %v", err)
	}
}

// TestDispatchEffectLight_SetEffectBadLabel verifies non-string label returns ErrBadParam.
func TestDispatchEffectLight_SetEffectBadLabel(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	el := buildEffectLightDP(t, "EFF003", w, []string{"Off"})
	disp, _ := buildDispatcher(t, "EFF003", "LEVEL", el)

	params := map[string]any{"label": 42}
	err := disp.InvokeCustomDP(context.Background(), "EFF003", "LEVEL", "set_effect", params, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, hmapi.ErrBadParam) {
		t.Fatalf("expected ErrBadParam, got %v", err)
	}
}

// TestDispatchEffectLight_SetColorExercisesColorLightPath.
func TestDispatchEffectLight_SetColor(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	el := buildEffectLightDP(t, "EFF004", w, []string{"Off"})
	disp, _ := buildDispatcher(t, "EFF004", "LEVEL", el)

	params := map[string]any{"hue": float64(120), "saturation": 0.8}
	if err := disp.InvokeCustomDP(context.Background(), "EFF004", "LEVEL", "set_color", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_color on EffectLight: %v", err)
	}
}

// TestDispatchEffectLight_SetColorTempNotSupported verifies that SetColorTemp
// returns an error when the effect light does not support colour temperature.
func TestDispatchEffectLight_SetColorTempNotSupported(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	el := buildEffectLightDP(t, "EFF005", w, nil)
	disp, _ := buildDispatcher(t, "EFF005", "LEVEL", el)

	err := disp.InvokeCustomDP(context.Background(), "EFF005", "LEVEL", "set_color_temperature", nil, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, hmapi.ErrUnknownOperation) {
		t.Fatalf("expected ErrUnknownOperation, got %v", err)
	}
}

// TestDispatchEffectLight_TurnOn exercises the base Light path.
func TestDispatchEffectLight_TurnOn(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	el := buildEffectLightDP(t, "EFF006", w, nil)
	disp, _ := buildDispatcher(t, "EFF006", "LEVEL", el)

	if err := disp.InvokeCustomDP(context.Background(), "EFF006", "LEVEL", "turn_off", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("turn_off on EffectLight: %v", err)
	}
}

// ============================================================
// RGBWLight dispatcher tests
// ============================================================

// TestDispatchRGBWLight_SetColor exercises the hue+saturation path.
// Mode is seeded to "RGBW" by buildRGBWLightDP, which supports colour.
func TestDispatchRGBWLight_SetColor(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	rl := buildRGBWLightDP(t, "RGBW001", w)
	disp, _ := buildDispatcher(t, "RGBW001", "LEVEL", rl)

	params := map[string]any{"hue": float64(240), "saturation": 1.0}
	if err := disp.InvokeCustomDP(context.Background(), "RGBW001", "LEVEL", "set_color", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_color on RGBWLight: %v", err)
	}
}

// TestDispatchRGBWLight_SetColorTemperature exercises the kelvin path.
// Mode is seeded to "RGBW" by buildRGBWLightDP, which supports colour temperature.
func TestDispatchRGBWLight_SetColorTemperature(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	rl := buildRGBWLightDP(t, "RGBW002", w)
	disp, _ := buildDispatcher(t, "RGBW002", "LEVEL", rl)

	params := map[string]any{"kelvin": float64(3000)}
	if err := disp.InvokeCustomDP(context.Background(), "RGBW002", "LEVEL", "set_color_temperature", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_color_temperature on RGBWLight: %v", err)
	}
}

// TestDispatchRGBWLight_SetEffectByIndex exercises the index effect path.
func TestDispatchRGBWLight_SetEffectByIndex(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	rl := buildRGBWLightDP(t, "RGBW003", w)
	disp, _ := buildDispatcher(t, "RGBW003", "LEVEL", rl)

	params := map[string]any{"index": float64(1)}
	if err := disp.InvokeCustomDP(context.Background(), "RGBW003", "LEVEL", "set_effect", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_effect by index on RGBWLight: %v", err)
	}
}

// TestDispatchRGBWLight_SetEffectByLabel exercises the label effect path.
func TestDispatchRGBWLight_SetEffectByLabel(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	rl := buildRGBWLightDP(t, "RGBW004", w)
	disp, _ := buildDispatcher(t, "RGBW004", "LEVEL", rl)

	params := map[string]any{"label": "EFFECT1"}
	if err := disp.InvokeCustomDP(context.Background(), "RGBW004", "LEVEL", "set_effect", params, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("set_effect by label on RGBWLight: %v", err)
	}
}

// TestDispatchRGBWLight_SetEffectBadLabel verifies non-string label → ErrBadParam.
func TestDispatchRGBWLight_SetEffectBadLabel(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	rl := buildRGBWLightDP(t, "RGBW005", w)
	disp, _ := buildDispatcher(t, "RGBW005", "LEVEL", rl)

	params := map[string]any{"label": 99}
	err := disp.InvokeCustomDP(context.Background(), "RGBW005", "LEVEL", "set_effect", params, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, hmapi.ErrBadParam) {
		t.Fatalf("expected ErrBadParam, got %v", err)
	}
}

// TestDispatchRGBWLight_SetEffectUnknownLabel verifies unknown label → ErrBadParam.
func TestDispatchRGBWLight_SetEffectUnknownLabel(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	rl := buildRGBWLightDP(t, "RGBW006", w)
	disp, _ := buildDispatcher(t, "RGBW006", "LEVEL", rl)

	params := map[string]any{"label": "NO_SUCH_EFFECT"}
	err := disp.InvokeCustomDP(context.Background(), "RGBW006", "LEVEL", "set_effect", params, hmenum.CommandPriorityHigh, "test")
	if !errors.Is(err, hmapi.ErrBadParam) {
		t.Fatalf("expected ErrBadParam for unknown label, got %v", err)
	}
}

// TestDispatchRGBWLight_TurnOff exercises the base Light path.
func TestDispatchRGBWLight_TurnOff(t *testing.T) {
	t.Parallel()
	w := &dispatchWriter{}
	rl := buildRGBWLightDP(t, "RGBW007", w)
	disp, _ := buildDispatcher(t, "RGBW007", "LEVEL", rl)

	if err := disp.InvokeCustomDP(context.Background(), "RGBW007", "LEVEL", "turn_off", nil, hmenum.CommandPriorityHigh, "test"); err != nil {
		t.Fatalf("turn_off on RGBWLight: %v", err)
	}
}

// ============================================================
// paramStringOptional tests (pure logic)
// ============================================================

func TestParamStringOptionalPresent(t *testing.T) {
	t.Parallel()
	p := map[string]any{"state": "ON"}
	v, ok := paramStringOptional(p, "state")
	if !ok || v != "ON" {
		t.Errorf("paramStringOptional = (%q, %v), want (ON, true)", v, ok)
	}
}

func TestParamStringOptionalMissing(t *testing.T) {
	t.Parallel()
	_, ok := paramStringOptional(map[string]any{}, "state")
	if ok {
		t.Error("missing key must return false")
	}
}

func TestParamStringOptionalWrongType(t *testing.T) {
	t.Parallel()
	_, ok := paramStringOptional(map[string]any{"state": 42}, "state")
	if ok {
		t.Error("non-string value must return false")
	}
}

// ============================================================
// extractSoundOptions tests
// ============================================================

func TestExtractSoundOptionsAllFields(t *testing.T) {
	t.Parallel()
	p := map[string]any{
		"sound":       "alarm",
		"repetitions": "3",
		"interval":    "10s",
	}
	opts, err := extractSoundOptions(p)
	if err != nil {
		t.Fatalf("extractSoundOptions: %v", err)
	}
	if opts.Sound != "alarm" {
		t.Errorf("Sound = %q, want alarm", opts.Sound)
	}
	if opts.Repetitions != "3" {
		t.Errorf("Repetitions = %q, want 3", opts.Repetitions)
	}
	if opts.Interval != "10s" {
		t.Errorf("Interval = %q, want 10s", opts.Interval)
	}
}

func TestExtractSoundOptionsBadSound(t *testing.T) {
	t.Parallel()
	p := map[string]any{"sound": 99}
	_, err := extractSoundOptions(p)
	if !errors.Is(err, hmapi.ErrBadParam) {
		t.Fatalf("expected ErrBadParam for bad sound, got %v", err)
	}
}

func TestExtractSoundOptionsBadRepetitions(t *testing.T) {
	t.Parallel()
	p := map[string]any{"sound": "alarm", "repetitions": 3}
	_, err := extractSoundOptions(p)
	if !errors.Is(err, hmapi.ErrBadParam) {
		t.Fatalf("expected ErrBadParam for bad repetitions, got %v", err)
	}
}

func TestExtractSoundOptionsBadInterval(t *testing.T) {
	t.Parallel()
	p := map[string]any{"sound": "alarm", "interval": true}
	_, err := extractSoundOptions(p)
	if !errors.Is(err, hmapi.ErrBadParam) {
		t.Fatalf("expected ErrBadParam for bad interval, got %v", err)
	}
}

// ============================================================
// SweepUnobservedForCentral tests
// ============================================================

func TestSweepUnobservedForCentralNilSweep(t *testing.T) {
	t.Parallel()
	var s *UnobservedSweep
	loaded, errored := s.SweepUnobservedForCentral(context.Background(), nil)
	if loaded != 0 || errored != 0 {
		t.Errorf("nil sweep: loaded=%d errored=%d, want 0,0", loaded, errored)
	}
}

func TestSweepUnobservedForCentralNilUnit(t *testing.T) {
	t.Parallel()
	s := &UnobservedSweep{}
	loaded, errored := s.SweepUnobservedForCentral(context.Background(), nil)
	if loaded != 0 || errored != 0 {
		t.Errorf("nil unit: loaded=%d errored=%d, want 0,0", loaded, errored)
	}
}

// ============================================================
// resolveAction tests
// ============================================================

func TestResolveActionWriteOnlyWithButtonParam(t *testing.T) {
	t.Parallel()
	cfg := generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "DEV:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterResetMotion),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeAction,
			Operations: hmenum.OperationsWrite,
		},
	}
	dp := resolveAction(cfg, hmenum.ParameterResetMotion, cfg.Descriptor)
	if dp == nil {
		t.Fatal("RESET_MOTION write-only action must resolve to a Button")
	}
}

func TestResolveActionWriteOnlyResetPresence(t *testing.T) {
	t.Parallel()
	cfg := generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "DEV:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "RESET_PRESENCE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeAction,
			Operations: hmenum.OperationsWrite,
		},
	}
	dp := resolveAction(cfg, "RESET_PRESENCE", cfg.Descriptor)
	if dp == nil {
		t.Fatal("RESET_PRESENCE write-only action must resolve to a Button")
	}
}

func TestResolveActionWriteOnlyWithValueList(t *testing.T) {
	t.Parallel()
	cfg := generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "DEV:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "CUSTOM_ACTION",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeAction,
			Operations: hmenum.OperationsWrite,
			ValueList:  []string{"A", "B", "C"},
		},
	}
	dp := resolveAction(cfg, "CUSTOM_ACTION", cfg.Descriptor)
	if dp == nil {
		t.Fatal("write-only action with value list must resolve to an ActionSelect")
	}
}

func TestResolveActionWriteOnlyPlain(t *testing.T) {
	t.Parallel()
	cfg := generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "DEV:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "SOME_ACTION",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeAction,
			Operations: hmenum.OperationsWrite,
		},
	}
	dp := resolveAction(cfg, "SOME_ACTION", cfg.Descriptor)
	if dp == nil {
		t.Fatal("plain write-only action must resolve to an Action")
	}
}

func TestResolveActionClickEvent(t *testing.T) {
	t.Parallel()
	cfg := generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "DEV:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterPressShort),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeAction,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	}
	dp := resolveAction(cfg, hmenum.ParameterPressShort, cfg.Descriptor)
	if dp == nil {
		t.Fatal("click-event action must resolve to a Button")
	}
}

func TestResolveActionReadWriteReturnsSwitch(t *testing.T) {
	t.Parallel()
	cfg := generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "DEV:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "TOGGLE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeAction,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		},
	}
	dp := resolveAction(cfg, "TOGGLE", cfg.Descriptor)
	if dp == nil {
		t.Fatal("read+write action must resolve to a Switch")
	}
}

// TestRecordAudit_OmitsRawValues verifies that the audit note captures only
// the NAMES of the written parameters, never the raw written values — a
// write payload can carry secrets (e.g. a lock PIN) that must not be
// persisted into the audit log.
func TestRecordAudit_OmitsRawValues(t *testing.T) {
	t.Parallel()
	spy := &spyAudit{}
	d := NewCustomDPDispatcher(central.NewRegistry()).SetAuditRecorder(spy)

	const secretPIN = "8765"
	d.recordAudit("00021BE9957782", 1, "PIN", "set_pin", "rest", map[string]any{
		"PIN":   secretPIN,
		"STATE": true,
	})

	if spy.count() != 1 {
		t.Fatalf("expected 1 audit entry, got %d", spy.count())
	}
	e := spy.entries[0]
	if e.Parameter != "PIN" {
		t.Errorf("Parameter=%q, want PIN", e.Parameter)
	}
	if strings.Contains(e.Note, secretPIN) {
		t.Errorf("audit note must NOT contain the raw written value %q: %q", secretPIN, e.Note)
	}
	// Parameter names are recorded (sorted, comma-joined) so the note stays
	// useful for a reviewer without leaking the payload.
	if want := "params=PIN,STATE"; !strings.Contains(e.Note, want) {
		t.Errorf("audit note must list parameter names %q: %q", want, e.Note)
	}
	if !strings.Contains(e.Note, "source=rest") || !strings.Contains(e.Note, "op=set_pin") {
		t.Errorf("audit note must carry source + operation: %q", e.Note)
	}
}

// TestRecordAudit_NoParams keeps the note well-formed when no parameters
// were written (the params= segment is dropped rather than left empty).
func TestRecordAudit_NoParams(t *testing.T) {
	t.Parallel()
	spy := &spyAudit{}
	d := NewCustomDPDispatcher(central.NewRegistry()).SetAuditRecorder(spy)

	d.recordAudit("ADDR", 2, "STATE", "turn_on", "ws", nil)

	if spy.count() != 1 {
		t.Fatalf("expected 1 audit entry, got %d", spy.count())
	}
	if note := spy.entries[0].Note; strings.Contains(note, "params=") {
		t.Errorf("empty params must not emit a params= segment: %q", note)
	}
}
