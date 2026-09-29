// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/north/rest"
	"github.com/SukramJ/openccu-loom/internal/pairing"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

type pinMinter struct{ tokens *sqlite.TokenStore }

func (m pinMinter) MintPairedToken(ctx context.Context, subject string, role auth.Role) (token, fingerprint string, err error) {
	res, err := m.tokens.Create(ctx, sqlite.CreateInput{Subject: subject, Role: role})
	if err != nil {
		return "", "", err
	}
	return res.Token, res.Fingerprint, nil
}

// TestClientPairingRoundTripMintsAUsableToken drives the whole ADR 0076
// collaboration the way production wires it: the anonymous ask through
// the router, the reveal, the admin card's code, the typed approval, the
// one-time token hand-out — and then the effect that matters: the minted
// token, read back from the REAL sqlite store by the REAL auth
// middleware, authenticates an ordinary API request with the paired
// role's reach and not more.
func TestClientPairingRoundTripMintsAUsableToken(t *testing.T) {
	db, err := sqlite.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	tokens := sqlite.NewTokenStore(db)

	fingerprint := bytes.Repeat([]byte{0xA5}, 32)
	changes := 0
	mgr := &pairing.Manager{
		Minter:      pinMinter{tokens: tokens},
		Local:       pairing.LocalHost,
		Fingerprint: func() []byte { return fingerprint },
		OnChange:    func() { changes++ },
	}

	adminTokens := auth.NewMemoryTokenStore(map[string]auth.Identity{
		"tok-admin": {Subject: "markus", Scheme: auth.SchemeBearer, Role: auth.RoleAdmin},
	})
	mw := auth.NewMiddleware(nil, auth.ChainedTokenStore{Primary: tokens, Secondary: adminTokens})
	router := rest.NewRouter(rest.Deps{
		Pairing:     mgr,
		AuthResolve: mw.Resolve,
		AuthRequire: mw.Require,
		RequireAdmin: func(next http.Handler) http.Handler {
			return mw.RequireRole(auth.RoleAdmin, next)
		},
		RequireOperator: func(next http.Handler) http.Handler {
			return mw.RequireRole(auth.RoleOperator, next)
		},
	})

	do := func(method, path, bearer, pollSecret string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.RemoteAddr = "192.168.7.7:41234"
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if pollSecret != "" {
			req.Header.Set("Authorization", "Pairing "+pollSecret)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	// 1. The anonymous ask.
	clientNonce := bytes.Repeat([]byte{0x42}, 32)
	commit := sha256.Sum256(clientNonce)
	askBody, _ := json.Marshal(pairing.Ask{
		App: "openccu-loom-client", AppVersion: "2026.9.1", Instance: "ha",
		Role: "operator", Purpose: "device control", Commit: hex.EncodeToString(commit[:]),
	})
	rec := do(http.MethodPost, "/api/v1/pairing", "", "", askBody)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("ask: %d %s", rec.Code, rec.Body.String())
	}
	var ans pairing.Answer
	if err := json.Unmarshal(rec.Body.Bytes(), &ans); err != nil {
		t.Fatal(err)
	}
	if ans.Fingerprint != hex.EncodeToString(fingerprint) {
		t.Fatalf("answer fingerprint %q", ans.Fingerprint)
	}

	// 2. First poll reveals the client nonce.
	rec = do(http.MethodGet, "/api/v1/pairing/"+ans.ID+"?client_nonce="+hex.EncodeToString(clientNonce), "", ans.Poll, nil)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("pending")) {
		t.Fatalf("reveal poll: %d %s", rec.Code, rec.Body.String())
	}

	// 3. The admin card shows the request with the code the client also
	// derives from its own halves.
	rec = do(http.MethodGet, "/api/v1/pairing-requests", "tok-admin", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin list: %d %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Items []pairing.View `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Items) != 1 {
		t.Fatalf("admin list: %v %s", err, rec.Body.String())
	}
	nonce, _ := hex.DecodeString(ans.Nonce)
	if clientSide := pairing.Code(nonce, clientNonce, fingerprint); clientSide != list.Items[0].Code {
		t.Fatalf("client derives %q, card shows %q", clientSide, list.Items[0].Code)
	}
	// The card is admin-gated: an anonymous read is refused.
	if rec := do(http.MethodGet, "/api/v1/pairing-requests", "", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous admin list: %d", rec.Code)
	}

	// 4. The typed code approves.
	approveBody, _ := json.Marshal(map[string]string{"code": list.Items[0].Code})
	rec = do(http.MethodPost, "/api/v1/pairing-requests/"+list.Items[0].ID+"/approve", "tok-admin", "", approveBody)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body.String())
	}

	// 5. The next poll hands out the token exactly once.
	rec = do(http.MethodGet, "/api/v1/pairing/"+ans.ID+"?wait=5", "", ans.Poll, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("approved poll: %d %s", rec.Code, rec.Body.String())
	}
	var res pairing.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.State != pairing.StateApproved || res.Token == "" || res.Role != "operator" {
		t.Fatalf("result: %+v", res)
	}
	if rec := do(http.MethodGet, "/api/v1/pairing/"+ans.ID, "", ans.Poll, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("second poll: %d", rec.Code)
	}

	// 6. The minted token authenticates a real request through the real
	// store — and carries operator reach, not admin.
	if rec := do(http.MethodGet, "/api/v1/warnings", res.Token, "", nil); rec.Code != http.StatusOK {
		t.Fatalf("paired token on /warnings: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodGet, "/api/v1/pairing-requests", res.Token, "", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("paired operator token on an admin route: %d", rec.Code)
	}

	if changes == 0 {
		t.Fatal("OnChange never fired — the live card signal is unwired")
	}
}
