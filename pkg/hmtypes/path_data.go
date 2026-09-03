// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmtypes

import "strings"

// PathData describes a data point's location in a logical namespace of the
// form:
//
//	central/<central>/device/<model>/<channel_address>/<parameter>
//
// It has no consumer inside the daemon, and it is not the shape the daemon
// publishes. MQTT topics and REST routes are rendered from
// internal/model/naming.PathData, which carries different fields — interface,
// channel number and bucket rather than the device model — so the two do not
// agree on any input and neither is derived from the other.
//
// The reachability claim in the paragraph above is measured, not asserted, by
// TestW2PkgHmtypesPathDataHasNoDaemonConsumer in tests/contract: wire this
// type into the daemon and that guard requires this comment to be updated.
// script/reachability cannot report it, because it auto-whitelists every
// symbol under pkg/hmtypes.
type PathData struct {
	CentralName    string
	DeviceModel    string
	ChannelAddress string
	Parameter      string
}

// Segments returns the path segments in canonical order. Empty fields
// are omitted so callers can safely join with "/".
func (p PathData) Segments() []string {
	out := make([]string, 0, 6)
	if p.CentralName != "" {
		out = append(out, "central", p.CentralName)
	}
	if p.DeviceModel != "" {
		out = append(out, "device", p.DeviceModel)
	}
	if p.ChannelAddress != "" {
		out = append(out, p.ChannelAddress)
	}
	if p.Parameter != "" {
		out = append(out, p.Parameter)
	}
	return out
}

// String returns the "/"-joined canonical path.
func (p PathData) String() string {
	return strings.Join(p.Segments(), "/")
}
