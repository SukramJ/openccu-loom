// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmenum

// ParamsetKey names one of the paramset compartments a CCU exposes per
// channel (MASTER/VALUES/LINK/SERVICE) plus the synthetic compartments
// openccu-loom uses internally (CALCULATED/COMBINED/DUMMY).
type ParamsetKey string

// ParamsetKey values.
const (
	ParamsetKeyCalculated ParamsetKey = "CALCULATED"
	ParamsetKeyCombined   ParamsetKey = "COMBINED"
	ParamsetKeyDummy      ParamsetKey = "DUMMY"
	ParamsetKeyLink       ParamsetKey = "LINK"
	ParamsetKeyMaster     ParamsetKey = "MASTER"
	ParamsetKeyService    ParamsetKey = "SERVICE"
	ParamsetKeyValues     ParamsetKey = "VALUES"
)

// String returns the wire representation.
func (k ParamsetKey) String() string { return string(k) }

// ParameterType is the CCU-level data type of a parameter.
type ParameterType string

// ParameterType values.
const (
	ParameterTypeAction  ParameterType = "ACTION"
	ParameterTypeBool    ParameterType = "BOOL"
	ParameterTypeDummy   ParameterType = "DUMMY"
	ParameterTypeEnum    ParameterType = "ENUM"
	ParameterTypeFloat   ParameterType = "FLOAT"
	ParameterTypeInteger ParameterType = "INTEGER"
	ParameterTypeString  ParameterType = "STRING"
	ParameterTypeEmpty   ParameterType = ""
)

// String returns the wire representation.
func (t ParameterType) String() string { return string(t) }

// ParameterStatus is the status-flavour enum some paired *_STATUS
// parameters carry (HmIP devices).
type ParameterStatus string

// ParameterStatus values.
const (
	ParameterStatusNormal    ParameterStatus = "NORMAL"
	ParameterStatusUnknown   ParameterStatus = "UNKNOWN"
	ParameterStatusOverflow  ParameterStatus = "OVERFLOW"
	ParameterStatusUnderflow ParameterStatus = "UNDERFLOW"
	ParameterStatusError     ParameterStatus = "ERROR"
	ParameterStatusInvalid   ParameterStatus = "INVALID"
	ParameterStatusUnused    ParameterStatus = "UNUSED"
	ParameterStatusExternal  ParameterStatus = "EXTERNAL"
)

// String returns the wire representation.
func (s ParameterStatus) String() string { return string(s) }

// Operations is the OPERATIONS bitmask from a paramset description.
//
// The zero value is "no operations". Values combine with bitwise OR.
type Operations int

// Operations values.
//
// The CCU encodes a fourth, less-common bit next to READ/WRITE/EVENT:
// DETERMINE (0x08). Firmware descriptors spell it out symbolically as
// operations="read,write,determine"; the paramset-description wire form
// carries it as bit 8. It marks a parameter whose live value can be read
// straight from the device on demand (the WebUI's "Determine" button),
// which is what backs the determineParameter operation.
const (
	OperationsNone      Operations = 0
	OperationsRead      Operations = 1
	OperationsWrite     Operations = 2
	OperationsEvent     Operations = 4
	OperationsDetermine Operations = 8
)

// Has reports whether every bit of want is set in o.
func (o Operations) Has(want Operations) bool { return o&want == want }

// IsReadable reports whether the READ bit is set.
func (o Operations) IsReadable() bool { return o.Has(OperationsRead) }

// IsWritable reports whether the WRITE bit is set.
func (o Operations) IsWritable() bool { return o.Has(OperationsWrite) }

// IsEvent reports whether the EVENT bit is set.
func (o Operations) IsEvent() bool { return o.Has(OperationsEvent) }

// IsDeterminable reports whether the DETERMINE bit is set — the parameter
// exposes an on-demand live read via determineParameter.
func (o Operations) IsDeterminable() bool { return o.Has(OperationsDetermine) }

// Flag is the FLAGS bitmask from a paramset description. The CCU encodes
// visibility and classification hints here.
//
// STICKY is documented as 0x10 but
// the value stays faithful to that encoding for wire parity, even though
// it is a known eQ-3 documentation bug.
type Flag int

// Flag values.
const (
	FlagVisible   Flag = 1
	FlagInternal  Flag = 2
	FlagTransform Flag = 4
	FlagService   Flag = 8
	FlagSticky    Flag = 10
)

// Has reports whether every bit of want is set in f.
func (f Flag) Has(want Flag) bool { return f&want == want }

// IsVisible reports whether the VISIBLE bit is set.
func (f Flag) IsVisible() bool { return f.Has(FlagVisible) }

// IsInternal reports whether the INTERNAL bit is set.
func (f Flag) IsInternal() bool { return f.Has(FlagInternal) }

// IsService reports whether the SERVICE bit is set.
func (f Flag) IsService() bool { return f.Has(FlagService) }

// IsTransform reports whether the TRANSFORM bit is set.
func (f Flag) IsTransform() bool { return f.Has(FlagTransform) }

// IsSticky reports whether the STICKY bit is set.
func (f Flag) IsSticky() bool { return f.Has(FlagSticky) }

// RxMode is a device's receive-mode bitmask.
type RxMode int

// RxMode values.
const (
	RxModeUndefined  RxMode = 0
	RxModeAlways     RxMode = 1
	RxModeBurst      RxMode = 2
	RxModeConfig     RxMode = 4
	RxModeWakeup     RxMode = 8
	RxModeLazyConfig RxMode = 16
)

// Has reports whether every bit of want is set in m.
func (m RxMode) Has(want RxMode) bool { return m&want == want }
