// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// snapshotLocale is the i18n locale used to resolve every label in
// the snapshot. Both stacks must use the same locale so the diff is
// apples-to-apples. Override via OPENCCU_LOOM_SNAPSHOT_LOCALE.
const snapshotLocaleDefault = "en"

func snapshotLocale() string {
	if v := os.Getenv("OPENCCU_LOOM_SNAPSHOT_LOCALE"); v != "" {
		return v
	}
	return snapshotLocaleDefault
}

// TestModelSnapshotDumpAgainstGodevccu produces the openccu-loom side
// of the cross-stack model-snapshot diff described in
// `notes/parity/model_snapshot_schema.md`. The test boots godevccu in
// HOMEGEAR mode with [defaultMockDevices], hydrates the openccu-loom
// device pipeline against it, walks every Device → Channel → DataPoint
// produced and writes a sorted JSON snapshot to
// `tests/integration/testdata/model_snapshot_openccu-loom.json`.
//
// The
// Emits the same shape against
// fleet. `script/model_snapshot_diff.py` compares both files
// structurally — any drift is reported per (device, channel, dp).
//
// This test always succeeds (it dumps; it does not assert). The diff
// script is what fails or passes the comparison.
func TestModelSnapshotDumpAgainstGodevccu(t *testing.T) {
	srv := startMockCCUWithDevices(t, snapshotDevices(t))

	xmlClient := newXMLRPCClient(t, srv.URL())
	caller := &xmlrpcBackendCaller{client: xmlClient}
	backend := backends.NewCcuBackend(caller, nil, nil)

	c, err := central.New(central.Config{Name: "snapshot-ccu"})
	if err != nil {
		t.Fatalf("central: %v", err)
	}
	locale := snapshotLocale()
	translations, err := ccudata.LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("translations: %v", err)
	}
	// Wire the visibility registry so the un_ignore-aware DP creation
	// path (HM-Sec-Key/HM-Sec-Win ERROR, HmIP-DLD/HmIP-DLP ERROR_JAMMED)
	// matches the production pipeline. Without WithVisibility the
	// resolveDataPointWithUnIgnore call falls back to false and the
	// 4 only_py snapshot drift items reappear ( regression).
	visReg := visibility.NewRegistry()
	pipeline := adapter.NewDevicePipeline(c).
		WithTranslations(translations, locale).
		WithVisibility(visReg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := pipeline.IngestFromBackend(ctx, "HmIP-RF", hmenum.InterfaceHmIPRF, backend, nil, nil, logger); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	devices := c.ModelRegistry.List()
	sort.Slice(devices, func(i, j int) bool { return devices[i].Address < devices[j].Address })

	snapshot := snapshotRoot{
		Stack:         "openccu-loom",
		StackVersion:  stackVersion(),
		Devccu:        "godevccu",
		DevccuVersion: godevccuVersion(),
		Locale:        locale,
		CapturedAt:    time.Now().UTC().Format(time.RFC3339),
		Devices:       make([]snapshotDevice, 0, len(devices)),
	}
	dumper := &snapshotDumper{translations: translations, locale: locale}
	for _, d := range devices {
		snapshot.Devices = append(snapshot.Devices, dumper.device(d))
	}

	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatalf("mkdir testdata: %v", err)
	}
	out := filepath.Join("testdata", "model_snapshot_openccu-loom.json")
	// Use an Encoder with HTML-escape disabled so the JSON keeps real `°C` /
	// `µm` characters instead of `°C` / `µm`.
	var bb bytes.Buffer
	enc := json.NewEncoder(&bb)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(snapshot); err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(out, bb.Bytes(), 0o644); err != nil {
		t.Fatalf("write %s: %v", out, err)
	}
	t.Logf("snapshot written: %s (devices=%d)", out, len(snapshot.Devices))
}

