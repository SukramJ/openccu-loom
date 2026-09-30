// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mcp

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SukramJ/openccu-loom/internal/audit"
)

// RFInterfaceAssigner assigns a BidCos-RF device to an RF gateway — the
// slice of the device-admin port assign_rf_interface needs.
type RFInterfaceAssigner interface {
	AssignRFInterface(ctx context.Context, address, interfaceAddress string, roaming bool) error
}

// maxReceiverMarginDB mirrors the REST bound on margin_db: beyond it no
// realistic pair of readings clears the margin.
const maxReceiverMarginDB = 30

type getRSSIMatrixIn struct {
	CentralName string `json:"central_name,omitempty" jsonschema:"restrict to one CCU; every central otherwise"`
}

type rssiMatrixPartnerOut struct {
	Address string `json:"address"`
	RxDBm   *int   `json:"rx_dbm" jsonschema:"strength at which the device hears this partner; null when unknown"`
	TxDBm   *int   `json:"tx_dbm" jsonschema:"strength at which this partner hears the device; null when unknown"`
}

type rssiMatrixDeviceOut struct {
	Address  string                 `json:"address"`
	Name     string                 `json:"name,omitempty"`
	Partners []rssiMatrixPartnerOut `json:"partners"`
}

type rssiMatrixInterfaceOut struct {
	Address     string `json:"address"`
	Description string `json:"description"`
	Connected   bool   `json:"connected"`
	Default     bool   `json:"default"`
	DutyCycle   int    `json:"duty_cycle"`
}

type rssiMatrixCentralOut struct {
	Central     string                   `json:"central"`
	InterfaceID string                   `json:"interface_id"`
	Interfaces  []rssiMatrixInterfaceOut `json:"interfaces" jsonschema:"the central's RF gateways"`
	Devices     []rssiMatrixDeviceOut    `json:"devices"`
	Error       string                   `json:"error,omitempty" jsonschema:"why this central's matrix could not be read; its rows are then empty"`
}

type getRSSIMatrixOut struct {
	Items []rssiMatrixCentralOut `json:"items"`
}

type getReceiverProposalIn struct {
	CentralName string `json:"central_name,omitempty" jsonschema:"restrict to one CCU; every central otherwise"`
	// MarginDB is a pointer so an omitted argument takes the default
	// rather than reading as zero.
	MarginDB *int `json:"margin_db,omitempty" jsonschema:"minimum dB advantage before a switch is proposed, 0..30, default 6"`
}

type receiverProposalOut struct {
	Address          string `json:"address"`
	Name             string `json:"name,omitempty"`
	Central          string `json:"central"`
	CurrentInterface string `json:"current_interface"`
	BestInterface    string `json:"best_interface"`
	CurrentRxDBm     *int   `json:"current_rx_dbm"`
	BestRxDBm        *int   `json:"best_rx_dbm"`
	Roaming          bool   `json:"roaming"`
	Verdict          string `json:"verdict" jsonschema:"switch, keep, marginal, unheard, unmeasured or roaming"`
}

type getReceiverProposalOut struct {
	Items []receiverProposalOut `json:"items"`
}

type assignRFInterfaceIn struct {
	CentralName      string `json:"central_name,omitempty" jsonschema:"the CCU that owns the device; when given it must match the device's central"`
	Address          string `json:"address" jsonschema:"the BidCos-RF device address"`
	InterfaceAddress string `json:"interface_address" jsonschema:"serial of the RF gateway to assign"`
	Roaming          bool   `json:"roaming,omitempty" jsonschema:"allow the daemon to re-assign the device by signal strength"`
}

type assignRFInterfaceOut struct {
	OK bool `json:"ok"`
}

