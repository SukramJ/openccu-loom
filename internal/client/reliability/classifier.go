// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package reliability

import (
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

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

// readMethods enumerates every method that returns CCU state without mutating
// it. When adding a new transport method, add its canonical wire name here —
// and check what the method does on the CCU rather than what its name
// suggests: this table is also what the project's rule for the developer's
// live CCU treats as free of consequence.
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
	// The WebUI's API handlers for these only read: a file test or read,
	// a ReGa object walk, or the matching XML-RPC getter
	// (OpenCCU-Base www/api/methods/ccu/getauthenabled.tcl,
	// getheatinggrouplist.tcl, gethttpsredirectenabled.tcl;
	// device/getregaidbyaddress.tcl; interface/getinstallmode.tcl,
	// getlinkinfo.tcl, getlinks.tcl, getsuppressedservicemessages.tcl,
	// listbidcosinterfaces.tcl; program/get.tcl).
	"ccu.getauthenabled":                     {},
	"ccu.getheatinggrouplist":                {},
	"ccu.gethttpsredirectenabled":            {},
	"device.getregaidbyaddress":              {},
	"interface.getinstallmode":               {},
	"interface.getlinkinfo":                  {},
	"interface.getlinks":                     {},
	"interface.getsuppressedservicemessages": {},
	"interface.listbidcosinterfaces":         {},
	"program.get":                            {},
}

// writeMethods enumerates every method that mutates CCU state. Same
// curation source as readMethods.
var writeMethods = map[string]struct{}{
	// XML-RPC writes
	"setvalue":       {},
	"putparamset":    {},
	"setmetadata":    {},
	"setinstallmode": {},
	"setteamid":      {},
	"addlink":        {},
	"removelink":     {},
	// reportValueUsage reads like a query and is a device reconfiguration:
	// the CCU documents it as telling the interface process how often a value
	// is used so that it can establish or delete the connection to the
	// component (src/rfd/XmlRpcMethods.cpp:700-723). It persists the
	// per-channel usage record and adds or removes the direct link peer
	// between the channel and the central; a false return means the device
	// was unreachable, the change is queued, and CONFIG_PENDING is set on its
	// MAINTENANCE channel.
	"reportvalueusage":     {},
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

// RepeatableAfterLostReply reports whether a call to method may be sent
// again after its reply was lost ([hmerr.ErrReplyLost]): the request went
// out, the connection broke, and whether the CCU executed it is unknown.
// Only a read is safe to repeat. A write, a session or liveness call and
// every method this table does not know are sent once — repeating an
// ACTION press, a program run or a firmware install does it twice.
//
// A call that never reached the wire (a refused dial) is not a lost reply
// and stays retryable whatever the method.
func RepeatableAfterLostReply(method string) bool {
	return ClassifyMethod(method) == RPCClassRead
}

// readOnlyScripts lists the ReGa scripts that are safe to run again after a
// lost reply. Each entry is judged on the script's body under
// internal/client/rega/scripts, never on its name: they only walk ReGa
// objects and Write() the result, and their system.Exec calls only read
// (get_backend_info: version file, hostname, environment; get_serial:
// board serial files).
//
// Not listed although named like reads: get_system_update_info runs
// /bin/checkFirmwareUpdate.sh, and create_backup_status removes the
// backup's pid file once it reports the run as completed or failed, so
// running it twice turns a "failed" answer into "idle".
var readOnlyScripts = map[hmenum.RegaScript]struct{}{
	hmenum.RegaScriptFetchAllDeviceData:            {},
	hmenum.RegaScriptGetAlarmMessages:              {},
	hmenum.RegaScriptGetBackendInfo:                {},
	hmenum.RegaScriptGetInboxDevices:               {},
	hmenum.RegaScriptGetProgramDescriptions:        {},
	hmenum.RegaScriptGetSerial:                     {},
	hmenum.RegaScriptGetServiceMessages:            {},
	hmenum.RegaScriptGetSystemVariableDescriptions: {},
	hmenum.RegaScriptGetUserLevel:                  {},
	hmenum.RegaScriptPollComTest:                   {},
	hmenum.RegaScriptUsageBySysvar:                 {},
}

// RepeatableScriptAfterLostReply is [RepeatableAfterLostReply] for a ReGa
// script: true only for a script listed in readOnlyScripts. A script that
// is not listed — every new one until someone has read it — runs once.
func RepeatableScriptAfterLostReply(script hmenum.RegaScript) bool {
	_, ok := readOnlyScripts[script]
	return ok
}