// snapshotDevices selects the godevccu device fleet for the snapshot
// test. The default is `nil` which makes godevccu load every embedded
// model (~399 devices). Set OPENCCU_LOOM_SNAPSHOT_DEVICES to a
// comma-separated list (e.g.
// "HmIP-BWTH,HmIP-BSM,HmIP-BROLL,HmIP-SWSD") for a faster smoke run.
func snapshotDevices(t *testing.T) []string {
	t.Helper()
	override := os.Getenv("OPENCCU_LOOM_SNAPSHOT_DEVICES")
	if override == "" {
		return nil // nil → all embedded models
	}
	parts := []string{}
	for _, p := range splitCommas(override) {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

func splitCommas(s string) []string {
	out := make([]string, 0, 8)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

// ---------------------------------------------------------------------------
// Snapshot shapes — keep in sync with notes/parity/model_snapshot_schema.md
// and with script/aiohomematic_snapshot.py.
// ---------------------------------------------------------------------------

type snapshotRoot struct {
	Stack         string           `json:"stack"`
	StackVersion  string           `json:"stack_version"`
	Devccu        string           `json:"devccu"`
	DevccuVersion string           `json:"devccu_version"`
	Locale        string           `json:"locale"`
	CapturedAt    string           `json:"captured_at"`
	Devices       []snapshotDevice `json:"devices"`
}

type snapshotDevice struct {
	Address      string            `json:"address"`
	Model        string            `json:"model"`
	ModelLabel   string            `json:"model_label,omitempty"`
	Name         string            `json:"name,omitempty"`
	InterfaceID  string            `json:"interface_id"`
	Firmware     string            `json:"firmware"`
	Version      int               `json:"version"`
	ProductGroup string            `json:"product_group"`
	Channels     []snapshotChannel `json:"channels"`
}

type snapshotChannel struct {
	Address   string   `json:"address"`
	Number    int      `json:"number"`
	Type      string   `json:"type"`
	TypeLabel string   `json:"type_label,omitempty"`
	Name      string   `json:"name,omitempty"`
	Rooms     []string `json:"rooms"`
	Functions []string `json:"functions"`
	GroupNo   int      `json:"group_no"`
	// Paramsets reflects which paramsets of the wire CHANNEL_DESCRIPTION
	// the channel exposes. openccu-loom only retains the
	// `ParamsetIn`-style routing hint, not the verbatim list — for the
	// snapshot diff we therefore emit the field but flag it as
	// best-effort (DP-presence-derived). The diff tolerates this field
	// because the underlying paramset_descriptions diff already
	// confirmed identity at the wire layer.
	Paramsets            []string          `json:"paramsets,omitempty"`
	OperationMode        string            `json:"operation_mode,omitempty"`
	GenericDataPoints    []snapshotGeneric `json:"generic_data_points"`
	CustomDataPoints     []snapshotCustom  `json:"custom_data_points"`
	CalculatedDataPoints []snapshotCalc    `json:"calculated_data_points"`
}

type snapshotGeneric struct {
	ParamsetKey    string         `json:"paramset_key"`
	Parameter      string         `json:"parameter"`
	ParameterLabel string         `json:"parameter_label,omitempty"`
	Type           string         `json:"type"`
	Operations     int            `json:"operations"`
	Flags          int            `json:"flags"`
	Min            any            `json:"min,omitempty"`
	Max            any            `json:"max,omitempty"`
	Default        any            `json:"default,omitempty"`
	Unit           string         `json:"unit,omitempty"`
	Multiplier     float64        `json:"multiplier"`
	Special        []specialEntry `json:"special,omitempty"`
	ValueList      []string       `json:"value_list,omitempty"`
	Control        string         `json:"control,omitempty"`
	ID             string         `json:"id,omitempty"`
	TabOrder       *int           `json:"tab_order,omitempty"`
	Category       string         `json:"category"`
	Usage          string         `json:"usage"`
	IsWritable     bool           `json:"is_writable"`
	IsReadable     bool           `json:"is_readable"`
	IsVisible      bool           `json:"is_visible"`
	EnabledDefault bool           `json:"enabled_default"`
	IsForcedSensor bool           `json:"is_forced_sensor"`
	IsUnIgnored    bool           `json:"is_un_ignored"`
	ForcedUsage    string         `json:"forced_usage,omitempty"`
}

// SpecialEntry mirrors
// — a list of {id, value} maps. The wire shape differs across firmwares
// (sometimes a flat dict, sometimes a list); both stacks must agree on
// the canonical form for the snapshot diff to be meaningful.
type specialEntry struct {
	ID    string `json:"id"`
	Value any    `json:"value"`
}

type snapshotCustom struct {
	Profile  string   `json:"profile"`
	Category string   `json:"category"`
	Wrapped  []string `json:"wrapped_dps"`
}

type snapshotCalc struct {
	Parameter string `json:"parameter"`
	Category  string `json:"category"`
}

// ---------------------------------------------------------------------------
// Dumping helpers
// ---------------------------------------------------------------------------

func stackVersion() string {
	if v := os.Getenv("OPENCCU_LOOM_VERSION"); v != "" {
		return v
	}
	return "dev"
}

func godevccuVersion() string {
	return "embedded"
}

// snapshotDumper bundles the per-locale Translations the dump
// helpers need to resolve human-readable labels for devices,
// channels and parameters. Keeping the translations on a receiver
// avoids passing them through every helper.
type snapshotDumper struct {
	translations *ccudata.Translations
	locale       string
}

func (s *snapshotDumper) device(d *device.Device) snapshotDevice {
	return s.dumpDevice(d)
}

func (s *snapshotDumper) dumpDevice(d *device.Device) snapshotDevice {
	chs := d.Channels()
	sort.Slice(chs, func(i, j int) bool { return chs[i].Number < chs[j].Number })

	fw := ""
	if info := d.Firmware().Info(); info.Current != "" {
		fw = info.Current
	}
	// interface_id in the snapshot mirrors the Python snapshot's
	// `str(device.product_group)` (aiohomematic_snapshot.py:552) — not
	// the raw CCU interface string. For most devices these are the same,
	// but model-prefix-derived product groups (e.g. HmIPW-xxx discovered
	// on HmIP-RF) differ: Python emits "HmIP-Wired", Go previously emitted
	// "HmIP-RF". Using ProductGroup here closes that gap.
	// version mirrors the Python snapshot's
	// `device._device_description.get("VERSION") or 0`
	// (aiohomematic_snapshot.py:531) — the CCU's device-schema version
	// integer. Stored on Device.SchemaVersion since the ingest pipeline
	// now propagates DeviceDescription.Version.
	out := snapshotDevice{
		Address:      d.Address,
		Model:        d.Model,
		ModelLabel:   d.ModelLabel,
		Name:         d.Name(),
		InterfaceID:  string(d.ProductGroup),
		Firmware:     fw,
		Version:      d.SchemaVersion,
		ProductGroup: string(d.ProductGroup),
		Channels:     make([]snapshotChannel, 0, len(chs)),
	}
	for _, ch := range chs {
		if ch == nil {
			continue
		}
		out.Channels = append(out.Channels, s.dumpChannel(ch))
	}
	return out
}

func (s *snapshotDumper) dumpChannel(ch *device.Channel) snapshotChannel {
	rooms := ch.Rooms()
	functions := ch.Functions()
	sort.Strings(rooms)
	sort.Strings(functions)

	// Hardcoding ["VALUES", "MASTER"] would over-report on channels that the
	// wire reports with PARAMSETS=["VALUES"] only (most maintenance / virtual
	// channels).
	paramsets := []string{}
	if len(ch.DataPoints()) > 0 {
		paramsets = append(paramsets, "VALUES")
	}
	if len(ch.MasterDataPoints()) > 0 {
		paramsets = append(paramsets, "MASTER")
	}

	typeLabel := ""
	if s.translations != nil {
		typeLabel = s.translations.ChannelType(s.locale, ch.Type)
	}

	out := snapshotChannel{
		Address:              ch.Address,
		Number:               ch.Number,
		Type:                 ch.Type,
		TypeLabel:            typeLabel,
		Name:                 ch.Name(),
		Rooms:                rooms,
		Functions:            functions,
		GroupNo:              ch.GroupNumber(),
		Paramsets:            paramsets,
		OperationMode:        ch.OperationMode(),
		GenericDataPoints:    s.dumpGenericDPs(ch),
		CustomDataPoints:     dumpCustomDPs(ch),
		CalculatedDataPoints: dumpCalculatedDPs(ch),
	}
	return out
}

func (s *snapshotDumper) dumpGenericDPs(ch *device.Channel) []snapshotGeneric {
	out := make([]snapshotGeneric, 0)
	for _, dp := range ch.DataPoints() {
		out = append(out, s.dumpGenericDP(ch.Type, "VALUES", dp))
	}
	for _, dp := range ch.MasterDataPoints() {
		out = append(out, s.dumpGenericDP(ch.Type, "MASTER", dp))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ParamsetKey != out[j].ParamsetKey {
			return out[i].ParamsetKey < out[j].ParamsetKey
		}
		return out[i].Parameter < out[j].Parameter
	})
	return out
}

// dpAttributes is the union of optional helper interfaces every
// `*generic.DataPoint[T]` satisfies. The snapshot reads through this
// rather than reflecting concrete generic types.
type dpAttributes interface {
	device.ParameterDataPoint
	IsWritable() bool
	IsReadable() bool
	Visible() bool
	EnabledByDefault() bool
	IsForcedSensor() bool
	IsUnIgnored() bool
	Usage() hmenum.DataPointUsage
	Category() hmenum.DataPointCategory
	Multiplier() float64
}

func (s *snapshotDumper) dumpGenericDP(channelType, paramsetKey string, raw device.ParameterDataPoint) snapshotGeneric {
	desc := raw.ParameterData()
	dp, _ := raw.(dpAttributes)

	parameterLabel := ""
	if s.translations != nil {
		parameterLabel = s.translations.ParameterLabel(s.locale, channelType, string(raw.Parameter()))
	}
	g := snapshotGeneric{
		ParamsetKey:    paramsetKey,
		Parameter:      string(raw.Parameter()),
		ParameterLabel: parameterLabel,
		Type:           string(desc.Type),
		Operations:     int(desc.Operations),
		Flags:          int(desc.Flags),
		Min:            decodeTyped(desc.Min, desc.Type),
		Max:            decodeTyped(desc.Max, desc.Type),
		Default:        decodeTyped(desc.Default, desc.Type),
		Unit:           generic.CleanupUnit(raw.Parameter(), desc.Unit),
		Multiplier:     generic.MultiplierForUnit(desc.Unit),
		Special:        decodeSpecial(desc.Special),
		ValueList:      append([]string{}, desc.ValueList...),
		Control:        desc.Control,
		ID:             desc.ID,
		TabOrder:       desc.TabOrder,
	}
	if dp != nil {
		// openccu-loom applies a per-parameter multiplier override for
		// `TIME_OF_OPERATION` (CCU reports seconds; HA expects days, so the runtime
		// emits 1/86400). The snapshot is the apples-to-apples wire-attribution
		// view, so prefer the unit-based multiplier here. The HA-friendly per-param
		// override stays available via `dp.Multiplier()` for north-bound consumers
		// and is documented in `notes/parity/by_design.md` (/HA-multiplier).
		g.Multiplier = generic.MultiplierForUnit(desc.Unit)
		g.Category = string(dp.Category())
		g.Usage = string(dp.Usage())
		g.IsWritable = dp.IsWritable()
		g.IsReadable = dp.IsReadable()
		// Is_visible mirrors: bool = flags &
		// Flag.VISIBLE == Flag.VISIBLE` — a wire-flag property,
		// independent of usage. The snapshot diff treats them as the
		// same canonical attribute, so emit the flag-derived value.
		g.IsVisible = desc.Flags.IsVisible()
		// Enabled_default mirrors
		// `BaseDataPoint.enabled_default` — true when the entity should
		// surface in HA without operator action. The Go side derives
		// it from the effective Usage(), which already factors in the
		// no_create suppression pipeline; that matches Python's
		// behaviour for the snapshot diff.
		g.EnabledDefault = dp.EnabledByDefault()
		g.IsForcedSensor = dp.IsForcedSensor()
		g.IsUnIgnored = dp.IsUnIgnored()

		// The Go resolver detects the same condition and assigns KindBinarySensor,
		// which surfaces as DataPointCategoryBinarySensor. Align the snapshot type
		// field with Python's observation.
		if desc.Type == hmenum.ParameterTypeEnum &&
			dp.Category() == hmenum.DataPointCategoryBinarySensor {
			g.Type = string(hmenum.ParameterTypeBool)
			// min/max for a BOOL parameter are false/true.
			g.Min = false
			g.Max = true
		}
	}
	// - force_usage(DATA_POINT) is called by `_mark_data_points` /
	// additional_data_points-promotion (visibility configurator, custom-DP
	// "force-promote" pipeline). Both stacks emit a value here; the snapshot
	// must surface it unchanged. - force_usage(NO_CREATE) is the suppression
	// pipeline that openccu-loom runs internally to mirror Python's
	// `_get_data_point_usage` returning NO_CREATE for undefined DPs on custom-DP
	// devices. Python attributes that to `usage` directly (no `_forced_usage`
	// write); openccu-loom uses `SetForcedUsage(NoCreate)` as the implementation
	// lever. The canonical wire attribute is `usage`; we suppress the duplicate
	// `forced_usage=no_create` to keep the snapshot symmetric.
	if forced, ok := raw.(interface {
		ForcedUsage() (hmenum.DataPointUsage, bool)
	}); ok {
		if u, set := forced.ForcedUsage(); set {
			if u != hmenum.DataPointUsageNoCreate {
				g.ForcedUsage = string(u)
			}
		}
	}
	return g
}

func dumpCustomDPs(ch *device.Channel) []snapshotCustom {
	out := make([]snapshotCustom, 0, 1)
	dp := ch.CustomDataPoint()
	if dp == nil {
		return out
	}
	profile := fmt.Sprintf("%T", dp)
	// Always derive the category from the Custom-DP's Go package segment. The
	// DP's own Category() method delegates to a wrapped Generic-DP whose
	// category reflects the wire shape (e.g. LEVEL → "number") rather than the
	// custom domain (`light`).
	cat := customCategoryFromType(profile)
	wrapped := []string{}
	if w, ok := dp.(interface{ WrappedDataPointKeys() []string }); ok {
		func() {
			defer func() { _ = recover() }()
			wrapped = w.WrappedDataPointKeys()
			sort.Strings(wrapped)
		}()
	}
	out = append(out, snapshotCustom{Profile: profile, Category: cat, Wrapped: wrapped})
	return out
}

// customCategoryFromType extracts the domain segment from a Go
// type-name string like `*climate.Climate` or
// `*cover.Cover`. Returns the empty string when no segment is
// recognisable.
//
// Two Go-package names diverge.
// `DataPointCategory` string values because the package would otherwise
// collide with a Go reserved word (`switch` → `switchdev`) or carry
// an internal-only abbreviation (`textdisplay` → `text_display`).
// Map them back to the canonical
// model-snapshot diff (`script/model_snapshot_diff.py`) does not
// report a fleet-wide false-positive drift.
//
//	`switch.py:32` → `DataPointCategory.SWITCH = "switch"`
//	`text_display.py` → `DataPointCategory.TEXT_DISPLAY = "text_display"`
func customCategoryFromType(typeName string) string {
	// Strip leading `*` for pointer types.
	if len(typeName) > 0 && typeName[0] == '*' {
		typeName = typeName[1:]
	}
	// Take the package portion (everything before the first dot).
	pkg := ""
	for i := 0; i < len(typeName); i++ {
		if typeName[i] == '.' {
			pkg = typeName[:i]
			break
		}
	}
	switch pkg {
	case "switchdev":
		return "switch"
	case "textdisplay":
		return "text_display"
	}
	return pkg
}

func dumpCalculatedDPs(ch *device.Channel) []snapshotCalc {
	out := make([]snapshotCalc, 0)
	for _, dp := range ch.CalculatedDataPoints() {
		key := dp.DataPointKey()
		c := snapshotCalc{Parameter: string(key.Parameter)}
		if cat, ok := dp.(interface {
			Category() hmenum.DataPointCategory
		}); ok {
			c.Category = string(cat.Category())
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Parameter < out[j].Parameter })
	return out
}

// decodeTyped converts a raw descriptor value (delivered by the CCU as
// a JSON string regardless of TYPE) into the typed Go representation
// The consumer expects. The
// returned value is then JSON-encoded by encoding/json without the
// surrounding quotes that confused the previous compactJSON path. nil
// signals "absent" so the snapshot omits the key.
func decodeTyped(rm json.RawMessage, paramType hmenum.ParameterType) any {
	if len(rm) == 0 {
		return nil
	}
	// First decode whatever the wire put on the line — could be a
	// string ("5.0"), a number (5), a bool (true) etc.
	var raw any
	if err := json.Unmarshal(rm, &raw); err != nil {
		return string(rm)
	}
	switch paramType {
	case hmenum.ParameterTypeBool, hmenum.ParameterTypeAction:
		return coerceBool(raw)
	case hmenum.ParameterTypeInteger, hmenum.ParameterTypeEnum:
		return coerceInt(raw)
	case hmenum.ParameterTypeFloat:
		return coerceFloat(raw)
	case hmenum.ParameterTypeString:
		// The CCU sometimes encodes STRING min/max as integers (e.g.
		// LEVEL_COMBINED has MIN=0, MAX=0 on the wire). Python's str()
		// converts them to "0"; Go must do the same so the snapshot
		// diff stays apples-to-apples.
		//
		// JSON null decodes to a nil `any` — `fmt.Sprintf("%v", nil)`
		// would emit the literal string "<nil>", which doesn't match
		// the field is omitted). Mirror the omission explicitly.
		if raw == nil {
			return nil
		}
		return fmt.Sprintf("%v", raw)
	}
	return raw
}

// CoerceBool mirrors. A
// string value that does not parse as a bool/non-zero number maps to
// false — matches the wire-string-encoded "5.0" / "30.0" inputs we
// see for BOOL-typed LOWBAT etc.
func coerceBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		switch strings.ToLower(x) {
		case "true", "1":
			return true
		}
		return false
	}
	return false
}

