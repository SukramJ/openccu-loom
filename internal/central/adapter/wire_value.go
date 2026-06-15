// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ParamValueFromWire collapses a typed XML-RPC value into a
// [hmtypes.ParamValue] so downstream consumers don't have to handle
// the sum type. Lives in the adapter layer, which owns the xmlrpc
// import; the domain EventCoordinator receives a [hmtypes.ParamValue]
// directly and remains free of transport dependencies.
func ParamValueFromWire(v xmlrpc.Value) hmtypes.ParamValue {
	switch x := v.(type) {
	case nil, xmlrpc.NilValue:
		return hmtypes.NoneValue()
	case xmlrpc.BoolValue:
		return hmtypes.BoolValue(bool(x))
	case xmlrpc.IntValue:
		return hmtypes.IntValue(int(x))
	case xmlrpc.DoubleValue:
		return hmtypes.FloatValue(float64(x))
	case xmlrpc.StringValue:
		return hmtypes.StringValue(string(x))
	case xmlrpc.ArrayValue:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(xmlrpc.StringValue); ok {
				out = append(out, string(s))
			}
		}
		return hmtypes.ListValue(out)
	default:
		return hmtypes.NoneValue()
	}
}
