// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package visibility_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// putMasterStringDP plants a string-typed MASTER paramset entry on
// the channel — used to seed CHANNEL_OPERATION_MODE before invoking
// the gating helper. The default value is observed via OnEvent so
// `Channel.OperationMode()` returns it.
func putMasterStringDP(ch *device.Channel, param hmenum.Parameter, value string) {
	cfg := generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeEnum, Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
	}
	dp := generic.NewSensor[string](cfg)
	dp.OnEvent(value)
	ch.PutMaster(dp)
}

// putValuesBoolDP / putValuesEventDP add the gated wire parameters
// to the channel's VALUES paramset for the gating loop to traverse.
func putValuesBoolDP(ch *device.Channel, param hmenum.Parameter) *generic.Switch {
	cfg := generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeBool, Operations: hmenum.OperationsRead | hmenum.OperationsWrite},
	}
	dp := generic.NewSwitch(cfg)
	ch.Put(dp)
	return dp
}

// TestApplyChannelOperationModeGatingHidesNonMatchingParameters
// pins the gating override for a multi-mode KEY_TRANSCEIVER channel
// running in BINARY_BEHAVIOR. STATE is in the allowed-set so it is
// promoted to DataPoint usage; PRESS_SHORT is gated but not allowed
// in BINARY_BEHAVIOR so it gets NoCreate.
func TestApplyChannelOperationModeGatingHidesNonMatchingParameters(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "iface", Address: "FCI001", Model: "HmIP-FCI1"})
	ch := d.AddChannel("FCI001:1", 1, "KEY_TRANSCEIVER", hmenum.ParamsetKeyValues)

	state := putValuesBoolDP(ch, hmenum.ParameterState)
	pressShort := putValuesBoolDP(ch, hmenum.ParameterPressShort)
	pressLong := putValuesBoolDP(ch, hmenum.ParameterPressLong)
	putMasterStringDP(ch, hmenum.ParameterChannelOperationMode, "BINARY_BEHAVIOR")

	visibility.ApplyChannelOperationModeGating(ch)

	if got, _ := state.ForcedUsage(); got != hmenum.DataPointUsageDataPoint {
		t.Errorf("STATE usage in BINARY_BEHAVIOR = %q, want DataPoint", got)
	}
	if got, _ := pressShort.ForcedUsage(); got != hmenum.DataPointUsageIgnored {
		t.Errorf("PRESS_SHORT usage in BINARY_BEHAVIOR = %q, want Ignored", got)
	}
	if got, _ := pressLong.ForcedUsage(); got != hmenum.DataPointUsageIgnored {
		t.Errorf("PRESS_LONG usage in BINARY_BEHAVIOR = %q, want Ignored", got)
	}
}

// TestApplyChannelOperationModeGatingKeyBehavior verifies the
// reverse: KEY_BEHAVIOR mode hides STATE and exposes PRESS_*.
func TestApplyChannelOperationModeGatingKeyBehavior(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "iface", Address: "FCI002", Model: "HmIP-FCI1"})
	ch := d.AddChannel("FCI002:1", 1, "KEY_TRANSCEIVER", hmenum.ParamsetKeyValues)

	state := putValuesBoolDP(ch, hmenum.ParameterState)
	pressShort := putValuesBoolDP(ch, hmenum.ParameterPressShort)
	putMasterStringDP(ch, hmenum.ParameterChannelOperationMode, "KEY_BEHAVIOR")

	visibility.ApplyChannelOperationModeGating(ch)

	if got, _ := state.ForcedUsage(); got != hmenum.DataPointUsageIgnored {
		t.Errorf("STATE usage in KEY_BEHAVIOR = %q, want Ignored", got)
	}
	if got, _ := pressShort.ForcedUsage(); got != hmenum.DataPointUsageDataPoint {
		t.Errorf("PRESS_SHORT usage in KEY_BEHAVIOR = %q, want DataPoint", got)
	}
}

