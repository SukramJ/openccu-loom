// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package backends

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/tests/harness/litefake"
)

// liteReadOnlyToken is a token that carries rpc:read and nothing else.
const liteReadOnlyToken = "olt_readonly0123456789abcdef012345"

// liteXMLRPCCaller bridges Loom's xmlrpc.Client to [Caller] with the
// value conversion the production XML-RPC caller in
// internal/central/adapter applies (plain Go scalars, []any and
// map[string]any in; the same flattened shapes out). That caller is
// unexported in a package that imports this one, so the conversion is
// restated here for the argument shapes LiteBackend sends.
type liteXMLRPCCaller struct{ client *xmlrpc.Client }

func (c *liteXMLRPCCaller) Call(ctx context.Context, method string, args ...any) (any, error) {
	params := make([]xmlrpc.Value, 0, len(args))
	for _, a := range args {
		v, err := liteGoToXMLRPC(a)
		if err != nil {
			return nil, err
		}
		params = append(params, v)
	}
	reply, err := c.client.Call(ctx, method, params)
	if err != nil {
		return nil, err
	}
	return liteXMLRPCToGo(reply), nil
}

func (c *liteXMLRPCCaller) CallAt(ctx context.Context, _ hmenum.CommandPriority, method string, args ...any) (any, error) {
	return c.Call(ctx, method, args...)
}

func liteGoToXMLRPC(v any) (xmlrpc.Value, error) {
	switch x := v.(type) {
	case nil:
		return xmlrpc.NilValue{}, nil
	case string:
		return xmlrpc.StringValue(x), nil
	case int:
		return xmlrpc.IntValue(int32(x)), nil //nolint:gosec // test values are small
	case bool:
		return xmlrpc.BoolValue(x), nil
	case float64:
		return xmlrpc.DoubleValue(x), nil
	case []any:
		out := make(xmlrpc.ArrayValue, 0, len(x))
		for _, e := range x {
			ev, err := liteGoToXMLRPC(e)
			if err != nil {
				return nil, err
			}
			out = append(out, ev)
		}
		return out, nil
	case map[string]any:
		members := make([]xmlrpc.Member, 0, len(x))
		for k, e := range x {
			ev, err := liteGoToXMLRPC(e)
			if err != nil {
				return nil, err
			}
			members = append(members, xmlrpc.Member{Name: k, Value: ev})
		}
		return xmlrpc.StructValue{Members: members}, nil
	}
	return nil, fmt.Errorf("unsupported arg %T", v)
}

func liteXMLRPCToGo(v xmlrpc.Value) any {
	switch x := v.(type) {
	case xmlrpc.StringValue:
		return string(x)
	case xmlrpc.IntValue:
		return int(x)
	case xmlrpc.BoolValue:
		return bool(x)
	case xmlrpc.DoubleValue:
		return float64(x)
	case xmlrpc.Base64Value:
		return base64.StdEncoding.EncodeToString([]byte(x))
	case xmlrpc.ArrayValue:
		out := make([]any, 0, len(x))
		for _, e := range x {
			out = append(out, liteXMLRPCToGo(e))
		}
		return out
	case xmlrpc.StructValue:
		out := make(map[string]any, len(x.Members))
		for _, m := range x.Members {
			out[m.Name] = liteXMLRPCToGo(m.Value)
		}
		return out
	}
	return nil
}

