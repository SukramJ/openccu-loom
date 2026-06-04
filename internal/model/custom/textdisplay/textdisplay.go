// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package textdisplay implements the text-display custom data point.
//
// A TextDisplay is the HmIP-SDV* family: a screen with multiple rows,
// each one addressable through DISPLAY_DATA_ID + DISPLAY_DATA_STRING
// plus optional icon, alignment, and colour parameters. A separate
// DISPLAY_DATA_COMMIT pulse commits the current row's configuration
// to the physical display.
package textdisplay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ErrInvalidRow is returned when a caller writes to a row index outside
// the supported 1…5 range.
var ErrInvalidRow = errors.New("textdisplay: invalid row index")

// ErrInvalidRepetitions is returned when an opts.Repetitions label is not in
// the device's available repetitions list.
var ErrInvalidRepetitions = errors.New("textdisplay: invalid repetitions value")

// ErrInvalidIcon is returned when a Row.Icon label is not in the
// device's available icons list.
var ErrInvalidIcon = errors.New("textdisplay: invalid icon value")

// ErrInvalidSound is returned when a [SoundOptions].Sound label is not in
// the device's available sounds list.
var ErrInvalidSound = errors.New("textdisplay: invalid sound value")

// ErrRowTooLong is returned by [Row.Validate] when the text field exceeds
// [MaxRowLength] bytes.
var ErrRowTooLong = errors.New("textdisplay: row text exceeds maximum length")

// ErrInvalidBackgroundColor is returned when a Row.BackgroundColor label is not
// in the device's available background-color list.
var ErrInvalidBackgroundColor = errors.New("textdisplay: invalid background color value")

// ErrInvalidTextColor is returned when a Row.TextColor label is not in the
// device's available text-color list.
var ErrInvalidTextColor = errors.New("textdisplay: invalid text color value")

// ErrInvalidAlignment is returned when a Row.Alignment value is not in the
// device's available alignment list.
var ErrInvalidAlignment = errors.New("textdisplay: invalid alignment value")

// ErrInvalidInterval is returned when SoundOptions.Interval is not in the
// device's available interval list.
var ErrInvalidInterval = errors.New("textdisplay: invalid interval value")

// maxDisplayID is the maximum DISPLAY_DATA_ID slot index (1-based).
const maxDisplayID = 5

// MaxRowLength is the maximum allowed text length for a single display row.
// Matches the HmIP-SDV* firmware limit.
const MaxRowLength = 24

// defaultIcons is the static fallback icon list for HmIP-WRCD when no
// runtime paramset is available. Sourced from the DISPLAY_DATA_ICON
// VALUE_LIST present on all HmIP-SDV* channel 1 MASTER paramsets.
var defaultIcons = []string{
	"OFF",
	"ON",
	"OPEN",
	"CLOSED",
	"ERROR",
	"OK",
	"INFORMATION",
	"NEW_MESSAGE",
	"SERVICE_MESSAGE",
	"SIGNAL_NEW_MESSAGE",
	"SIGNAL_SERVICE_MESSAGE",
	"SIGNAL_NEW_INFORMATION",
}

// defaultSounds is the static fallback sound list for HmIP-WRCD when
// no runtime paramset is available. Sourced from the
// ACOUSTIC_NOTIFICATION_SELECTION VALUE_LIST on HmIP-SDV* devices.
var defaultSounds = []string{
	"SOUND_OFF",
	"LONG_LONG",
	"LONG_SHORT",
	"LONG_SHORT_SHORT",
	"SHORT",
	"SHORT_SHORT",
	"LONG",
}

// Writer is the outbound-command contract.
type Writer interface {
	SetValue(ctx context.Context, address string, parameter hmenum.Parameter, value any, priority hmenum.CommandPriority) error
}

// Alignment label constants for DISPLAY_DATA_ALIGNMENT. The CCU VALUE_LIST
// uses these string labels; the device firmware maps them to its internal
// enum positions.
const (
	AlignLeft   = "LEFT"
	AlignCenter = "CENTER"
	AlignRight  = "RIGHT"
)

// Row captures one line on the display. Any zero-valued field is left
// at the display's current value (no wire write).
type Row struct {
	ID              int32 // DISPLAY_DATA_ID — required, 1-based
	Text            string
	Icon            string
	Alignment       *string // DISPLAY_DATA_ALIGNMENT label (e.g. "LEFT", "CENTER", "RIGHT")
	TextColor       *string // DISPLAY_DATA_TEXT_COLOR label (e.g. "BLACK", "WHITE", "RED")
	BackgroundColor *string // DISPLAY_DATA_BG_COLOR label (e.g. "WHITE", "BLACK", "BLUE")
}

