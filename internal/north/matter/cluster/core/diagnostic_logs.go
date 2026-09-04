// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/matterport"
)

// DiagnosticLogs implements the Matter DiagnosticLogs cluster (0x0032)
// per Matter Core Specification 1.5.1 §11.11. Mandatory on the Root
// endpoint. Provides on-demand log retrieval for the three intents
// EndUserSupport / NetworkDiagnostics / CrashLogs.
//
// Inline-response model: openccu-loom returns the entire log payload
// in the RetrieveLogsResponse's `LogContent` field (capped at the
// inline size below) without involving BDX. Larger payloads are
// truncated and the response status flips to LogStatusExhausted so
// the commissioner knows there's more. Real BDX streaming for
// payloads > inline cap is post-v1.1 work tracked in the roadmap.
type DiagnosticLogs struct {
	mu        sync.RWMutex
	provider  LogProvider
	bootEpoch time.Time
}

// LogProvider supplies the log payload for a given intent. The daemon
// wires an implementation via [DiagnosticLogs.AttachProvider] — a
// nil provider keeps the cluster's NoLogs default behaviour.
//
// Implementations MUST be safe for concurrent calls. The returned
// slice may be retained by the bridge briefly after Logs returns; do
// not mutate it after surrender.
type LogProvider interface {
	// Logs returns the captured log content for the supplied intent.
	// Return nil/empty for "no logs available" — the cluster maps
	// that to LogStatusNoLogs. Non-nil with a non-error result is
	// flagged LogStatusSuccess (or LogStatusExhausted when the slice
	// exceeds the inline payload cap).
	Logs(ctx context.Context, intent uint8) ([]byte, error)
}

// Cluster ID + revision per Matter §11.11.
const (
	diaglogsClusterID       uint32 = 0x0032
	diaglogsClusterRevision uint16 = 1

	diaglogsCmdRetrieveLogsRequest  uint32 = 0x00
	diaglogsCmdRetrieveLogsResponse uint32 = 0x01

	// MatterDiagnosticLogsInlineCap is the upper bound on the
	// LogContent octet-string size the bridge ships in a single
	// response. Matter §11.11.7.2 specifies the field as
	// `octstr<= 1024 bytes`. Larger payloads are truncated to this
	// cap and flagged LogStatusExhausted; BDX streaming for the
	// remainder is a post-v1.1 follow-up.
	MatterDiagnosticLogsInlineCap = 1024
)

// IntentEnum values (Matter §11.11.5.1).
const (
	IntentEndUserSupport uint8 = 0
	IntentNetworkDiag    uint8 = 1
	IntentCrashLogs      uint8 = 2
)

// StatusEnum values (Matter §11.11.5.2).
const (
	LogStatusSuccess   uint8 = 0
	LogStatusExhausted uint8 = 1
	LogStatusNoLogs    uint8 = 2
	LogStatusBusy      uint8 = 3
	LogStatusDenied    uint8 = 4
)

// RetrieveLogsResponse mirrors the response shape (Matter §11.11.7.2).
type RetrieveLogsResponse struct {
	Status        uint8
	LogContent    []byte
	UTCTimeStamp  uint64
	TimeSinceBoot uint64
}

// NewDiagnosticLogs returns a cluster server with the boot epoch
// stamped to "now" — the TimeSinceBoot field on responses tracks
// nanoseconds since this point. Operators that need a precise boot
// epoch can override it via [DiagnosticLogs.SetBootEpoch].
func NewDiagnosticLogs() *DiagnosticLogs {
	return &DiagnosticLogs{bootEpoch: time.Now()}
}

// AttachProvider wires the log-content source. Pass nil to revert
// to the NoLogs default (cluster shape stays valid; commissioners
// just get an empty payload).
func (d *DiagnosticLogs) AttachProvider(p LogProvider) {
	d.mu.Lock()
	d.provider = p
	d.mu.Unlock()
}

// SetBootEpoch overrides the timestamp the cluster computes
// `TimeSinceBoot` against. Most operators won't need this; default
// is the cluster construction time.
func (d *DiagnosticLogs) SetBootEpoch(t time.Time) {
	d.mu.Lock()
	d.bootEpoch = t
	d.mu.Unlock()
}

// Compile-time assertions: DiagnosticLogs satisfies MatterClusterServer,
// the attribute-lister capability, and the command-lister capability.
var (
	_ matterport.ClusterServer          = (*DiagnosticLogs)(nil)
	_ matterport.ClusterAttributeLister = (*DiagnosticLogs)(nil)
	_ matterport.ClusterCommandLister   = (*DiagnosticLogs)(nil)
)

// MatterClusterID implements [matterport.ClusterServer].
func (d *DiagnosticLogs) MatterClusterID() uint32 { return diaglogsClusterID }

