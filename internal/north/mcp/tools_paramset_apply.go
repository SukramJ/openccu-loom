// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmreqctx"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

type listParamsetApplyTargetsIn struct {
	CentralName   string `json:"central_name" jsonschema:"the CCU that owns the source channel (required; must match its central)"`
	SourceChannel string `json:"source_channel" jsonschema:"the channel whose MASTER values would be applied, e.g. 0001D3C99C1234:1"`
}

type paramsetApplyTargetOut struct {
	Address       string `json:"address"`
	Name          string `json:"name,omitempty"`
	DeviceAddress string `json:"device_address,omitempty"`
	DeviceName    string `json:"device_name,omitempty"`
	DeviceModel   string `json:"device_model,omitempty"`
	InterfaceID   string `json:"interface_id,omitempty"`
}

type listParamsetApplyTargetsOut struct {
	Targets []paramsetApplyTargetOut `json:"targets" jsonschema:"channels whose stored MASTER description is identical to the source's; the source itself is not listed"`
}

type applyParamsetToChannelsIn struct {
	CentralName   string `json:"central_name" jsonschema:"the CCU that owns the source channel (required; must match its central)"`
	SourceChannel string `json:"source_channel" jsonschema:"the channel whose MASTER description the targets must match, e.g. 0001D3C99C1234:1"`
	// Values is decoded JSON; each value is coerced against every
	// target's own stored description before its write.
	Values  map[string]any `json:"values" jsonschema:"the MASTER parameter→value map to apply"`
	Targets []string       `json:"targets" jsonschema:"target channel addresses; list_paramset_apply_targets names the eligible ones"`
	DryRun  bool           `json:"dry_run,omitempty" jsonschema:"when true, run every gate but write nothing"`
	// EditToken is the source channel's MASTER edit-lock token, the same
	// lock the REST apply-to route demands.
	EditToken string `json:"edit_token,omitempty" jsonschema:"edit-lock token for the SOURCE channel's MASTER session; obtain it with open_edit_session"`
}

type paramsetApplyOutcomeOut struct {
	Address string                  `json:"address"`
	Status  string                  `json:"status" jsonschema:"applied, would_apply, refused (a gate rejected the target before any write) or failed (the write itself failed)"`
	Reason  string                  `json:"reason,omitempty"`
	Result  *paramsetWriteReportOut `json:"result,omitempty" jsonschema:"post-write read-back of an applied target"`
}

type applyParamsetToChannelsOut struct {
	Outcomes []paramsetApplyOutcomeOut `json:"outcomes" jsonschema:"one outcome per target, in request order"`
}

// requireSourceOwner applies the multi-CCU ownership check the paramset-apply
// tools perform: central_name is explicit and authoritative, never a
// fallback.
func requireSourceOwner(d Deps, central, source string) error {
	if central == "" || source == "" {
		return errors.New("central_name and source_channel are required")
	}
	if owner := d.Devices.CentralOf(hmtypes.DeviceAddress(source)); owner != central {
		return fmt.Errorf("device %s belongs to central %q, not %q", source, owner, central)
	}
	return nil
}

func registerListParamsetApplyTargets(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name: "list_paramset_apply_targets",
		Description: "List the channels a source channel's MASTER configuration can be applied to: every channel of the same " +
			"central and interface whose stored MASTER paramset description is identical to the source's. Channel type " +
			"or device model equality is not enough — only description identity qualifies.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listParamsetApplyTargetsIn) (*mcpsdk.CallToolResult, listParamsetApplyTargetsOut, error) {
		central := strings.TrimSpace(in.CentralName)
		source := strings.TrimSpace(in.SourceChannel)
		if err := requireSourceOwner(d, central, source); err != nil {
			return nil, listParamsetApplyTargetsOut{}, err
		}
		targets, err := d.ParamsetApply.ApplyTargets(ctx, source)
		if err != nil {
			return nil, listParamsetApplyTargetsOut{}, fmt.Errorf("list apply targets: %w", err)
		}
		out := listParamsetApplyTargetsOut{Targets: make([]paramsetApplyTargetOut, 0, len(targets))}
		for _, t := range targets {
			out.Targets = append(out.Targets, paramsetApplyTargetOut{
				Address:       t.Address,
				Name:          t.Name,
				DeviceAddress: t.DeviceAddress,
				DeviceName:    t.DeviceName,
				DeviceModel:   t.DeviceModel,
				InterfaceID:   t.InterfaceID,
			})
		}
		return nil, out, nil
	})
}

