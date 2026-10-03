// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"os"

	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/deployment"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// resolveDeployment reads where the daemon runs: the packaging's
// declaration plus the two facts only the host can supply (ADR 0081). It
// is a pure function of the environment and the host, so every caller
// gets the same answer for the life of the process.
func resolveDeployment() (deployment.Kind, error) {
	return deployment.Resolve(os.Getenv(deployment.EnvVar), deployment.Host{
		AddonInstall: build.IsAddon(),
		LiteBox:      isLiteAddonHost(),
	})
}

// deploymentKind is resolveDeployment for the wiring that runs after the
// start-up check in runDaemon refused an invalid declaration. A caller
// that bypasses that check (a test wiring one seam) gets standalone, the
// kind that switches nothing on.
func deploymentKind() deployment.Kind {
	kind, err := resolveDeployment()
	if err != nil {
		return deployment.Standalone
	}
	return kind
}

// deploymentInfo renders the deployment for `GET /info`.
func deploymentInfo(kind deployment.Kind) handlers.DeploymentInfo {
	return handlers.DeploymentInfo{Kind: string(kind), IngressPath: kind.IngressPath()}
}
