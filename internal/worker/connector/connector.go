// Package connector mounts allowed MCP tools.
//
// The tool list and the descriptions come from a third-party server: untrusted
// text the model reads. So a tool is offered only if the config lists it
// (deny by default), a call to any other name is refused here before it reaches
// the server, and credentials are the NAMES of environment variables, never
// values. A connector starts on first use and stays for the run; one that fails
// to start or answer is unavailable, not fatal.
//
// The package does not import internal/worker: that package's tool-set builder
// imports this one. It also holds what the worker does before it starts any child
// process it does not trust with its own environment (ChildEnv, KillGroupOnCancel,
// RedactEnv); the exec recipes of the same plan reuse them.
package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Transports a connector speaks.
const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
)

// Separator joins the connector name and a tool name in the name offered to the
// model. No KB tool name contains it (a test in internal/mcpserver pins that),
// so routing by prefix cannot shadow a server tool.
const Separator = "__"

const (
	maxDescriptionBytes = 300
	maxSchemaBytes      = 8 << 10
)

// Config is one connector as the worker config declares it, already validated.
type Config struct {
	Name      string
	Transport string
	// stdio
	Command string // absolute, or a bare name looked up on PATH
	Args    []string
	Env     map[string]string // name the child sees -> worker variable holding the value
	// http
	URL               string
	AllowInsecureHTTP bool
	TokenEnv          string
	Headers           map[string]string // header -> worker variable holding the value
	// both
	Tools          []string
	CallTimeout    time.Duration
	MaxResultBytes int
}

// Tool is one allowed tool, named as the model sees it.
type Tool struct {
	Name        string // <connector>__<tool>
	Description string
	InputSchema any
}

// Result is the answer of a tool call.
type Result struct {
	Text    string
	IsError bool
}

// Set is the connectors of one run.
type Set struct {
	ctx  context.Context
	logf func(format string, args ...any)
	cfgs map[string]Config

	mu   sync.Mutex
	live map[string]*conn
}

type conn struct {
	once    sync.Once
	session *mcp.ClientSession
	cancel  context.CancelFunc // ends a stdio child and its process group
	cmd     *exec.Cmd
	offered map[string]bool // server tool names offered
	tools   []Tool
	err     error
}

// Open prepares the connectors; none is started until first use. ctx bounds
// the lifetime of every child process. logf may be nil.
func Open(ctx context.Context, cfgs []Config, logf func(string, ...any)) *Set {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Set{ctx: ctx, logf: logf, cfgs: map[string]Config{}, live: map[string]*conn{}}
	for _, c := range cfgs {
		s.cfgs[c.Name] = c
	}
	return s
}

// Has reports whether the config declares a connector of that name.
func (s *Set) Has(name string) bool { _, ok := s.cfgs[name]; return ok }

// Tools starts the named connector if needed and returns its allowed tools. An
// error means the connector is unavailable for this run (it is not retried):
// the caller skips the units whose Source names it.
func (s *Set) Tools(ctx context.Context, name string) ([]Tool, error) {
	c, err := s.start(ctx, name)
	if err != nil {
		return nil, err
	}
	return c.tools, nil
}

// Call runs an allowed tool by its qualified name. A name outside the allowlist
// is refused without any request to the server.
func (s *Set) Call(ctx context.Context, qualified string, args map[string]any) (Result, error) {
	name, tool, ok := strings.Cut(qualified, Separator)
	cfg, known := s.cfgs[name]
	if !ok || !known {
		return Result{}, fmt.Errorf("connector: %q is not a connector tool", qualified)
	}
	c, err := s.start(ctx, name)
	if err != nil {
		return Result{}, err
	}
	if !c.offered[tool] {
		return Result{}, fmt.Errorf("connector %s: tool %q is not allowed", name, tool)
	}
	cctx, cancel := context.WithTimeout(ctx, cfg.CallTimeout)
	defer cancel()
	res, err := c.session.CallTool(cctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return Result{}, fmt.Errorf("connector %s: calling %s: %w", name, tool, err)
	}
	// Stop appending once the cap is reached: a huge result is never built whole.
	var b strings.Builder
	truncated := false
	for _, blk := range res.Content {
		t, ok := blk.(*mcp.TextContent)
		if !ok {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		if room := cfg.MaxResultBytes - b.Len(); len(t.Text) > room {
			if room > 0 {
				b.WriteString(cutUTF8(t.Text, room))
			}
			truncated = true
			break
		}
		b.WriteString(t.Text)
	}
	text := b.String()
	if truncated || len(text) > cfg.MaxResultBytes {
		text = cutUTF8(text, cfg.MaxResultBytes) + "\n[truncated]"
	}
	return Result{Text: text, IsError: res.IsError}, nil
}

// Close ends every started connector.
func (s *Set) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.live {
		if c.session != nil {
			_ = c.session.Close()
		}
		if c.cancel != nil {
			c.cancel()
		}
		if c.cmd != nil {
			// The connector's own exit does not take its grandchildren with it.
			KillGroup(c.cmd)
		}
	}
	s.live = map[string]*conn{}
}