// registerGetRSSIMatrix exposes the BidCos-RF pairwise reception matrix.
// Its REST twin is admin-only, so the tool re-checks the caller's role.
func registerGetRSSIMatrix(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name: "get_rssi_matrix",
		Description: "Read the BidCos-RF pairwise reception matrix live from every central: per device, each partner it " +
			"exchanges frames with, with rx_dbm (device hears partner) and tx_dbm (partner hears device); null when " +
			"unknown. The central's RF gateways appear as partners and are listed under interfaces. HmIP has no " +
			"pairwise matrix. Requires an admin identity.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in getRSSIMatrixIn) (*mcpsdk.CallToolResult, getRSSIMatrixOut, error) {
		if !callerIsAdmin(ctx) {
			return nil, getRSSIMatrixOut{}, errors.New("the RSSI matrix is admin-only")
		}
		central := strings.TrimSpace(in.CentralName)
		centrals, err := d.RSSIMatrix.RSSIMatrix(ctx)
		if err != nil {
			return nil, getRSSIMatrixOut{}, fmt.Errorf("rssi matrix: %w", err)
		}
		out := getRSSIMatrixOut{Items: make([]rssiMatrixCentralOut, 0, len(centrals))}
		for _, c := range centrals {
			if central != "" && c.Central != central {
				continue
			}
			item := rssiMatrixCentralOut{
				Central:     c.Central,
				InterfaceID: c.InterfaceID,
				Interfaces:  make([]rssiMatrixInterfaceOut, 0, len(c.Interfaces)),
				Devices:     make([]rssiMatrixDeviceOut, 0, len(c.Devices)),
				Error:       c.Error,
			}
			for _, g := range c.Interfaces {
				item.Interfaces = append(item.Interfaces, rssiMatrixInterfaceOut(g))
			}
			for _, dev := range c.Devices {
				row := rssiMatrixDeviceOut{
					Address:  dev.Address,
					Name:     dev.Name,
					Partners: make([]rssiMatrixPartnerOut, 0, len(dev.Partners)),
				}
				for _, p := range dev.Partners {
					row.Partners = append(row.Partners, rssiMatrixPartnerOut(p))
				}
				item.Devices = append(item.Devices, row)
			}
			out.Items = append(out.Items, item)
		}
		return nil, out, nil
	})
}

// registerGetReceiverProposal exposes the best-gateway dry run. Nothing is
// written; its REST twin is admin-only, so the tool re-checks the role.
func registerGetReceiverProposal(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name: "get_receiver_proposal",
		Description: "Dry run over the BidCos-RF reception matrix: per BidCos-RF device, the RF gateway that hears it " +
			"best compared with the assigned one. A switch is proposed only when the best gateway is at least " +
			"margin_db (default 6) stronger. Nothing is written. Requires an admin identity.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in getReceiverProposalIn) (*mcpsdk.CallToolResult, getReceiverProposalOut, error) {
		if !callerIsAdmin(ctx) {
			return nil, getReceiverProposalOut{}, errors.New("the receiver proposal is admin-only")
		}
		margin := -1 // the service default
		if in.MarginDB != nil {
			if *in.MarginDB < 0 || *in.MarginDB > maxReceiverMarginDB {
				return nil, getReceiverProposalOut{}, errors.New("margin_db must be between 0 and 30")
			}
			margin = *in.MarginDB
		}
		central := strings.TrimSpace(in.CentralName)
		proposals, err := d.RSSIMatrix.ReceiverProposal(ctx, margin)
		if err != nil {
			return nil, getReceiverProposalOut{}, fmt.Errorf("receiver proposal: %w", err)
		}
		out := getReceiverProposalOut{Items: make([]receiverProposalOut, 0, len(proposals))}
		for _, p := range proposals {
			if central != "" && p.Central != central {
				continue
			}
			out.Items = append(out.Items, receiverProposalOut(p))
		}
		return nil, out, nil
	})
}

// registerAssignRFInterface exposes the BidCos-RF gateway assignment. Its
// REST twin is admin-only, so the tool re-checks the caller's role, and it
// records the same change-log row the REST handler does.
func registerAssignRFInterface(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name: "assign_rf_interface",
		Description: "Pin a BidCos-RF device to the RF gateway with the given serial, or with roaming true let the " +
			"BidCos-RF daemon re-assign it by signal strength. BidCos-RF devices only. Requires an admin identity.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in assignRFInterfaceIn) (*mcpsdk.CallToolResult, assignRFInterfaceOut, error) {
		if !callerIsAdmin(ctx) {
			return nil, assignRFInterfaceOut{}, errors.New("assigning an RF interface is admin-only")
		}
		address := strings.TrimSpace(in.Address)
		gateway := strings.TrimSpace(in.InterfaceAddress)
		if address == "" || gateway == "" {
			return nil, assignRFInterfaceOut{}, errors.New("address and interface_address are required")
		}
		if err := requireDeviceOwner(d, strings.TrimSpace(in.CentralName), address); err != nil {
			return nil, assignRFInterfaceOut{}, err
		}
		if err := d.RFInterface.AssignRFInterface(ctx, address, gateway, in.Roaming); err != nil {
			return nil, assignRFInterfaceOut{}, fmt.Errorf("assign rf interface: %w", err)
		}
		if d.Audit != nil {
			d.Audit.Record(audit.Entry{
				Timestamp:     time.Now().UTC(),
				User:          callerSubject(ctx),
				Action:        audit.ActionDeviceRFInterfaceAssign,
				DeviceAddress: address,
				Note:          "via mcp: interface " + gateway + ", roaming " + strconv.FormatBool(in.Roaming),
			})
		}
		return nil, assignRFInterfaceOut{OK: true}, nil
	})
}
