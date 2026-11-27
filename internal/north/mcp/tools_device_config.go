// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/pkg/hmreqctx"
)

// ConfigCacheClearer discards an interface process's cached configuration of
// a device — the slice of the device-admin port clear_device_config_cache
// needs.
type ConfigCacheClearer interface {
	ClearConfigCache(ctx context.Context, address string) error
}

type repairDeviceConfigIn struct {
	CentralName string   `json:"central_name,omitempty" jsonschema:"the CCU that holds the device; when given, the call acts on that CCU's device and fails if it holds none"`
	Address     string   `json:"address" jsonschema:"the device address, e.g. 0001D3C99C1234"`
	Channels    []string `json:"channels,omitempty" jsonschema:"restrict the repair to these channel addresses; every MASTER-bearing channel of the device otherwise"`
	// DryRun is a pointer so an omitted argument keeps the safe default.
	DryRun *bool `json:"dry_run,omitempty" jsonschema:"defaults to true: read and report only; pass false to write the repaired configuration"`
}

type configRepairCorrectionOut struct {
	Parameter string `json:"parameter"`
	// Stored and Corrected are wire values of the parameter's own type
	// (bool, number or string), decoded before type dispatch.
	Stored    any    `json:"stored"`
	Corrected any    `json:"corrected" jsonschema:"null when the parameter is dropped from the rewrite"`
	Reason    string `json:"reason,omitempty"`
}

type configRepairOutcomeOut struct {
	Channel     string                      `json:"channel"`
	Status      string                      `json:"status" jsonschema:"clean, repaired, would_repair, foreign_parameters or failed"`
	Corrections []configRepairCorrectionOut `json:"corrections"`
	Foreign     []string                    `json:"foreign" jsonschema:"stored parameter names the description does not carry; no paramset write can remove them"`
	Error       string                      `json:"error,omitempty"`
	Result      *paramsetWriteReportOut     `json:"result,omitempty" jsonschema:"post-write read-back of a performed rewrite"`
}

type repairDeviceConfigOut struct {
	Outcomes []configRepairOutcomeOut `json:"outcomes" jsonschema:"one outcome per channel"`
}

type clearDeviceConfigCacheIn struct {
	CentralName string `json:"central_name,omitempty" jsonschema:"the CCU that holds the device; when given, the call acts on that CCU's device and fails if it holds none"`
	Address     string `json:"address" jsonschema:"the device address"`
}

type clearDeviceConfigCacheOut struct {
	OK bool `json:"ok"`
}

// bindDeviceCentral binds a tool call to the central it names, with the
// semantics of the REST device routes (middleware.DeviceCentral): an unknown
// central, or one that does not hold the address, is refused; otherwise the
// returned context carries [hmreqctx.WithCentralScope], which every address
// lookup of the domain services behind these tools honours.
//
// Checking the name against the first central that holds the address is not
// enough: device addresses repeat across CCUs, so such a check refuses the
// second CCU's device and lets the call through to the first one's. An empty
// central leaves the call unbound. A nil device lister refuses a call that
// names a central rather than serving it unbound.
func bindDeviceCentral(ctx context.Context, d Deps, central, address string) (context.Context, error) {
	if central == "" {
		return ctx, nil
	}
	if d.Devices == nil {
		return ctx, errors.New("central binding unavailable: no device model")
	}
	known, holds := d.Devices.HoldsDevice(central, address)
	switch {
	case !known:
		return ctx, fmt.Errorf("no central named %q", central)
	case !holds:
		return ctx, fmt.Errorf("central %q holds no device %s", central, address)
	}
	return hmreqctx.WithCentralScope(ctx, central), nil
}