// Validate reports whether the row is well-formed.
// Returns [ErrRowTooLong] when Text exceeds [MaxRowLength] bytes.
func (r Row) Validate() error {
	if len(r.Text) > MaxRowLength {
		return fmt.Errorf("%w: len=%d, max=%d", ErrRowTooLong, len(r.Text), MaxRowLength)
	}
	return nil
}

// TextDisplay is the write-only display device.
type TextDisplay struct {
	custom.BaseDP

	Address string
	Writer  Writer

	// ServiceRegistry implements the write-half of [payload.Source].
	// Service methods are registered in [New].
	payload.ServiceRegistry

	// key is the composite data-point key used by [DataPointKey] to
	// satisfy [device.AttachableDataPoint]. Populated by the D.12
	// constructor via init.go; zero for instances created by [New]
	// directly (e.g. in unit tests).
	key hmtypes.DataPointKey

	// availableIcons holds the DISPLAY_DATA_ICON VALUE_LIST captured from the
	// channel paramset at construction time (or the static fallback for
	// HmIP-WRCD). Icon validation is only enforced when iconsFromDevice is true
	// (i.e. when SetAvailableIcons was called with a runtime list).
	availableIcons  []string
	iconsFromDevice bool

	// availableSounds holds the ACOUSTIC_NOTIFICATION_SELECTION VALUE_LIST
	// captured at construction time (or the static fallback). Sound validation
	// is only enforced when soundsFromDevice is true.
	availableSounds  []string
	soundsFromDevice bool

	// availableRepetitions holds the REPETITIONS VALUE_LIST captured at
	// construction time. Used to validate opts.Repetitions labels in
	// [WriteWithSound].
	availableRepetitions []string

	// availableAlignments holds the DISPLAY_DATA_ALIGNMENT VALUE_LIST
	// captured at construction time. When populated, [Write] and
	// [WriteWithSound] validate Row.Alignment against this list.
	availableAlignments []string

	// availableBackgroundColors holds the DISPLAY_DATA_BG_COLOR VALUE_LIST
	// captured at construction time.
	availableBackgroundColors []string

	// availableTextColors holds the DISPLAY_DATA_TEXT_COLOR VALUE_LIST
	// captured at construction time.
	availableTextColors []string

	// availableIntervals holds the INTERVAL VALUE_LIST captured at
	// construction time. Used to validate SoundOptions.Interval in [WriteWithSound].
	availableIntervals []string

	// burstLimitWarningDP is the optional BURST_LIMIT_WARNING binary-sensor
	// data point. When present and true, [Write] / [WriteWithSound] emit a
	// warning log entry before dispatching the wire write — mirroring the
	// Python reference's `send_text` guard.
	burstLimitWarningDP *generic.BinarySensor
}

// SoundOptions bundles the optional acoustic / repetition parameters that the
// HmIP-SDV-class displays accept alongside a row write.
type SoundOptions struct {
	// Sound is the ACOUSTIC_NOTIFICATION_SELECTION label (e.g.
	// "DISABLE_ACOUSTIC_SIGNAL", "SOUND_SHORT", "SOUND_LONG"). Empty
	// means "leave at default".
	Sound string
	// Repetitions is the REPETITIONS label ("NO_REP", "REPETITIONS_2",
	// "INFINITE", …).
	Repetitions string
	// Interval is the INTERVAL label ("100MS", "1S", …) controlling
	// the time between repetitions.
	Interval string
}

// New constructs a TextDisplay with the static capability fallback
// lists for HmIP-WRCD. When constructed via the profile registry
// (init.go), the constructor calls [SetAvailableIcons] and
// [SetAvailableSounds] with the runtime paramset values so that
// state payloads reflect the actual device capabilities.
func New(address string, w Writer) *TextDisplay {
	t := &TextDisplay{
		Address:         address,
		Writer:          w,
		availableIcons:  append([]string(nil), defaultIcons...),
		availableSounds: append([]string(nil), defaultSounds...),
	}
	t.registerTextDisplayServices()
	return t
}

