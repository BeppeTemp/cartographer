package worker

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/worker/connector"
)

// rawConnector is one connectors.<name> entry as written (D1262).
type rawConnector struct {
	Transport         string            `yaml:"transport"`
	Command           string            `yaml:"command"`
	Args              []string          `yaml:"args"`
	Env               map[string]string `yaml:"env"`
	URL               string            `yaml:"url"`
	AllowInsecureHTTP bool              `yaml:"allow_insecure_http"`
	TokenEnv          string            `yaml:"token_env"`
	Headers           map[string]string `yaml:"headers"`
	Tools             []string          `yaml:"tools"`
	CallTimeout       string            `yaml:"call_timeout"`
	MaxResultBytes    int               `yaml:"max_result_bytes"`
}

var (
	connectorNameRE = regexp.MustCompile(`^[a-z0-9-]{1,30}$`)
	envNameRE       = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
)

// reservedConnectors are names a connector cannot take: git and fetch are the
// built-in readers, artifact_ is the deny prefix of the agent loop's allowlist.
var reservedConnectors = []string{"git", "fetch", "artifact"}

const (
	defaultConnectorTimeout = 60 * time.Second
	defaultConnectorResult  = 256 << 10
)

// connectors validates the connectors key. Every value of env, headers and
// token_env must be the NAME of a variable: a literal secret looks like anything
// else, so the error never echoes the value. The variables themselves are read
// when the connector starts, not here: an unset one skips its units, it does not
// stop the worker.
func (v *validator) connectors() error {
	names := make([]string, 0, len(v.raw.Connectors))
	for n := range v.raw.Connectors {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		r := v.raw.Connectors[name]
		at := "connectors." + name
		ln := func(key ...string) int {
			p := []any{"connectors", name}
			for _, k := range key {
				p = append(p, k)
			}
			if l := v.line(p...); l > 0 {
				return l
			}
			return v.line("connectors", name)
		}
		if !connectorNameRE.MatchString(name) {
			return v.errf(at, ln(), "a connector name is 1 to 30 characters of a-z, 0-9 and -")
		}
		for _, bad := range reservedConnectors {
			if name == bad {
				return v.errf(at, ln(), "the name %q is reserved", name)
			}
		}
		c := connector.Config{Name: name, Transport: r.Transport, Args: r.Args, Env: r.Env, URL: r.URL,
			AllowInsecureHTTP: r.AllowInsecureHTTP, TokenEnv: r.TokenEnv, Headers: r.Headers, Tools: r.Tools}
		switch r.Transport {
		case connector.TransportStdio:
			switch {
			case r.Command == "":
				return v.errf(at+".command", ln(), "required for transport stdio")
			case r.URL != "" || r.TokenEnv != "" || len(r.Headers) > 0 || r.AllowInsecureHTTP:
				return v.errf(at, ln(), "url, token_env, headers and allow_insecure_http belong to transport http")
			}
			c.Command = r.Command
			if strings.ContainsAny(r.Command, `/\`) && v.dir != "" {
				if !filepath.IsAbs(r.Command) {
					c.Command = filepath.Join(v.dir, r.Command)
				}
				abs, err := filepath.Abs(c.Command)
				if err != nil {
					return v.errf(at+".command", ln("command"), "cannot be made absolute")
				}
				c.Command = abs
			}
			if err := envNames(r.Env); err != nil {
				return v.errf(at+".env", ln("env"), "%v", err)
			}
		case connector.TransportHTTP:
			switch {
			case r.URL == "":
				return v.errf(at+".url", ln(), "required for transport http")
			case r.Command != "" || len(r.Args) > 0 || len(r.Env) > 0:
				return v.errf(at, ln(), "command, args and env belong to transport stdio")
			}
			if err := checkURL(r.URL, r.AllowInsecureHTTP); err != nil {
				return v.errf(at+".url", ln("url"), "%v", err)
			}
			if r.TokenEnv != "" && !envNameRE.MatchString(r.TokenEnv) {
				return v.errf(at+".token_env", ln("token_env"), "must be the name of an environment variable (capitals, digits and _), not a value")
			}
			if err := envNames(r.Headers); err != nil {
				return v.errf(at+".headers", ln("headers"), "%v", err)
			}
		default:
			return v.errf(at+".transport", ln(), "required: stdio or http")
		}
		if len(r.Tools) == 0 {
			return v.errf(at+".tools", ln(), "required: the tools the model may use, named as the server names them (the server's own list is untrusted)")
		}
		seen := map[string]bool{}
		for _, t := range r.Tools {
			if strings.TrimSpace(t) == "" || strings.ContainsAny(t, " \t\n") || seen[t] {
				return v.errf(at+".tools", ln("tools"), "a tool name is non-empty, has no space and is listed once")
			}
			seen[t] = true
		}
		c.CallTimeout = defaultConnectorTimeout
		if r.CallTimeout != "" {
			d, err := durationOf(r.CallTimeout)
			if err != nil || d <= 0 {
				return v.errf(at+".call_timeout", ln("call_timeout"), "must be a positive duration such as 60s")
			}
			c.CallTimeout = d
		}
		c.MaxResultBytes = defaultConnectorResult
		switch {
		case r.MaxResultBytes < 0:
			return v.errf(at+".max_result_bytes", ln("max_result_bytes"), "must be above 0")
		case r.MaxResultBytes > 0:
			c.MaxResultBytes = r.MaxResultBytes
		}
		v.c.Connectors = append(v.c.Connectors, c)
	}
	return nil
}

// envNames checks that the values of m are variable names; the error names the
// key and never prints the value, which may be a secret written by mistake.
func envNames(m map[string]string) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == "" || strings.ContainsAny(k, "=\x00") {
			return fmt.Errorf("the name %q the child sees must be non-empty and hold no = or NUL", k)
		}
		if !envNameRE.MatchString(m[k]) {
			return fmt.Errorf("the value of %q must be the name of an environment variable (capitals, digits and _), not a value", k)
		}
	}
	return nil
}