// TestApplyChannelOperationModeGatingNonConfigurableChannelIsNoop guards the
// channel-type filter: a SWITCH_VIRTUAL_RECEIVER (the HmIP-BWTH ch9 type) is
// *not* in the configurable set, so the gate must leave the channel's DPs
// untouched.
func TestApplyChannelOperationModeGatingNonConfigurableChannelIsNoop(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "iface", Address: "BWTH001", Model: "HmIP-BWTH"})
	ch := d.AddChannel("BWTH001:9", 9, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)

	state := putValuesBoolDP(ch, hmenum.ParameterState)
	putMasterStringDP(ch, hmenum.ParameterChannelOperationMode, "KEY_BEHAVIOR")

	visibility.ApplyChannelOperationModeGating(ch)

	if _, set := state.ForcedUsage(); set {
		t.Errorf("BWTH ch9 STATE forced usage was changed by the gate; the gate must skip non-configurable channels")
	}
}

// TestApplyChannelOperationModeGatingMissingModeIsNoop pins the
// `cop is None` branch: when the channel does not yet carry an
// observed CHANNEL_OPERATION_MODE master value the gate must leave
// every DP untouched.
func TestApplyChannelOperationModeGatingMissingModeIsNoop(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "iface", Address: "FCI003", Model: "HmIP-FCI1"})
	ch := d.AddChannel("FCI003:1", 1, "KEY_TRANSCEIVER", hmenum.ParamsetKeyValues)

	state := putValuesBoolDP(ch, hmenum.ParameterState)

	visibility.ApplyChannelOperationModeGating(ch)

	if _, set := state.ForcedUsage(); set {
		t.Errorf("STATE forced usage must not be set when CHANNEL_OPERATION_MODE has not been observed")
	}
}

// TestApplyChannelOperationModeGatingPreservesCDPVisible guards the
// ce_visible parity ( resolution):
// a LEVEL DP that has been force-marked CDPVisible by the custom-DP
// profile materializer (e.g. IPThermostat channel_fields[0][LEVEL] =
// Visible(LEVEL) in py) must survive the
// operation-mode gate unchanged, because the channel type
// HEATING_CLIMATECONTROL_RECEIVER is NOT in configurableChannelTypes.
//
// This is the test-side pin for the snapshot parity:
// OpenCCU-Loom go=ce_visible
func TestApplyChannelOperationModeGatingPreservesCDPVisible(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "SCTH001", Model: "HmIP-SCTH230"})
	ch := d.AddChannel("SCTH001:0", 0, "HEATING_CLIMATECONTROL_RECEIVER", hmenum.ParamsetKeyValues)

	levelDP := putValuesBoolDP(ch, hmenum.ParameterLevel)
	// Simulate what the custom-DP materializer does for the
	// IPThermostat ChannelFields[0][LEVEL] = Visible(LEVEL) entry.
	levelDP.SetForcedUsage(hmenum.DataPointUsageCDPVisible)

	// The gate is a no-op for non-configurable channel types, so LEVEL
	// must retain CDPVisible regardless of any seed CHANNEL_OPERATION_MODE.
	putMasterStringDP(ch, hmenum.ParameterChannelOperationMode, "ANY_MODE")

	visibility.ApplyChannelOperationModeGating(ch)

	if got, _ := levelDP.ForcedUsage(); got != hmenum.DataPointUsageCDPVisible {
		t.Errorf("LEVEL ForcedUsage after gate = %q, want ce_visible (gate must not touch non-configurable channels)", got)
	}
}

// putValuesIntegerSensorDP adds a read-only integer-typed VALUES
// parameter (e.g. DIRECTION, ERROR on HM-Sec-Key channels).
func putValuesIntegerSensorDP(ch *device.Channel, param hmenum.Parameter) *generic.Sensor[int32] {
	cfg := generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	}
	dp := generic.NewIntegerSensor(cfg)
	ch.Put(dp)
	return dp
}

