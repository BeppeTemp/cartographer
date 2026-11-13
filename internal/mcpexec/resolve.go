// Package mcpexec resolves an approved MCP descriptor and its secrets.
// It is what a client needs to start an MCP server that a KB distributes.
// It is the shared core of `cartographer mcp exec` and of the probe; running
// the server is left to the caller. A secret value lives only in Launch.Env
// and Launch.Headers: nothing here writes, caches or logs one.
package mcpexec

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// Deps is what Resolve needs from its caller, so tests and the probe can fake it.
type Deps interface {
	// Call runs a tool on the routed endpoint and returns the text of its
	// result (the first block when the server appends a notice); a tool error
	// is the error. The caller adds the kb argument's value from args.
	Call(ctx context.Context, tool string, args map[string]any) (string, error)
	// Approved returns the content hash the person approved for mcp/<slug>.json
	// of kb, with ok false when there is none.
	Approved(kb, slug string) (hash string, ok bool)
	// Environ is the environment the launcher inherited (os.Environ).
	Environ() []string
}

// TokenEnver is implemented by a Deps that knows the variable carrying the
// Cartographer bearer token. The launcher needs it for its own calls to the
// server and no descriptor may read it or inherit it: the token would reach a
// third-party process or host.
type TokenEnver interface {
	TokenEnv() string
}

