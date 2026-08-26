// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package addonupdate

import (
	"os"
	"testing"
	"time"
)

// fakeFileInfo is a minimal os.FileInfo implementation shared across this
// package's tests so a capability check (or anything else that stats a
// path) never has to touch the real filesystem.
type fakeFileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
	dir     bool
}

func (f fakeFileInfo) Name() string       { return f.name }
func (f fakeFileInfo) Size() int64        { return f.size }
func (f fakeFileInfo) Mode() os.FileMode  { return f.mode }
func (f fakeFileInfo) ModTime() time.Time { return f.modTime }
func (f fakeFileInfo) IsDir() bool        { return f.dir }
func (f fakeFileInfo) Sys() any           { return nil }

func TestCapabilityProbeSupported(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		isAddonBuild bool
		statInfo     os.FileInfo
		statErr      error
		want         bool
	}{
		{
			name:         "not an addon build ignores an otherwise-valid installer",
			isAddonBuild: false,
			statInfo:     fakeFileInfo{mode: 0o755},
			want:         false,
		},
		{
			name:         "addon build with executable installer is supported",
			isAddonBuild: true,
			statInfo:     fakeFileInfo{mode: 0o755},
			want:         true,
		},
		{
			name:         "addon build with missing installer is unsupported",
			isAddonBuild: true,
			statErr:      os.ErrNotExist,
			want:         false,
		},
		{
			name:         "addon build with installer path being a directory is unsupported",
			isAddonBuild: true,
			statInfo:     fakeFileInfo{mode: 0o755 | os.ModeDir, dir: true},
			want:         false,
		},
		{
			name:         "addon build with non-executable installer is unsupported",
			isAddonBuild: true,
			statInfo:     fakeFileInfo{mode: 0o644},
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			probe := CapabilityProbe{
				IsAddonBuild: func() bool { return tt.isAddonBuild },
				StatInstaller: func(path string) (os.FileInfo, error) {
					if path != InstallerPath {
						t.Errorf("StatInstaller called with %q, want %q", path, InstallerPath)
					}
					return tt.statInfo, tt.statErr
				},
			}
			if got := probe.Supported(); got != tt.want {
				t.Errorf("Supported() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestNewCapabilityProbeWiresRealFuncs checks that NewCapabilityProbe
// wires non-nil seams to the real build flag and filesystem, and that a
// smoke call to Supported never panics regardless of the real
// filesystem/build state of the machine running the test.
func TestNewCapabilityProbeWiresRealFuncs(t *testing.T) {
	t.Parallel()

	probe := NewCapabilityProbe()
	if probe.IsAddonBuild == nil {
		t.Fatal("IsAddonBuild is nil")
	}
	if probe.StatInstaller == nil {
		t.Fatal("StatInstaller is nil")
	}
	_ = probe.Supported()
}