// TestApplyHiddenParameterMarksPreservesDataPointPromotion is the regression
// test: DIRECTION and ERROR are in hiddenParameters but on HM-Sec-Key /
// HM-Sec-Win they are explicitly promoted to DataPoint usage by the custom-DP
// pipeline (markAdditionalDataPoints → SetForcedUsage(DataPoint)).
// ApplyHiddenParameterMarks must NOT overwrite that promotion with NoCreate.
func TestApplyHiddenParameterMarksPreservesDataPointPromotion(t *testing.T) {
	dev := device.New(device.Config{
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Address:     "LOCK001",
		Model:       "HM-Sec-Key",
	})
	ch := dev.AddChannel("LOCK001:1", 1, "KEY", hmenum.ParamsetKeyValues)

	// Create DIRECTION and ERROR as read-only integer sensors
	// (same type as the real datapoint_resolver creates for ENUM+ReadOnly).
	dirDP := putValuesIntegerSensorDP(ch, hmenum.ParameterDirection)
	errDP := putValuesIntegerSensorDP(ch, hmenum.ParameterError)

	// Simulate what markAdditionalDataPoints does: promote to DataPoint.
	dirDP.SetForcedUsage(hmenum.DataPointUsageDataPoint)
	errDP.SetForcedUsage(hmenum.DataPointUsageDataPoint)

	// Run the hidden-parameter pass (step 390 in device_pipeline.go).
	visibility.ApplyHiddenParameterMarks(dev)

	// The custom-DP promotion must survive the hidden-parameter pass.
	if u, _ := dirDP.ForcedUsage(); u != hmenum.DataPointUsageDataPoint {
		t.Errorf("G-47: DIRECTION ForcedUsage = %q after ApplyHiddenParameterMarks, want data_point", u)
	}
	if dirDP.Usage() != hmenum.DataPointUsageDataPoint {
		t.Errorf("G-47: DIRECTION Usage() = %q, want data_point", dirDP.Usage())
	}
	if u, _ := errDP.ForcedUsage(); u != hmenum.DataPointUsageDataPoint {
		t.Errorf("G-47: ERROR ForcedUsage = %q after ApplyHiddenParameterMarks, want data_point", u)
	}
	if errDP.Usage() != hmenum.DataPointUsageDataPoint {
		t.Errorf("G-47: ERROR Usage() = %q, want data_point", errDP.Usage())
	}
}

// TestApplyHiddenParameterMarksStillSuppressesNonPromotedHiddenParams
// is the complement of : a hidden parameter that has NOT been
// promoted by the custom-DP pipeline must still receive NoCreate.
func TestApplyHiddenParameterMarksStillSuppressesNonPromotedHiddenParams(t *testing.T) {
	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "THERM001",
		Model:       "HmIP-eTRV",
	})
	ch := dev.AddChannel("THERM001:1", 1, "HEATING_CLIMATECONTROL_RECEIVER", hmenum.ParamsetKeyValues)

	// DIRECTION is hidden and NOT promoted — it must get NoCreate.
	dirDP := putValuesIntegerSensorDP(ch, hmenum.ParameterDirection)

	visibility.ApplyHiddenParameterMarks(dev)

	if u, _ := dirDP.ForcedUsage(); u != hmenum.DataPointUsageIgnored {
		t.Errorf("G-47: non-promoted DIRECTION ForcedUsage = %q, want ignored", u)
	}
}

// putValuesInternalActionDP plants an INTERNAL-flagged write-only ACTION
// parameter on the channel — used to seed INSTALL_TEST and similar
// internal triggers.
func putValuesInternalActionDP(ch *device.Channel, param hmenum.Parameter) *generic.Action {
	cfg := generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeAction,
			Operations: hmenum.OperationsWrite,
			Flags:      hmenum.FlagVisible | hmenum.FlagInternal,
		},
	}
	dp := generic.NewAction(cfg)
	ch.Put(dp)
	return dp
}

// TestApplyInternalParameterMarksSuppressesInternalFlagged is the happy-path
// regression: a parameter whose FLAGS field has the INTERNAL bit and which is
// NOT in [generic.AllowedInternalParameters] must receive NoCreate.
func TestApplyInternalParameterMarksSuppressesInternalFlagged(t *testing.T) {
	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "TEST001",
		Model:       "HmIP-Generic",
	})
	ch := dev.AddChannel("TEST001:1", 1, "ANY", hmenum.ParamsetKeyValues)

	dp := putValuesInternalActionDP(ch, hmenum.ParameterInstallTest)

	visibility.ApplyInternalParameterMarks(dev)

	if u, _ := dp.ForcedUsage(); u != hmenum.DataPointUsageIgnored {
		t.Errorf("INSTALL_TEST (FLAG.INTERNAL, not whitelisted) ForcedUsage = %q, want ignored", u)
	}
}

