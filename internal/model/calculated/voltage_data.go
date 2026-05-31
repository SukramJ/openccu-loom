// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package calculated

// BatteryType identifies the cell chemistry / form-factor a Homematic device
// uses.
type BatteryType string

// BatteryType values.
const (
	BatteryTypeCR2032  BatteryType = "CR2032"
	BatteryTypeLR44    BatteryType = "LR44"
	BatteryTypeAAA     BatteryType = "AAA"
	BatteryTypeBaby    BatteryType = "BABY"
	BatteryTypeAA      BatteryType = "AA"
	BatteryTypeUnknown BatteryType = "UNKNOWN"
)

// CellVoltage returns the nominal voltage of a single cell of the
// given type.
// Returns 0 when the type is unknown.
func CellVoltage(t BatteryType) float64 {
	switch t { //nolint:exhaustive // BatteryTypeUnknown has no nominal voltage; falls through to the return 0 below
	case BatteryTypeCR2032:
		return 3.0
	case BatteryTypeLR44, BatteryTypeAAA, BatteryTypeBaby, BatteryTypeAA:
		return 1.5
	}
	return 0
}

// BatteryConfig records the per-model battery configuration
// OPERATING_VOLTAGE_LEVEL formula's `voltage_max` reference.
type BatteryConfig struct {
	// Battery is the cell chemistry / form-factor.
	Battery BatteryType
	// Quantity is the number of cells in series.
	Quantity int
}

// VoltageMax returns the nominal pack voltage (cell voltage × cells).
// Returns 0 when the battery type is unknown.
func (b BatteryConfig) VoltageMax() float64 {
	v := CellVoltage(b.Battery)
	if v == 0 {
		return 0
	}
	q := b.Quantity
	if q <= 0 {
		q = 1
	}
	return v * float64(q)
}

