// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// expiringTokens hands out one identity per token, so a single store can
// serve both a bounded and an unbounded credential to the same connection.
type expiringTokens map[string]auth.Identity

func (s expiringTokens) AuthenticateToken(_ context.Context, token string) (auth.Identity, error) {
	if id, ok := s[token]; ok {
		return id, nil
	}
	return auth.Identity{}, auth.ErrUnauthenticated
}

// TestReauthOkCarriesExpiry pins the deadline onto the reauth acknowledgement.
//
// The in-band reauth op exists so a long-lived socket can refill its
// credential without reconnecting, and [client.watchCredentialExpiry] closes
// the connection the instant the new credential's deadline passes. A client
// that is not told the deadline cannot schedule the next refill, so the
// mechanism only ever fires after the connection is already gone — which is
// the reconnect it was built to avoid.
//
// The unbounded case is the negative control: the same code path must leave
// the field out rather than report a zero time a client would read as long
// expired.
func TestReauthOkCarriesExpiry(t *testing.T) {
	t.Parallel()
	deadline := time.Now().Add(45 * time.Minute).UTC().Truncate(time.Second)
	hub, _, _, _, _, _ := newTestHub(t)
	hub.SetTokenStore(expiringTokens{
		"bounded": {
			Subject: "ha-bridge", Role: auth.RoleOperator,
			Scheme: auth.SchemeBearer, TokenID: "tok-1", ExpiresAt: deadline,
		},
		"unbounded": {
			Subject: "ha-bridge", Role: auth.RoleOperator,
			Scheme: auth.SchemeBearer, TokenID: "tok-2",
		},
	})
	server := httptest.NewServer(Handler(hub, nil, nil))
	t.Cleanup(server.Close)

	c := dialWS(t, server)
	waitForClientCount(t, hub, 1)

	c.send(map[string]any{"op": "reauth", "token": "bounded"})
	raw := c.recv(nil)
	var ack struct {
		Op        string `json:"op"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(raw, &ack); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	if ack.Op != "reauth_ok" {
		t.Fatalf("op = %q, want reauth_ok; frame=%s", ack.Op, raw)
	}
	if ack.ExpiresAt == "" {
		t.Fatalf("reauth_ok carries no expires_at — the client has just refilled "+
			"a credential it cannot schedule the next refill for; frame=%s", raw)
	}
	got, err := time.Parse(time.RFC3339Nano, ack.ExpiresAt)
	if err != nil {
		t.Fatalf("expires_at %q is not RFC3339: %v", ack.ExpiresAt, err)
	}
	if !got.Equal(deadline) {
		t.Errorf("expires_at = %s, want %s", got.Format(time.RFC3339), deadline.Format(time.RFC3339))
	}

	c.send(map[string]any{"op": "reauth", "token": "unbounded"})
	raw = c.recv(nil)
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	if probe["op"] != "reauth_ok" {
		t.Fatalf("op = %v, want reauth_ok; frame=%s", probe["op"], raw)
	}
	if v, present := probe["expires_at"]; present {
		t.Errorf("expires_at = %v on an unbounded credential, want the field absent", v)
	}
}
