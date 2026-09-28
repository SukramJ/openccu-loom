// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SukramJ/openccu-loom/internal/warnings"
)

type warningSummary struct {
	ID       string            `json:"id"`
	Severity string            `json:"severity"`
	Central  string            `json:"central,omitempty"`
	Message  string            `json:"message_key"`
	Args     map[string]string `json:"args,omitempty"`
}

type listWarningsOut struct {
	Warnings []warningSummary `json:"warnings"`
}

func registerListWarnings(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name: "list_warnings",
		Description: "Read the active operator warnings: unhealthy or degraded health components, " +
			"error-grade incidents of the last 24h grouped per component, and per-central " +
			"service-message backlogs. Errors sort first. The same aggregate the Config UI's " +
			"Status card shows, without per-user silences.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, listWarningsOut, error) {
		active := d.Warnings.Active()
		out := listWarningsOut{Warnings: make([]warningSummary, 0, len(active))}
		for _, w := range active {
			out.Warnings = append(out.Warnings, warningSummary{
				ID:       w.ID,
				Severity: string(w.Severity),
				Central:  w.Central,
				Message:  w.MessageKey,
				Args:     w.Args,
			})
		}
		return nil, out, nil
	})
}

// Ensure the aggregator keeps satisfying the narrow facade.
var _ WarningsSource = (*warnings.Aggregator)(nil)
