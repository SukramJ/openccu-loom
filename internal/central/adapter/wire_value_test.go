// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Tests the xmlrpc-wire → hmtypes.ParamValue conversion the adapter performs on
// behalf of the (now transport-agnostic) EventCoordinator.
package adapter

import (
	"reflect"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

func TestParamValueFromWire(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   xmlrpc.Value
		want hmtypes.ParamValue
	}{
		{"nil interface", nil, hmtypes.NoneValue()},
		{"NilValue", xmlrpc.NilValue{}, hmtypes.NoneValue()},
		{"bool", xmlrpc.BoolValue(true), hmtypes.BoolValue(true)},
		{"int", xmlrpc.IntValue(7), hmtypes.IntValue(7)},
		{"double maps to float", xmlrpc.DoubleValue(1.5), hmtypes.FloatValue(1.5)},
		{"string", xmlrpc.StringValue("hi"), hmtypes.StringValue("hi")},
		{
			"array keeps only the string members",
			xmlrpc.ArrayValue{xmlrpc.StringValue("a"), xmlrpc.IntValue(42), xmlrpc.StringValue("b")},
			hmtypes.ListValue([]string{"a", "b"}),
		},
		{"unknown wire type maps to none", xmlrpc.DateTimeValue(time.Now()), hmtypes.NoneValue()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ParamValueFromWire(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParamValueFromWire(%v) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}