// batteryData is the per-model battery configuration table.
// Key is the device model (case-sensitive prefix). Lookup is
// Prefix-based to honour.
var batteryData = map[string]BatteryConfig{
	// HM long model str
	"HM-CC-RT-DN":      {Battery: BatteryTypeAA, Quantity: 2},
	"HM-Dis-EP-WM55":   {Battery: BatteryTypeAAA, Quantity: 2},
	"HM-ES-TX-WM":      {Battery: BatteryTypeAA, Quantity: 4},
	"HM-OU-CFM-TW":     {Battery: BatteryTypeBaby, Quantity: 2},
	"HM-PB-2-FM":       {Battery: BatteryTypeAAA, Quantity: 2},
	"HM-PB-2-WM55":     {Battery: BatteryTypeAAA, Quantity: 2},
	"HM-PB-6-WM55":     {Battery: BatteryTypeAAA, Quantity: 2},
	"HM-PBI-4-FM":      {Battery: BatteryTypeCR2032, Quantity: 1},
	"HM-RC-4-2":        {Battery: BatteryTypeAAA, Quantity: 1},
	"HM-RC-8":          {Battery: BatteryTypeAAA, Quantity: 2},
	"HM-RC-Key4-3":     {Battery: BatteryTypeAAA, Quantity: 1},
	"HM-SCI-3-FM":      {Battery: BatteryTypeCR2032, Quantity: 1},
	"HM-Sec-Key":       {Battery: BatteryTypeAA, Quantity: 3},
	"HM-Sec-MDIR-2":    {Battery: BatteryTypeAA, Quantity: 3},
	"HM-Sec-RHS":       {Battery: BatteryTypeLR44, Quantity: 2},
	"HM-Sec-SC-2":      {Battery: BatteryTypeLR44, Quantity: 2},
	"HM-Sec-SCo":       {Battery: BatteryTypeAAA, Quantity: 1},
	"HM-Sec-SD-2":      {Battery: BatteryTypeUnknown, Quantity: 1},
	"HM-Sec-Sir-WM":    {Battery: BatteryTypeBaby, Quantity: 2},
	"HM-Sec-TiS":       {Battery: BatteryTypeCR2032, Quantity: 1},
	"HM-Sec-Win":       {Battery: BatteryTypeUnknown, Quantity: 1},
	"HM-Sen-MDIR-O-2":  {Battery: BatteryTypeAA, Quantity: 3},
	"HM-Sen-MDIR-SM":   {Battery: BatteryTypeAA, Quantity: 3},
	"HM-Sen-MDIR-WM55": {Battery: BatteryTypeAAA, Quantity: 2},
	"HM-SwI-3-FM":      {Battery: BatteryTypeCR2032, Quantity: 1},
	"HM-TC-IT-WM-W-EU": {Battery: BatteryTypeAAA, Quantity: 2},
	"HM-WDS10-TH-O":    {Battery: BatteryTypeAA, Quantity: 2},
	"HM-WDS30-OT2-SM":  {Battery: BatteryTypeAA, Quantity: 2},
	"HM-WDS30-T-O":     {Battery: BatteryTypeAAA, Quantity: 2},
	"HM-WDS40-TH-I":    {Battery: BatteryTypeAA, Quantity: 2},
	// HM short model str
	"HM-Sec-SD": {Battery: BatteryTypeAA, Quantity: 3},
	// HmIP model > 4 chars
	"HmIP-ASIR-O":    {Battery: BatteryTypeUnknown, Quantity: 1},
	"HmIP-DSD-PCB":   {Battery: BatteryTypeAAA, Quantity: 2},
	"HmIP-PCBS-BAT":  {Battery: BatteryTypeUnknown, Quantity: 1},
	"HmIP-SMI55":     {Battery: BatteryTypeAAA, Quantity: 2},
	"HmIP-SMO230":    {Battery: BatteryTypeUnknown, Quantity: 1},
	"HmIP-STE2-PCB":  {Battery: BatteryTypeAA, Quantity: 2},
	"HmIP-SWDO-I":    {Battery: BatteryTypeAAA, Quantity: 2},
	"HmIP-SWDO-PL":   {Battery: BatteryTypeAAA, Quantity: 2},
	"HmIP-UDI-SMI55": {Battery: BatteryTypeAAA, Quantity: 2},
	"HmIP-WRC6-230":  {Battery: BatteryTypeUnknown, Quantity: 1},
	"HmIP-WTH-B-2":   {Battery: BatteryTypeAA, Quantity: 2},
	"HmIP-eTRV-CL":   {Battery: BatteryTypeAA, Quantity: 4},
	// HmIP model 4 chars
	"ELV-SH-SW1-BAT": {Battery: BatteryTypeAA, Quantity: 2},
	"ELV-SH-TACO":    {Battery: BatteryTypeAAA, Quantity: 1},
	"HmIP-ASIR":      {Battery: BatteryTypeAA, Quantity: 3},
	"HmIP-FCI1":      {Battery: BatteryTypeCR2032, Quantity: 1},
	"HmIP-FCI6":      {Battery: BatteryTypeAAA, Quantity: 1},
	"HmIP-MP3P":      {Battery: BatteryTypeBaby, Quantity: 2},
	"HmIP-RCB1":      {Battery: BatteryTypeAAA, Quantity: 2},
	"HmIP-SPDR":      {Battery: BatteryTypeAA, Quantity: 2},
	"HmIP-STHD":      {Battery: BatteryTypeAAA, Quantity: 2},
	"HmIP-STHO":      {Battery: BatteryTypeAA, Quantity: 2},
	"HmIP-SWDM":      {Battery: BatteryTypeAAA, Quantity: 2},
	"HmIP-SWDO":      {Battery: BatteryTypeAAA, Quantity: 1},
	"HmIP-SWSD":      {Battery: BatteryTypeUnknown, Quantity: 1},
	"HmIP-eTRV":      {Battery: BatteryTypeAA, Quantity: 2},
	// HmIP model 3 chars
	"ELV-SH-CTH": {Battery: BatteryTypeCR2032, Quantity: 1},
	"ELV-SH-WSM": {Battery: BatteryTypeAA, Quantity: 2},
	"HmIP-DBB":   {Battery: BatteryTypeAAA, Quantity: 1},
	"HmIP-DLD":   {Battery: BatteryTypeAA, Quantity: 3},
	"HmIP-DLP":   {Battery: BatteryTypeAA, Quantity: 4},
	"HmIP-DLS":   {Battery: BatteryTypeCR2032, Quantity: 1},
	"HmIP-ESI":   {Battery: BatteryTypeAA, Quantity: 2},
	"HmIP-KRC":   {Battery: BatteryTypeAAA, Quantity: 1},
	"HmIP-RC8":   {Battery: BatteryTypeAAA, Quantity: 2},
	"HmIP-SAM":   {Battery: BatteryTypeAA, Quantity: 2},
	"HmIP-SCI":   {Battery: BatteryTypeAAA, Quantity: 2},
	"HmIP-SLO":   {Battery: BatteryTypeAA, Quantity: 2},
	"HmIP-SMI":   {Battery: BatteryTypeAA, Quantity: 2},
	"HmIP-SMO":   {Battery: BatteryTypeAA, Quantity: 2},
	"HmIP-SPI":   {Battery: BatteryTypeAA, Quantity: 2},
	"HmIP-SRH":   {Battery: BatteryTypeAAA, Quantity: 1},
	"HmIP-STH":   {Battery: BatteryTypeAAA, Quantity: 2},
	"HmIP-STV":   {Battery: BatteryTypeAAA, Quantity: 2},
	"HmIP-SWD":   {Battery: BatteryTypeAAA, Quantity: 2},
	"HmIP-SWO":   {Battery: BatteryTypeAA, Quantity: 3},
	"HmIP-WGC":   {Battery: BatteryTypeAA, Quantity: 2},
	"HmIP-WKP":   {Battery: BatteryTypeAAA, Quantity: 2},
	"HmIP-WRC":   {Battery: BatteryTypeAAA, Quantity: 2},
	"HmIP-WSM":   {Battery: BatteryTypeAA, Quantity: 2},
	"HmIP-WTH":   {Battery: BatteryTypeAAA, Quantity: 2},
}