// SetAvailableIcons replaces the icon list with the values from the device's
// runtime paramset. Called by the profile-registry constructor in init.go.
// Once called, [Write] validates the Row.Icon field against this list.
func (t *TextDisplay) SetAvailableIcons(icons []string) {
	if len(icons) > 0 {
		t.availableIcons = append([]string(nil), icons...)
		t.iconsFromDevice = true
	}
}

// SetAvailableSounds replaces the sound list with the values from the
// device's runtime paramset. Called by the profile-registry constructor in
// init.go. Once called, [WriteWithSound] validates SoundOptions.Sound against
// this list.
func (t *TextDisplay) SetAvailableSounds(sounds []string) {
	if len(sounds) > 0 {
		t.availableSounds = append([]string(nil), sounds...)
		t.soundsFromDevice = true
	}
}

// AvailableIcons returns a copy of the icon list (runtime or fallback).
func (t *TextDisplay) AvailableIcons() []string {
	if t == nil || t.availableIcons == nil {
		return nil
	}
	return append([]string(nil), t.availableIcons...)
}

// AvailableSounds returns a copy of the sound list (runtime or fallback).
func (t *TextDisplay) AvailableSounds() []string {
	if t == nil || t.availableSounds == nil {
		return nil
	}
	return append([]string(nil), t.availableSounds...)
}

// SetAvailableRepetitions replaces the repetitions list with the values from
// the device's runtime paramset. Called by the profile-registry constructor
// in init.go.
func (t *TextDisplay) SetAvailableRepetitions(reps []string) {
	if len(reps) > 0 {
		t.availableRepetitions = append([]string(nil), reps...)
	}
}

// AvailableRepetitions returns a copy of the repetitions list.
func (t *TextDisplay) AvailableRepetitions() []string {
	if t == nil || t.availableRepetitions == nil {
		return nil
	}
	return append([]string(nil), t.availableRepetitions...)
}

// SetAvailableAlignments replaces the alignment list with the values from the
// device's runtime paramset. Called by the profile-registry constructor in
// init.go when the channel carries DISPLAY_DATA_ALIGNMENT.
func (t *TextDisplay) SetAvailableAlignments(alignments []string) {
	if len(alignments) > 0 {
		t.availableAlignments = append([]string(nil), alignments...)
	}
}

// AvailableAlignments returns a copy of the alignment labels list (e.g.
// "LEFT", "CENTER", "RIGHT").
func (t *TextDisplay) AvailableAlignments() []string {
	if t == nil || t.availableAlignments == nil {
		return nil
	}
	return append([]string(nil), t.availableAlignments...)
}

// SetAvailableBackgroundColors replaces the background-color list with the
// values from the device's runtime paramset.
func (t *TextDisplay) SetAvailableBackgroundColors(colors []string) {
	if len(colors) > 0 {
		t.availableBackgroundColors = append([]string(nil), colors...)
	}
}

// AvailableBackgroundColors returns a copy of the background-color labels list.
func (t *TextDisplay) AvailableBackgroundColors() []string {
	if t == nil || t.availableBackgroundColors == nil {
		return nil
	}
	return append([]string(nil), t.availableBackgroundColors...)
}

// SetAvailableTextColors replaces the text-color list with the values from
// the device's runtime paramset.
func (t *TextDisplay) SetAvailableTextColors(colors []string) {
	if len(colors) > 0 {
		t.availableTextColors = append([]string(nil), colors...)
	}
}

// AvailableTextColors returns a copy of the text-color labels list.
func (t *TextDisplay) AvailableTextColors() []string {
	if t == nil || t.availableTextColors == nil {
		return nil
	}
	return append([]string(nil), t.availableTextColors...)
}

// SetAvailableIntervals replaces the interval list with the values from the
// device's runtime paramset. Called by the profile-registry constructor in
// init.go when the channel carries INTERVAL.
func (t *TextDisplay) SetAvailableIntervals(intervals []string) {
	if len(intervals) > 0 {
		t.availableIntervals = append([]string(nil), intervals...)
	}
}

// AvailableIntervals returns a copy of the interval labels list.
func (t *TextDisplay) AvailableIntervals() []string {
	if t == nil || t.availableIntervals == nil {
		return nil
	}
	return append([]string(nil), t.availableIntervals...)
}

// SetBurstLimitWarningDP installs the BURST_LIMIT_WARNING binary-sensor
// data point. Called by the profile-registry constructor in init.go when
// the channel carries the parameter.
func (t *TextDisplay) SetBurstLimitWarningDP(dp *generic.BinarySensor) {
	t.burstLimitWarningDP = dp
}