// CoerceInt mirrors
// like "5.0" become 5; numeric values are truncated. Returns 0 on a
// non-numeric input.
func coerceInt(v any) int {
	switch x := v.(type) {
	case bool:
		if x {
			return 1
		}
		return 0
	case float64:
		return int(x)
	case string:
		if x == "" {
			return 0
		}
		var f float64
		if err := json.Unmarshal([]byte(x), &f); err == nil {
			return int(f)
		}
		return 0
	}
	return 0
}

// CoerceFloat mirrors Strings parse as
// numbers; bools map to 0/1.
func coerceFloat(v any) float64 {
	switch x := v.(type) {
	case bool:
		if x {
			return 1
		}
		return 0
	case float64:
		return x
	case string:
		if x == "" {
			return 0
		}
		var f float64
		if err := json.Unmarshal([]byte(x), &f); err == nil {
			return f
		}
	}
	return 0
}

// TestParameterMinMaxTypeMatchesAiohomematic pins the type-coercion
// behaviour of [decodeTyped] against the cases that caused snapshot
// drift in parity_v13_diff.json:
//
// - FLOAT with string-encoded bounds ("5.0") → float64 5.0
// - STRING with integer-encoded bounds (0) → string "0"
// (LEVEL_COMBINED: VCU0000144, VCU0000145)
// - BOOL with bool-encoded bounds (false / true) → bool false / true
// - ENUM with integer-encoded bounds (0 / 1) → int 0 / 1
func TestParameterMinMaxTypeMatchesAiohomematic(t *testing.T) {
	mustRaw := func(v any) json.RawMessage {
		b, err := json.Marshal(v)
		if err != nil {
			t.Helper()
			t.Fatalf("json.Marshal(%v): %v", v, err)
		}
		return json.RawMessage(b)
	}

	cases := []struct {
		name      string
		paramType hmenum.ParameterType
		wireValue any // as stored in paramset_descriptions JSON
		want      any
	}{
		// FLOAT "5.0" → float64(5.0) — SET_TEMPERATURE min
		{
			name:      "FLOAT string 5.0",
			paramType: hmenum.ParameterTypeFloat,
			wireValue: "5.0",
			want:      5.0,
		},
		// FLOAT "30.0" → float64(30.0) — SET_TEMPERATURE max
		{
			name:      "FLOAT string 30.0",
			paramType: hmenum.ParameterTypeFloat,
			wireValue: "30.0",
			want:      30.0,
		},
		// STRING 0 → "0" — LEVEL_COMBINED min/max (VCU0000144/VCU0000145)
		// Wire stores integer 0 for STRING type
		{
			name:      "STRING int 0",
			paramType: hmenum.ParameterTypeString,
			wireValue: 0,
			want:      "0",
		},
		// STRING "hello" → "hello"
		{
			name:      "STRING string passthrough",
			paramType: hmenum.ParameterTypeString,
			wireValue: "hello",
			want:      "hello",
		},
		// BOOL false → false
		{
			name:      "BOOL false",
			paramType: hmenum.ParameterTypeBool,
			wireValue: false,
			want:      false,
		},
		// BOOL true → true
		{
			name:      "BOOL true",
			paramType: hmenum.ParameterTypeBool,
			wireValue: true,
			want:      true,
		},
		// ENUM int 0 → int 0
		{
			name:      "ENUM int 0",
			paramType: hmenum.ParameterTypeEnum,
			wireValue: 0,
			want:      0,
		},
		// ENUM int 1 → int 1
		{
			name:      "ENUM int 1",
			paramType: hmenum.ParameterTypeEnum,
			wireValue: 1,
			want:      1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decodeTyped(mustRaw(tc.wireValue), tc.paramType)
			if got != tc.want {
				t.Fatalf("decodeTyped(%v, %s) = %v (%T), want %v (%T)",
					tc.wireValue, tc.paramType, got, got, tc.want, tc.want)
			}
		})
	}
}

// decodeSpecial normalises SPECIAL to the canonical
// [{id, value}, …] shape both stacks emit. The wire shape varies
// (flat dict on classic HM, list on HmIP); we accept either and
// Always output the list-of-pairs form
func decodeSpecial(rm json.RawMessage) []specialEntry {
	if len(rm) == 0 {
		return nil
	}
	// Try list of dicts first.
	var listForm []map[string]any
	if err := json.Unmarshal(rm, &listForm); err == nil {
		out := make([]specialEntry, 0, len(listForm))
		for _, m := range listForm {
			id, _ := m["ID"].(string)
			if id == "" {
				id, _ = m["id"].(string)
			}
			val := m["VALUE"]
			if val == nil {
				val = m["value"]
			}
			if id == "" {
				continue
			}
			out = append(out, specialEntry{ID: id, Value: val})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
		if len(out) > 0 {
			return out
		}
	}
	// Fallback: flat dict {ID: value, ...}.
	var dictForm map[string]any
	if err := json.Unmarshal(rm, &dictForm); err == nil {
		out := make([]specialEntry, 0, len(dictForm))
		for k, v := range dictForm {
			out = append(out, specialEntry{ID: k, Value: v})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
		return out
	}
	return nil
}
