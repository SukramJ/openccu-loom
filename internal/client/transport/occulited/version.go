// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// Supported API majors: the client speaks major 1 of every API it uses
// and refuses a box that announces a higher one.
const (
	SupportedMetaMajor   = 1
	SupportedRPCMajor    = 1
	SupportedSystemMajor = 1
	SupportedAuthMajor   = 1
)

// supportedMajors maps the capabilities.apis keys to the major spoken.
func supportedMajors() map[string]int {
	return map[string]int{
		"meta":   SupportedMetaMajor,
		"rpc":    SupportedRPCMajor,
		"system": SupportedSystemMajor,
		"auth":   SupportedAuthMajor,
	}
}

// Version is the open GET /api/meta/v1/version answer.
type Version struct {
	API            string        `json:"api"`
	Version        int           `json:"version"`
	Format         int           `json:"format"`
	Revision       int           `json:"revision"`
	Implementation string        `json:"implementation"`
	HMIP           *hmipKeyMode  `json:"hmip"`
	Capabilities   *Capabilities `json:"capabilities"`
	// Raw is the whole answer.
	Raw json.RawMessage `json:"-"`
}

// hmipKeyMode is the key-mode summary of the HomeMatic IP radio; it never
// carries a key. Nil on a box that predates it.
type hmipKeyMode struct {
	KeyserverMode  string `json:"keyserver_mode"`
	DeviceKeys     int    `json:"device_keys"`
	OfflinePairing bool   `json:"offline_pairing"`
}

// Capabilities is the capabilities member of the version answer. Nil on
// a box that predates it.
type Capabilities struct {
	Pairing    bool           `json:"pairing"`
	State      bool           `json:"state"`
	History    bool           `json:"history"`
	APIs       map[string]int `json:"apis"`
	Transports []string       `json:"transports"`
	Limits     *streamLimits  `json:"limits"`
	JSONDouble bool           `json:"json_double"`
}

// streamLimits are the stream figures the box advertises.
type streamLimits struct {
	StreamsPerToken int `json:"streams_per_token"`
	StreamsTotal    int `json:"streams_total"`
	BufferSeconds   int `json:"buffer_seconds"`
	BufferEvents    int `json:"buffer_events"`
}

// Majors returns the announced API majors; a box without capabilities
// (or without apis) is treated as speaking major 1 of each.
func (v *Version) Majors() map[string]int {
	out := map[string]int{"meta": 1, "rpc": 1, "system": 1, "auth": 1}
	if v.Capabilities != nil {
		for k, n := range v.Capabilities.APIs {
			out[k] = n
		}
	}
	return out
}

// Pairing reports whether the box offers client pairing; false on a box
// without capabilities.
func (v *Version) Pairing() bool { return v.Capabilities != nil && v.Capabilities.Pairing }

// ErrUnsupportedMajor reports a box announcing an API major above the
// one the client speaks.
var ErrUnsupportedMajor = errors.New("occulited: unsupported occulited API major")

// MajorError names the API whose major is too new.
type MajorError struct {
	API       string
	Got       int
	Supported int
}

// Error implements error.
func (e *MajorError) Error() string {
	return fmt.Sprintf("%s: %s is at major %d, this client speaks %d", ErrUnsupportedMajor, e.API, e.Got, e.Supported)
}

// Is makes a MajorError match [ErrUnsupportedMajor].
func (e *MajorError) Is(target error) bool { return target == ErrUnsupportedMajor }

// CheckMajors refuses the first API (in name order) whose announced
// major is above the supported one. APIs the client does not use are
// ignored.
func (v *Version) CheckMajors() error {
	majors := v.Majors()
	names := make([]string, 0, len(majors))
	for k := range majors {
		names = append(names, k)
	}
	sort.Strings(names)
	supported := supportedMajors()
	for _, name := range names {
		want, used := supported[name]
		if used && majors[name] > want {
			return &MajorError{API: name, Got: majors[name], Supported: want}
		}
	}
	return nil
}

