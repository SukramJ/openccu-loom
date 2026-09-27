// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// TestTypedValueUsesParamsetDescription pins the re-typing of stream
// values: the described TYPE decides the XML-RPC kind, so a FLOAT sent as
// JSON 1 is a double again (the cache compares by kind), and without a
// description the JSON value keeps its natural type.
func TestTypedValueUsesParamsetDescription(t *testing.T) {
	t.Parallel()
	pd := func(ty hmenum.ParameterType) hmproto.ParameterData { return hmproto.ParameterData{Type: ty} }
	cases := []struct {
		name  string
		pd    hmproto.ParameterData
		known bool
		raw   string
		want  xmlrpc.Value
	}{
		{"float from integral json", pd(hmenum.ParameterTypeFloat), true, `1`, xmlrpc.DoubleValue(1)},
		{"float", pd(hmenum.ParameterTypeFloat), true, `21.5`, xmlrpc.DoubleValue(21.5)},
		{"integer", pd(hmenum.ParameterTypeInteger), true, `3`, xmlrpc.IntValue(3)},
		{"integer rounds half away", pd(hmenum.ParameterTypeInteger), true, `2.5`, xmlrpc.IntValue(3)},
		{"enum", pd(hmenum.ParameterTypeEnum), true, `2`, xmlrpc.IntValue(2)},
		{"bool", pd(hmenum.ParameterTypeBool), true, `true`, xmlrpc.BoolValue(true)},
		{"bool from number", pd(hmenum.ParameterTypeBool), true, `0`, xmlrpc.BoolValue(false)},
		{"action", pd(hmenum.ParameterTypeAction), true, `true`, xmlrpc.BoolValue(true)},
		{"string", pd(hmenum.ParameterTypeString), true, `"x"`, xmlrpc.StringValue("x")},
		{"string from number", pd(hmenum.ParameterTypeString), true, `1.25`, xmlrpc.StringValue("1.25")},
		{"unknown integral", hmproto.ParameterData{}, false, `7`, xmlrpc.IntValue(7)},
		{"unknown fractional", hmproto.ParameterData{}, false, `7.5`, xmlrpc.DoubleValue(7.5)},
		{"unknown string", hmproto.ParameterData{}, false, `"a"`, xmlrpc.StringValue("a")},
		{"unknown null", hmproto.ParameterData{}, false, `null`, xmlrpc.NilValue{}},
		{"unknown array", hmproto.ParameterData{}, false, `[1,"a"]`, xmlrpc.ArrayValue{xmlrpc.IntValue(1), xmlrpc.StringValue("a")}},
		{"not json", pd(hmenum.ParameterTypeFloat), true, `{`, xmlrpc.NilValue{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := typedValue(tc.pd, tc.known, json.RawMessage(tc.raw))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("typedValue(%s, %v, %s) = %#v, want %#v", tc.pd.Type, tc.known, tc.raw, got, tc.want)
			}
		})
	}
}
