// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/godevccu/pkg/litefake"

	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

const liteDimmer = "VCU2128127" // the fake's HmIP-BSM on HmIP-RF

func stickyUnreach(since time.Time) []litefake.ServiceMessage {
	return []litefake.ServiceMessage{{
		Interface: "HmIP-RF", Address: liteDimmer, Channel: "0", Key: "STICKY_UNREACH",
		Value: json.RawMessage("true"), Since: since.Format(time.RFC3339), Seen: "event",
	}}
}

// TestLiteServiceMessagesMapToHubModel pins the service-message refresh of
// a lite central: the box's messages reach the hub model with the channel
// address, the parameter, the interface and the type the key implies.
func TestLiteServiceMessagesMapToHubModel(t *testing.T) {
	fake := startFake(t, litefake.Options{})
	since := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	fake.SetServiceMessages(stickyUnreach(since))
	c := startLiteSystemCentral(t, fake, litefake.DefaultToken)
	if err := c.unit.Hub.RefreshServiceMessages(context.Background()); err != nil {
		t.Fatalf("RefreshServiceMessages: %v", err)
	}
	msgs := c.unit.HubModel.ServiceMessages.List()
	if len(msgs) != 1 {
		t.Fatalf("service messages = %+v, want one", msgs)
	}
	m := msgs[0]
	if m.Address != liteDimmer+":0" || m.Parameter != "STICKY_UNREACH" || m.InterfaceID != "HmIP-RF" ||
		m.Type != hmenum.ServiceMessageTypeSticky || !m.Timestamp.Equal(since) || m.Quittable {
		t.Errorf("service message = %+v", m)
	}
}

// TestLiteServiceMessagesCannotBeAcknowledged pins the refusal of an
// acknowledge on a lite central: the box has no acknowledge, and the
// refusal names why while matching the error callers branch on today.
func TestLiteServiceMessagesCannotBeAcknowledged(t *testing.T) {
	fake := startFake(t, litefake.Options{})
	c := startLiteSystemCentral(t, fake, litefake.DefaultToken)
	err := c.unit.HubModel.ServiceMessages.Acknowledge(context.Background(), "HmIP-RF."+liteDimmer+":0.STICKY_UNREACH")
	fe, ok := errors.AsType[*hmerr.FeatureUnavailableError](err)
	if !ok || fe.Reason != hmenum.FeatureReasonNotSupported {
		t.Fatalf("err = %v, want FeatureUnavailableError not_supported_by_system", err)
	}
	if !errors.Is(err, hub.ErrNoServiceMessageAcknowledger) {
		t.Error("the refusal does not match hub.ErrNoServiceMessageAcknowledger")
	}
}

// TestLiteSuppressNeedsRPCAdmin pins that suppressing a service message
// goes to the interface process, which wants the admin tier: a token
// without it is refused with the scope named.
func TestLiteSuppressNeedsRPCAdmin(t *testing.T) {
	fake := startFake(t, litefake.Options{Tokens: map[string][]string{
		litefake.DefaultToken: {"rpc:configure", "meta:read", "system:read"},
	}})
	fake.SetServiceMessages(stickyUnreach(time.Now()))
	c := startLiteSystemCentral(t, fake, litefake.DefaultToken)
	if err := c.unit.Hub.RefreshServiceMessages(context.Background()); err != nil {
		t.Fatalf("RefreshServiceMessages: %v", err)
	}
	err := c.unit.HubModel.ServiceMessages.Disable(context.Background(), "HmIP-RF."+liteDimmer+":0.STICKY_UNREACH")
	sm, ok := errors.AsType[*hmerr.ScopeMissingError](err)
	if !ok || sm.Scope != "rpc:admin" {
		t.Fatalf("Disable err = %v, want a refusal naming rpc:admin", err)
	}
}

// TestLiteDutyCycleFromListBidcosInterfaces pins the duty-cycle refresh:
// the BidCos-RF gateway's duty cycle the interface process reports over
// XML-RPC — an integer, not the JSON-RPC wrapper's string — reaches the
// hub's per-interface snapshot.
func TestLiteDutyCycleFromListBidcosInterfaces(t *testing.T) {
	fake := startFake(t, litefake.Options{})
	c := startLiteSystemCentral(t, fake, litefake.DefaultToken)

	oc, err := occulited.New(occulited.Config{BaseURL: fake.URL(), Token: litefake.DefaultToken})
	if err != nil {
		t.Fatalf("occulited.New: %v", err)
	}
	xc, err := xmlrpc.NewClient(xmlrpc.Config{URL: oc.XMLRPCURL("BidCos-RF"), HTTPClient: oc.HTTPClient()})
	if err != nil {
		t.Fatalf("xmlrpc.NewClient: %v", err)
	}
	raw, err := xc.Call(context.Background(), "listBidcosInterfaces", nil)
	if err != nil {
		t.Fatalf("listBidcosInterfaces: %v", err)
	}
	gateways, ok := raw.(xmlrpc.ArrayValue)
	if !ok || len(gateways) == 0 {
		t.Fatalf("listBidcosInterfaces answered %#v, want at least one gateway", raw)
	}
	var want int
	found := false
	if st, ok := gateways[0].(xmlrpc.StructValue); ok {
		for _, m := range st.Members {
			if m.Name == "DUTY_CYCLE" {
				if v, ok := m.Value.(xmlrpc.IntValue); ok {
					want, found = int(v), true
				}
			}
		}
	}
	if !found {
		t.Fatalf("the gateway reports no integer DUTY_CYCLE: %#v", gateways[0])
	}

	if err := c.unit.Hub.RefreshBidcosInterfaces(context.Background()); err != nil {
		t.Fatalf("RefreshBidcosInterfaces: %v", err)
	}
	info, ok := c.unit.Hub.BidcosInterface(adapter.WireInterfaceID("box", hmenum.InterfaceBidCosRF))
	if !ok {
		t.Fatal("no BidCos-RF snapshot after the refresh")
	}
	if info.DutyCycle != want {
		t.Errorf("duty cycle = %d, want %d as the interface process reports it", info.DutyCycle, want)
	}
}

// TestLiteConnectivityFromInterfacesEndpoint pins the connectivity probe:
// an interface process the box reports down is unreachable after the
// reconcile pass.
func TestLiteConnectivityFromInterfacesEndpoint(t *testing.T) {
	fake := startFake(t, litefake.Options{})
	c := startLiteSystemCentral(t, fake, litefake.DefaultToken)
	id := adapter.WireInterfaceID("box", hmenum.InterfaceBidCosRF)
	conn := c.unit.HubModel.ConnectivityDataPoints()
	if conn == nil {
		t.Fatal("no connectivity aggregate on the hub model")
	}
	if err := c.unit.Hub.RefreshConnectivity(context.Background()); err != nil {
		t.Fatalf("RefreshConnectivity: %v", err)
	}
	if reachable, observed := conn.Reachable(id); !observed || !reachable {
		t.Fatalf("BidCos-RF reachable=%v observed=%v while running", reachable, observed)
	}
	if err := fake.SetInterfaceDown("BidCos-RF", true); err != nil {
		t.Fatalf("SetInterfaceDown: %v", err)
	}
	if err := c.unit.Hub.RefreshConnectivity(context.Background()); err != nil {
		t.Fatalf("RefreshConnectivity: %v", err)
	}
	if reachable, _ := conn.Reachable(id); reachable {
		t.Error("BidCos-RF still reachable after the box reported it down")
	}
}
