// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/tests/harness/litefake"
)

func TestParseTierFault(t *testing.T) {
	for _, tc := range []struct {
		msg, method, scope string
		ok                 bool
	}{
		{"not permitted: setValue needs rpc:operate", "setValue", "rpc:operate", true},
		{"not permitted: system.multicall needs rpc:admin", "system.multicall", "rpc:admin", true},
		{"not permitted: setValue needs power", "", "", false},
		{"not permitted: needs rpc:read", "", "", false},
		{"forbidden: setValue requires rpc:operate", "", "", false},
		{"not permitted: set Value needs rpc:operate", "", "", false},
	} {
		m, s, ok := occulited.ParseTierFault(tc.msg)
		if m != tc.method || s != tc.scope || ok != tc.ok {
			t.Errorf("%q → %q %q %v", tc.msg, m, s, ok)
		}
	}
}

func TestClassifyFaultLeavesOtherErrorsAlone(t *testing.T) {
	plain := errors.New("dial tcp: refused")
	if got := occulited.ClassifyFault(plain); got != plain { //nolint:errorlint // identity is the assertion
		t.Errorf("plain error rewritten: %v", got)
	}
	other := fmt.Errorf("wrapped: %w", &hmerr.XMLRPCFault{Code: -2, Message: "Invalid device"})
	if got := occulited.ClassifyFault(other); got != other { //nolint:errorlint // identity is the assertion
		t.Errorf("daemon fault rewritten: %v", got)
	}
	if occulited.ClassifyFault(nil) != nil {
		t.Error("nil rewritten")
	}
	if occulited.IsInitRefusal(plain) {
		t.Error("plain error is an init refusal")
	}
}

// TestInitRefusalIsRecognised drives init through the proxy, alone and
// inside a multicall.
func TestInitRefusalIsRecognised(t *testing.T) {
	f := startFake(t, litefake.Options{})
	c := newClient(t, f.URL(), litefake.DefaultToken)
	x, err := xmlrpc.NewClient(xmlrpc.Config{URL: c.XMLRPCURL(hmipRF), HTTPClient: c.HTTPClient()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, err = x.Call(ctx, "init", []xmlrpc.Value{xmlrpc.StringValue("http://127.0.0.1:1/"), xmlrpc.StringValue("loom")})
	if !occulited.IsInitRefusal(err) || !errors.Is(occulited.ClassifyFault(err), occulited.ErrInitRefused) {
		t.Errorf("init: %v", err)
	}
	inner := xmlrpc.StructValue{Members: []xmlrpc.Member{
		{Name: "methodName", Value: xmlrpc.StringValue("init")},
		{Name: "params", Value: xmlrpc.ArrayValue{xmlrpc.StringValue("http://127.0.0.1:1/")}},
	}}
	_, err = x.Call(ctx, "system.multicall", []xmlrpc.Value{xmlrpc.ArrayValue{inner}})
	if !occulited.IsInitRefusal(err) {
		t.Errorf("multicall init: %v", err)
	}
}
