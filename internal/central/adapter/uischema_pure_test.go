// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// uischema_pure_test.go covers the pure-logic helpers in
// uischema_adapter.go and uischema_link.go that need no registry or
// CCU setup.

package adapter

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// makeBaseCfg builds a minimal generic.Spec for resolver tests.
func makeBaseCfg(addr, param string, typ hmenum.ParameterType, ops hmenum.Operations) generic.Spec {
	return generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      param,
		},
		Descriptor: hmproto.ParameterData{
			Type:       typ,
			Operations: ops,
		},
	}
}

// paramNames extracts Names from a UISchemaParameter slice for error messages.
func paramNames(ps []hmapi.UISchemaParameter) []string {
	out := make([]string, len(ps))
	for i := range ps {
		out[i] = ps[i].Name
	}
	return out
}

// ============================================================
// looseEqual tests
// ============================================================

func TestLooseEqualIdentical(t *testing.T) {
	t.Parallel()
	if !looseEqual("x", "x") {
		t.Error("identical strings must be equal")
	}
}

func TestLooseEqualNumericEquality(t *testing.T) {
	t.Parallel()
	// int 1 vs float64 1.0 — same numeric value.
	if !looseEqual(int(1), float64(1)) {
		t.Error("int(1) and float64(1) must be loosely equal")
	}
}

func TestLooseEqualStringFallback(t *testing.T) {
	t.Parallel()
	// Sprint("abc") == Sprint("abc") via string comparison.
	if !looseEqual("abc", "abc") {
		t.Error("same string must be loosely equal")
	}
}

func TestLooseEqualDifferentStrings(t *testing.T) {
	t.Parallel()
	if looseEqual("a", "b") {
		t.Error("different strings must not be equal")
	}
}

func TestLooseEqualDifferentNumbers(t *testing.T) {
	t.Parallel()
	if looseEqual(float64(1), float64(2)) {
		t.Error("1.0 and 2.0 must not be equal")
	}
}

// ============================================================
// joinSorted tests
// ============================================================

func TestJoinSortedEmpty(t *testing.T) {
	t.Parallel()
	if got := joinSorted(nil); got != "" {
		t.Errorf("joinSorted(nil) = %q, want empty", got)
	}
}

func TestJoinSortedSingle(t *testing.T) {
	t.Parallel()
	if got := joinSorted([]string{"z"}); got != "z" {
		t.Errorf("joinSorted([z]) = %q, want z", got)
	}
}

func TestJoinSortedMultiple(t *testing.T) {
	t.Parallel()
	got := joinSorted([]string{"c", "a", "b"})
	if got != "a|b|c" {
		t.Errorf("joinSorted = %q, want a|b|c", got)
	}
}

func TestJoinSortedDoesNotMutateInput(t *testing.T) {
	t.Parallel()
	in := []string{"c", "a", "b"}
	_ = joinSorted(in)
	if in[0] != "c" {
		t.Error("joinSorted must not mutate the input slice")
	}
}

// ============================================================
// subsetOptionMatches tests
// ============================================================

func TestSubsetOptionMatchesAllMatch(t *testing.T) {
	t.Parallel()
	current := map[string]any{"A": 1, "B": "x"}
	values := map[string]any{"A": float64(1)} // looseEqual: int vs float64
	if !subsetOptionMatches(values, current) {
		t.Error("matching subset must return true")
	}
}

func TestSubsetOptionMatchesMissingKey(t *testing.T) {
	t.Parallel()
	current := map[string]any{"A": 1}
	values := map[string]any{"B": 1}
	if subsetOptionMatches(values, current) {
		t.Error("missing key must return false")
	}
}

func TestSubsetOptionMatchesWrongValue(t *testing.T) {
	t.Parallel()
	current := map[string]any{"A": 1}
	values := map[string]any{"A": 2}
	if subsetOptionMatches(values, current) {
		t.Error("wrong value must return false")
	}
}