// LookupBatteryConfig returns the battery configuration for `model`,
// Honouring The boolean reports
// whether a match was found and whether the type is known (UNKNOWN
// Types return ok=false to mirror
func LookupBatteryConfig(model string) (BatteryConfig, bool) {
	if model == "" {
		return BatteryConfig{}, false
	}
	// Longest-prefix wins so HM-Sec-SD-2 beats HM-Sec-SD.
	var best string
	for k := range batteryData {
		if hasPrefixFold(model, k) && len(k) > len(best) {
			best = k
		}
	}
	if best == "" {
		return BatteryConfig{}, false
	}
	cfg := batteryData[best]
	if cfg.Battery == BatteryTypeUnknown {
		return cfg, false
	}
	return cfg, true
}

// IsOperatingVoltageLevelRelevant reports whether a channel qualifies for an
// OperatingVoltageLevel derived sensor.
//
// The model must be in the battery table with a known cell type AND the channel
// must satisfy one of two conditions (mirroring
// OperatingVoltageLevel.is_relevant_for_model in operating_voltage_level.py):
//
//   - OPERATING_VOLTAGE (VALUES) present on the channel AND LOW_BAT_LIMIT
//     (MASTER) present on the same channel, OR
//   - BATTERY_STATE (VALUES) present on the channel AND LOW_BAT_LIMIT
//     (MASTER) present on the device-root channel.
//
// When ch does not implement [voltageChannelInspector] (i.e. does not expose
// HasMasterParameter/HasDeviceMasterParameter), the function falls back to
// the VALUES-only check for backward compatibility with callers that supply
// a minimal stub.
func IsOperatingVoltageLevelRelevant(ch ChannelInspector, model string) bool {
	if ch == nil {
		return false
	}
	if _, ok := LookupBatteryConfig(model); !ok {
		return false
	}
	vi, hasVI := ch.(voltageChannelInspector)
	if ch.HasParameter("OPERATING_VOLTAGE") {
		if hasVI {
			return vi.HasMasterParameter("LOW_BAT_LIMIT")
		}
		return true // fallback: MASTER check unavailable
	}
	if ch.HasParameter("BATTERY_STATE") {
		if hasVI {
			return vi.HasDeviceMasterParameter("LOW_BAT_LIMIT")
		}
		return true // fallback: MASTER check unavailable
	}
	return false
}
