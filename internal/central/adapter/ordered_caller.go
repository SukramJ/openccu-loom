// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"encoding/base64"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/internal/orderedjson"
)

// xmlRPCValueToOrdered converts a decoded xmlrpc.Value into the
// order-preserving [orderedjson] model. Unlike [xmlRPCValueToGo] it keeps
// struct member order, which the device-definition export needs to reproduce
// the Python reference's byte-for-byte wire-ordered output. BIN-RPC shares the
// xmlrpc.Value hierarchy, so the same converter serves both transports.
//
// Scalars map to the narrowest type the [orderedjson] encoder formats like
// orjson: integers to int64, doubles to float64 (orjson float repr), strings
// verbatim. dateTime/base64 do not occur in device or paramset descriptions;
// they degrade to their textual forms defensively rather than failing.
func xmlRPCValueToOrdered(v xmlrpc.Value) any {
	switch x := v.(type) {
	case xmlrpc.NilValue:
		return nil
	case xmlrpc.StringValue:
		return string(x)
	case xmlrpc.IntValue:
		return int64(x)
	case xmlrpc.BoolValue:
		return bool(x)
	case xmlrpc.DoubleValue:
		return float64(x)
	case xmlrpc.DateTimeValue:
		return x.Time().Format(xmlrpc.ISO8601CompactLayout)
	case xmlrpc.Base64Value:
		return base64.StdEncoding.EncodeToString([]byte(x))
	case xmlrpc.ArrayValue:
		out := make(orderedjson.Array, 0, len(x))
		for _, e := range x {
			out = append(out, xmlRPCValueToOrdered(e))
		}
		return out
	case xmlrpc.StructValue:
		out := orderedjson.NewObject(len(x.Members))
		for _, m := range x.Members {
			out.Set(m.Name, xmlRPCValueToOrdered(m.Value))
		}
		return out
	}
	return nil
}

// CallOrdered issues an XML-RPC call and returns the reply as an
// order-preserving [orderedjson] value. It mirrors [xmlrpcCaller.Call] but
// taps the decoded xmlrpc.Value before it is flattened into an unordered map.
func (c *xmlrpcCaller) CallOrdered(ctx context.Context, method string, args ...any) (any, error) {
	reply, err := c.callRaw(ctx, method, args)
	if err != nil {
		return nil, err
	}
	return xmlRPCValueToOrdered(reply), nil
}

// CallOrdered issues a BIN-RPC call and returns the reply as an
// order-preserving [orderedjson] value. CUxD speaks the xmlrpc.Value type
// set over a binary codec, so the xmlrpc converter applies unchanged.
func (c *binrpcCaller) CallOrdered(ctx context.Context, method string, args ...any) (any, error) {
	reply, err := c.callRaw(ctx, method, args)
	if err != nil {
		return nil, err
	}
	return xmlRPCValueToOrdered(reply), nil
}
