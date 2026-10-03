// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/deployment"
)

// TestResolveDeploymentReadsTheDeclarationAndTheHost pins the two inputs
// of the composition root's resolver: the variable the packaging sets, and
// the lite-box facts read from the host.
func TestResolveDeploymentReadsTheDeclarationAndTheHost(t *testing.T) {
	t.Run("nothing declared on a plain host", func(t *testing.T) {
		t.Setenv(deployment.EnvVar, "")
		if got := deploymentKind(); got != deployment.Standalone {
			t.Errorf("deploymentKind() = %q, want standalone", got)
		}
	})
	t.Run("the packaging's declaration", func(t *testing.T) {
		t.Setenv(deployment.EnvVar, "ha-addon")
		if got := deploymentKind(); got != deployment.HAAddon {
			t.Errorf("deploymentKind() = %q, want ha-addon", got)
		}
	})
	t.Run("ccu-addon on a lite box", func(t *testing.T) {
		dir := t.TempDir()
		version := filepath.Join(dir, "VERSION")
		if err := os.WriteFile(version, []byte("VARIANT=lite\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		token := filepath.Join(dir, "openccu-loom.api")
		if err := os.WriteFile(token, []byte("olt_test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(liteAddonVersionPathEnv, version)
		t.Setenv(liteAddonTokenPathEnv, token)
		t.Setenv(deployment.EnvVar, "ccu-addon")
		if got := deploymentKind(); got != deployment.LiteAddon {
			t.Errorf("deploymentKind() = %q, want lite-addon", got)
		}
		if got := deploymentInfo(deploymentKind()); got.Kind != "lite-addon" || got.IngressPath != "/addons/loom/" {
			t.Errorf("deploymentInfo = %+v", got)
		}
	})
}

// TestRunDaemonRefusesAnUnknownDeployment pins the start-up check: a
// declaration the daemon does not know stops the start with a message
// naming the variable, instead of running as standalone.
func TestRunDaemonRefusesAnUnknownDeployment(t *testing.T) {
	t.Setenv(deployment.EnvVar, "docker")
	if _, err := resolveDeployment(); err == nil {
		t.Fatal("resolveDeployment accepted an unknown declaration")
	}
	var stderr strings.Builder
	err := runDaemon([]string{"-config", filepath.Join(t.TempDir(), "absent.yaml")}, io.Discard, &stderr)
	if err == nil {
		t.Fatal("runDaemon started with an unknown deployment")
	}
	if !strings.Contains(err.Error()+stderr.String(), deployment.EnvVar) {
		t.Errorf("the refusal does not name %s: err=%v stderr=%q", deployment.EnvVar, err, stderr.String())
	}
}
