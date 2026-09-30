// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package backends

import (
	"context"
	"errors"
	"os"
	"regexp"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// liteScriptedCaller answers each method with a scripted reply (nil when
// unscripted) and records every call it receives.
type liteScriptedCaller struct {
	mu      sync.Mutex
	replies map[string]any
	errs    map[string]error
	calls   []liteRecordedCall
}

type liteRecordedCall struct {
	method string
	args   []any
}

func (c *liteScriptedCaller) Call(_ context.Context, method string, args ...any) (any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, liteRecordedCall{method: method, args: args})
	if err := c.errs[method]; err != nil {
		return nil, err
	}
	return c.replies[method], nil
}

func (c *liteScriptedCaller) CallAt(
	ctx context.Context, _ hmenum.CommandPriority, method string, args ...any,
) (any, error) {
	return c.Call(ctx, method, args...)
}

func (c *liteScriptedCaller) recorded() []liteRecordedCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]liteRecordedCall(nil), c.calls...)
}

// liteClassCCase is one class-c operation and the feature key its refusal
// must name.
type liteClassCCase struct {
	name    string
	feature hmenum.Feature
	call    func(ctx context.Context, b *LiteBackend) error
}

// liteClassCCases lists every operation without a counterpart on
// openccu-lite, with the feature key its refusal carries.
func liteClassCCases() []liteClassCCase {
	return []liteClassCCase{
		{"TestDevice", hmenum.FeatureDeviceCommunicationTest, func(ctx context.Context, b *LiteBackend) error {
			_, err := b.TestDevice(ctx, "A", 1, 1)
			return err
		}},
		{"GetDeviceDetails", hmenum.FeatureTaxonomyRead, func(ctx context.Context, b *LiteBackend) error {
			_, err := b.GetDeviceDetails(ctx, nil)
			return err
		}},
		{"RenameDevice", hmenum.FeatureDeviceRename, func(ctx context.Context, b *LiteBackend) error {
			_, err := b.RenameDevice(ctx, 1, "n")
			return err
		}},
		{"RenameChannel", hmenum.FeatureDeviceRename, func(ctx context.Context, b *LiteBackend) error {
			_, err := b.RenameChannel(ctx, 1, "n")
			return err
		}},
		{"AcceptDeviceInInbox", hmenum.FeatureHubInbox, func(ctx context.Context, b *LiteBackend) error {
			_, err := b.AcceptDeviceInInbox(ctx, "A")
			return err
		}},
		{"GetInboxDevices", hmenum.FeatureHubInbox, func(ctx context.Context, b *LiteBackend) error {
			_, err := b.GetInboxDevices(ctx, "HmIP-RF")
			return err
		}},
		{"TriggerFirmwareUpdate", hmenum.FeatureHubSystemUpdateInstall, func(ctx context.Context, b *LiteBackend) error {
			_, err := b.TriggerFirmwareUpdate(ctx)
			return err
		}},
		{"DownloadFirmware", hmenum.FeatureHubSystemUpdateInstall, func(ctx context.Context, b *LiteBackend) error {
			return b.DownloadFirmware(ctx)
		}},
		{"CreateBackupAndDownload", hmenum.FeatureSystemBackupCreate, func(ctx context.Context, b *LiteBackend) error {
			_, err := b.CreateBackupAndDownload(ctx, 1, 1)
			return err
		}},
		{"GetServiceMessages", hmenum.FeatureHubServiceMessages, func(ctx context.Context, b *LiteBackend) error {
			_, err := b.GetServiceMessages(ctx, "")
			return err
		}},
		{"GetAlarmMessages", hmenum.FeatureHubAlarmMessages, func(ctx context.Context, b *LiteBackend) error {
			_, err := b.GetAlarmMessages(ctx)
			return err
		}},
		{"GetAllRooms", hmenum.FeatureTaxonomyRead, func(ctx context.Context, b *LiteBackend) error {
			_, err := b.GetAllRooms(ctx)
			return err
		}},
		{"GetAllFunctions", hmenum.FeatureTaxonomyRead, func(ctx context.Context, b *LiteBackend) error {
			_, err := b.GetAllFunctions(ctx)
			return err
		}},
		{"GetAllPrograms", hmenum.FeatureHubPrograms, func(ctx context.Context, b *LiteBackend) error {
			_, err := b.GetAllPrograms(ctx)
			return err
		}},
		{"SetProgramState", hmenum.FeatureHubPrograms, func(ctx context.Context, b *LiteBackend) error {
			return b.SetProgramState(ctx, "1", true)
		}},
		{"ExecuteProgram", hmenum.FeatureHubPrograms, func(ctx context.Context, b *LiteBackend) error {
			_, err := b.ExecuteProgram(ctx, "1")
			return err
		}},
		{"HasProgramIDs", hmenum.FeatureHubPrograms, func(ctx context.Context, b *LiteBackend) error {
			_, err := b.HasProgramIDs(ctx, "1")
			return err
		}},
		{"GetSystemVariable", hmenum.FeatureHubSysvars, func(ctx context.Context, b *LiteBackend) error {
			_, err := b.GetSystemVariable(ctx, "v")
			return err
		}},
		{"GetAllSystemVariables", hmenum.FeatureHubSysvars, func(ctx context.Context, b *LiteBackend) error {
			_, err := b.GetAllSystemVariables(ctx)
			return err
		}},
		{"SetSystemVariable", hmenum.FeatureHubSysvars, func(ctx context.Context, b *LiteBackend) error {
			return b.SetSystemVariable(ctx, "v", true)
		}},
		{"CreateSystemVariableBool", hmenum.FeatureHubSysvars, func(ctx context.Context, b *LiteBackend) error {
			_, err := b.CreateSystemVariableBool(ctx, "v", true)
			return err
		}},
		{"CreateSystemVariableEnum", hmenum.FeatureHubSysvars, func(ctx context.Context, b *LiteBackend) error {
			_, err := b.CreateSystemVariableEnum(ctx, "v", []string{"a"})
			return err
		}},
		{"CreateSystemVariableFloat", hmenum.FeatureHubSysvars, func(ctx context.Context, b *LiteBackend) error {
			_, err := b.CreateSystemVariableFloat(ctx, "v", 0, 1)
			return err
		}},
		{"DeleteSystemVariable", hmenum.FeatureHubSysvars, func(ctx context.Context, b *LiteBackend) error {
			_, err := b.DeleteSystemVariable(ctx, "v")
			return err
		}},
		{"GetSystemUpdateInfo", hmenum.FeatureHubSystemUpdate, func(ctx context.Context, b *LiteBackend) error {
			_, err := b.GetSystemUpdateInfo(ctx)
			return err
		}},
	}
}

