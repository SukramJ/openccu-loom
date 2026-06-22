// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/restapi"
)

// HubAdapter satisfies restapi.HubIndex by projecting every
// registered Unit's hub onto one aggregated hub view.
type HubAdapter struct {
	registry *central.Registry
}

// NewHubAdapter constructs the adapter.
func NewHubAdapter(r *central.Registry) *HubAdapter {
	return &HubAdapter{registry: r}
}

// Hub returns the first central's hub, or nil when no central is
// registered yet. Retained for back-compat; prefer Hubs/HubFor.
func (a *HubAdapter) Hub() *hub.Hub {
	if a.registry == nil {
		return nil
	}
	list := a.registry.List()
	if len(list) == 0 {
		return nil
	}
	return list[0].HubModel
}

// Hubs returns every registered central's hub in stable name order,
// skipping centrals whose HubModel is nil.
func (a *HubAdapter) Hubs() []restapi.NamedHub {
	if a.registry == nil {
		return nil
	}
	list := a.registry.List()
	out := make([]restapi.NamedHub, 0, len(list))
	for _, c := range list {
		if c == nil || c.HubModel == nil {
			continue
		}
		out = append(out, restapi.NamedHub{Central: c.Name(), Hub: c.HubModel})
	}
	return out
}

// HubFor returns the named central's hub, or nil when the central is
// not registered or its HubModel is not yet initialised.
func (a *HubAdapter) HubFor(centralName string) *hub.Hub {
	if a.registry == nil {
		return nil
	}
	c, ok := a.registry.Get(centralName)
	if !ok || c == nil {
		return nil
	}
	return c.HubModel
}

// SerialSuffix delegates to the registry's central → serial-suffix mapping so
// the hub endpoints can stamp the canonical `unique_id` onto sysvar / program
// / singleton summaries. Empty string when the central is unknown.
func (a *HubAdapter) SerialSuffix(centralName string) string {
	if a.registry == nil {
		return ""
	}
	return a.registry.SerialSuffix(centralName)
}
