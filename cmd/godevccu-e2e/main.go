// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Command godevccu-e2e is a thin driver around the godevccu CCU
// simulator for end-to-end tests of external clients (e.g. the Python
// openccu-loom-client). It boots a CCU-personality simulator seeded
// with a fixed set of HmIP devices, prints the resolved listener ports
// as a single JSON line on stdout, and exposes a tiny HTTP control API
// so a test process can stimulate CCU-side events:
//
//	{"xml_rpc_port":34001,"json_rpc_port":34002,"control_port":34003}
//
// Control API (all POST, JSON body):
//
//	/set_value   {"address":"VCU...:3","value_key":"STATE","value":true}
//	             -> RPCFunctions.SetValue(address, value_key, value, true)
//	/fire_event  {"interface_id":"...","address":"VCU...:1",
//	              "value_key":"PRESS_SHORT","value":true}
//	             -> RPCFunctions.FireEvent(interface_id, address, value_key, value)
//	/healthz     -> 200 once the simulator is running
//
// The process runs until it receives SIGINT/SIGTERM, then stops the
// simulator cleanly. It mirrors the test harness in
// tests/e2e/harness/godevccu.go but, unlike that *testing.T-bound
// helper, is a standalone binary a non-Go test can spawn.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/SukramJ/godevccu/pkg/godevccu"
)

// defaultDevices mirrors harness.DefaultDevices so client tests can rely
// on a stable device set: a smoke detector, a wall thermostat, a switch
// + power meter, and a roller shutter — one device per HA domain shape.
var defaultDevices = []string{
	"HmIP-SWSD",  // smoke detector — STATE channel
	"HmIP-BWTH",  // wall thermostat — climate
	"HmIP-BSM",   // switch + power meter — switch + sensor
	"HmIP-BROLL", // roller shutter — cover
}

type ports struct {
	XMLRPCPort  int `json:"xml_rpc_port"`
	JSONRPCPort int `json:"json_rpc_port"`
	ControlPort int `json:"control_port"`
}

// resolveDevices maps the -devices flag onto godevccu's Devices config:
// empty keeps the fixed default set, "all" loads every embedded device type
// (nil allowlist), and a comma-separated list restricts to those types.
func resolveDevices(flagValue string) []string {
	switch trimmed := strings.TrimSpace(flagValue); {
	case trimmed == "":
		return defaultDevices
	case strings.EqualFold(trimmed, "all"):
		return nil
	default:
		out := make([]string, 0)
		for _, name := range strings.Split(trimmed, ",") {
			if name = strings.TrimSpace(name); name != "" {
				out = append(out, name)
			}
		}
		return out
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "godevccu-e2e:", err)
		os.Exit(1)
	}
}

func run() error {
	host := flag.String("host", godevccu.IPLocalhostV4, "bind address")
	xmlRPCPort := flag.Int("xml-rpc-port", godevccu.EphemeralPort, "XML-RPC port (0 = OS-assigned)")
	jsonRPCPort := flag.Int("json-rpc-port", godevccu.EphemeralPort, "JSON-RPC port (0 = OS-assigned)")
	controlAddr := flag.String("control-addr", "127.0.0.1:0", "control API bind address (port 0 = OS-assigned)")
	username := flag.String("username", "Admin", "JSON-RPC username")
	password := flag.String("password", "", "JSON-RPC password")
	devicesFlag := flag.String("devices", "",
		`device-type allowlist: empty = the fixed default set, "all" = every embedded type, `+
			`or a comma-separated list (e.g. "HmIP-BDT,HmIP-SWDO")`)
	flag.Parse()

	var v *godevccu.VirtualCCU
	v, err := godevccu.New(godevccu.Config{
		Mode:        godevccu.BackendModeCCU,
		Host:        *host,
		XMLRPCPort:  *xmlRPCPort,
		JSONRPCPort: *jsonRPCPort,
		Username:    *username,
		Password:    *password,
		AuthEnabled: true,
		Devices:     resolveDevices(*devicesFlag),
		Serial:      "GODEVCCU0001",
		// Real HmIP actuator firmware aggregates the virtual-receiver group
		// onto the state (…_TRANSMITTER) channel; consumers that read state
		// there (the reference stack's custom data points) never see a
		// command take effect without this mirroring.
		OnSetValue: func(address, valueKey string, value any) {
			mirrorVirtualReceiverWrite(v, address, valueKey, value)
		},
		SetupDefaults: true,
	})
	if err != nil {
		return fmt.Errorf("godevccu.New: %w", err)
	}
	if err := v.Start(); err != nil {
		return fmt.Errorf("godevccu.Start: %w", err)
	}
	defer func() { _ = v.Stop() }()

	// Bind the control listener first so we can advertise its port
	// together with the simulator's resolved RPC ports.
	var lc net.ListenConfig
	ctlLn, err := lc.Listen(context.Background(), "tcp", *controlAddr)
	if err != nil {
		return fmt.Errorf("control listen: %w", err)
	}

	p := ports{
		XMLRPCPort:  tcpPort(v.XMLRPCAddr()),
		JSONRPCPort: tcpPort(v.JSONRPCAddr()),
		ControlPort: tcpPort(ctlLn.Addr()),
	}
	if err := json.NewEncoder(os.Stdout).Encode(p); err != nil {
		return fmt.Errorf("encode ports: %w", err)
	}
	// Stdout is the readiness signal for the spawning test; flush it.
	_ = os.Stdout.Sync()

	srv := &http.Server{Handler: controlMux(v)}
	go func() { _ = srv.Serve(ctlLn) }()
	defer func() { _ = srv.Close() }()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	return nil
}