// TestLiteBackendClassCReturnsFeatureUnavailable pins every operation
// openccu-lite cannot serve to a FeatureUnavailableError with the right
// key, the not-supported reason and the legacy sentinel — and to no wire
// call at all.
func TestLiteBackendClassCReturnsFeatureUnavailable(t *testing.T) {
	t.Parallel()
	for _, tc := range liteClassCCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			caller := &liteScriptedCaller{}
			b := NewLiteBackend(hmenum.InterfaceHmIPRF, caller, nil)
			err := tc.call(context.Background(), b)
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("errors.Is(err, ErrUnsupported) = false; err = %v", err)
			}
			var fe *hmerr.FeatureUnavailableError
			if !errors.As(err, &fe) {
				t.Fatalf("err = %T %v, want *hmerr.FeatureUnavailableError", err, err)
			}
			if fe.Feature != tc.feature {
				t.Errorf("feature = %q, want %q", fe.Feature, tc.feature)
			}
			if fe.Reason != hmenum.FeatureReasonNotSupported {
				t.Errorf("reason = %q, want %q", fe.Reason, hmenum.FeatureReasonNotSupported)
			}
			if n := len(caller.recorded()); n != 0 {
				t.Errorf("a refused operation reached the wire %d time(s)", n)
			}
		})
	}
}

// TestLiteBackendKeylessRefusalsStayBareSentinel pins the two operations
// no feature key names: they refuse with ErrUnsupported alone and never
// reach the wire.
func TestLiteBackendKeylessRefusalsStayBareSentinel(t *testing.T) {
	t.Parallel()
	caller := &liteScriptedCaller{}
	b := NewLiteBackend(hmenum.InterfaceHmIPRF, caller, nil)
	ctx := context.Background()
	if _, err := b.GetAllDeviceData(ctx); !errors.Is(err, ErrUnsupported) {
		t.Errorf("GetAllDeviceData: %v, want ErrUnsupported", err)
	}
	if _, err := b.GetIseIDByAddress(ctx, "A"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("GetIseIDByAddress: %v, want ErrUnsupported", err)
	}
	if n := len(caller.recorded()); n != 0 {
		t.Errorf("a refused operation reached the wire %d time(s)", n)
	}
}