// MatterRead implements [matterport.ClusterServer]. The cluster
// has no readable attributes other than the global ones.
func (d *DiagnosticLogs) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case cluster.AttrGlobalFeatureMap:
		return uint32(0), true
	case cluster.AttrGlobalClusterRevision:
		return diaglogsClusterRevision, true
	}
	return nil, false
}

// MatterWrite always rejects — DiagnosticLogs has no writable attributes.
func (d *DiagnosticLogs) MatterWrite(_ context.Context, attrID uint32, _ any, _ hmenum.CommandPriority) error {
	return fmt.Errorf("matter: DiagnosticLogs is read-only (got attr 0x%04X)", attrID)
}

// MatterInvoke implements RetrieveLogsRequest. Looks up the configured
// [LogProvider]; without one (default), or when the provider returns
// nil/empty, the response carries LogStatusNoLogs and an empty
// payload. Provider errors map to LogStatusBusy with empty content
// per §11.11.6.1 (the spec frames Busy as "transient failure to
// access logs", which fits any provider exception).
func (d *DiagnosticLogs) MatterInvoke(ctx context.Context, cmdID uint32, fields any, _ hmenum.CommandPriority) (any, error) {
	if cmdID != diaglogsCmdRetrieveLogsRequest {
		return nil, im.UnsupportedCommandf("matter: DiagnosticLogs command 0x%02X not supported", cmdID)
	}
	intent := decodeRetrieveLogsIntent(fields)

	d.mu.RLock()
	provider := d.provider
	bootEpoch := d.bootEpoch
	d.mu.RUnlock()

	resp := RetrieveLogsResponse{
		Status:        LogStatusNoLogs,
		LogContent:    []byte{},
		UTCTimeStamp:  uint64(time.Now().UnixNano()),               //nolint:gosec // wall clock; uint64 wide enough until year 2554; see #20
		TimeSinceBoot: uint64(time.Since(bootEpoch).Nanoseconds()), //nolint:gosec // monotonic-ish; uint64 wide enough; see #20
	}

	if provider == nil {
		return resp, nil
	}
	payload, err := provider.Logs(ctx, intent)
	if err != nil {
		resp.Status = LogStatusBusy
		return resp, nil //nolint:nilerr // provider failure surfaces through Status=Busy, not as an IM error
	}
	if len(payload) == 0 {
		return resp, nil
	}
	if len(payload) > MatterDiagnosticLogsInlineCap {
		resp.Status = LogStatusExhausted
		resp.LogContent = append([]byte(nil), payload[:MatterDiagnosticLogsInlineCap]...)
		return resp, nil
	}
	resp.Status = LogStatusSuccess
	resp.LogContent = append([]byte(nil), payload...)
	return resp, nil
}

// decodeRetrieveLogsIntent extracts the intent enum from a generic
// command-fields decoded value. The bridge passes `fields` as the
// decoded TLV struct (or nil); we tolerate both nil and a
// `map[uint8]any`-shaped struct so test fixtures can drive the
// cluster without going through the full TLV decoder. Unknown
// shapes default to EndUserSupport — Matter §11.11.6.1 lists it as
// the safe fallback for malformed requests.
func decodeRetrieveLogsIntent(fields any) uint8 {
	switch f := fields.(type) {
	case nil:
		return IntentEndUserSupport
	case map[uint8]any:
		if raw, ok := f[0]; ok {
			if v, ok := raw.(uint64); ok {
				return uint8(v & 0xFF) // intent is a 1-byte enum
			}
			if v, ok := raw.(uint8); ok {
				return v
			}
		}
		return IntentEndUserSupport
	}
	return IntentEndUserSupport
}

// MatterAcceptedCommands implements [matterport.ClusterCommandLister].
// Lists the command IDs the server handles via MatterInvoke.
// Mirrors matter.js packages/model/src/standard/elements/
// diagnostic-logs.element.ts accepted commands.
func (d *DiagnosticLogs) MatterAcceptedCommands() []uint32 {
	return []uint32{
		diaglogsCmdRetrieveLogsRequest, // 0x00
	}
}

// MatterGeneratedCommands implements [matterport.ClusterCommandLister].
// Lists the response command IDs this server may emit.
// Mirrors matter.js packages/model/src/standard/elements/
// diagnostic-logs.element.ts generated commands.
func (d *DiagnosticLogs) MatterGeneratedCommands() []uint32 {
	return []uint32{
		diaglogsCmdRetrieveLogsResponse, // 0x01
	}
}

// MatterReportable returns no attributes — nothing on this cluster
// changes outside command flow.
func (d *DiagnosticLogs) MatterReportable() []uint32 { return nil }

// MatterAttributes lists every DiagnosticLogs (0x0032) attribute the
// server implements via MatterRead. The cluster exposes no attributes
// beyond the globals (FeatureMap + ClusterRevision); Apple Home's HAP
// service rebuild is satisfied by an empty list here since globals are
// always injected by the dispatcher.
func (d *DiagnosticLogs) MatterAttributes() []uint32 { return nil }
