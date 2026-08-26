// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package backends

import (
	"context"
	"errors"
	"strings"
)

// DetectionConfig carries the inputs used by [DetectBackend] to
// auto-probe which backend kind a given endpoint speaks.
type DetectionConfig struct {
	// XMLRPCCaller is the transport the probe uses for XML-RPC pings.
	// Must not be nil.
	XMLRPCCaller Caller

	// BINRPCCaller is the transport used for BIN-RPC pings.
	// May be nil; when nil CUxD detection is skipped.
	BINRPCCaller Caller

	// InterfaceID is the identifier echoed to the CCU in the ping
	// call. A non-empty value is required.
	InterfaceID string

	// CallbackURL is passed to the ping call where the protocol
	// requires it. May be empty — the probe only reads the ping
	// reply, it does not register a real callback.
	CallbackURL string
}

// BackendDetectionResult summarises the outcome of [DetectBackend].
type BackendDetectionResult struct {
	// Kind is the detected backend kind. Set to [KindCCU] as a
	// fallback when no more specific match is found.
	Kind Kind

	// Capabilities is the static capability profile for the detected
	// kind, possibly adjusted for the observed software version.
	Capabilities Capabilities

	// SoftwareVersion is the version string the endpoint returned
	// during probing. Empty when the endpoint did not respond or the
	// probe method is not supported.
	SoftwareVersion string

	// IsHomegear reports whether the endpoint identified itself as a
	// Homegear daemon rather than a CCU.
	IsHomegear bool
}

// DetectBackend probes the endpoint described by cfg and returns the
// best matching [Kind].
//
// Detection strategy (in order):
// 1. Try a BIN-RPC ping → if the caller is wired and succeeds, this
// is a CUxD endpoint → [KindCUxD].
// 2. Try an XML-RPC "listDevices" call. If the response carries a
// "Homegear" signature in any returned device description, classify
// as [KindHomegear].
// 3. Fallback → [KindCCU].
//
// Errors from individual probes are swallowed — the function always
// returns a result. The caller should check [BackendDetectionResult.Kind]
// and not treat a nil error return as "all probes passed".
func DetectBackend(ctx context.Context, cfg DetectionConfig) (BackendDetectionResult, error) {
	if cfg.XMLRPCCaller == nil {
		return BackendDetectionResult{}, errors.New("backends.DetectBackend: XMLRPCCaller is required")
	}
	if cfg.InterfaceID == "" {
		return BackendDetectionResult{}, errors.New("backends.DetectBackend: InterfaceID is required")
	}

	// Step 1 — try BIN-RPC ping (CUxD uses BIN-RPC exclusively).
	if cfg.BINRPCCaller != nil {
		_, err := cfg.BINRPCCaller.Call(ctx, "ping", cfg.InterfaceID)
		if err == nil {
			return BackendDetectionResult{
				Kind:         KindCUxD,
				Capabilities: CapabilityFor(KindCUxD),
			}, nil
		}
	}

	// Step 2 — XML-RPC listDevices; sniff for Homegear.
	reply, err := cfg.XMLRPCCaller.Call(ctx, "listDevices")
	if err == nil {
		isHomegear, version := sniffHomegear(reply)
		if isHomegear {
			caps := CapabilityFor(KindHomegear)
			if version != "" {
				caps = UpdateCapabilitiesForVersion(caps, version)
			}
			return BackendDetectionResult{
				Kind:            KindHomegear,
				Capabilities:    caps,
				SoftwareVersion: version,
				IsHomegear:      true,
			}, nil
		}
	}

	// Fallback — CCU.
	return BackendDetectionResult{
		Kind:         KindCCU,
		Capabilities: CapabilityFor(KindCCU),
	}, nil
}

// sniffHomegear inspects the listDevices reply for Homegear signatures.
// Returns (true, version) when the reply looks like a Homegear endpoint.
//
// Homegear encodes a "TYPE" field that starts with "HG-" on its
// internal management device, and optionally encodes a "SOFTWARE_VERSION"
// in the description. We match either signal.
func sniffHomegear(reply any) (isHomegear bool, version string) {
	// listDevices returns []map[string]any on the wire.
	devs, ok := reply.([]any)
	if !ok {
		return false, ""
	}
	for _, d := range devs {
		m, ok := d.(map[string]any)
		if !ok {
			continue
		}
		if t, ok := m["TYPE"].(string); ok {
			if strings.HasPrefix(t, "HG-") || strings.EqualFold(t, "homegear") {
				ver, _ := m["SOFTWARE_VERSION"].(string)
				return true, ver
			}
		}
		// Some Homegear builds report a "FIRMWARE" key instead of TYPE.
		if fw, ok := m["FIRMWARE"].(string); ok && strings.Contains(strings.ToLower(fw), "homegear") {
			return true, fw
		}
	}
	return false, ""
}