// liteCapabilityProbe exercises the operations one advertised capability
// stands for.
type liteCapabilityProbe struct {
	iface hmenum.Interface
	call  func(ctx context.Context, b *LiteBackend) error
}

// liteCapabilityProbes maps every capability field to the operations it
// gates. A field with no backend operation (a flag other layers read)
// maps to nil and is listed in liteFlagOnlyCapabilities.
func liteCapabilityProbes() map[string]*liteCapabilityProbe {
	rf := hmenum.InterfaceHmIPRF
	return map[string]*liteCapabilityProbe{
		"RPCCallback": {rf, func(ctx context.Context, b *LiteBackend) error {
			if err := b.Init(ctx, "id", "http://cb"); err != nil {
				return err
			}
			return b.Deinit(ctx, "http://cb")
		}},
		"PingPong":    {rf, func(ctx context.Context, b *LiteBackend) error { return b.Ping(ctx, "id") }},
		"ListDevices": {rf, func(ctx context.Context, b *LiteBackend) error { _, err := b.ListDevices(ctx); return err }},
		"FirmwareUpdate": {rf, func(ctx context.Context, b *LiteBackend) error {
			return b.UpdateFirmware(ctx, "A")
		}},
		"ConfigRestore": {rf, func(ctx context.Context, b *LiteBackend) error {
			return b.RestoreConfigToDevice(ctx, "A")
		}},
		"ReplaceDevice": {hmenum.InterfaceBidCosRF, func(ctx context.Context, b *LiteBackend) error {
			if _, err := b.ListReplaceableDevices(ctx, "N"); err != nil {
				return err
			}
			return b.ReplaceDevice(ctx, "O", "N")
		}},
		"SearchDevices": {hmenum.InterfaceBidCosWired, func(ctx context.Context, b *LiteBackend) error {
			_, err := b.SearchDevices(ctx)
			return err
		}},
		"TeamAssignment": {rf, func(ctx context.Context, b *LiteBackend) error {
			if err := b.SetTeam(ctx, "A:1", "T:1"); err != nil {
				return err
			}
			_, err := b.ListTeams(ctx)
			return err
		}},
		"DeleteDevice": {rf, func(ctx context.Context, b *LiteBackend) error { return b.DeleteDevice(ctx, "A", 0) }},
		"InstallMode": {rf, func(ctx context.Context, b *LiteBackend) error {
			if _, err := b.GetInstallMode(ctx); err != nil {
				return err
			}
			return b.SetInstallMode(ctx, true, 60, 1, "")
		}},
		"InstallModeLocal": {rf, func(ctx context.Context, b *LiteBackend) error {
			return b.SetInstallModeLocal(ctx, 60, "3014F711A0000000000000AB", "00112233445566778899AABBCCDDEEFF")
		}},
		"LinkOperations": {rf, func(ctx context.Context, b *LiteBackend) error {
			steps := []func() error{
				func() error { _, err := b.GetLinks(ctx, "A:1"); return err },
				func() error { _, err := b.GetLinkPeers(ctx, "A:1"); return err },
				func() error { return b.AddLink(ctx, "A:1", "B:1", "", "") },
				func() error { return b.RemoveLink(ctx, "A:1", "B:1") },
				func() error { _, err := b.GetLinkParamsetDescription(ctx, "A:1", "B:1"); return err },
				func() error { _, err := b.GetLinkParamset(ctx, "A:1", "B:1"); return err },
				func() error { return b.PutLinkParamset(ctx, "A:1", "B:1", map[string]any{}) },
				func() error { return b.ActivateLinkParamset(ctx, "B:1", "A:1", false) },
				func() error { _, err := b.GetLinkInfo(ctx, "HmIP-RF", "A:1", "B:1"); return err },
				func() error { _, err := b.SetLinkInfo(ctx, "HmIP-RF", "A:1", "B:1", "n", "d"); return err },
			}
			for _, s := range steps {
				if err := s(); err != nil {
					return err
				}
			}
			return nil
		}},
		"SuppressServiceMessage": {rf, func(ctx context.Context, b *LiteBackend) error {
			if err := b.SuppressServiceMessage(ctx, "A:0", "LOW_BAT", true); err != nil {
				return err
			}
			_, err := b.GetSuppressedServiceMessages(ctx, "HmIP-RF", "A:0")
			return err
		}},
		"ValueListRead": {rf, func(ctx context.Context, b *LiteBackend) error {
			_, err := b.GetParamset(ctx, "A:1", hmenum.ParamsetKeyValues)
			return err
		}},
		"Metadata": {rf, func(ctx context.Context, b *LiteBackend) error {
			if _, err := b.GetMetadata(ctx, "A", "NAME"); err != nil {
				return err
			}
			return b.SetMetadata(ctx, "A", "NAME", "n")
		}},
		"VirtualKey": nil,
	}
}