func TestSubsetOptionMatchesEmpty(t *testing.T) {
	t.Parallel()
	// Empty values subset matches anything.
	if !subsetOptionMatches(map[string]any{}, map[string]any{"A": 1}) {
		t.Error("empty subset must match any current")
	}
}

// ============================================================
// paramShouldRender tests
// ============================================================

func TestParamShouldRenderVisible(t *testing.T) {
	t.Parallel()
	pd := hmproto.ParameterData{
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		Type:       hmenum.ParameterTypeFloat,
		Flags:      hmenum.FlagVisible,
	}
	if !paramShouldRender("LEVEL", pd) {
		t.Error("visible read+write param must render")
	}
}

func TestParamShouldRenderInvisible(t *testing.T) {
	t.Parallel()
	pd := hmproto.ParameterData{
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		Type:       hmenum.ParameterTypeFloat,
		// Flags zero — not visible.
	}
	if paramShouldRender("LEVEL", pd) {
		t.Error("invisible param must not render")
	}
}

func TestParamShouldRenderWeekProgram(t *testing.T) {
	t.Parallel()
	pd := hmproto.ParameterData{
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		Type:       hmenum.ParameterTypeFloat,
		Flags:      hmenum.FlagVisible,
	}
	if paramShouldRender("WEEK_PROGRAM_POINTER", pd) {
		t.Error("WEEK_PROGRAM param must not render")
	}
}

func TestParamShouldRenderSchedulePattern(t *testing.T) {
	t.Parallel()
	pd := hmproto.ParameterData{
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		Type:       hmenum.ParameterTypeFloat,
		Flags:      hmenum.FlagVisible,
	}
	if paramShouldRender("1_WP_ENDTIME_MONDAY_1", pd) {
		t.Error("schedule pattern must not render")
	}
}

func TestParamShouldRenderNoReadWrite(t *testing.T) {
	t.Parallel()
	pd := hmproto.ParameterData{
		Operations: hmenum.OperationsEvent, // no read, no write
		Type:       hmenum.ParameterTypeFloat,
		Flags:      hmenum.FlagVisible,
	}
	if paramShouldRender("TEMPERATURE", pd) {
		t.Error("non-readable non-writable param must not render")
	}
}

// ============================================================
// applyOrder tests
// ============================================================

func TestApplyOrderNilMeta(t *testing.T) {
	t.Parallel()
	a := &UISchemaAdapter{}
	params := []hmapi.UISchemaParameter{
		{Name: "C"},
		{Name: "A"},
		{Name: "B"},
	}
	a.applyOrder(params, nil)
	if params[0].Name != "A" || params[1].Name != "B" || params[2].Name != "C" {
		t.Errorf("nil meta → alphabetical order, got %v", paramNames(params))
	}
}

func TestApplyOrderWithOrder(t *testing.T) {
	t.Parallel()
	a := &UISchemaAdapter{}
	params := []hmapi.UISchemaParameter{
		{Name: "B"},
		{Name: "A"},
		{Name: "C"},
	}
	meta := &ccudata.SenderTypeMetadata{
		ParameterOrder: []string{"C", "A", "B"},
	}
	a.applyOrder(params, meta)
	if params[0].Name != "C" || params[1].Name != "A" || params[2].Name != "B" {
		t.Errorf("explicit order not followed, got %v", paramNames(params))
	}
}

func TestApplyOrderWithExtraParams(t *testing.T) {
	t.Parallel()
	a := &UISchemaAdapter{}
	params := []hmapi.UISchemaParameter{
		{Name: "Z"},
		{Name: "A"},
		{Name: "B"},
	}
	meta := &ccudata.SenderTypeMetadata{
		ParameterOrder: []string{"B", "A"},
	}
	a.applyOrder(params, meta)
	// B and A come first in that order, then Z (unknown → alphabetical after).
	if params[0].Name != "B" || params[1].Name != "A" {
		t.Errorf("ordered params must come first, got %v", paramNames(params))
	}
	if params[2].Name != "Z" {
		t.Errorf("extra param Z must be last, got %v", paramNames(params))
	}
}