// TestApplyInternalParameterMarksWhitelistedSurvives verifies that
// CHANNEL_OPERATION_MODE / DIRECTION / ON_TIME_LIST_1 / REPETITIONS
// keep their unforced state even though FLAGS.INTERNAL is set —
// they live in [generic.AllowedInternalParameters].
func TestApplyInternalParameterMarksWhitelistedSurvives(t *testing.T) {
	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "TEST002",
		Model:       "HmIP-Generic",
	})
	ch := dev.AddChannel("TEST002:1", 1, "ANY", hmenum.ParamsetKeyValues)

	dp := putValuesInternalActionDP(ch, hmenum.ParameterDirection)

	visibility.ApplyInternalParameterMarks(dev)

	if _, set := dp.ForcedUsage(); set {
		t.Errorf("DIRECTION (whitelisted) ForcedUsage was set; expected unforced")
	}
}

// TestApplyInternalParameterMarksUnIgnoredOverridesSuppression
// pins the operator-override branch: a DP marked un_ignored survives
// the INTERNAL filter even when the parameter is not whitelisted.
func TestApplyInternalParameterMarksUnIgnoredOverridesSuppression(t *testing.T) {
	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "TEST003",
		Model:       "HmIP-Generic",
	})
	ch := dev.AddChannel("TEST003:1", 1, "ANY", hmenum.ParamsetKeyValues)

	dp := putValuesInternalActionDP(ch, hmenum.ParameterInstallTest)
	dp.MarkUnIgnored()

	visibility.ApplyInternalParameterMarks(dev)

	if _, set := dp.ForcedUsage(); set {
		t.Errorf("un_ignored INSTALL_TEST ForcedUsage was set; operator override must win")
	}
}

// ---------------------------------------------------------------------------
// Category A: ApplyIgnoredParameterMarks must NOT overwrite an explicit
// ForcedUsage=DataPoint that was set by the custom-DP pipeline
// (markAdditionalDataPoints). Regression tests for parity.
// ---------------------------------------------------------------------------

// TestApplyIgnoredParameterMarksPreservesDataPointPromotionHmSecKey verifies
// that DIRECTION and ERROR on HM-Sec-Key are in ignoreParametersByDevice
// (indirectly via hiddenParameters merged into ignored rules), but the
// custom-DP pipeline promotes them to DataPoint. ApplyIgnoredParameterMarks
// must NOT overwrite that promotion.
func TestApplyIgnoredParameterMarksPreservesDataPointPromotionHmSecKey(t *testing.T) {
	t.Parallel()

	dev := device.New(device.Config{
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Address:     "SECKEY01",
		Model:       "HM-Sec-Key",
	})
	ch := dev.AddChannel("SECKEY01:1", 1, "KEY", hmenum.ParamsetKeyValues)

	dirDP := putValuesIntegerSensorDP(ch, hmenum.ParameterDirection)
	errDP := putValuesIntegerSensorDP(ch, hmenum.ParameterError)

	// Simulate what markAdditionalDataPoints does before the ignored pass.
	dirDP.SetForcedUsage(hmenum.DataPointUsageDataPoint)
	errDP.SetForcedUsage(hmenum.DataPointUsageDataPoint)

	decider := visibility.NewParameterDecider(nil)
	visibility.ApplyIgnoredParameterMarks(dev, decider)

	if u, _ := dirDP.ForcedUsage(); u != hmenum.DataPointUsageDataPoint {
		t.Errorf("G-52: HM-Sec-Key DIRECTION ForcedUsage = %q after ApplyIgnoredParameterMarks, want data_point", u)
	}
	if u, _ := errDP.ForcedUsage(); u != hmenum.DataPointUsageDataPoint {
		t.Errorf("G-52: HM-Sec-Key ERROR ForcedUsage = %q after ApplyIgnoredParameterMarks, want data_point", u)
	}
	if dirDP.Usage() != hmenum.DataPointUsageDataPoint {
		t.Errorf("G-52: HM-Sec-Key DIRECTION Usage() = %q, want data_point", dirDP.Usage())
	}
	if errDP.Usage() != hmenum.DataPointUsageDataPoint {
		t.Errorf("G-52: HM-Sec-Key ERROR Usage() = %q, want data_point", errDP.Usage())
	}
}

