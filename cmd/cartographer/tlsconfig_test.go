package main

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/config"
)

// AC-tls-native-4: a start that asked for TLS and cannot have it stops with an
// error naming tls.enabled; a server without TLS is never judged.
func TestCheckTLSConfig(t *testing.T) {
	creds := t.TempDir()
	on := config.TLSConfig{Enabled: true}
	for name, tc := range map[string]struct {
		cfg  config.Config
		want string // "" = accepted
	}{
		"off":             {config.Config{}, ""},
		"stdio":           {config.Config{TLS: on, CredentialsDir: creds}, "tls.enabled"},
		"folder session":  {config.Config{HTTP: ":8443", TLS: on, KBs: []config.KBSpec{{Path: creds}}}, "credentials_dir"},
		"ok":              {config.Config{HTTP: ":8443", TLS: on, CredentialsDir: creds}, ""},
		"no derived addr": {config.Config{HTTP: ":65535", TLS: on, CredentialsDir: creds}, "tls.health_addr"},
	} {
		err := checkTLSConfig(&tc.cfg)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}
