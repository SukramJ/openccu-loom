// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"testing"
	"time"

	fabriclight "github.com/SukramJ/go-fabric/cluster/light"
	colorcontrolspec "github.com/SukramJ/go-fabric/cluster/spec/colorcontrol"
	"github.com/SukramJ/go-fabric/cluster/wire"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// --- Encoding helpers ---

// TestHueRoundTripFullCircle locks the hue encoding boundaries.
// HM 0° → Matter 0; HM 180° → Matter 127; HM 359° → Matter 253-254.
func TestHueRoundTripFullCircle(t *testing.T) {
	cases := []struct {
		hm    int32
		want  uint8
		delta uint8
	}{
		{0, 0, 0},
		{90, 64, 1},
		{180, 127, 1},
		{270, 191, 1},
		{359, 253, 2},
	}
	for _, tc := range cases {
		got := hueToMatter(tc.hm)
		if got+tc.delta < tc.want || got > tc.want+tc.delta {
			t.Errorf("hueToMatter(%d°) = %d, want %d±%d", tc.hm, got, tc.want, tc.delta)
		}
	}
}

// TestHueWrapAroundNegativeAndOver360 covers the wrap mathematics —
// negative hues and ≥360 should land on the equivalent positive
// segment.
func TestHueWrapAroundNegativeAndOver360(t *testing.T) {
	if hueToMatter(360) != 0 {
		t.Errorf("hueToMatter(360) = %d, want 0", hueToMatter(360))
	}
	if hueToMatter(-90) != hueToMatter(270) {
		t.Errorf("negative hue not normalised: -90→%d, 270→%d", hueToMatter(-90), hueToMatter(270))
	}
}

// TestSaturationClampsToMatterScale: HM 100 (HA-canonical) must encode to
// 254 (the non-null max), not 255.
func TestSaturationClampsToMatterScale(t *testing.T) {
	if got := saturationToMatter(100); got != 254 {
		t.Errorf("saturationToMatter(100) = %d, want 254 (Matter null=255)", got)
	}
	if got := saturationToMatter(0); got != 0 {
		t.Errorf("saturationToMatter(0) = %d, want 0", got)
	}
	if got := saturationToMatter(150); got != 254 {
		t.Errorf("saturationToMatter(150) over-range = %d, want clamped to 254", got)
	}
}

// TestKelvinToMiredsConversionRange covers the Mired conversion at
// the typical warm/cool extremes.
func TestKelvinToMiredsConversionRange(t *testing.T) {
	// 6500 K → 153 Mireds (cool white); spec defaults the cluster
	// physical-min to ~153.
	if got := kelvinToMireds(6500); got != 153 {
		t.Errorf("kelvinToMireds(6500) = %d, want 153", got)
	}
	// 2700 K → ~370 Mireds (warm white).
	if got := kelvinToMireds(2700); got != 370 {
		t.Errorf("kelvinToMireds(2700) = %d, want 370", got)
	}
	// Extremes clamp into [matterMinMireds, matterMaxMireds].
	if got := kelvinToMireds(10000); got != matterMinMireds {
		t.Errorf("kelvinToMireds(10000) = %d, want clamp to %d", got, matterMinMireds)
	}
	if got := kelvinToMireds(1500); got != matterMaxMireds {
		t.Errorf("kelvinToMireds(1500) = %d, want clamp to %d", got, matterMaxMireds)
	}
}

// colorServer returns the ColorControl server a colour variant serves,
// as the endpoint assembler sees it.
func colorServer(t *testing.T, src interfaces.MatterEndpointSource) *fabriclight.ColorControlServer {
	t.Helper()
	for _, s := range src.MatterClusterServers() {
		if srv, ok := s.(*fabriclight.ColorControlServer); ok {
			return srv
		}
	}
	t.Fatal("no *fabriclight.ColorControlServer among MatterClusterServers()")
	return nil
}

// readU8 / readU16 read an attribute that must be served.
func readU8(t *testing.T, srv *fabriclight.ColorControlServer, attr uint32) uint8 {
	t.Helper()
	v, ok := srv.MatterRead(attr)
	if !ok {
		t.Fatalf("attribute 0x%04X not served", attr)
	}
	u, isU8 := v.(uint8)
	if !isU8 {
		t.Fatalf("attribute 0x%04X read %T, want uint8", attr, v)
	}
	return u
}

func readU16(t *testing.T, srv *fabriclight.ColorControlServer, attr uint32) uint16 {
	t.Helper()
	v, ok := srv.MatterRead(attr)
	if !ok {
		t.Fatalf("attribute 0x%04X not served", attr)
	}
	u, isU16 := v.(uint16)
	if !isU16 {
		t.Fatalf("attribute 0x%04X read %T, want uint16", attr, v)
	}
	return u
}

