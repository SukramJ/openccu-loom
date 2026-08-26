// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package oidc

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// PKCEPair bundles the verifier/challenge used by RFC 7636. The
// verifier stays server-side (stored on the session); only the
// challenge + method are sent to the IdP.
type PKCEPair struct {
	Verifier  string
	Challenge string
	Method    string // always "S256"
}

// NewPKCEPair mints a cryptographically-random 43-char verifier and
// the matching S256 challenge.
func NewPKCEPair() (PKCEPair, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return PKCEPair{}, err
	}
	verifier := base64.RawURLEncoding.EncodeToString(raw[:])
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return PKCEPair{Verifier: verifier, Challenge: challenge, Method: "S256"}, nil
}