// liteScriptedRepliesForProbes answers every probe with a well-formed
// value of the shape the decoder expects.
func liteScriptedRepliesForProbes() map[string]any {
	return map[string]any{
		"listDevices":                  []any{},
		"listReplaceableDevices":       []any{},
		"listTeams":                    []any{},
		"getLinks":                     []any{},
		"getLinkPeers":                 []any{},
		"getParamsetDescription":       map[string]any{},
		"getParamset":                  map[string]any{},
		"getLinkInfo":                  map[string]any{"NAME": "n", "DESCRIPTION": "d"},
		"getSuppressedServiceMessages": []any{},
		"getInstallMode":               0,
		"searchDevices":                0,
	}
}

// TestLiteBackendAdvertisedCapabilitiesAreServed is the consistency
// guard between CapabilityFor(KindOpenCCULite) and LiteBackend: every
// capability the profile advertises must be backed by operations that do
// not refuse with ErrUnsupported.
func TestLiteBackendAdvertisedCapabilitiesAreServed(t *testing.T) {
	t.Parallel()
	caps := CapabilityFor(KindOpenCCULite)
	advertised := advertisedCapabilityNames(caps)
	probes := liteCapabilityProbes()
	for _, name := range advertised {
		probe, known := probes[name]
		if !known {
			t.Errorf("capability %s is advertised but has no probe; add one to liteCapabilityProbes", name)
			continue
		}
		if probe == nil {
			continue // flag read by other layers, no backend operation
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			caller := &liteScriptedCaller{replies: liteScriptedRepliesForProbes()}
			b := NewLiteBackend(probe.iface, caller, nil)
			err := probe.call(context.Background(), b)
			if errors.Is(err, ErrUnsupported) {
				t.Fatalf("capability %s is advertised but its operation refuses: %v", name, err)
			}
			if err != nil {
				t.Fatalf("capability %s: %v", name, err)
			}
		})
	}
}

// advertisedCapabilityNames lists the true fields of caps by name.
func advertisedCapabilityNames(caps Capabilities) []string {
	fields := map[string]bool{
		"RPCCallback": caps.RPCCallback, "PingPong": caps.PingPong, "ListDevices": caps.ListDevices,
		"GetAllPrograms": caps.GetAllPrograms, "GetAllSysvars": caps.GetAllSysvars,
		"FirmwareUpdate": caps.FirmwareUpdate, "ConfigRestore": caps.ConfigRestore,
		"ReplaceDevice": caps.ReplaceDevice, "SearchDevices": caps.SearchDevices,
		"CommunicationTest": caps.CommunicationTest, "TeamAssignment": caps.TeamAssignment,
		"RequiresPeriodicRefresh": caps.RequiresPeriodicRefresh, "AlarmMessages": caps.AlarmMessages,
		"Backup": caps.Backup, "CreateSystemVariable": caps.CreateSystemVariable,
		"DeleteDevice": caps.DeleteDevice, "DeleteSystemVariable": caps.DeleteSystemVariable,
		"ExecuteProgram": caps.ExecuteProgram, "InboxDevices": caps.InboxDevices,
		"InstallMode": caps.InstallMode, "InstallModeLocal": caps.InstallModeLocal,
		"LinkOperations": caps.LinkOperations, "ServiceMessages": caps.ServiceMessages,
		"SetProgramState": caps.SetProgramState, "SetSystemVariable": caps.SetSystemVariable,
		"SuppressServiceMessage": caps.SuppressServiceMessage, "ValueListRead": caps.ValueListRead,
		"VirtualKey": caps.VirtualKey, "Functions": caps.Functions, "Rooms": caps.Rooms,
		"Metadata": caps.Metadata, "Rename": caps.Rename, "IseIDLookup": caps.IseIDLookup,
	}
	out := make([]string, 0, len(fields))
	for name, on := range fields {
		if on {
			out = append(out, name)
		}
	}
	return out
}