// BurstLimitWarning reports whether the device has signalled a burst-limit
// overflow. Returns false when the data point is absent or unobserved.
func (t *TextDisplay) BurstLimitWarning() bool {
	if t.burstLimitWarningDP == nil {
		return false
	}
	v, ok := t.burstLimitWarningDP.Value()
	return ok && v
}

// checkBurstLimit emits a warning log when the burst-limit data point is
// asserted. Called at the start of every write operation.
func (t *TextDisplay) checkBurstLimit() {
	if t.BurstLimitWarning() {
		slog.Warn(
			"textdisplay: burst limit active — write may be suppressed by device",
			slog.String("address", t.Address),
		)
	}
}

// validateIcon checks that the icon label appears in availableIcons when the
// runtime device list has been set via [SetAvailableIcons]. Validation is
// skipped for the static fallback list populated by [New] because the fallback
// is incomplete — device firmware may expose icons not present in the static
// set. Empty icon string is always accepted (means "leave unchanged").
func (t *TextDisplay) validateIcon(icon string) error {
	if icon == "" || !t.iconsFromDevice || len(t.availableIcons) == 0 {
		return nil
	}
	if slices.Contains(t.availableIcons, icon) {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrInvalidIcon, icon)
}

// validateSound checks that the sound label appears in availableSounds when
// the runtime device list has been set via [SetAvailableSounds]. Validation is
// skipped for the static fallback list. Empty sound string is always accepted.
func (t *TextDisplay) validateSound(sound string) error {
	if sound == "" || !t.soundsFromDevice || len(t.availableSounds) == 0 {
		return nil
	}
	if slices.Contains(t.availableSounds, sound) {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrInvalidSound, sound)
}

// validateBackgroundColor checks that the background-color label appears in
// availableBackgroundColors when the list is populated. Empty string or nil
// pointer means "leave unchanged" and always passes.
func (t *TextDisplay) validateBackgroundColor(label *string) error {
	if label == nil || *label == "" || len(t.availableBackgroundColors) == 0 {
		return nil
	}
	if slices.Contains(t.availableBackgroundColors, *label) {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrInvalidBackgroundColor, *label)
}

// validateTextColor checks that the text-color label appears in
// availableTextColors when the list is populated. Empty string or nil pointer
// means "leave unchanged" and always passes.
func (t *TextDisplay) validateTextColor(label *string) error {
	if label == nil || *label == "" || len(t.availableTextColors) == 0 {
		return nil
	}
	if slices.Contains(t.availableTextColors, *label) {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrInvalidTextColor, *label)
}

// validateAlignment checks that the alignment label appears in
// availableAlignments when the list is populated. Empty string or nil pointer
// means "leave unchanged" and always passes.
func (t *TextDisplay) validateAlignment(a *string) error {
	if a == nil || *a == "" || len(t.availableAlignments) == 0 {
		return nil
	}
	if slices.Contains(t.availableAlignments, *a) {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrInvalidAlignment, *a)
}

// validateInterval checks that the interval label appears in availableIntervals
// when the list is populated. Empty interval string is always accepted.
func (t *TextDisplay) validateInterval(interval string) error {
	if interval == "" || len(t.availableIntervals) == 0 {
		return nil
	}
	if slices.Contains(t.availableIntervals, interval) {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrInvalidInterval, interval)
}

// Default string values for optional Row fields. These match the Python
// reference (text_display.py): WHITE background, BLACK text, CENTER alignment.
const (
	defaultAlignment       = AlignCenter
	defaultTextColor       = "BLACK"
	defaultBackgroundColor = "WHITE"
)

// applyRowDefaults fills in nil optional Row string fields with the standard
// defaults. This ensures every Write call sends a complete wire bundle (as
// the Python send_text always does) rather than leaving fields at whatever
// the device's current value happens to be.
//
// Callers that want to preserve the device's current value for a specific
// field must pass an explicit non-empty *string, not nil.
// noIcon is the sentinel value sent when no explicit icon is requested.
// Matches the first entry in the device's DISPLAY_DATA_ICON VALUE_LIST which
// the reference uses as the no-icon default (conventionally "NO_ICON" or the
// first available icon label).
const noIcon = "NO_ICON"

