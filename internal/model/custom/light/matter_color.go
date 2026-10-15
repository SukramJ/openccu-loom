// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"sync"

	fabriclight "github.com/SukramJ/go-fabric/cluster/light"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Compile-time assertions: the colour-capable light variants
// (ColorLight, ColorTempLight, RGBWLight) participate in the Matter
// source surface (ADR 0012). Each shadows the embedded Light's
// MatterDeviceType / MatterClusterServers methods to add a ColorControl
// (0x0300) projection on top of OnOff + LevelControl — go-fabric's
// [fabriclight.ColorControlServer] since the ADR 0084 ColorControl
// follow-up, with the light's CCU writes as its writer ports and the
// CCU's confirmed colour fed back through [fabriclight.ColorControlServer.Set].
//
// FixedColorLight has its own palette-quantised projection in
// matter_fixedcolor.go. EffectLight, DRGDaliLight and SoundPlayerLED stay
// with the inherited Light projection (OnOff + LevelControl): ADR 0012's
// mapping table bridges them as DimmableLight, and effect dispatch has no
// Matter cluster.
var (
	_ interfaces.MatterEndpointSource = (*ColorLight)(nil)
	_ interfaces.MatterEndpointSource = (*ColorTempLight)(nil)
	_ interfaces.MatterEndpointSource = (*RGBWLight)(nil)
	// The colour variants shadow the embedded Light's OnMatterValueChanged
	// (see below) so a colour change — hue/saturation, the RF COLOR
	// integer, or colour temperature — dirty-marks the endpoint in
	// addition to the inherited on/off + brightness propagation, exactly
	// as Climate.OnMatterValueChanged fans out its own value-bearing DPs.
	// The ColorControl attributes themselves are reported by the server's
	// own change notifier once the confirmed colour reaches it.
	_ interfaces.MatterChangeNotifier = (*ColorLight)(nil)
	_ interfaces.MatterChangeNotifier = (*ColorTempLight)(nil)
	_ interfaces.MatterChangeNotifier = (*RGBWLight)(nil)

	_ fabriclight.OnOffState = (*Light)(nil)
)

// OnMatterValueChanged implements [interfaces.MatterChangeNotifier],
// shadowing the embedded *Light's. A colour light's ColorControl cluster
// depends on HUE/SATURATION or the RF colour dimmers' single COLOR
// integer — DPs the embedded Light knows nothing about — so without this
// override a colour changed at the wall remote, in the CCU WebUI, or from
// a CCU program never dirty-marks the endpoint. Exactly one of
// (hue+saturation) and colorIndex is non-nil per device family; each
// embedded DP's own OnMatterValueChanged guards a nil receiver, so the
// unused one contributes a no-op unsubscribe.
func (l *ColorLight) OnMatterValueChanged(cb func()) func() {
	if l == nil || cb == nil {
		return func() {}
	}
	return custom.CombineUnsubs(
		l.Light.OnMatterValueChanged(cb),
		l.hue.OnMatterValueChanged(cb),
		l.saturation.OnMatterValueChanged(cb),
		l.colorIndex.OnMatterValueChanged(cb),
	)
}

// OnMatterValueChanged implements [interfaces.MatterChangeNotifier],
// shadowing the embedded *Light's — see [ColorLight.OnMatterValueChanged]
// for the rationale. Exactly one of kelvin and colorLevel is non-nil per
// device family (HmIP COLOR_TEMPERATURE vs. the RF tunable-white
// COLOR_LEVEL channel); the unused one's nil-guarded
// OnMatterValueChanged contributes a no-op unsubscribe.
func (l *ColorTempLight) OnMatterValueChanged(cb func()) func() {
	if l == nil || cb == nil {
		return func() {}
	}
	return custom.CombineUnsubs(
		l.Light.OnMatterValueChanged(cb),
		l.kelvin.OnMatterValueChanged(cb),
		l.colorLevel.OnMatterValueChanged(cb),
	)
}

// OnMatterValueChanged implements [interfaces.MatterChangeNotifier],
// shadowing the embedded *ColorLight's — see
// [ColorLight.OnMatterValueChanged] for the rationale. RGBWLight adds its
// own kelvin (COLOR_TEMPERATURE) axis on top of the embedded ColorLight's
// hue/saturation/colorIndex, so both must fan into the callback.
func (l *RGBWLight) OnMatterValueChanged(cb func()) func() {
	if l == nil || cb == nil {
		return func() {}
	}
	return custom.CombineUnsubs(
		l.ColorLight.OnMatterValueChanged(cb),
		l.kelvin.OnMatterValueChanged(cb),
	)
}

