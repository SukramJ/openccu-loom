// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package addonupdate

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"
)

// Runner spawns the firmware installer. ctx is checked once before
// spawning (a caller whose context is already cancelled should not
// start a new install) but is deliberately NOT wired into the child
// process's lifetime — see [DefaultRunner]. Injectable so tests never
// exec a real binary.
type Runner func(ctx context.Context, path string, args ...string) error

// Installer hands the staged archive to the firmware's install_addon,
// detached so it survives the daemon process the install replaces
// (ADR 0057 decision 3: the add-on's update_script stops the daemon
// that triggered the install).
type Installer struct {
	// InstallerPath is the firmware installer binary. Defaults to
	// [InstallerPath] (the package constant) when empty.
	InstallerPath string
	// TarballPath is the staged archive install_addon reads. Defaults
	// to [DefaultStagePath] when empty.
	TarballPath string
	// Run spawns the installer. Defaults to [DefaultRunner].
	Run Runner
}

// NewInstaller returns an Installer wired to the real firmware
// installer path, the default stage path, and [DefaultRunner].
func NewInstaller() *Installer {
	return &Installer{InstallerPath: InstallerPath, TarballPath: DefaultStagePath, Run: DefaultRunner}
}

func (i *Installer) installerPath() string {
	if i.InstallerPath != "" {
		return i.InstallerPath
	}
	return InstallerPath
}

func (i *Installer) tarballPath() string {
	if i.TarballPath != "" {
		return i.TarballPath
	}
	return DefaultStagePath
}

func (i *Installer) runner() Runner {
	if i.Run != nil {
		return i.Run
	}
	return DefaultRunner
}

// Spawn starts the firmware installer against the staged tarball. It
// returns once the installer process has been started, not once it
// finishes — success here means "handed off", not "installed".
func (i *Installer) Spawn(ctx context.Context) error {
	return i.runner()(ctx, i.installerPath(), i.tarballPath())
}

// DefaultRunner starts path with args in its own session and
// immediately releases the process handle — the Go-native equivalent
// of shelling out to `setsid path args… &`. This is deliberately
// exec.Command, not exec.CommandContext: the installer's update_script
// stops this very daemon process, so the child's lifetime must not be
// tied to this process's context (a CommandContext child is killed
// when ctx is cancelled, which would race the daemon's own shutdown
// against the install it just started).
func DefaultRunner(ctx context.Context, path string, args ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	//nolint:noctx,gosec // deliberately exec.Command not CommandContext (see doc comment above); path/args come from the daemon's own compiled-in installer path + staged tarball, not external input
	cmd := exec.Command(path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("addonupdate: spawn installer: %w", err)
	}
	// Release the *os.Process handle so this process's runtime does
	// not wait/reap it; init (pid 1) adopts the orphan once this
	// daemon exits, exactly like a shell's `setsid cmd &`.
	return cmd.Process.Release()
}