// writtenValues collects the last value w observed per parameter.
func writtenValues(w *colorStubWriter) map[hmenum.Parameter]any {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[hmenum.Parameter]any, len(w.calls))
	for _, c := range w.calls {
		out[c.param] = c.value
	}
	return out
}

const (
	ctCaps   = "ct"
	ctMinK   = 2700
	ctMaxK   = 6500
	onLevel  = 0.5
	offLevel = 0.0
)

func newCTLight(t *testing.T, w Writer) *ColorTempLight {
	t.Helper()
	caps := custom.LightCapabilities{SupportsColorTemp: true, Dimmable: true}
	ch := newColorTempRig(t, "HmIP-CTL:4", w, caps, ctMinK, ctMaxK)
	return NewColorTempLight(Config{Channel: ch, Writer: w, Capabilities: caps}, ctMinK, ctMaxK)
}

func newHSLight(t *testing.T, w Writer) *ColorLight {
	t.Helper()
	caps := custom.LightCapabilities{SupportsColor: true, Dimmable: true}
	ch := newColorRig(t, "HmIP-RGB:4", w, caps)
	return NewColorLight(Config{Channel: ch, Writer: w, Capabilities: caps})
}

func newRGBW(t *testing.T, w Writer) *RGBWLight {
	t.Helper()
	caps := custom.LightCapabilities{SupportsColor: true, SupportsColorTemp: true, Dimmable: true}
	ch := newRGBWRig(t, "HmIP-RGBW:4", w, caps)
	return NewRGBWLight(Config{Channel: ch, Writer: w, Capabilities: caps})
}

// --- ColorTempLight ---

// TestColorTempLightDeviceTypeIsColorTemperatureLight locks the
// 0x010C device type override on the embedded Light's 0x0101.
func TestColorTempLightDeviceTypeIsColorTemperatureLight(t *testing.T) {
	l := newCTLight(t, &stubWriter{})
	if got := l.MatterDeviceType(); got != 0x010C {
		t.Fatalf("ColorTempLight.MatterDeviceType = 0x%04X, want 0x010C", got)
	}
}

// TestColorTempLightExposesOnOffLevelAndColorControl confirms the
// three-cluster projection, with ColorControl served by go-fabric's
// server carrying the CT feature alone.
func TestColorTempLightExposesOnOffLevelAndColorControl(t *testing.T) {
	l := newCTLight(t, &stubWriter{})
	got := map[uint32]bool{}
	for _, s := range l.MatterClusterServers() {
		got[s.MatterClusterID()] = true
	}
	for _, want := range []uint32{0x0006, 0x0008, 0x0300} {
		if !got[want] {
			t.Errorf("ColorTempLight cluster 0x%04X missing from %v", want, got)
		}
	}
	srv := colorServer(t, l)
	fm, _ := srv.MatterRead(matterAttrFeatureMap)
	if fm != uint32(fabriclight.ColorFeatureColorTemperature) {
		t.Fatalf("FeatureMap = %v, want CT only (0x%X)", fm, uint32(fabriclight.ColorFeatureColorTemperature))
	}
	if got := readU8(t, srv, wire.ColorCtrlAttrColorMode); got != uint8(fabriclight.ColorModeColorTemperature) {
		t.Fatalf("ColorMode = %d, want 2 (ColorTemperature)", got)
	}
}

// TestColorTempServerIsOneInstanceAcrossReassemblies pins the holder: the
// assembler re-reads the servers on every reassembly and the bridge keeps
// the one it already serves.
func TestColorTempServerIsOneInstanceAcrossReassemblies(t *testing.T) {
	l := newCTLight(t, &stubWriter{})
	first, second := colorServer(t, l), colorServer(t, l)
	if first != second {
		t.Fatal("MatterClusterServers built a second ColorControl server")
	}
}

// TestColorTempServerMoveToColorTemperatureReachesTheCCU drives
// MoveToColorTemperature through go-fabric's server and expects the
// Kelvin write on the wire plus the attribute moved: 250 mireds is
// 4000 K. (370 mireds would be the server's own start value while the
// CCU has not reported one, and a command to the current value is no
// change — matter.js Datasource skips a deep-equal commit.)
func TestColorTempServerMoveToColorTemperatureReachesTheCCU(t *testing.T) {
	w := &colorStubWriter{}
	l := newCTLight(t, w)
	l.OnLevel(onLevel)
	srv := colorServer(t, l)
	req := wire.MoveToColorTemperatureRequest{ColorTemperatureMireds: 250}
	if _, err := srv.MatterInvoke(context.Background(), wire.ColorCtrlCmdMoveToColorTemperature, req); err != nil {
		t.Fatalf("MoveToColorTemperature: %v", err)
	}
	got, ok := writtenValues(w)[hmenum.ParameterColorTemperature]
	if !ok {
		t.Fatalf("COLOR_TEMPERATURE was not written; writes: %v", writtenValues(w))
	}
	if got != int32(4000) {
		t.Fatalf("COLOR_TEMPERATURE written = %v, want 4000 K", got)
	}
	if m := readU16(t, srv, wire.ColorCtrlAttrColorTemperatureMireds); m != 250 {
		t.Fatalf("ColorTemperatureMireds after the command = %d, want 250", m)
	}
}