func applyRowDefaults(r Row) Row {
	if r.Alignment == nil {
		v := defaultAlignment
		r.Alignment = &v
	}
	if r.TextColor == nil {
		v := defaultTextColor
		r.TextColor = &v
	}
	if r.BackgroundColor == nil {
		v := defaultBackgroundColor
		r.BackgroundColor = &v
	}
	return r
}

// Write lays out r on the display and commits the change in a single atomic
// put_paramset (every populated row field plus DISPLAY_DATA_COMMIT=true).
// DISPLAY_DATA_ID, DISPLAY_DATA_STRING, ..., DISPLAY_DATA_COMMIT: True})`).
// Validation: ID must be ≥ 1.
//
// Falls back to sequential SetValue + Commit when the writer is not a
// [generic.ParamsetWriter].
//
// A [generic.CallParameterCollector] is attached to ctx for
// forward-compatible batching.
func (t *TextDisplay) Write(ctx context.Context, r Row, priority hmenum.CommandPriority) error {
	if r.ID < 1 || r.ID > maxDisplayID {
		return fmt.Errorf("%w: id=%d (must be 1..%d)", ErrInvalidRow, r.ID, maxDisplayID)
	}
	r = applyRowDefaults(r)
	if err := r.Validate(); err != nil {
		return err
	}
	if err := t.validateIcon(r.Icon); err != nil {
		return err
	}
	if err := t.validateBackgroundColor(r.BackgroundColor); err != nil {
		return err
	}
	if err := t.validateTextColor(r.TextColor); err != nil {
		return err
	}
	if err := t.validateAlignment(r.Alignment); err != nil {
		return err
	}
	t.checkBurstLimit()
	ctx = custom.EnsureContext(ctx)
	if t.Writer != nil {
		coll := generic.NewCollector(generic.WriterAsBackend(t.Writer), generic.WithPriority(priority))
		ctx = generic.ContextWithCollector(ctx, coll)
		defer func() { _ = coll.Send(ctx) }()
	}
	if pw, ok := t.Writer.(generic.ParamsetWriter); ok {
		values := map[string]any{
			string(hmenum.ParameterDisplayDataID):     r.ID,
			string(hmenum.ParameterDisplayDataCommit): true,
			// STRING is always sent, even when empty — an empty string clears the
			// display row. Skipping it leaves the previous text visible.
			string(hmenum.ParameterDisplayDataString): r.Text,
		}
		// Icon is always sent. When the caller supplies an empty string the
		// first available icon (conventionally "NO_ICON") is used, so a
		// previously set icon is cleared rather than left visible.
		iconValue := r.Icon
		if iconValue == "" {
			if len(t.availableIcons) > 0 {
				iconValue = t.availableIcons[0]
			} else {
				iconValue = noIcon
			}
		}
		values[string(hmenum.ParameterDisplayDataIcon)] = iconValue
		if r.Alignment != nil && *r.Alignment != "" {
			values[string(hmenum.ParameterDisplayDataAlignment)] = *r.Alignment
		}
		if r.TextColor != nil && *r.TextColor != "" {
			values[string(hmenum.ParameterDisplayDataTextColor)] = *r.TextColor
		}
		if r.BackgroundColor != nil && *r.BackgroundColor != "" {
			values[string(hmenum.ParameterDisplayDataBackgroundColor)] = *r.BackgroundColor
		}
		return pw.PutParamset(ctx, t.Address, hmenum.ParamsetKeyValues, values, priority)
	}
	if err := t.writeRowFields(ctx, r, priority); err != nil {
		return err
	}
	return t.Commit(ctx, priority)
}

// Commit pulses DISPLAY_DATA_COMMIT, flushing the prepared row to the
// physical display.
func (t *TextDisplay) Commit(ctx context.Context, priority hmenum.CommandPriority) error {
	if err := t.Writer.SetValue(custom.EnsureContext(ctx), t.Address, hmenum.ParameterDisplayDataCommit, true, priority); err != nil {
		return fmt.Errorf("textdisplay: COMMIT: %w", err)
	}
	return nil
}

// Clear wipes row id by writing an empty string and committing.
func (t *TextDisplay) Clear(ctx context.Context, id int32, priority hmenum.CommandPriority) error {
	return t.Write(ctx, Row{ID: id, Text: ""}, priority)
}

// writeRowsDefaultIcon is the DISPLAY_DATA_ICON value sent by WriteRows when
// no icon is requested. Clears any previously-set icon.
const writeRowsDefaultIcon = "NONE"