// TestApplyIgnoredParameterMarksPreservesDataPointPromotionHmIPPCBSBAT
// pins the fix for the HmIP-PCBS-BAT side: LOW_BAT and
// OPERATING_VOLTAGE on ch0 are in ignoreParametersByDevice
// ("HmIP-PCBS" prefix match), but the DefaultDataPoints pipeline
// promotes them to DataPoint on custom-DP devices.
// ApplyIgnoredParameterMarks must NOT overwrite that promotion.
func TestApplyIgnoredParameterMarksPreservesDataPointPromotionHmIPPCBSBAT(t *testing.T) {
	t.Parallel()

	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "PCBSBAT01",
		Model:       "HmIP-PCBS-BAT",
	})
	ch := dev.AddChannel("PCBSBAT01:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)

	lowBatDP := putValuesIntegerSensorDP(ch, hmenum.ParameterLowBat)
	voltDP := putValuesIntegerSensorDP(ch, hmenum.ParameterOperatingVoltage)

	// Simulate what markAdditionalDataPoints (via DefaultDataPoints) does.
	lowBatDP.SetForcedUsage(hmenum.DataPointUsageDataPoint)
	voltDP.SetForcedUsage(hmenum.DataPointUsageDataPoint)

	decider := visibility.NewParameterDecider(nil)
	visibility.ApplyIgnoredParameterMarks(dev, decider)

	if u, _ := lowBatDP.ForcedUsage(); u != hmenum.DataPointUsageDataPoint {
		t.Errorf("G-52: HmIP-PCBS-BAT LOW_BAT ForcedUsage = %q after ApplyIgnoredParameterMarks, want data_point", u)
	}
	if u, _ := voltDP.ForcedUsage(); u != hmenum.DataPointUsageDataPoint {
		t.Errorf("G-52: HmIP-PCBS-BAT OPERATING_VOLTAGE ForcedUsage = %q after ApplyIgnoredParameterMarks, want data_point", u)
	}
}

// TestApplyIgnoredParameterMarksStillSuppressesUnpromotedIgnoredParam
// is the complement: a parameter that is in the ignored-parameters list
// and has NOT been promoted by the custom-DP pipeline must still receive
// NoCreate. Verifies Fix-A does not accidentally loosen ignored-param
// suppression for genuinely suppressed DPs.
func TestApplyIgnoredParameterMarksStillSuppressesUnpromotedIgnoredParam(t *testing.T) {
	t.Parallel()

	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "GENERIC01",
		Model:       "HmIP-STH",
	})
	ch := dev.AddChannel("GENERIC01:1", 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyValues)

	// INHIBIT is in ignoredParameters and not promoted → must become NoCreate.
	inhibitDP := putValuesBoolDP(ch, hmenum.ParameterInhibit)

	decider := visibility.NewParameterDecider(nil)
	visibility.ApplyIgnoredParameterMarks(dev, decider)

	if u, _ := inhibitDP.ForcedUsage(); u != hmenum.DataPointUsageIgnored {
		t.Errorf("HmIP-STH INHIBIT ForcedUsage = %q, want ignored (not promoted)", u)
	}
}

// ---------------------------------------------------------------------------
// ApplyUnIgnoredMarks computes the full set: a rule the operator removed
// has to take the promotion away again.
// ---------------------------------------------------------------------------

// TestApplyUnIgnoredMarksReHidesAfterRuleRemoval walks the operator's round
// trip: un-ignore a statically suppressed parameter, then delete the rule.
// Re-running the pass must put the data point back under the static verdict
// instead of leaving it promoted until the next daemon restart.
func TestApplyUnIgnoredMarksReHidesAfterRuleRemoval(t *testing.T) {
	t.Parallel()

	const (
		model = "HM-CC-RT-DN"
		param = hmenum.Parameter("BOOST_TIME")
	)

	decider := visibility.NewParameterDecider(nil)
	decider.LoadUnIgnore([]visibility.UnIgnoreEntry{{Parameter: param, IsSimple: true}})

	dev := device.New(device.Config{
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Address:     "TEST100",
		Model:       model,
	})
	ch := dev.AddChannel("TEST100:1", 1, "CLIMATECONTROL_RT_TRANSCEIVER", hmenum.ParamsetKeyValues)
	dp := putValuesBoolDP(ch, param)

	// Boot order: the un-ignore mark lands first, the suppression pass
	// afterwards, so the operator override survives it.
	visibility.ApplyUnIgnoredMarks(dev, decider)
	visibility.ApplyIgnoredParameterMarks(dev, decider)

	if !dp.IsUnIgnored() {
		t.Fatal("un-ignored parameter must carry the mark after the first pass")
	}
	if dp.Usage() != hmenum.DataPointUsageDataPoint {
		t.Fatalf("un-ignored parameter Usage()=%v, want DataPoint", dp.Usage())
	}

	// The operator deletes the pattern; the registry reloads and the pass
	// re-runs over the live model.
	decider.LoadUnIgnore(nil)
	visibility.ApplyUnIgnoredMarks(dev, decider)

	if dp.IsUnIgnored() {
		t.Error("mark survived the removal of the rule that set it")
	}
	if dp.Usage() == hmenum.DataPointUsageDataPoint {
		t.Error("parameter still surfaces as a data point after its un-ignore rule was removed")
	}
}

