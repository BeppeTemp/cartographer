package main

import (
	"errors"

	"github.com/BeppeTemp/cartographer/internal/config"
)

// checkTLSConfig refuses, before any listener, a configuration that asked for
// TLS and cannot have it: nothing falls back to cleartext. It judges the final
// settings, after the environment and the flags are merged.
func checkTLSConfig(cfg *config.Config) error {
	if !cfg.TLS.Enabled {
		return nil
	}
	if cfg.HTTP == "" {
		return errors.New("tls.enabled: needs an HTTP listener (set http or --http); stdio has no TLS")
	}
	if cfg.CredentialsPath() == "" {
		return errors.New("tls.enabled: a folder session has no credentials directory for tls.crt and tls.key; set credentials_dir")
	}
	return config.ValidateTLS(cfg)
}