// writeRowsDefaultScrolling is the DISPLAY_DATA_SCROLLING value sent by
// WriteRows when no scrolling mode is specified. Clears any previously-set
// scrolling mode.
const writeRowsDefaultScrolling = "NONE"

// WriteRows writes a sequence of rows as one atomic put_paramset per row
// followed by a single DISPLAY_DATA_COMMIT write. Each row is sent to its
// own channel address (row 1 → base address, row N → base device + :N).
// This matches the reference pattern where send_text is called once per row
// and each call emits one put_paramset to the row-specific channel.
//
// Each per-row paramset always includes STRING, ICON, ALIGNMENT, and SCROLLING
// so previously-set values are explicitly cleared rather than left as-is.
func (t *TextDisplay) WriteRows(ctx context.Context, rows []Row, priority hmenum.CommandPriority) error {
	if len(rows) == 0 {
		return nil
	}
	ctx = custom.EnsureContext(ctx)
	pw, hasPW := t.Writer.(generic.ParamsetWriter)
	for _, r := range rows {
		if r.ID < 1 || r.ID > maxDisplayID {
			return fmt.Errorf("%w: id=%d (must be 1..%d)", ErrInvalidRow, r.ID, maxDisplayID)
		}
		rowAddr := t.rowAddress(r.ID)
		iconVal := r.Icon
		if iconVal == "" {
			iconVal = writeRowsDefaultIcon
		}
		alignVal := AlignLeft
		if r.Alignment != nil && *r.Alignment != "" {
			alignVal = *r.Alignment
		}
		if hasPW {
			values := map[string]any{
				string(hmenum.ParameterDisplayDataString):    r.Text,
				string(hmenum.ParameterDisplayDataIcon):      iconVal,
				string(hmenum.ParameterDisplayDataAlignment): alignVal,
				string(hmenum.ParameterDisplayDataScrolling): writeRowsDefaultScrolling,
			}
			if r.TextColor != nil && *r.TextColor != "" {
				values[string(hmenum.ParameterDisplayDataTextColor)] = *r.TextColor
			}
			if r.BackgroundColor != nil && *r.BackgroundColor != "" {
				values[string(hmenum.ParameterDisplayDataBackgroundColor)] = *r.BackgroundColor
			}
			if err := pw.PutParamset(ctx, rowAddr, hmenum.ParamsetKeyValues, values, priority); err != nil {
				return fmt.Errorf("textdisplay: WriteRows row %d: %w", r.ID, err)
			}
		} else {
			if err := t.writeRowFieldsToAddr(ctx, r, rowAddr, iconVal, priority); err != nil {
				return err
			}
		}
	}
	return t.Commit(ctx, priority)
}

// rowAddress builds the channel address for the given 1-based row ID.
// Row 1 maps to the TextDisplay's own address; row N maps to the base
// device address with channel index N (e.g. "SDV0001:1" → "SDV0001:2").
func (t *TextDisplay) rowAddress(rowID int32) string {
	// Find the last colon to split base-device from channel index.
	for i := len(t.Address) - 1; i >= 0; i-- {
		if t.Address[i] == ':' {
			return fmt.Sprintf("%s:%d", t.Address[:i], rowID)
		}
	}
	return fmt.Sprintf("%s:%d", t.Address, rowID)
}

// writeRowFieldsToAddr is the SetValue fallback path when no ParamsetWriter is
// available; writes each display field individually to rowAddr.
func (t *TextDisplay) writeRowFieldsToAddr(ctx context.Context, r Row, rowAddr, iconVal string, priority hmenum.CommandPriority) error {
	if err := t.Writer.SetValue(ctx, rowAddr, hmenum.ParameterDisplayDataString, r.Text, priority); err != nil {
		return fmt.Errorf("textdisplay: STRING row %d: %w", r.ID, err)
	}
	if err := t.Writer.SetValue(ctx, rowAddr, hmenum.ParameterDisplayDataIcon, iconVal, priority); err != nil {
		return fmt.Errorf("textdisplay: ICON row %d: %w", r.ID, err)
	}
	if r.Alignment != nil && *r.Alignment != "" {
		if err := t.Writer.SetValue(ctx, rowAddr, hmenum.ParameterDisplayDataAlignment, *r.Alignment, priority); err != nil {
			return fmt.Errorf("textdisplay: ALIGNMENT row %d: %w", r.ID, err)
		}
	}
	return nil
}

