// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package mcp exposes the OpenCCU-Loom domain to LLM agents over the
// Model Context Protocol, as a north-bound adapter (ADR 0025). It is a
// thin projection of the same domain the REST surface serves: every
// tool is scoped per central, reads are always available, and writes
// are registered only when the operator opts in twice (Enabled +
// AllowWrites). Authorization is enforced by the REST listener the
// Streamable-HTTP handler mounts behind; the adapter holds no privilege
// path of its own.
package mcp

import (
	"context"
	"net/http"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// CentralLister enumerates the configured CCUs — the scoping dimension
// every central-touching tool names explicitly (ADR 0002).
type CentralLister interface {
	Names() []string
}

// DeviceLister is the read surface over the device model. It mirrors
// the REST DeviceIndex contract so both adapters project the same data.
type DeviceLister interface {
	Devices() []*device.Device
	Device(address string) (*device.Device, bool)
	// CentralOf returns the owning CCU name, or "" when the device is
	// unknown.
	CentralOf(address string) string
}

// ValueWriter pushes a value to the CCU. Same contract as the REST
// DataPointWriter; only reached by the write-gated set_datapoint tool.
type ValueWriter interface {
	SetValue(
		ctx context.Context,
		address string,
		parameter hmenum.Parameter,
		value any,
		priority hmenum.CommandPriority,
	) error
}

// ParamsetService reads and writes device paramsets (MASTER / VALUES).
// Same contract as the REST ParamsetService; the read half backs
// read_paramset, the write half backs the gated write_paramset.
type ParamsetService interface {
	GetParamset(ctx context.Context, address string, key hmenum.ParamsetKey) (map[string]any, error)
	PutParamset(ctx context.Context, address string, key hmenum.ParamsetKey, values map[string]any) error
}

// HealthReader exposes the daemon's component-health view (CCU
// connectivity and subsystem status). Same contract as the REST
// HealthReader.
type HealthReader interface {
	Overall() health.Status
	Snapshot() []health.Component
}

// HubResolver resolves a central's hub model by name — the seam the
// gated trigger_program tool uses to find and run a CCU program.
// *central.Registry satisfies it.
type HubResolver interface {
	HubFor(centralName string) *hub.Hub
}

// IncidentsReader projects the cross-central reliability incident journal —
// the same enriched, source-tagged list the REST GET /incidents handler
// serves. *adapter.IncidentsStoreReader satisfies it, so MCP and REST
// expose byte-identical incident data.
type IncidentsReader interface {
	Incidents() []hmapi.Incident
}

// Deps is the wiring surface. Writer may be nil (no writes available).
// Audit may be nil (no change-log surface); when present it serves both
// the read tool (List) and records MCP-origin writes (Record).
type Deps struct {
	Centrals  CentralLister
	Devices   DeviceLister
	Writer    ValueWriter
	Paramsets ParamsetService
	Health    HealthReader
	Hubs      HubResolver
	Audit     audit.Recorder
	Incidents IncidentsReader
	// Alarm / AlarmControl project the alarm system; Security projects
	// the Security & Safety domain. Each is optional: a nil seam leaves
	// its tools unregistered rather than advertising a tool that
	// cannot answer.
	Alarm        AlarmReader
	AlarmControl AlarmController
	Security     SecurityReader
	AllowWrites  bool
	Version      string
}

// NewServer builds the MCP server and registers the tool set. Read
// tools are always registered (each gated on its own dependency being
// wired); write tools only when AllowWrites is set, and each still gates
// on its own dependency so a partial wiring never exposes a half-tool.
func NewServer(d Deps) *mcpsdk.Server {
	version := d.Version
	if version == "" {
		version = "0.0.0"
	}
	s := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "openccu-loom",
		Version: version,
	}, nil)
	registerReadTools(s, d)
	if d.AllowWrites {
		registerWriteTools(s, d)
	}
	return s
}

// Handler returns the Streamable-HTTP handler to mount on the REST
// listener (e.g. at /mcp). The server is built once and shared across
// sessions; the SDK manages per-session state.
func Handler(d Deps) http.Handler {
	srv := NewServer(d)
	return mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return srv },
		nil,
	)
}