// ============================================================
// rawFloatGreaterThan tests (uischema_link.go)
// ============================================================

func TestRawFloatGreaterThanTrue(t *testing.T) {
	t.Parallel()
	raw, _ := json.Marshal(5.0)
	if !rawFloatGreaterThan(raw, 3.0) {
		t.Error("5.0 > 3.0 must return true")
	}
}

func TestRawFloatGreaterThanFalse(t *testing.T) {
	t.Parallel()
	raw, _ := json.Marshal(1.0)
	if rawFloatGreaterThan(raw, 3.0) {
		t.Error("1.0 > 3.0 must return false")
	}
}

func TestRawFloatGreaterThanEqual(t *testing.T) {
	t.Parallel()
	raw, _ := json.Marshal(3.0)
	if rawFloatGreaterThan(raw, 3.0) {
		t.Error("3.0 > 3.0 (not strictly) must return false")
	}
}

func TestRawFloatGreaterThanEmpty(t *testing.T) {
	t.Parallel()
	if rawFloatGreaterThan(nil, 0) {
		t.Error("nil raw must return false")
	}
}

func TestRawFloatGreaterThanNonNumeric(t *testing.T) {
	t.Parallel()
	raw := []byte(`"not a number"`)
	if rawFloatGreaterThan(raw, 0) {
		t.Error("non-numeric JSON must return false")
	}
}

// ============================================================
// combined_bridge: AnyUpdateAdapter.OnAnyUpdate
// ============================================================

type fakeTypedSubscriber struct {
	fn func(old, next int)
}

func (f *fakeTypedSubscriber) OnUpdate(fn func(old, next int)) func() {
	f.fn = fn
	return func() {}
}

func TestAnyUpdateAdapterOnAnyUpdate(t *testing.T) {
	t.Parallel()
	inner := &fakeTypedSubscriber{}
	a := AnyUpdateAdapter[int]{Inner: inner}

	var got any
	unsub := a.OnAnyUpdate(func(_, next any) {
		got = next
	})
	if unsub == nil {
		t.Fatal("unsub must not be nil")
	}
	// Simulate an update from the inner subscriber.
	if inner.fn == nil {
		t.Fatal("inner.OnUpdate must have been called")
	}
	inner.fn(0, 42)
	if got != 42 {
		t.Errorf("OnAnyUpdate received %v, want 42", got)
	}
}

func TestAnyUpdateAdapterNilInner(t *testing.T) {
	t.Parallel()
	a := AnyUpdateAdapter[int]{Inner: nil}
	unsub := a.OnAnyUpdate(func(_, _ any) {})
	// Should return a no-op closure, not panic.
	if unsub == nil {
		t.Fatal("unsub must not be nil even for nil inner")
	}
	unsub() // must not panic
}

// ============================================================
// datapoint_resolver: isBinarySensor, resolveWritable, resolveReadonly
// ============================================================

func TestIsBinarySensorBool(t *testing.T) {
	t.Parallel()
	pd := hmproto.ParameterData{Type: hmenum.ParameterTypeBool}
	if !isBinarySensor(pd) {
		t.Error("bool param must be binary sensor")
	}
}

func TestIsBinarySensorClosedOpen(t *testing.T) {
	t.Parallel()
	pd := hmproto.ParameterData{
		Type:      hmenum.ParameterTypeEnum,
		ValueList: []string{"CLOSED", "OPEN"},
	}
	if !isBinarySensor(pd) {
		t.Error("CLOSED/OPEN must be binary sensor")
	}
}

func TestIsBinarySensorDryRain(t *testing.T) {
	t.Parallel()
	pd := hmproto.ParameterData{
		Type:      hmenum.ParameterTypeEnum,
		ValueList: []string{"DRY", "RAIN"},
	}
	if !isBinarySensor(pd) {
		t.Error("DRY/RAIN must be binary sensor")
	}
}