// WriteWithSound writes a single row plus the acoustic options as one atomic
// put_paramset (row fields + sound/repetition/interval + COMMIT=true).
//
// Falls back to sequential SetValue + Commit when the writer is not a
// [generic.ParamsetWriter].
//
// A [generic.CallParameterCollector] is attached to ctx for
// forward-compatible batching.
func (t *TextDisplay) WriteWithSound(ctx context.Context, r Row, opts SoundOptions, priority hmenum.CommandPriority) error {
	if r.ID < 1 || r.ID > maxDisplayID {
		return fmt.Errorf("%w: id=%d (must be 1..%d)", ErrInvalidRow, r.ID, maxDisplayID)
	}
	r = applyRowDefaults(r)
	if err := r.Validate(); err != nil {
		return err
	}
	if err := t.validateIcon(r.Icon); err != nil {
		return err
	}
	if err := t.validateBackgroundColor(r.BackgroundColor); err != nil {
		return err
	}
	if err := t.validateTextColor(r.TextColor); err != nil {
		return err
	}
	if err := t.validateAlignment(r.Alignment); err != nil {
		return err
	}
	if err := t.validateSound(opts.Sound); err != nil {
		return err
	}
	if err := t.validateInterval(opts.Interval); err != nil {
		return err
	}
	t.checkBurstLimit()
	// Validate repetitions label against the device's available list when a list
	// has been populated.
	if opts.Repetitions != "" && len(t.availableRepetitions) > 0 {
		found := slices.Contains(t.availableRepetitions, opts.Repetitions)
		if !found {
			return fmt.Errorf("%w: %q", ErrInvalidRepetitions, opts.Repetitions)
		}
	}
	ctx = custom.EnsureContext(ctx)
	if t.Writer != nil {
		coll := generic.NewCollector(generic.WriterAsBackend(t.Writer), generic.WithPriority(priority))
		ctx = generic.ContextWithCollector(ctx, coll)
		defer func() { _ = coll.Send(ctx) }()
	}
	if pw, ok := t.Writer.(generic.ParamsetWriter); ok {
		values := map[string]any{
			string(hmenum.ParameterDisplayDataID):     r.ID,
			string(hmenum.ParameterDisplayDataCommit): true,
			// STRING is always sent, even when empty — an empty string clears the
			// display row. Skipping it leaves the previous text visible.
			string(hmenum.ParameterDisplayDataString): r.Text,
		}
		// Icon is always sent. When the caller supplies an empty string the
		// first available icon (conventionally "NO_ICON") is used, so a
		// previously set icon is cleared rather than left visible.
		iconValueWS := r.Icon
		if iconValueWS == "" {
			if len(t.availableIcons) > 0 {
				iconValueWS = t.availableIcons[0]
			} else {
				iconValueWS = noIcon
			}
		}
		values[string(hmenum.ParameterDisplayDataIcon)] = iconValueWS
		if r.Alignment != nil && *r.Alignment != "" {
			values[string(hmenum.ParameterDisplayDataAlignment)] = *r.Alignment
		}
		if r.TextColor != nil && *r.TextColor != "" {
			values[string(hmenum.ParameterDisplayDataTextColor)] = *r.TextColor
		}
		if r.BackgroundColor != nil && *r.BackgroundColor != "" {
			values[string(hmenum.ParameterDisplayDataBackgroundColor)] = *r.BackgroundColor
		}
		if opts.Sound != "" {
			values[string(hmenum.ParameterAcousticNotificationSelection)] = opts.Sound
		}
		if opts.Repetitions != "" {
			values[string(hmenum.ParameterRepetitions)] = opts.Repetitions
		}
		if opts.Interval != "" {
			values[string(hmenum.ParameterInterval)] = opts.Interval
		}
		return pw.PutParamset(ctx, t.Address, hmenum.ParamsetKeyValues, values, priority)
	}
	if err := t.writeRowFields(ctx, r, priority); err != nil {
		return err
	}
	if opts.Sound != "" {
		if err := t.Writer.SetValue(ctx, t.Address, hmenum.ParameterAcousticNotificationSelection, opts.Sound, priority); err != nil {
			return fmt.Errorf("textdisplay: SOUND: %w", err)
		}
	}
	if opts.Repetitions != "" {
		if err := t.Writer.SetValue(ctx, t.Address, hmenum.ParameterRepetitions, opts.Repetitions, priority); err != nil {
			return fmt.Errorf("textdisplay: REPETITIONS: %w", err)
		}
	}
	if opts.Interval != "" {
		if err := t.Writer.SetValue(ctx, t.Address, hmenum.ParameterInterval, opts.Interval, priority); err != nil {
			return fmt.Errorf("textdisplay: INTERVAL: %w", err)
		}
	}
	return t.Commit(ctx, priority)
}

