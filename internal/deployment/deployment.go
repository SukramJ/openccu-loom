// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package deployment names where the daemon process runs. It is a
// property of the process, not of the systems it talks to: a standalone
// daemon can hold an openccu-lite central, and the add-on on a lite box
// can hold a remote CCU as a further central
// (docs/adr/0081-deployment-self-description.md).
package deployment

import "fmt"

// Kind is the daemon's deployment.
type Kind string

const (
	// LiteAddon is the add-on on an openccu-lite box, fronted by the box's
	// web server and its gate.
	LiteAddon Kind = "lite-addon"
	// CCUAddon is the add-on on a classic CCU / OpenCCU.
	CCUAddon Kind = "ccu-addon"
	// HAAddon is the Home Assistant add-on.
	HAAddon Kind = "ha-addon"
	// Standalone is everything else: a container, a service, a plain
	// process.
	Standalone Kind = "standalone"
)

// EnvVar is the variable the packaging declares the deployment in.
const EnvVar = "OPENCCU_LOOM_DEPLOYMENT"

// liteIngressPath is where the box's web server serves the add-on; the
// add-on's lighttpd fragment proxies exactly this prefix.
const liteIngressPath = "/addons/loom/"

// Host carries the facts read from the machine the process runs on.
type Host struct {
	// AddonInstall reports the CCU add-on's build stamp or install path.
	// It recognises an add-on installed before the packaging declared
	// its deployment.
	AddonInstall bool
	// LiteBox reports an openccu-lite box that minted this add-on's
	// token. The classic and the lite add-on are one package with one
	// start script, so only the host can tell them apart.
	LiteBox bool
}

// Resolve turns the packaging's declaration and the host facts into the
// deployment. The declaration is one of ha-addon, ccu-addon, standalone
// or empty; lite-addon is never declared, it is what an add-on on a lite
// box resolves to. Any other value is an error, so a typo cannot silently
// become standalone.
func Resolve(declared string, host Host) (Kind, error) {
	switch Kind(declared) {
	case HAAddon, Standalone:
		return Kind(declared), nil
	case CCUAddon, "":
		switch {
		case host.LiteBox:
			return LiteAddon, nil
		case declared != "" || host.AddonInstall:
			return CCUAddon, nil
		}
		return Standalone, nil
	case LiteAddon:
		return "", fmt.Errorf("%s=%s: the lite add-on is recognised from its host, declare %s instead", EnvVar, declared, CCUAddon)
	}
	return "", fmt.Errorf("%s=%q: want %s, %s or %s", EnvVar, declared, HAAddon, CCUAddon, Standalone)
}

// OnCCU reports an add-on running on the system it serves — a classic
// CCU or a lite box.
func (k Kind) OnCCU() bool { return k == CCUAddon || k == LiteAddon }

// IngressPath is the path under which the hosting system's own web server
// serves the daemon, with a trailing slash; empty when there is none.
func (k Kind) IngressPath() string {
	if k == LiteAddon {
		return liteIngressPath
	}
	return ""
}