func TestIsBinarySensorStableNotStable(t *testing.T) {
	t.Parallel()
	pd := hmproto.ParameterData{
		Type:      hmenum.ParameterTypeEnum,
		ValueList: []string{"STABLE", "NOT_STABLE"},
	}
	if !isBinarySensor(pd) {
		t.Error("STABLE/NOT_STABLE must be binary sensor")
	}
}

func TestIsBinarySensorUnknownValueList(t *testing.T) {
	t.Parallel()
	pd := hmproto.ParameterData{
		Type:      hmenum.ParameterTypeEnum,
		ValueList: []string{"LOCKED", "UNLOCKED"},
	}
	if isBinarySensor(pd) {
		t.Error("unknown value list must not be binary sensor")
	}
}

func TestIsBinarySensorFloat(t *testing.T) {
	t.Parallel()
	pd := hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}
	if isBinarySensor(pd) {
		t.Error("float param must not be binary sensor")
	}
}

func TestResolveWritableAction(t *testing.T) {
	t.Parallel()
	cfg := makeBaseCfg("DEV:1", "ACTION_PARAM", hmenum.ParameterTypeAction, hmenum.OperationsWrite)
	dp := resolveWritable(cfg, "ACTION_PARAM", cfg.Descriptor)
	if dp == nil {
		t.Fatal("ACTION write-only must resolve to non-nil")
	}
}

func TestResolveWritableWriteOnlyValueList(t *testing.T) {
	t.Parallel()
	cfg := makeBaseCfg("DEV:1", "SELECT_PARAM", hmenum.ParameterTypeInteger, hmenum.OperationsWrite)
	cfg.Descriptor.ValueList = []string{"A", "B"}
	dp := resolveWritable(cfg, "SELECT_PARAM", cfg.Descriptor)
	if dp == nil {
		t.Fatal("write-only with value list must resolve")
	}
}

func TestResolveWritableWriteOnlyFloat(t *testing.T) {
	t.Parallel()
	cfg := makeBaseCfg("DEV:1", "F", hmenum.ParameterTypeFloat, hmenum.OperationsWrite)
	dp := resolveWritable(cfg, "F", cfg.Descriptor)
	if dp == nil {
		t.Fatal("write-only float must resolve")
	}
}

func TestResolveWritableWriteOnlyInteger(t *testing.T) {
	t.Parallel()
	cfg := makeBaseCfg("DEV:1", "I", hmenum.ParameterTypeInteger, hmenum.OperationsWrite)
	dp := resolveWritable(cfg, "I", cfg.Descriptor)
	if dp == nil {
		t.Fatal("write-only integer must resolve")
	}
}

func TestResolveWritableWriteOnlyBool(t *testing.T) {
	t.Parallel()
	cfg := makeBaseCfg("DEV:1", "B", hmenum.ParameterTypeBool, hmenum.OperationsWrite)
	dp := resolveWritable(cfg, "B", cfg.Descriptor)
	if dp == nil {
		t.Fatal("write-only bool must resolve")
	}
}

func TestResolveWritableWriteOnlyString(t *testing.T) {
	t.Parallel()
	cfg := makeBaseCfg("DEV:1", "S", hmenum.ParameterTypeString, hmenum.OperationsWrite)
	dp := resolveWritable(cfg, "S", cfg.Descriptor)
	if dp == nil {
		t.Fatal("write-only string must resolve")
	}
}

func TestResolveWritableWriteOnlyFallback(t *testing.T) {
	t.Parallel()
	// Type DUMMY → falls through to generic Action.
	cfg := makeBaseCfg("DEV:1", "X", hmenum.ParameterTypeDummy, hmenum.OperationsWrite)
	dp := resolveWritable(cfg, "X", cfg.Descriptor)
	if dp == nil {
		t.Fatal("write-only unknown type must resolve to Action")
	}
}

func TestResolveWritableReadWriteBool(t *testing.T) {
	t.Parallel()
	cfg := makeBaseCfg("DEV:1", "RW", hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsWrite)
	dp := resolveWritable(cfg, "RW", cfg.Descriptor)
	if dp == nil {
		t.Fatal("read+write bool must resolve to Switch")
	}
}

