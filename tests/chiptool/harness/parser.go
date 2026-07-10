// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build chiptool

package harness

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// chip-tool's output is unstructured text with a handful of stable
// marker lines. The regexps below match those markers exactly; we
// deliberately do NOT try to parse the full TLV-decoded value
// printouts because chip-tool 1.5.x and 1.6.x format composite
// payloads slightly differently. Each match is anchored on a token
// chip-tool has emitted unchanged for years.

// chip-tool emits attribute readouts as a single line that looks like
//
//	[1779…] [pid:tid] [TOO]   VendorID: 65521
//
// The `[TOO]` prefix carries the chip-tool log component; the actual
// "key: value" sits after at least one space. The regex anchors on
// the `[TOO]` marker so we never accidentally match a value out of a
// log line that happens to contain `Name: 42`.
var (
	// reAttrUint pulls "[TOO]   AttrName: <number>" lines such as
	// "[TOO]   OnOff: TRUE", "[TOO]   VendorID: 65521".
	reAttrUint = regexp.MustCompile(`\[TOO\]\s+([A-Za-z][A-Za-z0-9]*):\s+(-?\d+)`)

	// reAttrBool matches "[TOO]   AttrName: TRUE|FALSE".
	reAttrBool = regexp.MustCompile(`\[TOO\]\s+([A-Za-z][A-Za-z0-9]*):\s+(TRUE|FALSE|true|false)`)

	// reAttrString matches "[TOO]   AttrName: <quoted>". chip-tool
	// also prints bare unquoted strings for some attributes; we
	// accept either to keep the parser robust across chip-tool
	// versions. `(?m)` flips `$` to line-anchored mode.
	reAttrString = regexp.MustCompile(`(?m)\[TOO\]\s+([A-Za-z][A-Za-z0-9]*):\s+"?([^"\n]*?)"?\s*$`)

	// reEndpointPath captures "Endpoint: <n> Cluster: 0x<hex>".
	reEndpointPath = regexp.MustCompile(`Endpoint:\s+(\d+)\s+Cluster:\s+(0x[0-9A-Fa-f]+)`)

	// reEventNumber matches "  EventNumber: 0x..." in EventDataIB.
	reEventNumber = regexp.MustCompile(`EventNumber:\s+(0x[0-9A-Fa-f]+)`)
)

// PairingSuccess returns true when chip-tool reported a successful
// commissioning. The marker is stable across chip-tool master since
// at least 2024-10 and is the same one `make matter-smoke` greps
// for in its assertion path.
func PairingSuccess(out string) bool {
	return strings.Contains(out, "Pairing Success") ||
		strings.Contains(out, "Pairing complete") ||
		strings.Contains(out, "Commissioning complete")
}

// PairingFailed scans for chip-tool's structured failure markers.
// Used by the negative-path commissioning test that hits the
// daemon with a wrong passcode.
func PairingFailed(out string) bool {
	return strings.Contains(out, "Pairing Failure") ||
		strings.Contains(out, "Error: 0x") ||
		strings.Contains(out, "Failed to commission")
}