// convertRepetitions maps a numeric repetition count to the CCU VALUE_LIST
// label used by the REPETITIONS parameter:
//
//   - 0  → "NO_REPETITION"
//   - -1 → "INFINITE_REPETITIONS"
//   - 1..18 → "REPETITIONS_001" .. "REPETITIONS_018"
//
// Values outside [-1, 18] return an empty string so the caller can detect
// the out-of-range case without an extra error path.
func convertRepetitions(n int) string {
	const maxRep = 18
	switch {
	case n == 0:
		return "NO_REPETITION"
	case n == -1:
		return "INFINITE_REPETITIONS"
	case n >= 1 && n <= maxRep:
		return fmt.Sprintf("REPETITIONS_%03d", n)
	default:
		return ""
	}
}

// aggregate builds the [custom.AggregateView] over the TextDisplay's only
// observable sub-DP: the optional BURST_LIMIT_WARNING binary sensor. When
// no binary sensor has been wired, the view has no slots and IsRefreshed
// returns false.
func (t *TextDisplay) aggregate() custom.AggregateView {
	if t.burstLimitWarningDP != nil {
		return custom.AggregateStatus(t.burstLimitWarningDP)
	}
	return custom.AggregateStatus()
}

// IsRefreshed reports whether the device's BURST_LIMIT_WARNING binary sensor
// has delivered at least one value since process start. When no such sensor is
// present (write-only device with no observable parameters), returns false.
func (t *TextDisplay) IsRefreshed() bool { return t.aggregate().IsRefreshed() }

// SubDataPointKeys returns the wire identifiers of every observable sub-DP
// the TextDisplay composes (currently only BURST_LIMIT_WARNING when present).
// Returns an empty slice for write-only instances that carry no sensor DPs.
func (t *TextDisplay) SubDataPointKeys() []hmtypes.DataPointKey {
	return t.aggregate().SubDataPointKeys()
}

// writeRowFields writes every populated wire field of `r` without
// committing. Shared by [Write], [WriteRows] and [WriteWithSound].
func (t *TextDisplay) writeRowFields(ctx context.Context, r Row, priority hmenum.CommandPriority) error {
	if err := t.Writer.SetValue(ctx, t.Address, hmenum.ParameterDisplayDataID, r.ID, priority); err != nil {
		return fmt.Errorf("textdisplay: ID: %w", err)
	}
	// STRING is always sent, even when empty — an empty string clears the
	// display row. Skipping it leaves the previous text visible.
	if err := t.Writer.SetValue(ctx, t.Address, hmenum.ParameterDisplayDataString, r.Text, priority); err != nil {
		return fmt.Errorf("textdisplay: STRING: %w", err)
	}
	// Icon is always sent, even when empty — clears any previously set icon.
	iconValueFB := r.Icon
	if iconValueFB == "" {
		if len(t.availableIcons) > 0 {
			iconValueFB = t.availableIcons[0]
		} else {
			iconValueFB = noIcon
		}
	}
	if err := t.Writer.SetValue(ctx, t.Address, hmenum.ParameterDisplayDataIcon, iconValueFB, priority); err != nil {
		return fmt.Errorf("textdisplay: ICON: %w", err)
	}
	if r.Alignment != nil && *r.Alignment != "" {
		if err := t.Writer.SetValue(ctx, t.Address, hmenum.ParameterDisplayDataAlignment, *r.Alignment, priority); err != nil {
			return fmt.Errorf("textdisplay: ALIGNMENT: %w", err)
		}
	}
	if r.TextColor != nil && *r.TextColor != "" {
		if err := t.Writer.SetValue(ctx, t.Address, hmenum.ParameterDisplayDataTextColor, *r.TextColor, priority); err != nil {
			return fmt.Errorf("textdisplay: TEXT_COLOR: %w", err)
		}
	}
	if r.BackgroundColor != nil && *r.BackgroundColor != "" {
		if err := t.Writer.SetValue(ctx, t.Address, hmenum.ParameterDisplayDataBackgroundColor, *r.BackgroundColor, priority); err != nil {
			return fmt.Errorf("textdisplay: BG_COLOR: %w", err)
		}
	}
	return nil
}
