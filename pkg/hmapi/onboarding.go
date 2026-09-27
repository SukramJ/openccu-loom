// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmapi

// --- Central onboarding: probe and client pairing ---

// CentralProbeRequest names a system to identify before it is added.
type CentralProbeRequest struct {
	Host string `json:"host"`
	// Port is the system's web server port; 0 means 80, or 443 with TLS.
	Port                  int  `json:"port,omitempty"`
	TLS                   bool `json:"tls,omitempty"`
	TLSInsecureSkipVerify bool `json:"tls_insecure_skip_verify,omitempty"`
}

// CentralProbeResult is what answers at a probed address.
type CentralProbeResult struct {
	// SystemType is "ccu", "openccu-lite" or "unknown".
	SystemType string `json:"system_type"`
	// Ready reports whether the system serves its API right now; an
	// openccu-lite box whose API is still starting is identified but not
	// ready.
	Ready bool `json:"ready"`
	// TLSFingerprint is the lower-case hex SHA-256 of the certificate the
	// server presented over HTTPS, to pin; empty over plain HTTP.
	TLSFingerprint string `json:"tls_fingerprint,omitempty"`
	// Lite describes an answering openccu-lite box.
	Lite *LiteProbeInfo `json:"lite,omitempty"`
}

// LiteProbeInfo is the version information of an openccu-lite box.
type LiteProbeInfo struct {
	Implementation string `json:"implementation,omitempty"`
	// APIMajors are the API major versions the box speaks, per API.
	APIMajors map[string]int `json:"api_majors,omitempty"`
	// PairingAvailable reports whether the box offers client pairing.
	PairingAvailable bool `json:"pairing_available"`
	// HMIPKeyMode summarises the HomeMatic IP key mode; it never carries a
	// key. Absent on a box that does not report it.
	HMIPKeyMode *HMIPKeyMode `json:"hmip_key_mode,omitempty"`
}

// HMIPKeyMode is the key-mode summary of a box's HomeMatic IP radio.
type HMIPKeyMode struct {
	KeyserverMode  string `json:"keyserver_mode"`
	DeviceKeys     int    `json:"device_keys"`
	OfflinePairing bool   `json:"offline_pairing"`
}

// CentralPairingRequest starts client pairing with an openccu-lite box.
type CentralPairingRequest struct {
	Host string `json:"host"`
	Port int    `json:"port,omitempty"`
	TLS  bool   `json:"tls,omitempty"`
	// TLSFingerprint pins the box's certificate (from the probe); needed
	// over HTTPS when the certificate is not signed by a trusted CA.
	TLSFingerprint string `json:"tls_fingerprint,omitempty"`
	// Access is "full" (the default), "control" or "read".
	Access string `json:"access,omitempty"`
}

// CentralPairingStarted is a started pairing: the code the box's
// administrator enters on the box, and how long it is valid.
type CentralPairingStarted struct {
	PairingID string `json:"pairing_id"`
	Code      string `json:"code"`
	// Fingerprint is the certificate fingerprint both sides agreed on;
	// empty over plain HTTP.
	Fingerprint string `json:"fingerprint,omitempty"`
	ExpiresIn   int    `json:"expires_in"`
}

// CentralPairingStatus is the state of a pairing. The token an approved
// pairing yields never leaves the daemon: a central created or updated
// with the pairing's id takes it from there.
type CentralPairingStatus struct {
	// State is "pending", "approved", "rejected", "expired" or "error".
	State string `json:"state"`
	// Scopes are the scopes an approved pairing granted.
	Scopes []string `json:"scopes,omitempty"`
	Error  string   `json:"error,omitempty"`
}