// registerRepairDeviceConfig exposes the stored-configuration repair. Its
// REST twin is admin-only, so the tool re-checks the caller's role.
func registerRepairDeviceConfig(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name: "repair_device_config",
		Description: "Rebuild a device's stored MASTER configuration from its own paramset descriptions: invalid stored " +
			"values are clamped, coerced or replaced by the description DEFAULT and each channel is rewritten as one " +
			"full paramset. dry_run defaults to true (report only). Stored entries the description does not carry are " +
			"reported as foreign; no write removes them. Requires an admin identity.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in repairDeviceConfigIn) (*mcpsdk.CallToolResult, repairDeviceConfigOut, error) {
		if !callerIsAdmin(ctx) {
			return nil, repairDeviceConfigOut{}, errors.New("config repair is admin-only")
		}
		address := strings.TrimSpace(in.Address)
		if address == "" {
			return nil, repairDeviceConfigOut{}, errors.New("address is required")
		}
		ctx, err := bindDeviceCentral(ctx, d, strings.TrimSpace(in.CentralName), address)
		if err != nil {
			return nil, repairDeviceConfigOut{}, err
		}
		channels := make([]string, 0, len(in.Channels))
		for _, ch := range in.Channels {
			channels = append(channels, strings.TrimSpace(ch))
		}
		dryRun := true
		if in.DryRun != nil {
			dryRun = *in.DryRun
		}
		ctx = hmreqctx.WithOperation(ctx, "mcp:config-repair")
		outcomes, err := d.ConfigRepair.RepairDeviceConfig(ctx, address, channels, dryRun)
		if err != nil {
			return nil, repairDeviceConfigOut{}, fmt.Errorf("repair device config: %w", err)
		}
		out := repairDeviceConfigOut{Outcomes: make([]configRepairOutcomeOut, 0, len(outcomes))}
		for _, o := range outcomes {
			item := configRepairOutcomeOut{
				Channel:     o.Channel,
				Status:      o.Status,
				Corrections: make([]configRepairCorrectionOut, 0, len(o.Corrections)),
				Foreign:     append([]string{}, o.Foreign...),
				Error:       o.Error,
				Result:      writeReportOut(o.Result),
			}
			for _, c := range o.Corrections {
				item.Corrections = append(item.Corrections, configRepairCorrectionOut{
					Parameter: c.Parameter, Stored: c.Stored, Corrected: c.Corrected, Reason: c.Reason,
				})
			}
			out.Outcomes = append(out.Outcomes, item)
		}
		return nil, out, nil
	})
}

// registerClearDeviceConfigCache exposes the BidCos configuration-cache
// clear. Its REST twin is admin-only, so the tool re-checks the caller's
// role, and it records the same change-log row the REST handler does.
func registerClearDeviceConfigCache(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name: "clear_device_config_cache",
		Description: "Make the device's interface process forget its cached configuration so the next configuration " +
			"read or transfer rebuilds it. BidCos-RF and BidCos-Wired devices only. Requires an admin identity.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in clearDeviceConfigCacheIn) (*mcpsdk.CallToolResult, clearDeviceConfigCacheOut, error) {
		if !callerIsAdmin(ctx) {
			return nil, clearDeviceConfigCacheOut{}, errors.New("clearing the config cache is admin-only")
		}
		address := strings.TrimSpace(in.Address)
		if address == "" {
			return nil, clearDeviceConfigCacheOut{}, errors.New("address is required")
		}
		ctx, err := bindDeviceCentral(ctx, d, strings.TrimSpace(in.CentralName), address)
		if err != nil {
			return nil, clearDeviceConfigCacheOut{}, err
		}
		if err := d.ConfigCache.ClearConfigCache(ctx, address); err != nil {
			return nil, clearDeviceConfigCacheOut{}, fmt.Errorf("clear config cache: %w", err)
		}
		if d.Audit != nil {
			audit.RecordFrom(ctx, d.Audit, audit.Entry{
				Timestamp:     time.Now().UTC(),
				User:          callerSubject(ctx),
				Action:        audit.ActionDeviceConfigCacheClear,
				DeviceAddress: address,
				Note:          "via mcp",
			})
		}
		return nil, clearDeviceConfigCacheOut{OK: true}, nil
	})
}
