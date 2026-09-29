// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/SukramJ/openccu-loom/internal/pairing"
)

// Pairing error codes of the box that a caller branches on.
const (
	CodePairingOff = "pairing-off"
	CodeNotLocal   = "not-local"
	CodeSlowDown   = "slow_down"
)

// ErrPairingFingerprintMismatch reports that the certificate the box says
// it serves is not the one the client saw: something between the two
// terminated TLS. The pairing is aborted; the code would authenticate the
// interceptor.
var ErrPairingFingerprintMismatch = errors.New("occulited: pairing aborted: the certificate the box reports differs from the one seen — possible interception")

// Pairing states a poll reports.
const (
	PairingPending  = "pending"
	PairingApproved = "approved"
	PairingRejected = "rejected"
	PairingExpired  = "expired"
)

// PairingAccess is the access level a client asks for, per area. An empty
// area is not requested. The box turns the levels into scopes and never
// grants `*`, auth:admin, radio:keys, power or backup this way.
type PairingAccess struct {
	Devices string `json:"devices,omitempty"` // read | operate | configure | administer
	Names   string `json:"names,omitempty"`   // read | configure
	System  string `json:"system,omitempty"`  // read | configure
}

// PairingRequest is what the client asks the box's administrator to
// approve.
type PairingRequest struct {
	App        string
	AppVersion string
	Instance   string
	Name       string
	Access     PairingAccess
	// Purpose explains, per requested area, what the access is for; the
	// administrator reads it next to the request.
	Purpose map[string]string
}

// Pairing is a started pairing: the code to show and what the polls need.
type Pairing struct {
	ID string
	// Code is the six digits the administrator enters on the box. It binds
	// the box's nonce, the client's secret nonce and the certificate the
	// client saw, so an interceptor cannot complete a pairing.
	Code string
	// Fingerprint is the certificate fingerprint both sides agreed on;
	// empty over plain HTTP. Pin it with the token the pairing yields.
	Fingerprint string
	ExpiresIn   time.Duration
	Interval    time.Duration

	poll        string
	clientNonce []byte
}

// PairingResult is one poll's answer. Token, Name and Scopes are set only
// on the one approved answer; the box never repeats them.
type PairingResult struct {
	State  string   `json:"state"`
	Token  string   `json:"token"`
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
}

type pairingRequestBody struct {
	App        string            `json:"app"`
	AppVersion string            `json:"app_version,omitempty"`
	Instance   string            `json:"instance,omitempty"`
	Name       string            `json:"name,omitempty"`
	Access     PairingAccess     `json:"access"`
	Purpose    map[string]string `json:"purpose,omitempty"`
	Commit     string            `json:"commit"`
}

type pairingAnswer struct {
	ID          string `json:"id"`
	Poll        string `json:"poll"`
	Nonce       string `json:"nonce"`
	ExpiresIn   int    `json:"expires_in"`
	Interval    int    `json:"interval"`
	Fingerprint string `json:"fingerprint"`
}

// clientNonceLen is the size of the client's secret nonce; the box wants
// at least 16 bytes.
const clientNonceLen = 32

// StartPairing asks the box for an API token. It commits to a random
// secret nonce, sends the request (the route is open; no credential is
// attached), checks that the certificate fingerprint the box reports is the
// one this connection saw, and derives the code the administrator enters
// on the box. A box that refuses pairing answers an *APIError with Code
// [CodePairingOff] or [CodeNotLocal].
func (c *Client) StartPairing(ctx context.Context, pr PairingRequest) (*Pairing, error) {
	clientNonce := make([]byte, clientNonceLen)
	if _, err := rand.Read(clientNonce); err != nil {
		return nil, fmt.Errorf("occulited: pairing nonce: %w", err)
	}
	commit := sha256.Sum256(clientNonce)
	body, err := marshal(pairingRequestBody{
		App: pr.App, AppVersion: pr.AppVersion, Instance: pr.Instance, Name: pr.Name,
		Access: pr.Access, Purpose: pr.Purpose, Commit: hex.EncodeToString(commit[:]),
	})
	if err != nil {
		return nil, err
	}
	r := request{method: http.MethodPost, path: "/api/auth/v1/pairing/request", body: body}
	resp, err := c.send(withoutCredential(ctx), c.calls, r)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	seen := ""
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		seen = Fingerprint(resp.TLS.PeerCertificates[0])
	}
	ans, err := decodeBody[pairingAnswer](resp, r)
	if err != nil {
		return nil, err
	}
	if ans.Fingerprint != seen {
		return nil, fmt.Errorf("%w (box %q, seen %q)", ErrPairingFingerprintMismatch, ans.Fingerprint, seen)
	}
	nonce, err := hex.DecodeString(ans.Nonce)
	if err != nil || ans.ID == "" || ans.Poll == "" {
		return nil, fmt.Errorf("occulited: %w: pairing answer without id, poll secret or hex nonce", ErrProtocol)
	}
	fp, err := hex.DecodeString(ans.Fingerprint)
	if err != nil {
		return nil, fmt.Errorf("occulited: %w: pairing fingerprint is not hex", ErrProtocol)
	}
	return &Pairing{
		ID:          ans.ID,
		Code:        PairingCode(nonce, clientNonce, fp),
		Fingerprint: ans.Fingerprint,
		ExpiresIn:   time.Duration(ans.ExpiresIn) * time.Second,
		Interval:    time.Duration(ans.Interval) * time.Second,
		poll:        ans.Poll,
		clientNonce: clientNonce,
	}, nil
}

// PollPairing asks for the pairing's state, waiting up to wait (at most
// 30 s) on the box for a change. The first poll reveals the secret nonce;
// the administrator sees the request only after that. A too-early poll is
// an *APIError with Code [CodeSlowDown] and RetryAfter set; after the one
// approved answer the box answers 404.
func (c *Client) PollPairing(ctx context.Context, p *Pairing, wait time.Duration) (PairingResult, error) {
	q := url.Values{"client_nonce": {hex.EncodeToString(p.clientNonce)}}
	if secs := int(wait / time.Second); secs > 0 {
		q.Set("wait", strconv.Itoa(min(secs, 30)))
	}
	return call[PairingResult](withoutCredential(ctx), c, request{
		method: http.MethodGet, path: "/api/auth/v1/pairing/request/" + url.PathEscape(p.ID), query: q,
		header: http.Header{"Authorization": {"Pairing " + p.poll}},
	})
}

// CancelPairing withdraws a pending pairing.
func (c *Client) CancelPairing(ctx context.Context, p *Pairing) error {
	resp, err := c.send(withoutCredential(ctx), c.calls, request{
		method: http.MethodDelete, path: "/api/auth/v1/pairing/request/" + url.PathEscape(p.ID),
		body:   []byte("{}"),
		header: http.Header{"Authorization": {"Pairing " + p.poll}},
	})
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// PairingCode is the six-digit code both sides compute. The derivation
// is shared with this daemon's own pairing server (internal/pairing) —
// the two speak the same protocol, one as the asking side against an
// occulited box, one as the answering side towards external clients.
func PairingCode(nonce, clientNonce, fingerprint []byte) string {
	return pairing.Code(nonce, clientNonce, fingerprint)
}
