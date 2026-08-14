// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

package integration

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestDeviceGraphAgainstMockCCU exercises the daemon's ability
// to build its device graph from the paramsets a CCU returns.
func TestDeviceGraphAgainstMockCCU(t *testing.T) {
	srv := startMockCCU(t)

	xmlClient := newXMLRPCClient(t, srv.URL())
	caller := &xmlrpcBackendCaller{client: xmlClient}
	backend := backends.NewCcuBackend(caller, nil, nil)

	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central: %v", err)
	}
	pipeline := adapter.NewDevicePipeline(c)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := pipeline.IngestFromBackend(ctx, "HmIP-RF", hmenum.InterfaceHmIPRF, backend, nil, nil, logger); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if c.ModelRegistry.Len() == 0 {
		t.Fatal("expected at least one device instantiated from the mock CCU")
	}
	for _, d := range c.ModelRegistry.List() {
		if d.Address == "" || d.Model == "" {
			t.Fatalf("malformed device: %+v", d)
		}
		if len(d.Channels()) == 0 {
			t.Fatalf("device %s has no channels", d.Address)
		}
	}
}

// xmlrpcBackendCaller bridges the XMLRPC client to backends.Caller.
// It normalises the returned XML-RPC Value into plain Go types so
// backend wrappers can reflect over maps/slices/scalars.
type xmlrpcBackendCaller struct{ client *xmlrpc.Client }

func (c *xmlrpcBackendCaller) Call(ctx context.Context, method string, args ...any) (any, error) {
	params := make([]xmlrpc.Value, 0, len(args))
	for _, arg := range args {
		v, err := goToXMLRPCValue(arg)
		if err != nil {
			return nil, fmt.Errorf("arg to xmlrpc: %w", err)
		}
		params = append(params, v)
	}
	reply, err := c.client.Call(ctx, method, params)
	if err != nil {
		return nil, err
	}
	return xmlRPCValueToGo(reply), nil
}

func goToXMLRPCValue(v any) (xmlrpc.Value, error) {
	switch x := v.(type) {
	case nil:
		return xmlrpc.NilValue{}, nil
	case string:
		return xmlrpc.StringValue(x), nil
	case int:
		return xmlrpc.IntValue(x), nil //nolint:gosec // test-scope range
	case int32:
		return xmlrpc.IntValue(x), nil
	case bool:
		return xmlrpc.BoolValue(x), nil
	case float64:
		return xmlrpc.DoubleValue(x), nil
	case []string:
		out := make(xmlrpc.ArrayValue, 0, len(x))
		for _, s := range x {
			out = append(out, xmlrpc.StringValue(s))
		}
		return out, nil
	case map[string]any:
		// PutParamset / SetParamset payloads arrive as a typed map.
		// XML-RPC encodes them as a <struct> with one <member> per
		// key. Member order is preserved by the receiver, which the
		// CCU relies on for paramset responses — we iterate in map
		// order which is not deterministic across Go versions, but
		// the CCU side accepts any order on writes.
		members := make([]xmlrpc.Member, 0, len(x))
		for k, val := range x {
			sub, err := goToXMLRPCValue(val)
			if err != nil {
				return nil, fmt.Errorf("struct member %q: %w", k, err)
			}
			members = append(members, xmlrpc.Member{Name: k, Value: sub})
		}
		return xmlrpc.StructValue{Members: members}, nil
	case []any:
		out := make(xmlrpc.ArrayValue, 0, len(x))
		for _, e := range x {
			sub, err := goToXMLRPCValue(e)
			if err != nil {
				return nil, err
			}
			out = append(out, sub)
		}
		return out, nil
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

// CallAt implements backends.Caller: this fake has no scheduler, so the
// priority is recorded by the caller's own assertions, not here.
func (c *xmlrpcBackendCaller) CallAt(
	ctx context.Context, _ hmenum.CommandPriority, method string, args ...any,
) (any, error) {
	return c.Call(ctx, method, args...)
}