func (s *Set) start(ctx context.Context, name string) (*conn, error) {
	cfg, ok := s.cfgs[name]
	if !ok {
		return nil, fmt.Errorf("connector %q is not declared in the worker config", name)
	}
	s.mu.Lock()
	c := s.live[name]
	if c == nil {
		c = &conn{}
		s.live[name] = c
	}
	s.mu.Unlock()
	c.once.Do(func() { c.err = s.connect(ctx, cfg, c) })
	if c.err != nil {
		return nil, c.err
	}
	return c, nil
}

func (s *Set) connect(ctx context.Context, cfg Config, c *conn) error {
	tr, cancel, err := s.transport(cfg)
	if ct, ok := tr.(*mcp.CommandTransport); ok {
		c.cmd = ct.Command
	}
	if err != nil {
		return fmt.Errorf("connector %s: %w", cfg.Name, err)
	}
	c.cancel = cancel
	sctx, cancel := context.WithTimeout(ctx, cfg.CallTimeout)
	defer cancel()
	cl := mcp.NewClient(&mcp.Implementation{Name: "cartographer-worker", Version: "1"}, nil)
	sess, err := cl.Connect(sctx, tr, nil)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return fmt.Errorf("connector %s: cannot start: %w", cfg.Name, err)
	}
	list, err := sess.ListTools(sctx, nil)
	if err != nil {
		_ = sess.Close()
		if cancel != nil {
			cancel()
		}
		return fmt.Errorf("connector %s: listing tools: %w", cfg.Name, err)
	}
	allowed := map[string]bool{}
	for _, t := range cfg.Tools {
		allowed[t] = true
	}
	c.session, c.offered = sess, map[string]bool{}
	for _, t := range list.Tools {
		if !allowed[t.Name] {
			continue
		}
		if raw, _ := json.Marshal(t.InputSchema); len(raw) > maxSchemaBytes {
			s.logf("worker: connector %s: tool %s not offered: its input schema is over %d bytes", cfg.Name, t.Name, maxSchemaBytes)
			continue
		}
		c.offered[t.Name] = true
		c.tools = append(c.tools, Tool{Name: cfg.Name + Separator + t.Name, Description: cutUTF8(t.Description, maxDescriptionBytes), InputSchema: t.InputSchema})
	}
	return nil
}

func (s *Set) transport(cfg Config) (mcp.Transport, context.CancelFunc, error) {
	switch cfg.Transport {
	case TransportStdio:
		env, err := ChildEnv(cfg.Env)
		if err != nil {
			return nil, nil, err
		}
		pctx, cancel := context.WithCancel(s.ctx)
		cmd := exec.CommandContext(pctx, cfg.Command, cfg.Args...)
		cmd.Env = env
		KillGroupOnCancel(cmd)
		return &mcp.CommandTransport{Command: cmd}, cancel, nil
	case TransportHTTP:
		if err := client.CheckServerURL(cfg.URL, cfg.AllowInsecureHTTP); err != nil {
			return nil, nil, err
		}
		hdr := http.Header{}
		if cfg.TokenEnv != "" {
			v, err := EnvValue(cfg.TokenEnv)
			if err != nil {
				return nil, nil, err
			}
			hdr.Set("Authorization", "Bearer "+v)
		}
		for h, src := range cfg.Headers {
			v, err := EnvValue(src)
			if err != nil {
				return nil, nil, err
			}
			hdr.Set(h, v)
		}
		u, err := url.Parse(cfg.URL)
		if err != nil {
			return nil, nil, errors.New("not a valid URL")
		}
		hc := client.HTTPClient(&http.Client{Transport: headerTripper{h: hdr, host: u.Host}}, cfg.AllowInsecureHTTP)
		base := hc.CheckRedirect
		hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			// The credentials belong to the configured endpoint: a redirect to
			// another host is refused, not followed without them.
			if !strings.EqualFold(req.URL.Host, u.Host) {
				return fmt.Errorf("refusing redirect to another host (%s)", req.URL.Hostname())
			}
			return base(req, via)
		}
		return &mcp.StreamableClientTransport{Endpoint: cfg.URL, HTTPClient: hc, DisableStandaloneSSE: true}, nil, nil
	}
	return nil, nil, fmt.Errorf("unknown transport %q", cfg.Transport)
}

// headerTripper adds the configured credentials and headers to a request, and
// only when it goes to the configured endpoint's host (port included): net/http
// keeps custom headers across a redirect and re-runs RoundTrip on the next hop,
// so the host check here, and the refusal of cross-host redirects in
// CheckRedirect, keep the token on the endpoint the operator named.
type headerTripper struct {
	h    http.Header
	host string
}

func (t headerTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.EqualFold(r.URL.Host, t.host) {
		r = r.Clone(r.Context())
		for k, v := range t.h {
			r.Header[k] = v
		}
	}
	return http.DefaultTransport.RoundTrip(r)
}

func cutUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
