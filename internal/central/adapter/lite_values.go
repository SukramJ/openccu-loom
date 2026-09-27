// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"bytes"
	"encoding/json"
	"math"
	"sort"
	"strconv"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// typedValue rebuilds the XML-RPC value of an event the lite stream
// delivered as JSON. The stream loses the XML-RPC type — a double 1.0
// arrives as 1 — and the value cache compares values by kind, so a FLOAT
// delivered as an integer would publish a change that did not happen and
// flip the north-bound value type. The parameter's VALUES description
// (known false when there is none: PONG, a parameter not yet hydrated)
// supplies the type; without it the JSON value keeps its natural type.
func typedValue(pd hmproto.ParameterData, known bool, raw json.RawMessage) xmlrpc.Value {
	v, ok := decodeNumberJSON(raw)
	if !ok {
		return xmlrpc.NilValue{}
	}
	if !known {
		return nativeValue(v)
	}
	switch pd.Type {
	case hmenum.ParameterTypeFloat:
		if f, ok := asFloat(v); ok {
			return xmlrpc.DoubleValue(f)
		}
	case hmenum.ParameterTypeInteger, hmenum.ParameterTypeEnum:
		if f, ok := asFloat(v); ok {
			return xmlrpc.IntValue(clampInt32(math.Round(f)))
		}
	case hmenum.ParameterTypeBool, hmenum.ParameterTypeAction:
		switch x := v.(type) {
		case bool:
			return xmlrpc.BoolValue(x)
		case json.Number:
			if f, err := x.Float64(); err == nil {
				return xmlrpc.BoolValue(f != 0)
			}
		}
	case hmenum.ParameterTypeString:
		switch x := v.(type) {
		case string:
			return xmlrpc.StringValue(x)
		case json.Number:
			if f, err := x.Float64(); err == nil {
				return xmlrpc.StringValue(strconv.FormatFloat(f, 'f', -1, 64))
			}
		}
	default:
		// DUMMY and an empty type describe nothing to convert to.
	}
	// A value that does not fit the described type is passed on as it
	// came; the data point's own coercion decides what to make of it.
	return nativeValue(v)
}

// decodeNumberJSON decodes one JSON value keeping numbers as json.Number,
// so an integral value is not silently a float.
func decodeNumberJSON(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any // wire-decoded JSON before type-dispatch
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	return v, true
}

// nativeValue types a JSON value by its own shape: an integral number that
// fits int32 is an integer, any other number a double.
func nativeValue(v any) xmlrpc.Value {
	switch x := v.(type) {
	case nil:
		return xmlrpc.NilValue{}
	case bool:
		return xmlrpc.BoolValue(x)
	case string:
		return xmlrpc.StringValue(x)
	case json.Number:
		if i, err := x.Int64(); err == nil && i >= math.MinInt32 && i <= math.MaxInt32 {
			return xmlrpc.IntValue(int32(i))
		}
		if f, err := x.Float64(); err == nil {
			return xmlrpc.DoubleValue(f)
		}
		return xmlrpc.StringValue(x.String())
	case []any:
		out := make(xmlrpc.ArrayValue, 0, len(x))
		for _, e := range x {
			out = append(out, nativeValue(e))
		}
		return out
	case map[string]any:
		names := make([]string, 0, len(x))
		for k := range x {
			names = append(names, k)
		}
		sort.Strings(names)
		st := xmlrpc.StructValue{Members: make([]xmlrpc.Member, 0, len(x))}
		for _, k := range names {
			st.Members = append(st.Members, xmlrpc.Member{Name: k, Value: nativeValue(x[k])})
		}
		return st
	}
	return xmlrpc.NilValue{}
}

// asFloat reads a JSON number (or a boolean as 0/1) as a float.
func asFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

// clampInt32 bounds a rounded float to the XML-RPC integer range.
func clampInt32(f float64) int32 {
	switch {
	case f > math.MaxInt32:
		return math.MaxInt32
	case f < math.MinInt32:
		return math.MinInt32
	}
	return int32(f)
}