// TestColorTempServerMoveToColorTemperatureGatedWhileOff mirrors matter.js
// ColorControlServer.ts #optionsAllowExecution through go-fabric's server
// and the light's own OnOff: while the light is off the command silently
// no-ops unless ExecuteIfOff is effective (mask bit 0 AND override bit 0).
func TestColorTempServerMoveToColorTemperatureGatedWhileOff(t *testing.T) {
	w := &colorStubWriter{}
	l := newCTLight(t, w)
	l.OnLevel(offLevel)
	srv := colorServer(t, l)

	req := wire.MoveToColorTemperatureRequest{ColorTemperatureMireds: 250}
	if _, err := srv.MatterInvoke(context.Background(), wire.ColorCtrlCmdMoveToColorTemperature, req); err != nil {
		t.Fatalf("gated MoveToColorTemperature returned error: %v", err)
	}
	if _, written := writtenValues(w)[hmenum.ParameterColorTemperature]; written {
		t.Fatal("gated MoveToColorTemperature wrote COLOR_TEMPERATURE while off without ExecuteIfOff")
	}

	req.OptionsMask, req.OptionsOverride = 0x01, 0x01
	if _, err := srv.MatterInvoke(context.Background(), wire.ColorCtrlCmdMoveToColorTemperature, req); err != nil {
		t.Fatalf("ExecuteIfOff MoveToColorTemperature returned error: %v", err)
	}
	if _, written := writtenValues(w)[hmenum.ParameterColorTemperature]; !written {
		t.Fatal("ExecuteIfOff MoveToColorTemperature did not reach the CCU")
	}
}