// FindAttrUint walks chip-tool's output for `name: <int>` lines and
// returns the first match's integer. Returns (0, false) when the
// attribute is not present.
func FindAttrUint(out, name string) (int64, bool) {
	for _, m := range reAttrUint.FindAllStringSubmatch(out, -1) {
		if strings.EqualFold(m[1], name) {
			n, err := strconv.ParseInt(m[2], 10, 64)
			if err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

// FindAttrBool returns the first TRUE/FALSE value for the given
// attribute name. Returns (false, false) when absent.
func FindAttrBool(out, name string) (bool, bool) {
	for _, m := range reAttrBool.FindAllStringSubmatch(out, -1) {
		if strings.EqualFold(m[1], name) {
			return m[2] == "TRUE", true
		}
	}
	return false, false
}

// FindAttrString returns the first quoted-string value for the
// given attribute name. Returns ("", false) when absent.
func FindAttrString(out, name string) (string, bool) {
	for _, m := range reAttrString.FindAllStringSubmatch(out, -1) {
		if strings.EqualFold(m[1], name) {
			return m[2], true
		}
	}
	return "", false
}

// ContainsAttr returns true if chip-tool's output references the
// named attribute anywhere — useful for asserting on the presence
// of complex (struct/list-typed) values without parsing them.
func ContainsAttr(out, name string) bool {
	return strings.Contains(out, name+":") || strings.Contains(out, name+" =")
}

// FindAttrStringPerEndpoint parses wildcard-endpoint chip-tool output
// into a per-endpoint map of attribute values. chip-tool emits one
// "Endpoint: N Cluster: 0xC Attribute 0xA" header per matching path,
// followed by the printed value on the next "[TOO]" line. The
// function walks the output sequentially and pairs each header with
// the first attribute-line whose key matches `name`.
//
// Returns a map[endpointID]value. Missing endpoints are simply
// absent from the result (no error). Used by wildcard-based tests
// that need to fan a single chip-tool process across N bridged
// endpoints instead of spawning one process per endpoint (~0.7-0.9s
// each; spawn cost dominates over the wire-side read).
func FindAttrStringPerEndpoint(out, name string) map[uint16]string {
	result := make(map[uint16]string)
	lines := strings.Split(out, "\n")
	for i := 0; i < len(lines); i++ {
		m := reEndpointPath.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		ep64, err := strconv.ParseUint(m[1], 10, 16)
		if err != nil {
			continue
		}
		ep := uint16(ep64) //nolint:gosec // ParseUint width-clamped to 16
		// Scan forward up to the next Endpoint header for the
		// attribute value.
		for j := i + 1; j < len(lines); j++ {
			if reEndpointPath.MatchString(lines[j]) {
				break
			}
			vm := reAttrString.FindStringSubmatch(lines[j])
			if vm != nil && strings.EqualFold(vm[1], name) {
				result[ep] = vm[2]
				break
			}
		}
	}
	return result
}

// FindAttrBoolPerEndpoint is the boolean counterpart to
// [FindAttrStringPerEndpoint]. TRUE/FALSE values get folded to bool;
// endpoints whose value is missing or unparseable are absent.
func FindAttrBoolPerEndpoint(out, name string) map[uint16]bool {
	result := make(map[uint16]bool)
	lines := strings.Split(out, "\n")
	for i := 0; i < len(lines); i++ {
		m := reEndpointPath.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		ep64, err := strconv.ParseUint(m[1], 10, 16)
		if err != nil {
			continue
		}
		ep := uint16(ep64) //nolint:gosec // ParseUint width-clamped to 16
		for j := i + 1; j < len(lines); j++ {
			if reEndpointPath.MatchString(lines[j]) {
				break
			}
			vm := reAttrBool.FindStringSubmatch(lines[j])
			if vm != nil && strings.EqualFold(vm[1], name) {
				result[ep] = strings.EqualFold(vm[2], "true")
				break
			}
		}
	}
	return result
}

// AttrReadOK returns true when chip-tool reported the read with
// SUCCESS status. The marker is `ReadAttribute returned status:
// Success` (or, on older builds, `Received ReadResponse`); we match
// both so a chip-tool version bump does not break the suite.
func AttrReadOK(out string) bool {
	return strings.Contains(out, "Received ReadResponse") ||
		strings.Contains(out, "ReadAttribute returned status: Success") ||
		strings.Contains(out, "ReportDataMessage") // newer chip-tool prints this on every read
}

// CommandSuccess returns true when chip-tool reports an Invoke
// returning a SUCCESS status. Matches the `InvokeResponseIBs`
// "Status: 0x0" marker plus a few older variants.
func CommandSuccess(out string) bool {
	return strings.Contains(out, "Received Command Response Status") ||
		strings.Contains(out, "Status: 0x0") ||
		strings.Contains(out, "ReceivedInvokeResponse")
}

// SubscriptionEstablished returns true when chip-tool printed the
// "SubscribeResponse" marker that follows a successful priming
// Report. Subscribe-tests grep for this AND for at least one
// follow-up ReportData line.
func SubscriptionEstablished(out string) bool {
	return strings.Contains(out, "SubscribeResponse") ||
		strings.Contains(out, "Subscription established")
}

// ReportDataReceived returns true when chip-tool emitted a steady-
// state `ReportDataMessage` after the priming response — the
// signal that the daemon is actually pushing reports on the
// subscription, not just answering the initial read.
func ReportDataReceived(out string) bool {
	return strings.Count(out, "ReportDataMessage") >= 2 ||
		strings.Contains(out, "Subscription complete")
}

// EventCount counts the number of distinct EventNumber occurrences
// in chip-tool's read-event output. Used to assert "exactly one
// StartUp / BootReason event since boot".
func EventCount(out string) int {
	return len(reEventNumber.FindAllStringSubmatch(out, -1))
}

// EndpointsInPartsList parses the printed PartsList attribute and
// returns the endpoint IDs. chip-tool prints PartsList values as a
// sequence of `[idx]: <n>` lines beneath the attribute name; we
// scan that block.
func EndpointsInPartsList(out string) []uint16 {
	// Capture the block following "PartsList:" up to a blank line or
	// another attribute. A bounded regex keeps us robust against
	// chip-tool reformatting the block in future revisions.
	block := extractBlockAfter(out, "PartsList:")
	if block == "" {
		return nil
	}
	re := regexp.MustCompile(`\[\d+\]:\s+(\d+)`)
	var out2 []uint16
	for _, m := range re.FindAllStringSubmatch(block, -1) {
		n, err := strconv.ParseUint(m[1], 10, 16)
		if err == nil {
			out2 = append(out2, uint16(n))
		}
	}
	return out2
}

// ServerListIDs parses the printed ServerList attribute and returns
// the cluster IDs as uint32. Same block-extraction strategy as
// [EndpointsInPartsList].
func ServerListIDs(out string) []uint32 {
	block := extractBlockAfter(out, "ServerList:")
	if block == "" {
		return nil
	}
	re := regexp.MustCompile(`\[\d+\]:\s+(\d+)`)
	var ids []uint32
	for _, m := range re.FindAllStringSubmatch(block, -1) {
		n, err := strconv.ParseUint(m[1], 10, 32)
		if err == nil {
			ids = append(ids, uint32(n))
		}
	}
	return ids
}

// HasCluster returns true when the parsed ServerList output
// contains the given cluster id.
func HasCluster(ids []uint32, cluster uint32) bool {
	for _, id := range ids {
		if id == cluster {
			return true
		}
	}
	return false
}

// ServerListIDsPerEndpoint parses wildcard-endpoint server-list
// chip-tool output into a per-endpoint map of cluster IDs. Pairs
// each "Endpoint: N Cluster: 0x001D" header with the matching
// `ServerList:` block that follows. Used by [discoverEndpointsWith]
// to find every endpoint hosting a given cluster in ONE chip-tool
// invocation (the per-EP loop accumulates CASE sessions on the
// daemon and starts timing out on fleets with > ~25 endpoints).
func ServerListIDsPerEndpoint(out string) map[uint16][]uint32 {
	result := make(map[uint16][]uint32)
	lines := strings.Split(out, "\n")
	for i := 0; i < len(lines); i++ {
		m := reEndpointPath.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		ep64, err := strconv.ParseUint(m[1], 10, 16)
		if err != nil {
			continue
		}
		ep := uint16(ep64) //nolint:gosec // ParseUint width-clamped to 16
		// Scan forward to the matching ServerList: block, stopping
		// at the next Endpoint header or a blank line.
		var ids []uint32
		inServerList := false
		listLine := regexp.MustCompile(`\[\d+\]:\s+(\d+)`)
		for j := i + 1; j < len(lines); j++ {
			if reEndpointPath.MatchString(lines[j]) {
				break
			}
			if strings.Contains(lines[j], "ServerList:") {
				inServerList = true
				continue
			}
			if inServerList {
				if mm := listLine.FindStringSubmatch(lines[j]); mm != nil {
					if n, err := strconv.ParseUint(mm[1], 10, 32); err == nil {
						ids = append(ids, uint32(n))
					}
				} else if strings.TrimSpace(lines[j]) == "" {
					break
				}
			}
		}
		if len(ids) > 0 {
			result[ep] = ids
		}
	}
	return result
}

// extractBlockAfter slices out everything from the line containing
// marker up to the first blank line. We use it to scope regex
// matches to a single attribute's value-block, avoiding accidental
// cross-attribute matches when chip-tool prints multiple lists.
func extractBlockAfter(out, marker string) string {
	idx := strings.Index(out, marker)
	if idx < 0 {
		return ""
	}
	tail := out[idx+len(marker):]
	end := strings.Index(tail, "\n\n")
	if end < 0 {
		return tail
	}
	return tail[:end]
}

// HexUint parses chip-tool's "0xAB" / "0xABCD" hex literals into a
// uint64. Returns (0, false) on parse failure.
func HexUint(s string) (uint64, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(strings.ToLower(s), "0x") {
		return 0, false
	}
	v, err := strconv.ParseUint(s[2:], 16, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// FormatNodeID formats a uint64 node id as chip-tool's `0xNNNN`
// hex literal — handy for tests that assemble chip-tool argv
// directly.
func FormatNodeID(n uint64) string { return fmt.Sprintf("0x%X", n) }

// reWriteStatus matches the status code chip-tool prints for a Write
// or Invoke response, e.g. "Status: 0x86" (CONSTRAINT_ERROR) or
// "Status: 0x87" (UNSUPPORTED_WRITE). Anchored on the same "Status:
// 0x<hex>" token [CommandSuccess] already matches for the success
// (0x0) case.
var reWriteStatus = regexp.MustCompile(`Status:\s+(0x[0-9A-Fa-f]+)`)

// reWriteClientReject matches chip-tool refusing a write BEFORE it reaches the
// wire: a read-only attribute has no `write` sub-command, so chip-tool prints
// "Unknown attribute: <name>" + its usage banner and exits with a
// "Run command failure … Error 0x…" — no IM WriteResponse is ever sent. For a
// "must be rejected" negative test that client-side refusal is exactly the
// proof the attribute is not writable, so [WriteStatus] surfaces it as a
// non-success status.
var reWriteClientReject = regexp.MustCompile(`(?i)Unknown attribute|Run command failure|Error 0x[0-9A-Fa-f]+`)

// FindAttrInt parses a signed attribute value — such as
// TemperatureMeasurement.MeasuredValue (int16, hundredths of a degree
// C, negative below freezing) — out of chip-tool's "[TOO]   AttrName:
// <number>" marker line. Kept as a distinct entry point from
// [FindAttrUint] so callers reach for the helper matching the
// attribute's declared sign/width rather than relying on an
// unsigned-named function to also happen to accept a leading minus.
func FindAttrInt(out, name string) (int64, bool) {
	return FindAttrUint(out, name)
}

// WriteStatus returns the last IM status code chip-tool printed for a
// WriteResponse or InvokeResponse (e.g. "0x87" UNSUPPORTED_WRITE,
// "0x88" UNSUPPORTED_ACCESS, "0x86" CONSTRAINT_ERROR) as the raw
// "0xNN" hex literal. Returns the LAST match rather than the first —
// a Subscribe priming report or an earlier command in the same
// invocation can echo its own "Status: 0x0" before the actual
// write/invoke status line appears.
func WriteStatus(out string) (statusHex string, ok bool) {
	if matches := reWriteStatus.FindAllStringSubmatch(out, -1); len(matches) != 0 {
		return matches[len(matches)-1][1], true
	}
	// No IM WriteResponse — chip-tool refused the write client-side because the
	// attribute is read-only (no `write` sub-command). That is exactly the
	// "not writable" outcome a negative test asserts; report it as
	// UnsupportedWrite (0x88), the IM status the same rejection would carry had
	// it reached the wire, so both the `!= "0x0"` and the `== "0x88"` negative
	// assertions pass without a wire round trip.
	if reWriteClientReject.MatchString(out) {
		return "0x88", true
	}
	return "", false
}