// TestLiteBackendFaultMapping pins the refusal mapping on the three fault
// classes: a tier refusal, the init refusal, and an ordinary fault that
// must pass through unchanged.
func TestLiteBackendFaultMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tier := &liteScriptedCaller{errs: map[string]error{
		"setValue": &hmerr.XMLRPCFault{Code: -1, Message: "not permitted: setValue needs rpc:operate"},
	}}
	err := NewLiteBackend(hmenum.InterfaceHmIPRF, tier, nil).
		SetValue(ctx, "A:1", "STATE", true, hmenum.CommandPriorityLow, hmenum.CommandRxModeUnset)
	var sm *hmerr.ScopeMissingError
	if !errors.As(err, &sm) {
		t.Fatalf("tier fault: err = %T %v, want *hmerr.ScopeMissingError", err, err)
	}
	if sm.Scope != "rpc:operate" || sm.Operation != "setValue" {
		t.Errorf("tier fault: scope=%q operation=%q, want rpc:operate / setValue", sm.Scope, sm.Operation)
	}

	initRefused := &liteScriptedCaller{errs: map[string]error{
		"ping": &hmerr.XMLRPCFault{Code: -1, Message: occulited.InitRefusalText},
	}}
	err = NewLiteBackend(hmenum.InterfaceHmIPRF, initRefused, nil).Ping(ctx, "id")
	if !errors.Is(err, occulited.ErrInitRefused) {
		t.Errorf("init refusal: err = %v, want occulited.ErrInitRefused", err)
	}

	ordinary := &hmerr.XMLRPCFault{Code: -2, Message: "Unknown instance"}
	plain := &liteScriptedCaller{errs: map[string]error{"getValue": ordinary}}
	_, err = NewLiteBackend(hmenum.InterfaceHmIPRF, plain, nil).GetValue(ctx, "A:1", "STATE")
	var fault *hmerr.XMLRPCFault
	if !errors.As(err, &fault) || fault != ordinary {
		t.Errorf("ordinary fault: err = %v, want the fault unchanged", err)
	}
	if errors.Is(err, hmerr.ErrScopeMissing) || errors.Is(err, occulited.ErrInitRefused) {
		t.Errorf("ordinary fault was mapped: %v", err)
	}
}

// TestLiteBackendUpdateFirmwareFallsBackOnFault mirrors the CCU backend:
// installFirmware first, updateFirmware after a fault, but a tier refusal
// ends the attempt instead of falling back.
func TestLiteBackendUpdateFirmwareFallsBackOnFault(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := &liteScriptedCaller{errs: map[string]error{
		"installFirmware": &hmerr.XMLRPCFault{Code: -1, Message: "method not found"},
	}}
	if err := NewLiteBackend(hmenum.InterfaceBidCosRF, c, nil).UpdateFirmware(ctx, "A"); err != nil {
		t.Fatalf("UpdateFirmware: %v", err)
	}
	got := c.recorded()
	if len(got) != 2 || got[0].method != "installFirmware" || got[1].method != "updateFirmware" {
		t.Fatalf("calls = %+v, want installFirmware then updateFirmware", got)
	}

	denied := &liteScriptedCaller{errs: map[string]error{
		"installFirmware": &hmerr.XMLRPCFault{Code: -1, Message: "not permitted: installFirmware needs rpc:admin"},
	}}
	err := NewLiteBackend(hmenum.InterfaceHmIPRF, denied, nil).UpdateFirmware(ctx, "A")
	if !errors.Is(err, hmerr.ErrScopeMissing) {
		t.Fatalf("err = %v, want ErrScopeMissing", err)
	}
	if n := len(denied.recorded()); n != 1 {
		t.Errorf("a tier refusal triggered the fallback (%d calls)", n)
	}
}

// TestLiteBackendClearConfigCacheDispatchesXMLRPC pins the lite path to the
// same `clearConfigCache(address)` wire call the CCU backend makes, and
// its unwired form to ErrNotWired.
func TestLiteBackendClearConfigCacheDispatchesXMLRPC(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := &liteScriptedCaller{}
	if err := NewLiteBackend(hmenum.InterfaceBidCosRF, c, nil).ClearConfigCache(ctx, "A"); err != nil {
		t.Fatalf("ClearConfigCache: %v", err)
	}
	got := c.recorded()
	if len(got) != 1 || got[0].method != "clearConfigCache" || len(got[0].args) != 1 || got[0].args[0] != "A" {
		t.Fatalf("calls = %+v, want one clearConfigCache(A)", got)
	}
	if err := NewLiteBackend(hmenum.InterfaceBidCosRF, nil, nil).ClearConfigCache(ctx, "A"); !errors.Is(err, ErrNotWired) {
		t.Fatalf("unwired err = %v, want ErrNotWired", err)
	}
}

