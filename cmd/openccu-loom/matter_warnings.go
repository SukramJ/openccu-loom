// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"

	"github.com/SukramJ/go-fabric-ui/fabricui"

	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
)

// matterFindingSource is the assembler's findings registry
// (matteradapter.Assembler.Findings): the sources the bridge holds no
// endpoint for although the operator selected them.
type matterFindingSource interface {
	Findings() []matteradapter.Finding
}

// matterWarnings renders the assembler's findings as go-fabric-ui's host
// warnings (fabricui.Options.Warnings), which the Matter status and
// selection pages show. The registry is the record behind the assembler's
// log lines too, so the page and the log name the same sources.
//
// A warning's message names the device; go-fabric-ui sends it to admins
// only and gives every other caller the code alone. Messages are rendered
// in the daemon's locale, as the exposure provider's column headers are;
// without catalogues (a test wiring) the code stands in for the message.
func matterWarnings(src matterFindingSource, catalogs *i18n.Catalogs, locale string) func(context.Context) []fabricui.Warning {
	return func(context.Context) []fabricui.Warning {
		findings := src.Findings()
		out := make([]fabricui.Warning, 0, len(findings))
		for i := range findings {
			f := &findings[i]
			out = append(out, fabricui.Warning{
				Code:    string(f.Code),
				Message: matterFindingMessage(f, catalogs, locale),
				Since:   f.Since,
			})
		}
		return out
	}
}

// matterFindingMessage is one finding in the operator's words: the device's
// name and the address that tells two devices of the same name apart.
func matterFindingMessage(f *matteradapter.Finding, catalogs *i18n.Catalogs, locale string) string {
	if catalogs == nil {
		return string(f.Code)
	}
	address := f.ChannelAddress
	if address == "" {
		address = f.DeviceAddress
	}
	name := f.Name
	if name == "" {
		name = address
	}
	return catalogs.TF(locale, "matter.warning."+string(f.Code), map[string]string{
		"name":    name,
		"address": address,
		"error":   f.Err,
	})
}