// Matter device types of the colour projections (matter.js
// packages/node/src/devices/color-temperature-light.ts and
// extended-color-light.ts). The ColorControl cluster's ids, revision,
// attribute and command lists come from go-fabric's generated
// definition behind [fabriclight.ColorControlServer].
const (
	matterDeviceTypeColorTemperatureLight uint16 = 0x010C
	matterDeviceTypeExtendedColorLight    uint16 = 0x010D

	// matterCIEScale is the CurrentX / CurrentY encoding: CIE 1931 x and
	// y times 65536 (0..65279 served).
	matterCIEScale = 65536.0

	// matterHueScale matches Matter's CurrentHue range (0..254); the
	// HM hue is in degrees (0..359). Same reasoning as
	// [matterLevelMax] in matter.go: 0xFF is the null sentinel for
	// nullable uint8 attributes.
	matterHueScale = 254.0

	// Matter colour-temperature bounds. Per-device narrowing IS wired:
	// both CT projections configure ColorTempPhysicalMin/MaxMireds from
	// the light's own Kelvin bounds, which come from the
	// COLOR_TEMPERATURE descriptor (see [kelvinBoundsFromChannel]).
	//
	// This pair is what [kelvinToMireds] clamps into — the typical
	// "warm-cool LED" window. The clamp is loom-local and its width is
	// **unverified**: matter.js color-control.element.ts constrains the
	// attribute to the full uint16 range, not to this window, and no
	// device descriptor in the fleet names 2000/6535 K. A device
	// declaring a wider range therefore reaches Home Assistant unclamped
	// (payload.go derives min/max mireds straight from the bounds) and
	// Matter clamped. What would settle the width is a matter.js or
	// firmware statement about the advertised range; until then the
	// window stays as shipped rather than being widened on a guess.
	matterMinMireds uint16 = 153 // ≈ 6535 K
	matterMaxMireds uint16 = 500 // ≈ 2000 K

	// matterInitialMireds is the colour temperature the CT servers start
	// from while the CCU has not reported one — go-fabric's own default
	// (DefaultColorControlServerConfig, ≈ 2700 K warm white), cropped to
	// the light's physical range by the server. The first confirmed
	// COLOR_TEMPERATURE replaces it through Set.
	matterInitialMireds uint16 = 370
)

// hueToMatter encodes an HM hue (0..359°) into Matter CurrentHue.
func hueToMatter(hue int32) uint8 {
	hue = ((hue % 360) + 360) % 360
	v := float64(hue) * matterHueScale / 360.0
	if v > matterHueScale {
		v = matterHueScale
	}
	return uint8(v + 0.5)
}

// matterHueToHM is the inverse of [hueToMatter].
func matterHueToHM(m uint8) int32 {
	v := float64(m) * 360.0 / matterHueScale
	return int32(v + 0.5)
}

// saturationToMatter encodes an HA-canonical saturation (0..100, as reported
// by [ColorLight.Color]) into Matter CurrentSaturation (0..254).
func saturationToMatter(s float64) uint8 {
	if s < 0 {
		return 0
	}
	if s > 100 {
		return uint8(matterHueScale)
	}
	return uint8(s/100*matterHueScale + 0.5)
}

// matterSaturationToHM is the inverse of [saturationToMatter]: it decodes
// Matter CurrentSaturation (0..254) into the HA-canonical 0..100 value
// [ColorLight.SetColor] expects.
func matterSaturationToHM(m uint8) float64 {
	if float64(m) >= matterHueScale {
		return 100
	}
	return float64(m) / matterHueScale * 100
}

// kelvinToMireds converts Kelvin into Matter's reciprocal mireds.
// Matter spec 3.2.7.10: ColorTemperatureMireds = 1_000_000 / Kelvin.
func kelvinToMireds(k int32) uint16 {
	if k <= 0 {
		return matterMaxMireds
	}
	v := 1_000_000 / k
	if v < int32(matterMinMireds) {
		return matterMinMireds
	}
	if v > int32(matterMaxMireds) {
		return matterMaxMireds
	}
	return uint16(v)
}