// TestApplyUnIgnoredMarksLeavesUnrelatedDataPointsVisible guards the
// inverse: computing the full set must not suppress parameters the static
// rules never hid in the first place.
func TestApplyUnIgnoredMarksLeavesUnrelatedDataPointsVisible(t *testing.T) {
	t.Parallel()

	decider := visibility.NewParameterDecider(nil)

	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "TEST101",
		Model:       "HmIP-PS",
	})
	ch := dev.AddChannel("TEST101:4", 4, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	dp := putValuesBoolDP(ch, hmenum.ParameterState)

	visibility.ApplyUnIgnoredMarks(dev, decider)

	if dp.IsUnIgnored() {
		t.Error("STATE must not be marked un-ignored without a matching rule")
	}
	if dp.Usage() != hmenum.DataPointUsageDataPoint {
		t.Errorf("STATE Usage()=%v, want DataPoint — the pass must not suppress unrelated parameters", dp.Usage())
	}
}

// stubCustomDataPoint is the minimum a channel needs to count as
// carrying a custom data point: the suppression pass only asks whether
// one is attached, never what it does.
type stubCustomDataPoint struct{ key hmtypes.DataPointKey }

func (s stubCustomDataPoint) DataPointKey() hmtypes.DataPointKey { return s.key }

// TestApplyUnIgnoredMarksReHidesOnACustomDPDevice is the custom-DP half
// of the operator's round trip.
//
// On a device that carries a custom data point every VALUES parameter
// without a forced usage is suppressed, and un-ignored ones are skipped —
// which makes that pass the fourth consumer of the mark. Withdrawing the
// mark without re-running it leaves the parameter surfacing as
// `usage=data_point` on REST, MQTT and the SPA although the rule that
// promoted it is gone, until the daemon restarts.
func TestApplyUnIgnoredMarksReHidesOnACustomDPDevice(t *testing.T) {
	t.Parallel()

	const param = hmenum.Parameter("LEVEL_2")

	decider := visibility.NewParameterDecider(nil)
	decider.LoadUnIgnore([]visibility.UnIgnoreEntry{{Parameter: param, IsSimple: true}})

	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "TEST200",
		Model:       "HmIP-BROLL",
	})
	ch := dev.AddChannel("TEST200:4", 4, "SHUTTER_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	dp := putValuesBoolDP(ch, param)
	ch.SetCustomDataPoint(stubCustomDataPoint{key: hmtypes.DataPointKey{
		InterfaceID:    "HmIP-RF",
		ChannelAddress: ch.Address,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      "COVER",
	}})

	// Boot order: the un-ignore mark first, then the custom-DP
	// suppression pass, which skips the marked parameter.
	visibility.ApplyUnIgnoredMarks(dev, decider)
	custom.SuppressUndefinedGenericDataPoints(dev)

	if !dp.IsUnIgnored() {
		t.Fatal("un-ignored parameter must carry the mark after the first pass")
	}
	if dp.Usage() != hmenum.DataPointUsageDataPoint {
		t.Fatalf("un-ignored parameter Usage()=%v, want DataPoint", dp.Usage())
	}

	// The operator deletes the pattern; only the un-ignore pass re-runs.
	decider.LoadUnIgnore(nil)
	visibility.ApplyUnIgnoredMarks(dev, decider)

	if dp.IsUnIgnored() {
		t.Error("mark survived the removal of the rule that set it")
	}
	if dp.Usage() == hmenum.DataPointUsageDataPoint {
		t.Error("the parameter still surfaces as a data point although only the un-ignore rule kept it past the custom-DP suppression pass")
	}
}