// TestLiteBackendInitDeinitDelegateToAnnouncer pins Init/Deinit to the
// announcer and their nil-safety.
func TestLiteBackendInitDeinitDelegateToAnnouncer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if err := NewLiteBackend(hmenum.InterfaceHmIPRF, nil, nil).Init(ctx, "id", "u"); err != nil {
		t.Errorf("nil announcer Init: %v", err)
	}
	if err := NewLiteBackend(hmenum.InterfaceHmIPRF, nil, nil).Deinit(ctx, "u"); err != nil {
		t.Errorf("nil announcer Deinit: %v", err)
	}
	ann := &liteRecordingAnnouncer{}
	b := NewLiteBackend(hmenum.InterfaceHmIPRF, &liteScriptedCaller{}, ann)
	if err := b.Init(ctx, "id", "http://cb"); err != nil {
		t.Fatal(err)
	}
	if err := b.Deinit(ctx, "http://cb"); err != nil {
		t.Fatal(err)
	}
	if ann.inits != 1 || ann.deinits != 1 || ann.lastURL != "http://cb" {
		t.Errorf("announcer saw inits=%d deinits=%d url=%q", ann.inits, ann.deinits, ann.lastURL)
	}
}

type liteRecordingAnnouncer struct {
	inits, deinits int
	lastURL        string
}

func (a *liteRecordingAnnouncer) Init(_ context.Context, _, callbackURL string) error {
	a.inits++
	a.lastURL = callbackURL
	return nil
}

func (a *liteRecordingAnnouncer) Deinit(_ context.Context, callbackURL string) error {
	a.deinits++
	a.lastURL = callbackURL
	return nil
}

// TestLiteBackendInterfaceGates pins the interface gates the CCU backend
// also applies: the wired-bus scan and the LOCAL teach-in refuse without
// a wire call on interfaces that lack them.
func TestLiteBackendInterfaceGates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := &liteScriptedCaller{}
	if _, err := NewLiteBackend(hmenum.InterfaceHmIPRF, c, nil).SearchDevices(ctx); !errors.Is(err, ErrUnsupported) {
		t.Errorf("SearchDevices on HmIP-RF: %v, want ErrUnsupported", err)
	}
	if err := NewLiteBackend(hmenum.InterfaceBidCosRF, c, nil).SetInstallModeLocal(ctx, 60, "S", "K"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("SetInstallModeLocal on BidCos-RF: %v, want ErrUnsupported", err)
	}
	if n := len(c.recorded()); n != 0 {
		t.Errorf("a gated operation reached the wire %d time(s)", n)
	}
}

// jsonRPCMethodNameRE matches a quoted CCU JSON-RPC method name
// ("Interface.x", "SysVar.y", …): a capitalised namespace, a dot and a
// lower-case method.
var jsonRPCMethodNameRE = regexp.MustCompile(`"(Interface|Program|SysVar|Room|Function|Device|Channel|Session|CCU|ReGa|User|Event|Diagram|SafeMode|System)\.[a-z][A-Za-z]*"`)

// TestLiteBackendNamesNoJSONRPCMethod pins lite.go to the XML-RPC proxy:
// a box has no JSON-RPC surface, so the file must not name any JSON-RPC
// method — neither an absent one (covered package-wide) nor a real one.
func TestLiteBackendNamesNoJSONRPCMethod(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("lite.go")
	if err != nil {
		t.Fatalf("read lite.go: %v", err)
	}
	if m := jsonRPCMethodNameRE.FindString(string(src)); m != "" {
		t.Errorf("lite.go names the JSON-RPC method %s; openccu-lite has no JSON-RPC surface", m)
	}
	for _, m := range absentJSONRPCMethods {
		if regexp.MustCompile(regexp.QuoteMeta(m)).Match(src) {
			t.Errorf("lite.go names the absent JSON-RPC method %s", m)
		}
	}
	// Negative control: the matcher recognises a JSON-RPC name when one
	// is present.
	if !jsonRPCMethodNameRE.MatchString(`x := "Interface.getInstallMode"`) {
		t.Fatal("jsonRPCMethodNameRE does not match a JSON-RPC method name")
	}
}