// miredsToKelvin is the inverse of [kelvinToMireds].
func miredsToKelvin(m uint16) int32 {
	if m == 0 {
		return 0
	}
	return 1_000_000 / int32(m)
}

// OnOff implements [fabriclight.OnOffState]: the ExecuteIfOff gate of
// every ColorControl command reads the light's own on/off (the single
// LEVEL knob: LEVEL>0 is on), as matter.js reads the endpoint's OnOff
// behaviour (ColorControlServer.ts #optionsAllowExecution). Unobserved
// counts as off, so a command before the first CCU report is gated
// unless the controller sets ExecuteIfOff.
func (l *Light) OnOff() bool {
	on, _ := l.IsOn()
	return on
}

// matterColorHolder keeps the one ColorControl server a colour light
// serves across [interfaces.MatterEndpointSource.MatterClusterServers]
// calls, for the same reason [generic.MatterOnOffHolder] does: the
// assembler re-reads the servers on every reassembly and the bridge
// keeps the ones it already serves; a server rebuilt each time would
// lose its colour state and the subscription state behind it. It lives
// on the embedded [Light] so [Light.Close] can quiesce it whichever
// variant built it.
type matterColorHolder struct {
	mu  sync.Mutex
	srv *fabriclight.ColorControlServer
}

// server returns the held server, building it through build on first
// use.
func (h *matterColorHolder) server(build func() *fabriclight.ColorControlServer) *fabriclight.ColorControlServer {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.srv == nil {
		h.srv = build()
	}
	return h.srv
}

// current returns the held server, nil while none was built.
func (h *matterColorHolder) current() *fabriclight.ColorControlServer {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.srv
}

// quiesce stops the held server's transition engine when the light
// retires. A light that never served Matter has nothing to stop.
func (h *matterColorHolder) quiesce() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.srv != nil {
		h.srv.MatterQuiesce()
	}
}

// physicalMireds is the ColorTempPhysicalMin/MaxMireds pair of a light
// with the given Kelvin bounds: higher Kelvin → lower mireds, so the
// maximum Kelvin is the minimum mireds.
func physicalMireds(minKelvin, maxKelvin int32) (minMireds, maxMireds uint16) {
	return kelvinToMireds(maxKelvin), kelvinToMireds(minKelvin)
}

// hsColorWriter is the [fabriclight.ColorWriter] of a hue/saturation
// light: every colour the server applies becomes one [ColorLight.SetColor]
// at [matterDispatchPriority] — one atomic HUE+SATURATION put_paramset,
// or the RF dimmers' single COLOR integer. An x/y colour (MoveToColor
// and its Move/Step siblings, the XY feature) is converted to hue and
// saturation through go-fabric's matter.js conversion before the write;
// the device's HUE / SATURATION echo confirms the x/y write on the
// server in the device's own colour space (go-fabric #79). The server
// configures no transition engine (hand-off, like LevelControl), so a
// MoveToHue with a TransitionTime applies at once as matter.js does
// without managed transitions, and Move/Step/Stop move the in-process
// attributes and reach the CCU as whole colours.
type hsColorWriter struct{ l *ColorLight }

// SetColor implements [fabriclight.ColorWriter]. A colour in the CT mode
// (the RGBW server switching mode on a MoveToColorTemperature) is the
// colour temperature writer's business: the hue/saturation fields are
// the last of their mode and must not be written to the CCU.
func (w hsColorWriter) SetColor(ctx context.Context, c fabriclight.Color) error {
	switch c.Mode {
	case fabriclight.ColorModeColorTemperature:
		return nil
	case fabriclight.ColorModeXY:
		hue, sat := xyToHM(c.X, c.Y)
		return w.l.SetColor(ctx, hue, sat, matterDispatchPriority)
	default:
		return w.l.SetColor(ctx, matterHueToHM(c.Hue), matterSaturationToHM(c.Saturation), matterDispatchPriority)
	}
}

// xyToHM converts Matter CurrentX / CurrentY (CIE 1931 x and y times
// 65536) into the HM hue in whole degrees (0..359) and the HA-canonical
// saturation (0..100) [ColorLight.SetColor] expects, through matter.js's
// xyToHsv ([fabriclight.XYToHSV]).
func xyToHM(x, y uint16) (hue int32, saturation float64) {
	deg, sat := fabriclight.XYToHSV(float64(x)/matterCIEScale, float64(y)/matterCIEScale)
	return int32(deg+0.5) % 360, sat * 100
}