// registerApplyParamsetToChannels exposes the multi-channel MASTER apply. It
// calls the same domain service as the REST apply-to route and holds the same
// gate: the source channel's MASTER edit lock.
func registerApplyParamsetToChannels(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name: "apply_paramset_to_channels",
		Description: "Apply MASTER values to several channels whose stored MASTER description is identical to the source " +
			"channel's. Each target is re-checked and validated before its own write; a refused or failed target never " +
			"stops the rest. Requires central_name and the source channel's MASTER edit lock (open_edit_session). " +
			"Use dry_run to see what would happen without writing.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in applyParamsetToChannelsIn) (*mcpsdk.CallToolResult, applyParamsetToChannelsOut, error) {
		central := strings.TrimSpace(in.CentralName)
		source := strings.TrimSpace(in.SourceChannel)
		if err := requireSourceOwner(d, central, source); err != nil {
			return nil, applyParamsetToChannelsOut{}, err
		}
		if len(in.Values) == 0 || len(in.Targets) == 0 {
			return nil, applyParamsetToChannelsOut{}, errors.New("values and targets must not be empty")
		}
		if d.EditLocks != nil {
			lockKey := "channel:" + source + ":" + string(hmenum.ParamsetKeyMaster)
			if !d.EditLocks.Verify(lockKey, strings.TrimSpace(in.EditToken)) {
				return nil, applyParamsetToChannelsOut{}, fmt.Errorf(
					"edit lock required for MASTER apply from %s; open an edit session and pass edit_token", source,
				)
			}
		}
		// Refuse targets whose MASTER edit lock is currently held —
		// mirroring the REST route: the batch holds only the source lock,
		// and writing under someone's open edit session would clobber
		// their staged values. Refused targets keep their position.
		targets := make([]string, 0, len(in.Targets))
		locked := make(map[string]bool, len(in.Targets))
		for _, raw := range in.Targets {
			t := strings.TrimSpace(raw)
			if d.EditLocks != nil && d.EditLocks.Held("channel:"+t+":"+string(hmenum.ParamsetKeyMaster)) {
				locked[t] = true
				continue
			}
			targets = append(targets, t)
		}
		ctx = hmreqctx.WithOperation(ctx, "mcp:paramset-apply")
		var outcomes []interfaces.ParamsetApplyOutcome
		if len(targets) > 0 {
			var err error
			outcomes, err = d.ParamsetApply.ApplyToChannels(ctx, source, in.Values, targets, in.DryRun)
			if err != nil {
				return nil, applyParamsetToChannelsOut{}, fmt.Errorf("apply paramset: %w", err)
			}
		}
		byAddress := make(map[string]paramsetApplyOutcomeOut, len(outcomes))
		for _, o := range outcomes {
			byAddress[o.Address] = paramsetApplyOutcomeOut{
				Address: o.Address, Status: o.Status, Reason: o.Reason, Result: writeReportOut(o.Result),
			}
		}
		out := applyParamsetToChannelsOut{Outcomes: make([]paramsetApplyOutcomeOut, 0, len(in.Targets))}
		for _, raw := range in.Targets {
			t := strings.TrimSpace(raw)
			if locked[t] {
				out.Outcomes = append(out.Outcomes, paramsetApplyOutcomeOut{
					Address: t, Status: interfaces.ApplyRefused,
					Reason: "target channel has an open edit session",
				})
				continue
			}
			out.Outcomes = append(out.Outcomes, byAddress[t])
		}
		return nil, out, nil
	})
}
