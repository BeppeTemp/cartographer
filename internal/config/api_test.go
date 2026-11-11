package config

import (
	"strings"
	"testing"
)

func TestAPI_DefaultsOffWithCaps(t *testing.T) {
	cfg, err := loadComponents(t, "web:\n  enabled: false\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.API.Enabled || len(cfg.API.CORSOrigins) != 0 || cfg.API.MaxStreamsPerToken != 4 || cfg.API.MaxStreams != 64 {
		t.Fatalf("%+v", cfg.API)
	}
}

func TestAPI_Load(t *testing.T) {
	cfg, err := loadComponents(t, "api:\n  enabled: true\n  cors_origins: [\"https://dash.example.com\", \"http://localhost:5173\"]\n  max_streams_per_token: 2\n  max_streams: 10\n")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.API.Enabled || len(cfg.API.CORSOrigins) != 2 || cfg.API.MaxStreamsPerToken != 2 || cfg.API.MaxStreams != 10 {
		t.Fatalf("%+v", cfg.API)
	}
}

func TestAPI_LoadErrorsNameTheKey(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"wildcard":  {"api:\n  cors_origins: [\"*\"]\n", "api.cors_origins[0]"},
		"subdomain": {"api:\n  cors_origins: [\"https://*.example.com\"]\n", "api.cors_origins[0]"},
		"path":      {"api:\n  cors_origins: [\"https://example.com/app\"]\n", "api.cors_origins[0]"},
		"slash":     {"api:\n  cors_origins: [\"https://example.com/\"]\n", "api.cors_origins[0]"},
		"scheme":    {"api:\n  cors_origins: [\"ftp://example.com\"]\n", "api.cors_origins[0]"},
		"bare host": {"api:\n  cors_origins: [\"example.com\"]\n", "api.cors_origins[0]"},
		"per token": {"api:\n  max_streams_per_token: 0\n", "api.max_streams_per_token"},
		"over":      {"api:\n  max_streams_per_token: 8\n  max_streams: 4\n", "api.max_streams"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadComponents(t, tc.body)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestAPI_EnvAndFlagLayers(t *testing.T) {
	t.Setenv("CARTOGRAPHER_API_ENABLED", "true")
	cfg := Default()
	if err := FromEnv(cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.API.Enabled {
		t.Fatal("env did not enable")
	}
	off := false
	ApplyFlags(cfg, FlagOverrides{APIEnabled: &off})
	if cfg.API.Enabled {
		t.Fatal("flag did not override env")
	}
}