// confirmsXY reports whether the device's hue/saturation colour c is the
// colour the server holds in the XY mode — the x/y target converted the
// way [xyToHM] wrote it, within one attribute unit, hue around the wrap.
// go-fabric matches a confirmation against the in-flight colour only
// while its writer runs (#79); a real CCU confirms a write as a later
// event, after the command answered, and a Set with that echo would
// count as a colour of the device's own and move the server to HS. The
// echo is no change, so the hooks skip it; a colour that differs — the
// wall remote, a CCU program — reaches Set and is reported in HS.
func confirmsXY(held, c fabriclight.Color) bool {
	if held.Mode != fabriclight.ColorModeXY || c.Mode != fabriclight.ColorModeHueSaturation {
		return false
	}
	hue, sat := xyToHM(held.X, held.Y)
	wantHue, wantSat := hueToMatter(hue), saturationToMatter(sat)
	dh := absDiffU8(wantHue, c.Hue)
	if dh > uint8(matterHueScale)/2 {
		dh = uint8(matterHueScale) - dh
	}
	return dh <= 1 && absDiffU8(wantSat, c.Saturation) <= 1
}

func absDiffU8(a, b uint8) uint8 {
	if a > b {
		return a - b
	}
	return b - a
}

// hmToXY is the inverse of [xyToHM]: the x/y the server would report for
// the device's hue and saturation, through matter.js's hsvToXy.
func hmToXY(hue int32, saturation float64) (x, y uint16) {
	fx, fy := fabriclight.HSVToXY(float64(hue), saturation/100)
	return uint16(fx*matterCIEScale + 0.5), uint16(fy*matterCIEScale + 0.5)
}

// ctColorWriter is the [fabriclight.ColorTemperatureWriter] of a
// colour-temperature light: every mired value the server applies becomes
// one SetKelvin at [matterDispatchPriority].
type ctColorWriter struct {
	setKelvin func(ctx context.Context, kelvin int32) error
}

// SetColorTemperatureMireds implements [fabriclight.ColorTemperatureWriter].
func (w ctColorWriter) SetColorTemperatureMireds(ctx context.Context, mireds uint16) error {
	return w.setKelvin(ctx, miredsToKelvin(mireds))
}

// MatterDeviceType for ColorTempLight overrides Light's projection
// (DimmableLight) with ColorTemperatureLight (0x010C).
func (l *ColorTempLight) MatterDeviceType() uint16 {
	return matterDeviceTypeColorTemperatureLight
}

// MatterClusterServers for ColorTempLight returns OnOff + LevelControl
// (inherited via the embedded Light's projection) plus ColorControl with
// the CT feature.
func (l *ColorTempLight) MatterClusterServers() []interfaces.MatterClusterServer {
	servers := l.Light.MatterClusterServers()
	return append(servers, l.matterColorServer())
}

// hsColor is the server-side colour of the light's observed hue and
// saturation, in the HS mode.
func (l *ColorLight) hsColor() (fabriclight.Color, bool) {
	hue, sat, observed := l.Color()
	if !observed {
		return fabriclight.Color{}, false
	}
	return fabriclight.Color{
		Mode:       fabriclight.ColorModeHueSaturation,
		Hue:        hueToMatter(hue),
		Saturation: saturationToMatter(sat),
	}, true
}

// ctColor is the server-side colour of the light's observed colour
// temperature, in the CT mode.
func (l *ColorTempLight) ctColor() (fabriclight.Color, bool) {
	k, observed := l.Kelvin()
	if !observed {
		return fabriclight.Color{}, false
	}
	return fabriclight.Color{
		Mode:                   fabriclight.ColorModeColorTemperature,
		ColorTemperatureMireds: kelvinToMireds(k),
	}, true
}

