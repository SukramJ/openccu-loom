// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum

// VirtualRemoteModels enumerates the pseudo-device MODELS the CCU exposes as
// "virtual remotes". They are not real radio peers — they only forward press
// events from the WebUI or from scripts onto the bus, so they carry press
// parameters without any physical button behind them.
//
// The set is exact, not a prefix match: only these three model strings are
// virtual remotes, one per bus family (BidCos-RF, BidCos-Wired, HmIP-RF). A
// prefix form would classify any future model sharing the prefix, and the
// live consumers of this rule suppress behaviour on a match — click-event
// marks and remote-key candidates — so a false positive silently hides a real
// device's buttons.
//
// This is the MODEL axis. The virtual-remote ADDRESS roots are a different
// datum with a different home (internal/routingkey), and they feed published
// identity (unique ids, retained MQTT topics); do not merge the two sets.
var VirtualRemoteModels = map[string]struct{}{
	"HM-RCV-50":   {},
	"HMW-RCV-50":  {},
	"HmIP-RCV-50": {},
}

// IsVirtualRemoteModel reports whether model names one of the CCU's
// virtual-remote pseudo-devices. It lives here rather than on the device
// aggregate because callers that classify a raw wire device-type string have
// no device object in hand.
func IsVirtualRemoteModel(model string) bool {
	_, ok := VirtualRemoteModels[model]
	return ok
}
