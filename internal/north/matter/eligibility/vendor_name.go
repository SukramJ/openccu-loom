// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package eligibility

import "fmt"

// vendorNames maps the CSA-assigned vendor id a controller writes into
// its fabric to a name an operator recognises.
//
// The table is embedded rather than fetched. matter.js resolves vendor
// names from the Distributed Compliance Ledger over HTTPS
// (packages/protocol/src/dcl/DclVendorInfoService.ts), which is the
// right shape for a tool that runs anywhere; it is the wrong shape for a
// daemon on a home network. It would need the internet to render a list
// the operator can already see in their own app, and it would tell a
// third party which controllers this house is paired with.
//
// What the table has to cover is therefore not "every vendor" but "every
// vendor that commissions a bridge", which is the same short list the
// ecosystem classifier carries. Anything else renders as its id.
var vendorNames = map[uint16]string{
	0x1349: "Apple Home",
	0x1384: "Apple Keychain",
	0x6006: "Google",
	0x1217: "Amazon Alexa",
	0x110A: "Samsung SmartThings",
	0x115F: "Aqara",
	0x134B: "Home Assistant",
	0x100B: "Signify",
	0x125D: "Tuya",
	0x1037: "NXP Semiconductors",
}

// VendorName renders a fabric's vendor id for an operator.
//
// An id the table does not carry renders as the id itself, in the form
// every Matter tool prints it, so it stays searchable. Returning nothing
// would make the row look like a defect in the bridge rather than a
// controller nobody has a name for.
func VendorName(vendorID uint16) string {
	if name, ok := vendorNames[vendorID]; ok {
		return name
	}
	return fmt.Sprintf("0x%04X", vendorID)
}
