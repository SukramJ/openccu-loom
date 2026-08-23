package config_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
)

func TestYAMLUsersAndTokensNowDemandARestart(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*config.Config)
	}{
		{"users", func(c *config.Config) { c.North.REST.Auth.Users = map[string]string{"alice": "s3cret"} }},
		{"tokens", func(c *config.Config) { c.North.REST.Auth.Tokens = map[string]string{"tok": "admin"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			booted := &config.Config{}
			effective := &config.Config{}
			tc.mutate(effective)
			paths := config.RestartRequiredDiff(booted, effective)
			if len(paths) == 0 {
				t.Errorf("changing north.rest.auth.%s reports no restart required; the YAML "+
					"map is re-read only at boot, so the operator is told a credential "+
					"change took effect while the daemon keeps the old one", tc.name)
			}
		})
	}
}