func tcpPort(addr net.Addr) int {
	if tcp, ok := addr.(*net.TCPAddr); ok && tcp != nil {
		return tcp.Port
	}
	return 0
}

// mirrorVirtualReceiverWrite replicates a successful write on a
// <FAMILY>_VIRTUAL_RECEIVER channel onto the device's <FAMILY>_TRANSMITTER
// state channel, matching real HmIP actuator firmware where the state channel
// reports the aggregated result of the virtual-receiver group. Parameters the
// transmitter channel does not describe are skipped silently; writes on the
// transmitter itself never recurse (its TYPE carries no receiver suffix).
func mirrorVirtualReceiverWrite(v *godevccu.VirtualCCU, address, valueKey string, value any) {
	if v == nil || !strings.Contains(address, ":") {
		return
	}
	rpc := v.RPC()
	desc, err := rpc.GetDeviceDescription(address)
	if err != nil {
		return
	}
	channelType, _ := desc["TYPE"].(string)
	family, isReceiver := strings.CutSuffix(channelType, "_VIRTUAL_RECEIVER")
	if !isReceiver {
		return
	}
	parent, _ := desc["PARENT"].(string)
	if parent == "" {
		parent = strings.SplitN(address, ":", 2)[0]
	}
	parentDesc, err := rpc.GetDeviceDescription(parent)
	if err != nil {
		return
	}
	for _, child := range childAddresses(parentDesc["CHILDREN"]) {
		childDesc, err := rpc.GetDeviceDescription(child)
		if err != nil {
			continue
		}
		if childType, _ := childDesc["TYPE"].(string); childType == family+"_TRANSMITTER" {
			// Force semantics: state-channel parameters are usually not
			// operator-writable; missing parameters make this a no-op.
			_ = rpc.SimulateDeviceEvent(child, valueKey, value)
			return
		}
	}
}

// childAddresses normalizes a device description CHILDREN attribute.
func childAddresses(raw any) []string {
	switch children := raw.(type) {
	case []string:
		return children
	case []any:
		out := make([]string, 0, len(children))
		for _, child := range children {
			if addr, ok := child.(string); ok {
				out = append(out, addr)
			}
		}
		return out
	default:
		return nil
	}
}

type setValueReq struct {
	Address  string `json:"address"`
	ValueKey string `json:"value_key"`
	Value    any    `json:"value"`
}

type fireEventReq struct {
	InterfaceID string `json:"interface_id"`
	Address     string `json:"address"`
	ValueKey    string `json:"value_key"`
	Value       any    `json:"value"`
}

func controlMux(v *godevccu.VirtualCCU) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/set_value", func(w http.ResponseWriter, r *http.Request) {
		var req setValueReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// force=true: write even if the CCU would normally reject the
		// transition, so tests stay deterministic. RPC() methods are
		// exported on an internal type; we call them without naming it.
		if err := v.RPC().SetValue(req.Address, req.ValueKey, req.Value, true); err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("/fire_event", func(w http.ResponseWriter, r *http.Request) {
		var req fireEventReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		v.RPC().FireEvent(req.InterfaceID, req.Address, req.ValueKey, req.Value)
		w.WriteHeader(http.StatusNoContent)
	})

	return mux
}