// startLiteFake starts a fake box with a wildcard token and a
// read-only token.
func startLiteFake(t *testing.T) *litefake.Fake {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f, err := litefake.Start(ctx, litefake.Options{Tokens: map[string][]string{
		litefake.DefaultToken: {"*"},
		liteReadOnlyToken:     {"rpc:read"},
	}})
	if err != nil {
		t.Fatalf("litefake.Start: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// liteBackendOn builds a LiteBackend for iface against the fake's proxy,
// authenticating with token.
func liteBackendOn(t *testing.T, f *litefake.Fake, iface hmenum.Interface, token string) *LiteBackend {
	t.Helper()
	c, err := xmlrpc.NewClient(xmlrpc.Config{
		URL:      f.URL() + "/api/rpc/v1/xmlrpc/" + string(iface),
		Username: "x",
		Password: token,
	})
	if err != nil {
		t.Fatal(err)
	}
	return NewLiteBackend(iface, &liteXMLRPCCaller{client: c}, nil)
}

// lastProxyCall returns the decoded XML-RPC request of the most recent
// proxy call to method on iface, as the fake recorded it.
func lastProxyCall(t *testing.T, f *litefake.Fake, iface hmenum.Interface, method string) *xmlrpc.MethodCall {
	t.Helper()
	calls := f.Calls()
	for i := len(calls) - 1; i >= 0; i-- {
		c := calls[i]
		if c.Path != "/api/rpc/v1/xmlrpc/"+string(iface) || len(c.RPCMethods) != 1 || c.RPCMethods[0] != method {
			continue
		}
		mc, err := xmlrpc.DecodeCall(bytes.NewReader(c.Body))
		if err != nil {
			t.Fatalf("decode recorded %s: %v", method, err)
		}
		return mc
	}
	t.Fatalf("the fake recorded no %s call on %s", method, iface)
	return nil
}

// paramKinds renders each parameter's XML-RPC type, so a test can pin the
// argument shape.
func paramKinds(params []xmlrpc.Value) string {
	kinds := make([]string, 0, len(params))
	for _, p := range params {
		switch p.(type) {
		case xmlrpc.StringValue:
			kinds = append(kinds, "string")
		case xmlrpc.IntValue:
			kinds = append(kinds, "int")
		case xmlrpc.BoolValue:
			kinds = append(kinds, "bool")
		case xmlrpc.ArrayValue:
			kinds = append(kinds, "array")
		case xmlrpc.StructValue:
			kinds = append(kinds, "struct")
		default:
			kinds = append(kinds, fmt.Sprintf("%T", p))
		}
	}
	return strings.Join(kinds, ",")
}

func mustString(t *testing.T, v xmlrpc.Value) string {
	t.Helper()
	s, err := xmlrpc.AsString(v)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestLiteBackendMapsTierFaultToScopeMissing drives setValue with a token
// that carries only rpc:read through the fake's proxy: the box answers
// the tier refusal as a fault, and the backend must surface it as a
// ScopeMissingError naming rpc:operate.
func TestLiteBackendMapsTierFaultToScopeMissing(t *testing.T) {
	t.Parallel()
	f := startLiteFake(t)
	ctx := context.Background()
	b := liteBackendOn(t, f, hmenum.InterfaceHmIPRF, liteReadOnlyToken)

	// Positive control: the token authenticates, so the refusal below is
	// the tier and nothing else.
	if _, err := b.ListDevices(ctx); err != nil {
		t.Fatalf("ListDevices with an rpc:read token: %v", err)
	}

	err := b.SetValue(ctx, "0001D3C99C3C93:1", "LEVEL", 0.5, hmenum.CommandPriorityLow, hmenum.CommandRxModeUnset)
	var sm *hmerr.ScopeMissingError
	if !errors.As(err, &sm) {
		t.Fatalf("setValue with an rpc:read token: err = %T %v, want *hmerr.ScopeMissingError", err, err)
	}
	if sm.Scope != "rpc:operate" {
		t.Errorf("scope = %q, want rpc:operate", sm.Scope)
	}
	if sm.Operation != "setValue" {
		t.Errorf("operation = %q, want setValue", sm.Operation)
	}
	if !errors.Is(err, hmerr.ErrScopeMissing) {
		t.Error("errors.Is(err, hmerr.ErrScopeMissing) = false")
	}
	mc := lastProxyCall(t, f, hmenum.InterfaceHmIPRF, "setValue")
	if got := paramKinds(mc.Params); got != "string,string,xmlrpc.DoubleValue" {
		t.Errorf("setValue params = %s, want string,string,double", got)
	}
}

// TestLiteBackendClassBWireShapes runs every class-b operation against
// the fake box and pins the XML-RPC method and argument shape the fake
// recorded, plus the decoded answer where the interface process gives one.
// The subtests run in order on one fake (the link created first is read
// back later), so neither they nor the parent run in parallel.
func TestLiteBackendClassBWireShapes(t *testing.T) {
	f := startLiteFake(t)
	ctx := context.Background()
	hmip := liteBackendOn(t, f, hmenum.InterfaceHmIPRF, litefake.DefaultToken)
	bidcos := liteBackendOn(t, f, hmenum.InterfaceBidCosRF, litefake.DefaultToken)

	devices, err := hmip.ListDevices(ctx)
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	var channels []string
	for i := range devices {
		if devices[i].Parent != "" {
			channels = append(channels, devices[i].Address)
		}
	}
	if len(channels) < 2 {
		t.Fatalf("need two HmIP channels for a link, got %v", channels)
	}
	sender, receiver := channels[0], channels[1]

	t.Run("SetLinkInfo+GetLinkInfo", func(t *testing.T) {
		if err := hmip.AddLink(ctx, sender, receiver, "", ""); err != nil {
			t.Fatalf("AddLink: %v", err)
		}
		ok, err := hmip.SetLinkInfo(ctx, "HmIP-RF", sender, receiver, "Flur", "Taster zu Licht")
		if err != nil || !ok {
			t.Fatalf("SetLinkInfo: ok=%v err=%v", ok, err)
		}
		mc := lastProxyCall(t, f, hmenum.InterfaceHmIPRF, "setLinkInfo")
		if got := paramKinds(mc.Params); got != "string,string,string,string" {
			t.Errorf("setLinkInfo params = %s", got)
		}
		if mustString(t, mc.Params[0]) != sender || mustString(t, mc.Params[1]) != receiver ||
			mustString(t, mc.Params[2]) != "Flur" || mustString(t, mc.Params[3]) != "Taster zu Licht" {
			t.Errorf("setLinkInfo args = %v", mc.Params)
		}

		info, err := hmip.GetLinkInfo(ctx, "HmIP-RF", sender, receiver)
		if err != nil {
			t.Fatalf("GetLinkInfo: %v", err)
		}
		if info["name"] != "Flur" || info["description"] != "Taster zu Licht" {
			t.Errorf("GetLinkInfo = %v, want name/description round-tripped", info)
		}
		mc = lastProxyCall(t, f, hmenum.InterfaceHmIPRF, "getLinkInfo")
		if got := paramKinds(mc.Params); got != "string,string" {
			t.Errorf("getLinkInfo params = %s", got)
		}
	})

	t.Run("GetInstallMode", func(t *testing.T) {
		if _, err := hmip.GetInstallMode(ctx); err != nil {
			t.Fatalf("GetInstallMode: %v", err)
		}
		mc := lastProxyCall(t, f, hmenum.InterfaceHmIPRF, "getInstallMode")
		if len(mc.Params) != 0 {
			t.Errorf("getInstallMode params = %s, want none", paramKinds(mc.Params))
		}
	})

	t.Run("SetInstallMode/HmIP-RF", func(t *testing.T) {
		if err := hmip.SetInstallMode(ctx, true, 60, 1, "ignored"); err != nil {
			t.Fatalf("SetInstallMode: %v", err)
		}
		mc := lastProxyCall(t, f, hmenum.InterfaceHmIPRF, "setInstallMode")
		if got := paramKinds(mc.Params); got != "bool,int" {
			t.Errorf("HmIP-RF setInstallMode params = %s, want bool,int", got)
		}
	})

	t.Run("SetInstallMode/BidCos-RF", func(t *testing.T) {
		if err := bidcos.SetInstallMode(ctx, true, 60, 2, ""); err != nil {
			t.Fatalf("SetInstallMode: %v", err)
		}
		mc := lastProxyCall(t, f, hmenum.InterfaceBidCosRF, "setInstallMode")
		if got := paramKinds(mc.Params); got != "bool,int,int" {
			t.Errorf("BidCos-RF setInstallMode params = %s, want bool,int,int", got)
		}
		if err := bidcos.SetInstallMode(ctx, true, 60, 1, "ABC0000001"); err != nil {
			t.Fatalf("SetInstallMode with address: %v", err)
		}
		mc = lastProxyCall(t, f, hmenum.InterfaceBidCosRF, "setInstallMode")
		if got := paramKinds(mc.Params); got != "bool,int,string" {
			t.Errorf("BidCos-RF setInstallMode(address) params = %s, want bool,int,string", got)
		}
	})

	t.Run("SetInstallModeLocal", func(t *testing.T) {
		// The simulated interface process has no whitelist teach-in, so
		// the forward answers a fault; the request shape is what counts.
		_ = hmip.SetInstallModeLocal(ctx, 120, "3014F711A0000000000000AB", "00112233445566778899AABBCCDDEEFF")
		mc := lastProxyCall(t, f, hmenum.InterfaceHmIPRF, "setInstallModeWithWhitelist")
		if got := paramKinds(mc.Params); got != "bool,int,array" {
			t.Fatalf("setInstallModeWithWhitelist params = %s, want bool,int,array", got)
		}
		on, _ := xmlrpc.AsBool(mc.Params[0])
		secs, _ := xmlrpc.AsInt(mc.Params[1])
		arr, _ := xmlrpc.AsArray(mc.Params[2])
		if !on || secs != 120 || len(arr) != 1 {
			t.Fatalf("setInstallModeWithWhitelist args = %v", mc.Params)
		}
		for field, want := range map[string]string{
			"ADDRESS": "3014F711A0000000000000AB", "KEY": "00112233445566778899AABBCCDDEEFF", "KEY_MODE": "LOCAL",
		} {
			got, err := xmlrpc.StructField[xmlrpc.StringValue](arr[0], field)
			if err != nil || string(got) != want {
				t.Errorf("whitelist %s = %q (%v), want %q", field, got, err, want)
			}
		}
	})

	t.Run("SuppressServiceMessage+GetSuppressedServiceMessages", func(t *testing.T) {
		if err := hmip.SuppressServiceMessage(ctx, sender, "LOW_BAT", true); err != nil {
			t.Fatalf("SuppressServiceMessage: %v", err)
		}
		mc := lastProxyCall(t, f, hmenum.InterfaceHmIPRF, "suppressServiceMessages")
		if got := paramKinds(mc.Params); got != "string,string,bool" {
			t.Errorf("suppressServiceMessages params = %s, want string,string,bool", got)
		}
		got, err := hmip.GetSuppressedServiceMessages(ctx, "HmIP-RF", sender)
		if err != nil {
			t.Fatalf("GetSuppressedServiceMessages: %v", err)
		}
		if len(got) != 1 || got[0] != "LOW_BAT" {
			t.Errorf("GetSuppressedServiceMessages = %v, want [LOW_BAT]", got)
		}
		mc = lastProxyCall(t, f, hmenum.InterfaceHmIPRF, "getSuppressedServiceMessages")
		if kinds := paramKinds(mc.Params); kinds != "string" {
			t.Errorf("getSuppressedServiceMessages params = %s, want string", kinds)
		}
	})

	t.Run("ListBidcosInterfaces", func(t *testing.T) {
		gws, err := bidcos.ListBidcosInterfaces(ctx, "BidCos-RF")
		if err != nil {
			t.Fatalf("ListBidcosInterfaces: %v", err)
		}
		if len(gws) == 0 {
			t.Fatal("no gateway reported")
		}
		for _, key := range []string{"address", "description", "dutyCycle", "isConnected", "isDefault", "fwVersion", "type"} {
			if _, ok := gws[0][key]; !ok || gws[0][key] == nil {
				t.Errorf("gateway lacks %q: %v", key, gws[0])
			}
		}
		mc := lastProxyCall(t, f, hmenum.InterfaceBidCosRF, "listBidcosInterfaces")
		if len(mc.Params) != 0 {
			t.Errorf("listBidcosInterfaces params = %s, want none", paramKinds(mc.Params))
		}
	})
}
