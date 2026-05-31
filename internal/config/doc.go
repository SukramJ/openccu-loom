// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package config loads the daemon's effective configuration.
//
// The MVP parser accepts a single YAML file; koanf-style providers
// (env, flags, consul) are deferred. A parsed [Config] is immutable
// and passed down the dependency graph.
package config