func TestResolveWritableReadWriteEnum(t *testing.T) {
	t.Parallel()
	cfg := makeBaseCfg("DEV:1", "E", hmenum.ParameterTypeEnum, hmenum.OperationsRead|hmenum.OperationsWrite)
	dp := resolveWritable(cfg, "E", cfg.Descriptor)
	if dp == nil {
		t.Fatal("read+write enum must resolve to Select")
	}
}

func TestResolveWritableReadWriteFloat(t *testing.T) {
	t.Parallel()
	cfg := makeBaseCfg("DEV:1", "F", hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsWrite)
	dp := resolveWritable(cfg, "F", cfg.Descriptor)
	if dp == nil {
		t.Fatal("read+write float must resolve")
	}
}

func TestResolveWritableReadWriteInteger(t *testing.T) {
	t.Parallel()
	cfg := makeBaseCfg("DEV:1", "I", hmenum.ParameterTypeInteger, hmenum.OperationsRead|hmenum.OperationsWrite)
	dp := resolveWritable(cfg, "I", cfg.Descriptor)
	if dp == nil {
		t.Fatal("read+write integer must resolve")
	}
}

func TestResolveWritableReadWriteString(t *testing.T) {
	t.Parallel()
	cfg := makeBaseCfg("DEV:1", "S", hmenum.ParameterTypeString, hmenum.OperationsRead|hmenum.OperationsWrite)
	dp := resolveWritable(cfg, "S", cfg.Descriptor)
	if dp == nil {
		t.Fatal("read+write string must resolve")
	}
}

func TestResolveReadonlyClickEvent(t *testing.T) {
	t.Parallel()
	cfg := makeBaseCfg("DEV:1", string(hmenum.ParameterPressShort), hmenum.ParameterTypeAction, hmenum.OperationsEvent)
	dp := resolveReadonly(cfg, hmenum.ParameterPressShort, cfg.Descriptor)
	if dp == nil {
		t.Fatal("click event readonly must resolve to Button")
	}
}

func TestResolveReadonlyBinarySensor(t *testing.T) {
	t.Parallel()
	cfg := makeBaseCfg("DEV:1", "STATE", hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsEvent)
	dp := resolveReadonly(cfg, "STATE", cfg.Descriptor)
	if dp == nil {
		t.Fatal("bool readonly must resolve to BinarySensor")
	}
}

func TestResolveReadonlyFloat(t *testing.T) {
	t.Parallel()
	cfg := makeBaseCfg("DEV:1", "TEMP", hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsEvent)
	dp := resolveReadonly(cfg, "TEMP", cfg.Descriptor)
	if dp == nil {
		t.Fatal("float readonly must resolve to FloatSensor")
	}
}

func TestResolveReadonlyInteger(t *testing.T) {
	t.Parallel()
	cfg := makeBaseCfg("DEV:1", "COUNT", hmenum.ParameterTypeInteger, hmenum.OperationsRead|hmenum.OperationsEvent)
	dp := resolveReadonly(cfg, "COUNT", cfg.Descriptor)
	if dp == nil {
		t.Fatal("integer readonly must resolve to IntegerSensor")
	}
}

func TestResolveReadonlyString(t *testing.T) {
	t.Parallel()
	cfg := makeBaseCfg("DEV:1", "INFO", hmenum.ParameterTypeString, hmenum.OperationsRead|hmenum.OperationsEvent)
	dp := resolveReadonly(cfg, "INFO", cfg.Descriptor)
	if dp == nil {
		t.Fatal("string readonly must resolve to StringSensor")
	}
}

func TestResolveReadonlyUnknownType(t *testing.T) {
	t.Parallel()
	cfg := makeBaseCfg("DEV:1", "DUMMY", hmenum.ParameterTypeDummy, hmenum.OperationsRead|hmenum.OperationsEvent)
	dp := resolveReadonly(cfg, "DUMMY", cfg.Descriptor)
	// DUMMY type falls through to nil — no DP created.
	if dp != nil {
		t.Error("DUMMY type readonly must resolve to nil")
	}
}