// MetaVersion reads GET /api/meta/v1/version. An answer that is not a
// JSON document with "api":"meta" is an [ErrProtocol] error: whatever
// answered is not an openccu-lite box.
func (c *Client) MetaVersion(ctx context.Context) (Version, error) {
	resp, err := c.send(ctx, c.calls, request{method: http.MethodGet, path: "/api/meta/v1/version"})
	if err != nil {
		return Version{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	v, _, err := decodeVersion(resp)
	return v, err
}

// decodeVersion decodes a version answer; notLite is true when the body
// is not the meta version document at all.
func decodeVersion(resp *http.Response) (v Version, notLite bool, err error) {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, errorBodyLimit))
	if err != nil {
		return Version{}, false, fmt.Errorf("occulited: read version: %w: %w", hmerr.ErrNoConnection, err)
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.API != "meta" {
		return Version{}, true, fmt.Errorf("occulited: %w: /api/meta/v1/version is not the meta version document", ErrProtocol)
	}
	v.Raw = raw
	return v, false, nil
}

// DetectionKind classifies what answers at a base URL.
type DetectionKind string

// Detection outcomes.
const (
	// DetectLite is an answering openccu-lite box.
	DetectLite DetectionKind = "lite"
	// DetectLiteNotReady is an openccu-lite box whose web server answers
	// while occulited does not yet.
	DetectLiteNotReady DetectionKind = "lite-not-ready"
	// DetectCCU is a CCU: not an openccu-lite box, and its ReGa check
	// answers OK.
	DetectCCU DetectionKind = "ccu"
	// DetectUnknown is neither (yet): keep probing.
	DetectUnknown DetectionKind = "unknown"
)

// Detection is the result of [Client.Detect].
type Detection struct {
	Kind DetectionKind
	// Version is the version answer of a lite box.
	Version *Version
	// Majors are the API majors a lite box announces (defaults filled).
	Majors map[string]int
	// Pairing reports whether a lite box offers client pairing.
	Pairing bool
	// RetryAfter is the Retry-After of a box that is starting.
	RetryAfter time.Duration
	// TLSFingerprint is the fingerprint of the certificate the server
	// presented over HTTPS ("" over plain HTTP).
	TLSFingerprint string
}

// Detect classifies the base URL:
//
//  1. GET /api/meta/v1/version: a JSON document with "api":"meta" is a
//     lite box; a major above the supported one returns the detection
//     together with an error matching [ErrUnsupportedMajor];
//  2. 503 with the JSON error "starting" is a lite box that is starting;
//  3. anything else (404, HTML, other JSON) is not a lite box: then GET
//     /ise/checkrega.cgi without the token decides between a CCU (body
//     "OK") and unknown.
//
// A transport failure (no connection, TLS) is returned as an error.
func (c *Client) Detect(ctx context.Context) (Detection, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("/api/meta/v1/version", nil), http.NoBody)
	if err != nil {
		return Detection{}, fmt.Errorf("occulited: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.calls.Do(req)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusServiceUnavailable && apiErr.Code == CodeStarting {
			return Detection{Kind: DetectLiteNotReady, RetryAfter: apiErr.RetryAfter}, nil
		}
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized {
			// The version document is open; a 401 means something else
			// guards the path.
			return c.detectCCU(ctx, "")
		}
		return Detection{}, fmt.Errorf("occulited: detect: %w: %w", hmerr.ErrNoConnection, err)
	}
	defer func() { _ = resp.Body.Close() }()
	fp := peerFingerprint(resp)
	if resp.StatusCode != http.StatusOK {
		return c.detectCCU(ctx, fp)
	}
	v, notLite, err := decodeVersion(resp)
	if notLite {
		return c.detectCCU(ctx, fp)
	}
	if err != nil {
		return Detection{}, err
	}
	d := Detection{Kind: DetectLite, Version: &v, Majors: v.Majors(), Pairing: v.Pairing(), TLSFingerprint: fp}
	if err := v.CheckMajors(); err != nil {
		return d, err
	}
	return d, nil
}

// detectCCU runs the CCU probe for a base URL that is not a lite box.
// The token is withheld: the peer is not the box it was issued by.
func (c *Client) detectCCU(ctx context.Context, fp string) (Detection, error) {
	req, err := http.NewRequestWithContext(withoutCredential(ctx), http.MethodGet, c.endpoint("/ise/checkrega.cgi", nil), http.NoBody)
	if err != nil {
		return Detection{}, fmt.Errorf("occulited: build request: %w", err)
	}
	resp, err := c.calls.Do(req)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			return Detection{Kind: DetectUnknown, TLSFingerprint: fp}, nil
		}
		return Detection{}, fmt.Errorf("occulited: detect: %w: %w", hmerr.ErrNoConnection, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if fp == "" {
		fp = peerFingerprint(resp)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	if resp.StatusCode == http.StatusOK && strings.TrimSpace(string(body)) == "OK" {
		return Detection{Kind: DetectCCU, TLSFingerprint: fp}, nil
	}
	return Detection{Kind: DetectUnknown, TLSFingerprint: fp}, nil
}

// peerFingerprint returns the fingerprint of the leaf certificate an
// HTTPS answer came with.
func peerFingerprint(resp *http.Response) string {
	if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
		return ""
	}
	return Fingerprint(resp.TLS.PeerCertificates[0])
}
