// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
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
	flag.Parse()

	v, err := godevccu.New(godevccu.Config{
		Mode:          godevccu.BackendModeCCU,
		Host:          *host,
		XMLRPCPort:    *xmlRPCPort,
		JSONRPCPort:   *jsonRPCPort,
		Username:      *username,
		Password:      *password,
		AuthEnabled:   true,
		Devices:       defaultDevices,
		Serial:        "GODEVCCU0001",
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
	ctlLn, err := net.Listen("tcp", *controlAddr)
	if err != nil {
		return fmt.Errorf("control listen: %w", err)
	}

	p := ports{
		XMLRPCPort:  tcpPort(v.XMLRPCAddr()),
		JSONRPCPort: tcpPort(v.JSONRPCAddr()),
		ControlPort: ctlLn.Addr().(*net.TCPAddr).Port,
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
