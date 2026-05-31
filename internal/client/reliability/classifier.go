// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

import "strings"

// RPCClass classifies an outbound RPC method by its semantic role on the
// south-bound transport. Different classes are throttled independently so a
// slow chain of writes does not starve cheap reads (and vice-versa).
type RPCClass int

const (
	// RPCClassUnknown is the fallback for methods the classifier does
	// not recognise. Treated as a write to stay on the safe side
	// (writes are the more conservative throttle).
	RPCClassUnknown RPCClass = iota

	// RPCClassRead is a state-querying call that does not mutate the
	// CCU. Examples: getValue, getParamset, getParamsetDescription,
	// listDevices, getDeviceDescription.
	RPCClassRead

	// RPCClassWrite is a state-mutating call. Examples: setValue,
	// putParamset, addLink, removeLink, setMetadata, setInstallMode.
	RPCClassWrite

	// RPCClassControl is a session / liveness call that should not
	// share the regular read/write throttles — flooding the throttle
	// with init-heartbeat-style calls during reconnect storms would
	// stall device traffic. Examples: init, ping, system.listMethods.
	RPCClassControl
)

// String returns a stable lower-case label for log/metrics tags.
func (c RPCClass) String() string {
	switch c {
	case RPCClassRead:
		return "read"
	case RPCClassWrite:
		return "write"
	case RPCClassControl:
		return "control"
	default:
		return "unknown"
	}
}

// readMethods enumerates every method that returns CCU state without
// Mutating it.py +
// homegear.py + the generated hmproto wire shapes — when adding new
// transport methods, add their canonical wire name here.
var readMethods = map[string]struct{}{
	// XML-RPC reads
	"listdevices":             {},
	"getdevicedescription":    {},
	"getparamsetdescription":  {},
	"getparamset":             {},
	"getvalue":                {},
	"getmetadata":             {},
	"getlinkpeers":            {},
	"getlinks":                {},
	"reportvalueusage":        {},
	"getinstallmode":          {},
	"getserviceinformation":   {},
	"getserviceformessages":   {},
	"getsearchresults":        {},
	"clientserverinitialized": {},
	// JSON-RPC reads
	"interface.listinterfaces": {},
	"interface.ispresent":      {},
	"interface.listdevices":    {},
	"device.listalldetail":     {},
	"device.getiseidbyaddress": {},
	"room.getall":              {},
	"room.getchannelids":       {},
	"subsection.getall":        {},
	"function.getchannelids":   {},
	"program.getall":           {},
	"sysvar.getall":            {},
	"sysvar.getvaluebyname":    {},
	"ccu.getserialnumber":      {},
	"ccu.getversion":           {},
	"channel.getlist":          {},
	"channel.getvalue":         {},
}

// writeMethods enumerates every method that mutates CCU state. Same
// curation source as readMethods.
var writeMethods = map[string]struct{}{
	// XML-RPC writes
	"setvalue":             {},
	"putparamset":          {},
	"setmetadata":          {},
	"setinstallmode":       {},
	"setteamid":            {},
	"addlink":              {},
	"removelink":           {},
	"setlinkinfo":          {},
	"deletedevice":         {},
	"abortinstallmode":     {},
	"activatelinkparamset": {},
	// JSON-RPC writes
	"sysvar.setvaluebyname": {},
	"program.execute":       {},
	"channel.setvalue":      {},
}

// controlMethods enumerates session / liveness calls that bypass
// read/write throttles entirely.
var controlMethods = map[string]struct{}{
	"init":               {},
	"ping":               {},
	"system.listmethods": {},
	"system.methodhelp":  {},
	"session.login":      {},
	"session.logout":     {},
	"session.renew":      {},
	"ccu.ping":           {},
}

// ClassifyMethod maps an RPC method name to its [RPCClass]. The match
// is case-insensitive and ignores leading whitespace so callers can
// pass the wire form unchanged. Unknown methods classify as
// [RPCClassUnknown] — the consumer treats those as writes.
func ClassifyMethod(method string) RPCClass {
	key := strings.ToLower(strings.TrimSpace(method))
	if _, ok := readMethods[key]; ok {
		return RPCClassRead
	}
	if _, ok := writeMethods[key]; ok {
		return RPCClassWrite
	}
	if _, ok := controlMethods[key]; ok {
		return RPCClassControl
	}
	return RPCClassUnknown
}