// matterColorServer builds the CT light's ColorControl server once:
// go-fabric's server with the CT feature, the physical mired range from
// the light's Kelvin bounds, the CCU write as its colour temperature
// writer and the COLOR_TEMPERATURE (or COLOR_LEVEL) data point's
// confirmed-update hook feeding the CCU's value back through Set — the
// path that stores without asking the writer, so a value set at the wall
// or by a CCU program reaches a subscribed controller and a confirmed
// command does not echo into a second write.
func (l *ColorTempLight) matterColorServer() *fabriclight.ColorControlServer {
	return l.matterColor.server(func() *fabriclight.ColorControlServer {
		minMireds, maxMireds := physicalMireds(l.MinKelvin, l.MaxKelvin)
		cfg := fabriclight.ColorControlServerConfig{
			Features:      fabriclight.ColorFeatureColorTemperature,
			MinMireds:     minMireds,
			MaxMireds:     maxMireds,
			InitialMireds: matterInitialMireds,
			OnOff:         l.Light,
		}
		if c, observed := l.ctColor(); observed {
			cfg.InitialMireds = c.ColorTemperatureMireds
		}
		srv := fabriclight.NewColorControlServer(cfg)
		srv.SetWriter(ctColorWriter{setKelvin: func(ctx context.Context, k int32) error {
			return l.SetKelvin(ctx, k, matterDispatchPriority)
		}})
		resync := func() {
			if c, observed := l.ctColor(); observed {
				srv.Set(c)
			}
		}
		if l.kelvin != nil {
			_ = l.kelvin.OnConfirmedUpdate(func(_, _ int32) { resync() })
		}
		if l.colorLevel != nil {
			_ = l.colorLevel.OnConfirmedUpdate(func(_, _ float64) { resync() })
		}
		// A confirmation that landed between the initial read and the
		// hook registration is not lost: the current value is stored
		// again, which is a no-op when nothing changed.
		resync()
		return srv
	})
}

// MatterDeviceType for ColorLight overrides Light's projection with
// ExtendedColorLight (0x010D).
func (l *ColorLight) MatterDeviceType() uint16 {
	return matterDeviceTypeExtendedColorLight
}

// MatterClusterServers for ColorLight returns OnOff + LevelControl +
// ColorControl with the HS and XY features.
func (l *ColorLight) MatterClusterServers() []interfaces.MatterClusterServer {
	servers := l.Light.MatterClusterServers()
	return append(servers, l.matterColorServer())
}

// matterColorServer builds the HS light's ColorControl server once; see
// [ColorTempLight.matterColorServer] for the shape. The HUE / SATURATION
// pair or the RF dimmers' COLOR integer feed the confirmed colour back.
// The XY feature the ExtendedColorLight device type mandates is served
// next to HS: the server converts between the modes, the writer
// converts an x/y target for the device (see [hsColorWriter]), and the
// x/y attributes start from the observed colour so a read before the
// first MoveToColor is not the zero colour.
func (l *ColorLight) matterColorServer() *fabriclight.ColorControlServer {
	return l.matterColor.server(func() *fabriclight.ColorControlServer {
		mode := fabriclight.ColorModeHueSaturation
		cfg := fabriclight.ColorControlServerConfig{
			Features:         fabriclight.ColorFeatureHueSaturation | fabriclight.ColorFeatureXY,
			InitialColorMode: &mode,
			OnOff:            l.Light,
		}
		if c, observed := l.hsColor(); observed {
			cfg.InitialHue, cfg.InitialSaturation = c.Hue, c.Saturation
			cfg.InitialX, cfg.InitialY = hmToXY(matterHueToHM(c.Hue), matterSaturationToHM(c.Saturation))
		}
		srv := fabriclight.NewColorControlServer(cfg)
		srv.SetColorWriter(hsColorWriter{l: l})
		resync := func() {
			if c, observed := l.hsColor(); observed && !confirmsXY(srv.Color(), c) {
				srv.Set(c)
			}
		}
		l.onColorConfirmed(resync)
		resync()
		return srv
	})
}

// onColorConfirmed runs fn after every confirmed update of a colour data
// point of the light — HUE, SATURATION or COLOR, whichever the device
// family carries.
func (l *ColorLight) onColorConfirmed(fn func()) {
	if l.hue != nil {
		_ = l.hue.OnConfirmedUpdate(func(_, _ int32) { fn() })
	}
	if l.saturation != nil {
		_ = l.saturation.OnConfirmedUpdate(func(_, _ float64) { fn() })
	}
	if l.colorIndex != nil {
		_ = l.colorIndex.OnConfirmedUpdate(func(_, _ int32) { fn() })
	}
}

// MatterDeviceType for RGBWLight overrides Light's projection with
// ExtendedColorLight (0x010D) — RGBW devices switch between HS and CT
// modes via [RGBWLight.Mode].
func (l *RGBWLight) MatterDeviceType() uint16 {
	return matterDeviceTypeExtendedColorLight
}