// TestColorTempServerConfirmedKelvinReachesTheServer pins the inbound
// path: a COLOR_TEMPERATURE the CCU confirms (a wall transmitter, a CCU
// program) lands on the server through Set — readable at once and
// reported through the change notifier — without a write.
func TestColorTempServerConfirmedKelvinReachesTheServer(t *testing.T) {
	w := &colorStubWriter{}
	l := newCTLight(t, w)
	srv := colorServer(t, l)
	reported := make(chan []uint32, 4)
	unsub := srv.OnMatterAttributesChanged(func(ids []uint32) { reported <- ids })
	defer unsub()

	l.kelvin.OnEvent(int32(4000))

	if m := readU16(t, srv, wire.ColorCtrlAttrColorTemperatureMireds); m != 250 {
		t.Fatalf("ColorTemperatureMireds after the confirmed 4000 K = %d, want 250", m)
	}
	select {
	case ids := <-reported:
		if !containsAttr(ids, wire.ColorCtrlAttrColorTemperatureMireds) {
			t.Fatalf("reported %v, want ColorTemperatureMireds", ids)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the confirmed colour temperature was not reported through the change notifier")
	}
	if len(w.calls) != 0 {
		t.Fatalf("a confirmed value must not echo into a write; writes: %v", writtenValues(w))
	}
}

func containsAttr(ids []uint32, want uint32) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// TestColorTempServerPhysicalMiredsFollowTheLightsKelvinBounds pins the
// projection rule for ColorTempPhysicalMin/MaxMireds: the light's own
// Kelvin bounds, reciprocated (higher Kelvin → lower mireds).
func TestColorTempServerPhysicalMiredsFollowTheLightsKelvinBounds(t *testing.T) {
	l := newCTLight(t, &stubWriter{})
	srv := colorServer(t, l)
	if got := readU16(t, srv, wire.ColorCtrlAttrColorTempPhysicalMin); got != kelvinToMireds(ctMaxK) {
		t.Fatalf("ColorTempPhysicalMinMireds = %d, want %d", got, kelvinToMireds(ctMaxK))
	}
	if got := readU16(t, srv, wire.ColorCtrlAttrColorTempPhysicalMax); got != kelvinToMireds(ctMinK) {
		t.Fatalf("ColorTempPhysicalMaxMireds = %d, want %d", got, kelvinToMireds(ctMinK))
	}

	r := newRGBW(t, &stubWriter{})
	r.MinKelvin, r.MaxKelvin = 2700, 6000
	rs := colorServer(t, r)
	if got := readU16(t, rs, wire.ColorCtrlAttrColorTempPhysicalMin); got != kelvinToMireds(6000) {
		t.Fatalf("RGBW ColorTempPhysicalMinMireds = %d, want %d", got, kelvinToMireds(6000))
	}
	if got := readU16(t, rs, wire.ColorCtrlAttrColorTempPhysicalMax); got != kelvinToMireds(2700) {
		t.Fatalf("RGBW ColorTempPhysicalMaxMireds = %d, want %d", got, kelvinToMireds(2700))
	}
}

// --- ColorLight ---

// TestColorLightDeviceTypeIsExtendedColorLight locks the 0x010D
// device type override.
func TestColorLightDeviceTypeIsExtendedColorLight(t *testing.T) {
	l := newHSLight(t, &stubWriter{})
	if got := l.MatterDeviceType(); got != 0x010D {
		t.Fatalf("ColorLight.MatterDeviceType = 0x%04X, want 0x010D", got)
	}
}

// TestColorLightColorModeIsHS locks the feature set (HS and the XY
// feature the ExtendedColorLight device type mandates) and the starting
// mode, which is the device's: HS.
func TestColorLightColorModeIsHS(t *testing.T) {
	l := newHSLight(t, &stubWriter{})
	srv := colorServer(t, l)
	fm, _ := srv.MatterRead(matterAttrFeatureMap)
	if fm != uint32(fabriclight.ColorFeatureHueSaturation|fabriclight.ColorFeatureXY) {
		t.Fatalf("FeatureMap = %v, want HS | XY", fm)
	}
	for _, attr := range []uint32{wire.ColorCtrlAttrColorMode, wire.ColorCtrlAttrEnhancedColorMode} {
		if got := readU8(t, srv, attr); got != uint8(fabriclight.ColorModeHueSaturation) {
			t.Fatalf("attr 0x%04X = %d, want 0 (HueSaturation)", attr, got)
		}
	}
}

// TestColorLightMoveToHueAndSaturationReachesTheCCU routes
// MoveToHueAndSaturation through go-fabric's server into one SetColor:
// Matter hue 127 is 180°, saturation 254 is the wire's 1.0.
func TestColorLightMoveToHueAndSaturationReachesTheCCU(t *testing.T) {
	w := &colorStubWriter{}
	l := newHSLight(t, w)
	l.OnLevel(onLevel)
	srv := colorServer(t, l)
	req := wire.MoveToHueAndSaturationRequest{Hue: 127, Saturation: 254}
	if _, err := srv.MatterInvoke(context.Background(), wire.ColorCtrlCmdMoveToHueAndSaturation, req); err != nil {
		t.Fatalf("MoveToHueAndSaturation: %v", err)
	}
	got := writtenValues(w)
	if got[hmenum.ParameterHue] != int32(180) {
		t.Fatalf("HUE written = %v, want 180", got[hmenum.ParameterHue])
	}
	if got[hmenum.ParameterSaturation] != 1.0 {
		t.Fatalf("SATURATION written = %v, want 1.0", got[hmenum.ParameterSaturation])
	}
	if h := readU8(t, srv, wire.ColorCtrlAttrCurrentHue); h != 127 {
		t.Fatalf("CurrentHue after the command = %d, want 127", h)
	}
}

// TestColorLightMoveToHueAndSaturationGatedWhileOff is the HS twin of the
// CT gate test above.
func TestColorLightMoveToHueAndSaturationGatedWhileOff(t *testing.T) {
	w := &colorStubWriter{}
	l := newHSLight(t, w)
	l.OnLevel(offLevel)
	srv := colorServer(t, l)

	req := wire.MoveToHueAndSaturationRequest{Hue: 127, Saturation: 254}
	if _, err := srv.MatterInvoke(context.Background(), wire.ColorCtrlCmdMoveToHueAndSaturation, req); err != nil {
		t.Fatalf("gated MoveToHueAndSaturation returned error: %v", err)
	}
	if _, written := writtenValues(w)[hmenum.ParameterHue]; written {
		t.Fatal("gated MoveToHueAndSaturation wrote HUE while off without ExecuteIfOff")
	}

	req.OptionsMask, req.OptionsOverride = 0x01, 0x01
	if _, err := srv.MatterInvoke(context.Background(), wire.ColorCtrlCmdMoveToHueAndSaturation, req); err != nil {
		t.Fatalf("ExecuteIfOff MoveToHueAndSaturation returned error: %v", err)
	}
	if _, written := writtenValues(w)[hmenum.ParameterHue]; !written {
		t.Fatal("ExecuteIfOff MoveToHueAndSaturation did not reach the CCU")
	}
}

// TestColorLightConfirmedColourReachesTheServer pins the inbound HS path:
// HUE and SATURATION the CCU confirms land on the server through Set.
func TestColorLightConfirmedColourReachesTheServer(t *testing.T) {
	w := &colorStubWriter{}
	l := newHSLight(t, w)
	srv := colorServer(t, l)

	l.hue.OnEvent(int32(90))
	l.saturation.OnEvent(0.5)

	if h := readU8(t, srv, wire.ColorCtrlAttrCurrentHue); h != hueToMatter(90) {
		t.Fatalf("CurrentHue after the confirmed 90° = %d, want %d", h, hueToMatter(90))
	}
	if s := readU8(t, srv, wire.ColorCtrlAttrCurrentSaturation); s != saturationToMatter(50) {
		t.Fatalf("CurrentSaturation after the confirmed 0.5 = %d, want %d", s, saturationToMatter(50))
	}
	if len(w.calls) != 0 {
		t.Fatalf("a confirmed colour must not echo into a write; writes: %v", writtenValues(w))
	}
}

// TestColorLightServerStartsFromTheObservedColour: a colour observed
// before the server is built is its initial state, not the zero colour.
func TestColorLightServerStartsFromTheObservedColour(t *testing.T) {
	l := newHSLight(t, &stubWriter{})
	l.hue.OnEvent(int32(180))
	l.saturation.OnEvent(1.0)
	srv := colorServer(t, l)
	if h := readU8(t, srv, wire.ColorCtrlAttrCurrentHue); h != hueToMatter(180) {
		t.Fatalf("initial CurrentHue = %d, want %d", h, hueToMatter(180))
	}
}

// echoColorWriter is a CCU that confirms every HUE / SATURATION write
// before the write returns, as the simulator and a fast CCU do: the
// confirmed value reaches the light's data point — and through it the
// server's Set — from inside the writer the server is driving.
type echoColorWriter struct {
	colorStubWriter
	l *ColorLight
}

func (w *echoColorWriter) SetValue(ctx context.Context, addr string, p hmenum.Parameter, v any, prio hmenum.CommandPriority) error {
	_ = w.colorStubWriter.SetValue(ctx, addr, p, v, prio)
	switch p { //nolint:exhaustive // the two colour axes are the only parameters this writer echoes
	case hmenum.ParameterHue:
		w.l.hue.OnEvent(v.(int32))
	case hmenum.ParameterSaturation:
		w.l.saturation.OnEvent(v.(float64))
	default:
	}
	return nil
}

// sRGB red in CurrentX / CurrentY (matter.js hsvToXy(0, 1) × 65536).
const redX, redY = 45914, 19615

// TestColorLightMoveToColorWritesTheConvertedHueAndSaturation routes
// MoveToColor (the XY feature) through go-fabric's server into one
// SetColor: the x/y target is converted through matter.js's xyToHsv —
// sRGB red lands as HUE 0 and SATURATION 1.0 — and the server ends in the
// XY mode at the target. With a CCU that echoes each write before it
// returns, the HS echo confirms the x/y write in the device's own colour
// space instead of counting as a colour of its own (go-fabric #79): the
// server keeps the XY mode and the target, and the CCU holds red, not the
// converted colour of an intermediate step (HUE 358 was the measured
// failure).
func TestColorLightMoveToColorWritesTheConvertedHueAndSaturation(t *testing.T) {
	for _, tc := range []struct {
		name string
		echo bool
	}{{"silent CCU", false}, {"echoing CCU", true}} {
		t.Run(tc.name, func(t *testing.T) {
			var l *ColorLight
			stub := &colorStubWriter{}
			if tc.echo {
				e := &echoColorWriter{}
				l = newHSLight(t, e)
				e.l = l
				stub = &e.colorStubWriter
			} else {
				l = newHSLight(t, stub)
			}
			l.OnLevel(onLevel)
			l.hue.OnEvent(int32(180))
			l.saturation.OnEvent(0.5)
			srv := colorServer(t, l)

			req := colorcontrolspec.MoveToColorRequest{ColorX: redX, ColorY: redY}
			if _, err := srv.MatterInvoke(context.Background(), wire.ColorCtrlCmdMoveToColor, req); err != nil {
				t.Fatalf("MoveToColor: %v", err)
			}
			got := writtenValues(stub)
			if got[hmenum.ParameterHue] != int32(0) {
				t.Fatalf("HUE written = %v, want 0 (sRGB red)", got[hmenum.ParameterHue])
			}
			if s, _ := got[hmenum.ParameterSaturation].(float64); s < 0.99 {
				t.Fatalf("SATURATION written = %v, want 1.0", got[hmenum.ParameterSaturation])
			}
			if m := readU8(t, srv, wire.ColorCtrlAttrColorMode); m != uint8(fabriclight.ColorModeXY) {
				t.Fatalf("ColorMode after MoveToColor = %d, want 1 (XY)", m)
			}
			if x, y := readU16(t, srv, wire.ColorCtrlAttrCurrentX), readU16(t, srv, wire.ColorCtrlAttrCurrentY); x != redX || y != redY {
				t.Fatalf("CurrentX/Y after MoveToColor = %d/%d, want %d/%d", x, y, redX, redY)
			}
		})
	}
}

// TestColorLightConfirmedColourKeepsTheServersXYMode: a HUE / SATURATION
// the CCU confirms that is the colour the server holds in the XY mode
// (the late echo of a MoveToColor) does not flip the server back to HS;
// a different colour — the wall remote — does, and is reported in HS.
func TestColorLightConfirmedColourKeepsTheServersXYMode(t *testing.T) {
	stub := &colorStubWriter{}
	l := newHSLight(t, stub)
	l.OnLevel(onLevel)
	srv := colorServer(t, l)
	req := colorcontrolspec.MoveToColorRequest{ColorX: redX, ColorY: redY}
	if _, err := srv.MatterInvoke(context.Background(), wire.ColorCtrlCmdMoveToColor, req); err != nil {
		t.Fatalf("MoveToColor: %v", err)
	}
	// The CCU's late echo of the write: red in the device's colour space.
	l.hue.OnEvent(int32(0))
	l.saturation.OnEvent(1.0)
	if m := readU8(t, srv, wire.ColorCtrlAttrColorMode); m != uint8(fabriclight.ColorModeXY) {
		t.Fatalf("ColorMode after the confirmed echo = %d, want 1 (XY kept)", m)
	}
	if x := readU16(t, srv, wire.ColorCtrlAttrCurrentX); x != redX {
		t.Fatalf("CurrentX after the confirmed echo = %d, want %d (target kept)", x, redX)
	}
	// A colour of the device's own: the server follows it in HS.
	l.hue.OnEvent(int32(120))
	l.saturation.OnEvent(0.5)
	if m := readU8(t, srv, wire.ColorCtrlAttrColorMode); m != uint8(fabriclight.ColorModeHueSaturation) {
		t.Fatalf("ColorMode after a device-originated colour = %d, want 0 (HS)", m)
	}
	if h := readU8(t, srv, wire.ColorCtrlAttrCurrentHue); h != hueToMatter(120) {
		t.Fatalf("CurrentHue after the device-originated colour = %d, want %d", h, hueToMatter(120))
	}
}

// TestColorLightXYStartsFromTheObservedColour: CurrentX / CurrentY start
// as the x/y of the colour observed before the server was built, so a
// controller reading them before the first MoveToColor sees the light's
// colour rather than the zero colour.
func TestColorLightXYStartsFromTheObservedColour(t *testing.T) {
	l := newHSLight(t, &stubWriter{})
	l.hue.OnEvent(int32(0))
	l.saturation.OnEvent(1.0)
	srv := colorServer(t, l)
	x, y := readU16(t, srv, wire.ColorCtrlAttrCurrentX), readU16(t, srv, wire.ColorCtrlAttrCurrentY)
	if x < redX-2 || x > redX+2 || y < redY-2 || y > redY+2 {
		t.Fatalf("initial CurrentX/Y = %d/%d, want sRGB red %d/%d (±2)", x, y, redX, redY)
	}
}

// --- RGBWLight ---

// TestRGBWLightDeviceTypeIsExtendedColorLight locks the 0x010D device
// type override.
func TestRGBWLightDeviceTypeIsExtendedColorLight(t *testing.T) {
	l := newRGBW(t, &stubWriter{})
	if got := l.MatterDeviceType(); got != 0x010D {
		t.Fatalf("RGBWLight.MatterDeviceType = 0x%04X, want 0x010D", got)
	}
}

// TestRGBWLightFeatureMapAdvertisesHSAndCT confirms the FeatureMap
// carries both HS and CT.
func TestRGBWLightFeatureMapAdvertisesHSAndCT(t *testing.T) {
	l := newRGBW(t, &stubWriter{})
	srv := colorServer(t, l)
	v, _ := srv.MatterRead(matterAttrFeatureMap)
	want := uint32(fabriclight.ColorFeatureHueSaturation | fabriclight.ColorFeatureXY | fabriclight.ColorFeatureColorTemperature)
	if v != want {
		t.Fatalf("FeatureMap = %v, want 0x%X (HS|XY|CT)", v, want)
	}
}

// TestRGBWLightMoveToColorTemperatureWritesOnlyTheColourTemperature
// pins the writer split: a CT command reaches the CCU as one
// COLOR_TEMPERATURE write; the hue/saturation fields the colour writer is
// handed alongside are the last of their mode and must not be written.
func TestRGBWLightMoveToColorTemperatureWritesOnlyTheColourTemperature(t *testing.T) {
	w := &colorStubWriter{}
	l := newRGBW(t, w)
	l.recordMode("RGBW")
	l.OnLevel(onLevel)
	srv := colorServer(t, l)
	req := wire.MoveToColorTemperatureRequest{ColorTemperatureMireds: 250}
	if _, err := srv.MatterInvoke(context.Background(), wire.ColorCtrlCmdMoveToColorTemperature, req); err != nil {
		t.Fatalf("MoveToColorTemperature: %v", err)
	}
	got := writtenValues(w)
	if got[hmenum.ParameterColorTemperature] != int32(4000) {
		t.Fatalf("COLOR_TEMPERATURE written = %v, want 4000", got[hmenum.ParameterColorTemperature])
	}
	for _, p := range []hmenum.Parameter{hmenum.ParameterHue, hmenum.ParameterSaturation} {
		if _, written := got[p]; written {
			t.Fatalf("%s was written by a colour-temperature command; writes: %v", p, got)
		}
	}
	if got := readU8(t, srv, wire.ColorCtrlAttrColorMode); got != uint8(fabriclight.ColorModeColorTemperature) {
		t.Fatalf("ColorMode after MoveToColorTemperature = %d, want 2", got)
	}
}

// TestRGBWLightMoveToHueWritesTheColour: an HS command in RGBW mode
// reaches the CCU as HUE + SATURATION and leaves COLOR_TEMPERATURE alone.
func TestRGBWLightMoveToHueWritesTheColour(t *testing.T) {
	w := &colorStubWriter{}
	l := newRGBW(t, w)
	l.recordMode("RGBW")
	l.OnLevel(onLevel)
	srv := colorServer(t, l)
	req := wire.MoveToHueRequest{Hue: 127}
	if _, err := srv.MatterInvoke(context.Background(), wire.ColorCtrlCmdMoveToHue, req); err != nil {
		t.Fatalf("MoveToHue: %v", err)
	}
	got := writtenValues(w)
	if got[hmenum.ParameterHue] != int32(180) {
		t.Fatalf("HUE written = %v, want 180", got[hmenum.ParameterHue])
	}
	if _, written := got[hmenum.ParameterColorTemperature]; written {
		t.Fatalf("COLOR_TEMPERATURE was written by a hue command; writes: %v", got)
	}
}

// TestRGBWLightColorModeFollowsDeviceOperatingMode locks the ColorMode
// projection contract: CT when the device-side operating mode is
// TunableWhite, HS in every other case, HS before the mode was observed.
// Apple Home reads ColorMode at Subscribe-initial and routes MoveTo*
// commands accordingly.
func TestRGBWLightColorModeFollowsDeviceOperatingMode(t *testing.T) {
	cases := []struct {
		modeWire string
		want     fabriclight.ColorMode
	}{
		{"2_TUNABLE_WHITE", fabriclight.ColorModeColorTemperature},
		{"RGB", fabriclight.ColorModeHueSaturation},
		{"RGBW", fabriclight.ColorModeHueSaturation},
		{"4_PWM", fabriclight.ColorModeHueSaturation},
		{"", fabriclight.ColorModeHueSaturation},
	}
	for _, tc := range cases {
		t.Run(tc.modeWire, func(t *testing.T) {
			l := newRGBW(t, &stubWriter{})
			if tc.modeWire != "" {
				l.recordMode(tc.modeWire)
			}
			srv := colorServer(t, l)
			for _, attr := range []uint32{wire.ColorCtrlAttrColorMode, wire.ColorCtrlAttrEnhancedColorMode} {
				if got := readU8(t, srv, attr); got != uint8(tc.want) {
					t.Fatalf("attr 0x%04X mode=%q: got %d, want %d", attr, tc.modeWire, got, tc.want)
				}
			}
		})
	}
}

// TestRGBWLightOperatingModeChangeMovesTheServer pins the mode hook: a
// device switched to TUNABLE_WHITE after the server was built reports the
// CT mode through Set, with the observed colour temperature, and a switch
// back reports HS again.
func TestRGBWLightOperatingModeChangeMovesTheServer(t *testing.T) {
	l := newRGBW(t, &stubWriter{})
	l.recordMode("RGBW")
	l.hue.OnEvent(int32(180))
	l.saturation.OnEvent(1.0)
	l.kelvin.OnEvent(int32(4000))
	srv := colorServer(t, l)
	reported := make(chan []uint32, 8)
	unsub := srv.OnMatterAttributesChanged(func(ids []uint32) { reported <- ids })
	defer unsub()

	l.recordMode("2_TUNABLE_WHITE")
	if got := readU8(t, srv, wire.ColorCtrlAttrColorMode); got != uint8(fabriclight.ColorModeColorTemperature) {
		t.Fatalf("ColorMode after the switch to TUNABLE_WHITE = %d, want 2", got)
	}
	if m := readU16(t, srv, wire.ColorCtrlAttrColorTemperatureMireds); m != 250 {
		t.Fatalf("ColorTemperatureMireds after the switch = %d, want 250", m)
	}
	select {
	case ids := <-reported:
		if !containsAttr(ids, wire.ColorCtrlAttrColorMode) {
			t.Fatalf("reported %v, want ColorMode", ids)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the mode switch was not reported through the change notifier")
	}

	l.recordMode("RGB")
	if got := readU8(t, srv, wire.ColorCtrlAttrColorMode); got != uint8(fabriclight.ColorModeHueSaturation) {
		t.Fatalf("ColorMode after the switch back to RGB = %d, want 0", got)
	}
}

// echoRGBWWriter is a CCU that confirms every HUE / SATURATION /
// COLOR_TEMPERATURE write before the write returns, as the simulator and
// a fast CCU do.
type echoRGBWWriter struct {
	colorStubWriter
	l *RGBWLight
}

func (w *echoRGBWWriter) SetValue(ctx context.Context, addr string, p hmenum.Parameter, v any, prio hmenum.CommandPriority) error {
	_ = w.colorStubWriter.SetValue(ctx, addr, p, v, prio)
	switch p { //nolint:exhaustive // the three colour axes are the only parameters this writer echoes
	case hmenum.ParameterHue:
		w.l.hue.OnEvent(v.(int32))
	case hmenum.ParameterSaturation:
		w.l.saturation.OnEvent(v.(float64))
	case hmenum.ParameterColorTemperature:
		w.l.kelvin.OnEvent(v.(int32))
	default:
	}
	return nil
}

// TestRGBWCombinedLightColorModeFollowsTheConfirmedAxis: the HmIP-LSC
// runs hue/saturation and colour temperature at once and has no
// operating mode, so the colour mode is the axis the CCU confirmed last.
// With the colour-temperature axis carrying a value, a
// MoveToHueAndSaturation whose HUE / SATURATION the CCU echoes leaves the
// server in HS at the commanded colour — reporting the echo as a CT
// colour would contradict the command just carried out and leave the
// server in CT (the measured state before this rule). A later
// COLOR_TEMPERATURE confirmation moves it to CT.
func TestRGBWCombinedLightColorModeFollowsTheConfirmedAxis(t *testing.T) {
	e := &echoRGBWWriter{}
	l := newRGBW(t, e)
	e.l = l
	l.colorTempCombined = true
	l.OnLevel(onLevel)
	l.hue.OnEvent(int32(180))
	l.saturation.OnEvent(0.5)
	l.kelvin.OnEvent(int32(4000))
	srv := colorServer(t, l)
	if m := readU8(t, srv, wire.ColorCtrlAttrColorMode); m != uint8(fabriclight.ColorModeColorTemperature) {
		t.Fatalf("ColorMode with the CT axis active = %d, want 2 (CT)", m)
	}

	req := wire.MoveToHueAndSaturationRequest{Hue: 20, Saturation: 254}
	if _, err := srv.MatterInvoke(context.Background(), wire.ColorCtrlCmdMoveToHueAndSaturation, req); err != nil {
		t.Fatalf("MoveToHueAndSaturation: %v", err)
	}
	if m := readU8(t, srv, wire.ColorCtrlAttrColorMode); m != uint8(fabriclight.ColorModeHueSaturation) {
		t.Fatalf("ColorMode after the echoed MoveToHueAndSaturation = %d, want 0 (HS)", m)
	}
	if h := readU8(t, srv, wire.ColorCtrlAttrCurrentHue); h != 20 {
		t.Fatalf("CurrentHue after the echoed MoveToHueAndSaturation = %d, want 20", h)
	}

	l.kelvin.OnEvent(int32(3000)) // the wall remote turns the white axis
	if m := readU8(t, srv, wire.ColorCtrlAttrColorMode); m != uint8(fabriclight.ColorModeColorTemperature) {
		t.Fatalf("ColorMode after a confirmed COLOR_TEMPERATURE = %d, want 2 (CT)", m)
	}
	if mireds := readU16(t, srv, wire.ColorCtrlAttrColorTemperatureMireds); mireds != kelvinToMireds(3000) {
		t.Fatalf("ColorTemperatureMireds = %d, want %d", mireds, kelvinToMireds(3000))
	}
}

// TestLightCloseQuiescesTheColourServer: Close must not panic with or
// without a built server, and must leave the server readable.
func TestLightCloseQuiescesTheColourServer(t *testing.T) {
	l := newRGBW(t, &stubWriter{})
	l.Close()
	srv := colorServer(t, l)
	l.Close()
	readU8(t, srv, wire.ColorCtrlAttrColorMode)
}
