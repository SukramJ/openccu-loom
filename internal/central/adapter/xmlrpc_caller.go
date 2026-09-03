// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// toXMLRPCInt32 narrows a Go integer to the int32 range XML-RPC `<i4>`
// can carry, clamping out-of-range inputs to the type bounds rather than
// silently wrapping. CCU parameters are well within int32; the clamp is
// a defensive guard against an overflowing conversion.
func toXMLRPCInt32(n int64) int32 {
	switch {
	case n > math.MaxInt32:
		return math.MaxInt32
	case n < math.MinInt32:
		return math.MinInt32
	default:
		return int32(n)
	}
}

// xmlrpcCaller bridges the XML-RPC transport client to the
// [backends.Caller] interface expected by the backend factory. It
// normalises Go scalars/slices into xmlrpc.Value, calls the wire, and
// flattens the reply back into plain Go types so backend wrappers can
// reflect over maps/slices/scalars without knowing the xmlrpc.Value
// hierarchy.
//
// The type intentionally supports only the argument shapes that the
// CCU backends actually send (strings, ints, bools, floats, []string,
// maps of the same). If future backends need richer encodings, extend
// goToXMLRPCValue.
type xmlrpcCaller struct{ client *xmlrpc.Client }

func (c *xmlrpcCaller) Call(ctx context.Context, method string, args ...any) (any, error) {
	reply, err := c.callRaw(ctx, method, args)
	if err != nil {
		return nil, err
	}
	return xmlRPCValueToGo(reply), nil
}

// callRaw encodes the Go args into xmlrpc.Value params and returns the
// decoded reply Value untouched, so callers can flatten it to plain Go
// (Call) or preserve member order (CallOrdered).
func (c *xmlrpcCaller) callRaw(ctx context.Context, method string, args []any) (xmlrpc.Value, error) {
	params := make([]xmlrpc.Value, 0, len(args))
	for _, arg := range args {
		v, err := goToXMLRPCValue(arg)
		if err != nil {
			return nil, fmt.Errorf("arg to xmlrpc: %w", err)
		}
		params = append(params, v)
	}
	return c.client.Call(ctx, method, params)
}

func goToXMLRPCValue(v any) (xmlrpc.Value, error) {
	switch x := v.(type) {
	case nil:
		return xmlrpc.NilValue{}, nil
	case string:
		return xmlrpc.StringValue(x), nil
	case int:
		return xmlrpc.IntValue(toXMLRPCInt32(int64(x))), nil
	case int32:
		return xmlrpc.IntValue(x), nil
	case int64:
		return xmlrpc.IntValue(toXMLRPCInt32(x)), nil
	case bool:
		return xmlrpc.BoolValue(x), nil
	case float64:
		return xmlrpc.DoubleValue(x), nil
	case float32:
		return xmlrpc.DoubleValue(x), nil
	case []string:
		out := make(xmlrpc.ArrayValue, 0, len(x))
		for _, s := range x {
			out = append(out, xmlrpc.StringValue(s))
		}
		return out, nil
	case []any:
		out := make(xmlrpc.ArrayValue, 0, len(x))
		for _, e := range x {
			ev, err := goToXMLRPCValue(e)
			if err != nil {
				return nil, err
			}
			out = append(out, ev)
		}
		return out, nil
	case map[string]any:
		members := make([]xmlrpc.Member, 0, len(x))
		for k, val := range x {
			mv, err := goToXMLRPCValue(val)
			if err != nil {
				return nil, err
			}
			members = append(members, xmlrpc.Member{Name: k, Value: mv})
		}
		return xmlrpc.StructValue{Members: members}, nil
	}
	return nil, fmt.Errorf("unsupported arg %T", v)
}

func xmlRPCValueToGo(v xmlrpc.Value) any {
	switch x := v.(type) {
	case xmlrpc.NilValue:
		return nil
	case xmlrpc.StringValue:
		return string(x)
	case xmlrpc.IntValue:
		return int(x)
	case xmlrpc.BoolValue:
		return bool(x)
	case xmlrpc.DoubleValue:
		return float64(x)
	case xmlrpc.DateTimeValue:
		return x.Time().Format(xmlrpc.ISO8601CompactLayout)
	case xmlrpc.Base64Value:
		return base64.StdEncoding.EncodeToString([]byte(x))
	case xmlrpc.ArrayValue:
		out := make([]any, 0, len(x))
		for _, e := range x {
			out = append(out, xmlRPCValueToGo(e))
		}
		return out
	case xmlrpc.StructValue:
		out := make(map[string]any, len(x.Members))
		for _, m := range x.Members {
			out[m.Name] = xmlRPCValueToGo(m.Value)
		}
		return out
	}
	return nil
}