// sameEnvKey compares variable names the way the OS does.
func sameEnvKey(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// Launch is a descriptor ready to run.
type Launch struct {
	Spec    contract.MCPServerSpec
	Command string
	Args    []string
	// Env is the inherited environment plus the descriptor's: the only place a
	// resolved secret lives together with Headers.
	Env     []string
	URL     string
	Headers map[string]string

	ref       string
	redactKey map[string]bool // env names that carry a resolved value
	redactHdr map[string]bool // header names whose value held a reference
	secrets   []string        // every resolved value, to scrub errors
	pieces    []string        // every substituted value, secret or ${VAR}, as it was before composition
}

// Pieces returns every value the launch substituted into the descriptor, each
// ${VAR} and ${secret:...} alone: the composed "Bearer abc" hides abc only if
// abc is scrubbed too. A caller that prints a child's or a server's text uses
// them to scrub it; they are never printed themselves.
func (l Launch) Pieces() []string { return append([]string(nil), l.pieces...) }

// Redacted is the same launch without any resolved value, for reports: only the
// descriptor's own variables are listed, never the inherited environment.
type Redacted struct {
	Ref     string
	Command string
	Args    []string
	Env     []string
	URL     string
	Headers map[string]string
}

const redactedText = "<redacted>"

// Redacted returns the printable form of l.
func (l Launch) Redacted() Redacted {
	r := Redacted{Ref: l.ref, Command: l.Command, Args: l.Args, URL: l.URL, Headers: map[string]string{}}
	for k, v := range l.Headers {
		if l.redactHdr[k] {
			v = redactedText
		}
		r.Headers[k] = v
	}
	listed := map[string]bool{}
	for _, e := range l.Env {
		k, v, _ := strings.Cut(e, "=")
		if listed[k] {
			continue
		}
		if _, own := l.Spec.Env[k]; own {
			v = redactedText
		} else if _, lit := l.Spec.EnvLiteral[k]; !lit {
			continue
		}
		listed[k] = true
		r.Env = append(r.Env, k+"="+v)
	}
	return r
}

// String keeps a stray %v or %+v of a Launch from printing a value.
func (l Launch) String() string { return fmt.Sprintf("%+v", l.Redacted()) }

// GoString does the same for %#v.
func (l Launch) GoString() string { return l.String() }

// ParseRef splits "<kb>/<slug>": exactly one slash, both parts valid names.
func ParseRef(ref string) (kb, slug string, err error) {
	kb, slug, ok := strings.Cut(ref, "/")
	if !ok || !contract.ValidArtifactName(kb) || !contract.ValidArtifactName(slug) {
		return "", "", fmt.Errorf("%q is not <kb>/<slug>", client.Escape(ref))
	}
	return kb, slug, nil
}

var varRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Resolve reads the descriptor from the server, refuses it unless it is the
// one approved, and expands its environment references. Nothing is cached: a
// server that cannot be reached is an error, never a stale secret.
func Resolve(ctx context.Context, d Deps, ref string) (*Launch, error) {
	kb, slug, err := ParseRef(ref)
	if err != nil {
		return nil, err
	}
	res := &resolver{d: d, kb: kb}
	data, err := res.readDescriptor(ctx, slug)
	if err != nil {
		return nil, err
	}
	spec, err := contract.ParseMCPServerSpec(slug, data)
	if err != nil {
		return nil, fmt.Errorf("%s", client.Escape(err.Error()))
	}
	hash := contract.HashArtifactFiles([]contract.ArtifactFile{{Path: slug + ".json", Content: data}})
	if want, ok := d.Approved(kb, slug); !ok || want != hash {
		return nil, fmt.Errorf("mcp %s: not approved, or the descriptor changed since you approved it; run `cartographer sync`, then `cartographer approve mcp %s --kb %s`", ref, slug, kb)
	}
	l := &Launch{Spec: spec, Command: spec.Command, Args: spec.Args, URL: spec.URL, ref: ref,
		redactKey: map[string]bool{}, redactHdr: map[string]bool{}, Headers: map[string]string{}}
	if te, ok := d.(TokenEnver); ok {
		res.tokenEnv = te.TokenEnv()
	}
	// A key the descriptor sets replaces the inherited one (the child would see
	// both, and getenv takes the first); the token variable never reaches it.
	set := map[string]bool{}
	for k := range spec.Env {
		set[k] = true
	}
	for k := range spec.EnvLiteral {
		set[k] = true
	}
	var env []string
	for _, e := range d.Environ() {
		k, _, _ := strings.Cut(e, "=")
		if res.tokenEnv != "" && sameEnvKey(k, res.tokenEnv) {
			continue
		}
		own := false
		for sk := range set {
			if sameEnvKey(k, sk) {
				own = true
				break
			}
		}
		if !own {
			env = append(env, e)
		}
	}
	for _, k := range sortedKeys(spec.EnvLiteral) {
		env = append(env, k+"="+spec.EnvLiteral[k])
	}
	for _, k := range sortedKeys(spec.Env) {
		v, err := res.expand(ctx, spec.Env[k])
		if err != nil {
			return nil, fmt.Errorf("mcp %s: env %s: %w", ref, k, err)
		}
		env = append(env, k+"="+v)
		l.redactKey[k] = true
	}
	for _, k := range sortedKeys(spec.Headers) {
		v, err := res.expand(ctx, spec.Headers[k])
		if err != nil {
			return nil, fmt.Errorf("mcp %s: header %s: %w", ref, k, err)
		}
		l.Headers[k] = v
		l.redactHdr[k] = strings.Contains(spec.Headers[k], "${")
	}
	l.Env, l.secrets, l.pieces = env, res.secrets, res.pieces
	return l, nil
}

type resolver struct {
	d       Deps
	kb      string
	secrets []string
	pieces  []string
	// tokenEnv is the Cartographer token variable, which expand refuses.
	tokenEnv string
}

// call runs a tool and scrubs the values resolved so far from its error.
func (r *resolver) call(ctx context.Context, tool string, args map[string]any) (string, error) {
	args["kb"] = r.kb
	out, err := r.d.Call(ctx, tool, args)
	if err != nil {
		msg := err.Error()
		for _, s := range r.secrets {
			msg = strings.ReplaceAll(msg, s, redactedText)
		}
		return "", fmt.Errorf("%s: %s (secrets are never cached, the MCP server was not started)", tool, client.Escape(msg))
	}
	return out, nil
}

func (r *resolver) readDescriptor(ctx context.Context, slug string) ([]byte, error) {
	out, err := r.call(ctx, "artifact_read", map[string]any{"path": "mcp/" + slug + ".json"})
	if err != nil {
		return nil, err
	}
	var a struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := json.Unmarshal([]byte(out), &a); err != nil {
		return nil, errors.New("artifact_read: the answer is not a descriptor")
	}
	if a.Encoding == "base64" {
		b, err := base64.StdEncoding.DecodeString(a.Content)
		if err != nil {
			return nil, errors.New("artifact_read: invalid base64 content")
		}
		return b, nil
	}
	return []byte(a.Content), nil
}

// expandRef spots one ${VAR} or ${secret:...} in a value.
var expandRef = regexp.MustCompile(`\$\{(secret:[^}]*|[A-Za-z_][A-Za-z0-9_]*)\}`)

// expand replaces every ${VAR} from the launcher's environment and every
// ${secret:c#N} through one secret_resolve call each. A substituted value is
// never scanned again.
func (r *resolver) expand(ctx context.Context, v string) (string, error) {
	var b strings.Builder
	last := 0
	for _, m := range expandRef.FindAllStringSubmatchIndex(v, -1) {
		b.WriteString(v[last:m[0]])
		last = m[1]
		tok := v[m[2]:m[3]]
		if strings.HasPrefix(tok, "secret:") {
			refs := contract.SecretRefs(v[m[0]:m[1]])
			if len(refs) != 1 {
				return "", errors.New("malformed secret reference")
			}
			val, err := r.secret(ctx, refs[0])
			if err != nil {
				return "", err
			}
			r.pieces = append(r.pieces, val)
			b.WriteString(val)
			continue
		}
		if r.tokenEnv != "" && sameEnvKey(tok, r.tokenEnv) {
			return "", fmt.Errorf("${%s} is the Cartographer token variable: a descriptor may not read it", tok)
		}
		val, ok := environValue(r.d.Environ(), tok)
		if !ok {
			return "", fmt.Errorf("${%s} is not set in the environment this launcher inherited: the client was started without it (the \"Connection closed\" case); export it before starting the client", tok)
		}
		r.pieces = append(r.pieces, val)
		b.WriteString(val)
	}
	b.WriteString(v[last:])
	return b.String(), nil
}

func environValue(env []string, name string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && k == name {
			return v, true
		}
	}
	return "", false
}

// secret resolves one reference, one call per name: a value with a newline
// makes a multi-name answer ambiguous, so the answer is the whole text minus
// its NAME= prefix.
func (r *resolver) secret(ctx context.Context, ref contract.SecretRef) (string, error) {
	out, err := r.call(ctx, "secret_resolve", map[string]any{"concept_id": ref.Concept, "names": []string{ref.Name}, "reveal": true})
	if err != nil {
		return "", fmt.Errorf("%w (secret_resolve needs an rw token that allows secrets)", err)
	}
	val, ok := strings.CutPrefix(out, ref.Name+"=")
	if !ok || val == "" || val == redactedText {
		return "", fmt.Errorf("secret %s#%s came back empty or redacted", ref.Concept, ref.Name)
	}
	r.secrets = append(r.secrets, val)
	return val, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