// MatterClusterServers for RGBWLight returns OnOff + LevelControl +
// ColorControl with the HS, XY and CT features.
func (l *RGBWLight) MatterClusterServers() []interfaces.MatterClusterServer {
	servers := l.Light.MatterClusterServers()
	return append(servers, l.matterColorServer())
}

// matterColorMode is the colour mode the device is in: CT while the
// device-side operating mode is TUNABLE_WHITE, or — on the HmIP-LSC,
// which runs both axes at once and has no operating mode — while the
// colour-temperature axis carries a value; HS otherwise, including the
// RGB / RGBW modes whose hue/saturation is the user-visible control even
// when an extra CT channel rides along, and before the mode was observed
// (the cluster is only materialised for ColorLight-capable devices, so
// HS is the safer reading until the mode signal arrives). Matter Core
// Spec §3.2.7.20: ColorMode indicates which attributes currently
// determine the device output.
func (l *RGBWLight) matterColorMode() fabriclight.ColorMode {
	if l.colorTempCombined {
		if l.colorTempKelvinActive() {
			return fabriclight.ColorModeColorTemperature
		}
		return fabriclight.ColorModeHueSaturation
	}
	if l.Mode() == RGBWModeTunableWhite {
		return fabriclight.ColorModeColorTemperature
	}
	return fabriclight.ColorModeHueSaturation
}

// deviceColor is the server-side colour of the RGBW light: the device's
// mode with every observed axis filled in, so a Set in either mode
// carries that mode's fields. Unobserved axes keep the zero value the
// server ignores for the other mode.
func (l *RGBWLight) deviceColor() (fabriclight.Color, bool) {
	c := fabriclight.Color{Mode: l.matterColorMode()}
	hue, sat, hsObserved := l.Color()
	if hsObserved {
		c.Hue, c.Saturation = hueToMatter(hue), saturationToMatter(sat)
	}
	k, ctObserved := l.Kelvin()
	if ctObserved {
		c.ColorTemperatureMireds = kelvinToMireds(k)
	}
	switch c.Mode {
	case fabriclight.ColorModeColorTemperature:
		return c, ctObserved
	default:
		return c, hsObserved
	}
}

// matterColorServer builds the RGBW light's ColorControl server once:
// HS, XY and CT features, both writer ports, and the HUE / SATURATION /
// COLOR_TEMPERATURE data points plus the device operating mode feeding
// the confirmed colour back through Set.
func (l *RGBWLight) matterColorServer() *fabriclight.ColorControlServer {
	return l.matterColor.server(func() *fabriclight.ColorControlServer {
		minMireds, maxMireds := physicalMireds(l.MinKelvin, l.MaxKelvin)
		mode := l.matterColorMode()
		cfg := fabriclight.ColorControlServerConfig{
			Features:         fabriclight.ColorFeatureHueSaturation | fabriclight.ColorFeatureXY | fabriclight.ColorFeatureColorTemperature,
			MinMireds:        minMireds,
			MaxMireds:        maxMireds,
			InitialMireds:    matterInitialMireds,
			InitialColorMode: &mode,
			OnOff:            l.Light,
		}
		if c, _ := l.deviceColor(); c.ColorTemperatureMireds != 0 || c.Hue != 0 || c.Saturation != 0 {
			cfg.InitialHue, cfg.InitialSaturation = c.Hue, c.Saturation
			cfg.InitialX, cfg.InitialY = hmToXY(matterHueToHM(c.Hue), matterSaturationToHM(c.Saturation))
			if c.ColorTemperatureMireds != 0 {
				cfg.InitialMireds = c.ColorTemperatureMireds
			}
		}
		srv := fabriclight.NewColorControlServer(cfg)
		srv.SetColorWriter(hsColorWriter{l: l.ColorLight})
		srv.SetWriter(ctColorWriter{setKelvin: func(ctx context.Context, k int32) error {
			return l.SetKelvin(ctx, k, matterDispatchPriority)
		}})
		// The hooks close over srv rather than going through the holder:
		// the holder's lock is held while this builder runs, and the
		// first sync below runs inside it. Each colour axis confirms in
		// its own mode ([RGBWLight.syncAxisTo]); the operating mode and
		// the first sync use the device's mode.
		l.onColorConfirmed(func() { l.syncAxisTo(srv, fabriclight.ColorModeHueSaturation) })
		if l.kelvin != nil {
			_ = l.kelvin.OnConfirmedUpdate(func(_, _ int32) { l.syncAxisTo(srv, fabriclight.ColorModeColorTemperature) })
		}
		l.syncColorTo(srv)
		return srv
	})
}

// syncColorTo stores the device's current colour on srv, in the mode
// [RGBWLight.matterColorMode] derives from the device — unless it is
// the late confirmation of an x/y write ([confirmsXY]).
func (l *RGBWLight) syncColorTo(srv *fabriclight.ColorControlServer) {
	if c, observed := l.deviceColor(); observed && !confirmsXY(srv.Color(), c) {
		srv.Set(c)
	}
}

// syncAxisTo stores a confirmed colour axis on srv. On the HmIP-LSC,
// which runs hue/saturation and colour temperature at once and has no
// operating mode, the device's colour mode is whichever axis the CCU
// confirmed last: a HUE / SATURATION confirmation is the device in HS, a
// COLOR_TEMPERATURE confirmation the device in CT — [matterColorMode]
// alone would call it CT as long as the colour-temperature axis carries
// a value, and report the echo of every hue write as a CT colour (the
// server then contradicts the command it just carried out and stays in
// CT; measured on the chip-tool HmIP-LSC bridge). A light with an
// operating mode keeps the mode-derived colour: the mode names the axis
// the device obeys, whichever parameter the CCU repeats.
func (l *RGBWLight) syncAxisTo(srv *fabriclight.ColorControlServer, mode fabriclight.ColorMode) {
	if !l.colorTempCombined {
		l.syncColorTo(srv)
		return
	}
	// deviceColor fills every observed axis; only the mode is the axis's.
	c, _ := l.deviceColor()
	c.Mode = mode
	switch mode {
	case fabriclight.ColorModeColorTemperature:
		if !l.colorTempKelvinActive() {
			return
		}
	default:
		if _, _, hsObserved := l.Color(); !hsObserved {
			return
		}
	}
	srv.Set(c)
}

// matterColorSync stores the device's current colour on the ColorControl
// server, if one was built. [RGBWLight.recordMode] calls it on every
// operating-mode change so a device switched to TUNABLE_WHITE reports
// the CT mode without a colour event.
func (l *RGBWLight) matterColorSync() {
	// A bare RGBWLight literal (tests of the mode table) has no Light.
	if l == nil || l.ColorLight == nil || l.Light == nil {
		return
	}
	if srv := l.matterColor.current(); srv != nil {
		l.syncColorTo(srv)
	}
}

// MatterEligibility marks RGBWLight as partially mappable: OnOff +
// LevelControl + ColorControl (HS+XY+CT) cover the standard light
// surface, but the device's effect playlist (`Effects()` /
// `Effect()`) has no Matter cluster — Matter §3 Color Control covers
// scenes but not arbitrary effect dispatch. Effects stay MQTT-only.
func (l *RGBWLight) MatterEligibility() interfaces.MatterEligibilityVerdict {
	servers := l.MatterClusterServers()
	clusters := make([]uint32, 0, len(servers))
	for _, s := range servers {
		clusters = append(clusters, s.MatterClusterID())
	}
	return interfaces.MatterEligibilityVerdict{
		State:      interfaces.MatterEligibilityPartial,
		DeviceType: matterDeviceTypeExtendedColorLight,
		Clusters:   clusters,
		Reason:     "Effect playlist dispatch is MQTT-only — Matter Color Control has scenes but no general effect surface.",
	}
}

// MatterEligibility marks EffectLight as partially mappable: OnOff +
// LevelControl + ColorControl (HS+XY) cover the regular light surface,
// but the PROGRAM parameter (Slow / Medium / Fast color change,
// Campfire, Waterfall, …) has no Matter cluster equivalent. Effect
// dispatch stays MQTT-only.
func (l *EffectLight) MatterEligibility() interfaces.MatterEligibilityVerdict {
	servers := l.MatterClusterServers()
	clusters := make([]uint32, 0, len(servers))
	for _, s := range servers {
		clusters = append(clusters, s.MatterClusterID())
	}
	return interfaces.MatterEligibilityVerdict{
		State:      interfaces.MatterEligibilityPartial,
		DeviceType: l.MatterDeviceType(),
		Clusters:   clusters,
		Reason:     "PROGRAM-based effect dispatch is MQTT-only — Matter has no effect playlist cluster.",
	}
}
