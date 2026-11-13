package mcpprobe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BeppeTemp/cartographer/internal/procattr"
)

// SessionTimeout bounds initialize plus tools/list when the caller gives none.
const SessionTimeout = 20 * time.Second

// MaxToolNames is how many tool names a report lists.
const MaxToolNames = 10

// stderrLines is how much of a stdio child's stderr an error carries.
const stderrLines = 20

// Target is a launch ready to talk to: what mcpexec.Resolve produced, copied to
// plain fields so this package keeps no import of the launcher. It holds the
// resolved values; nothing here prints or stores one.
type Target struct {
	// Command, Args and Env start a stdio server (Env is the whole environment).
	Command string
	Args    []string
	Env     []string
	// URL and Headers reach an http server.
	URL     string
	Headers map[string]string
}

// Info is what a working session answered.
type Info struct {
	Protocol string
	Server   string
	Version  string
	Tools    []string // every tool name, in the server's order
}

// Run opens one MCP session with t, lists the tools and closes it, within
// timeout (0 means SessionTimeout). Every error text goes through scrub, which
// removes the resolved values; a stdio child that fails carries the last lines
// of its stderr, scrubbed too.
func Run(ctx context.Context, t Target, timeout time.Duration, scrub func(string) string) (Info, error) {
	if timeout <= 0 {
		timeout = SessionTimeout
	}
	if scrub == nil {
		scrub = func(s string) string { return s }
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var tr mcp.Transport
	var tail *tailBuffer
	if t.Command != "" {
		cmd := procattr.Hide(exec.CommandContext(ctx, t.Command, t.Args...))
		cmd.Env = t.Env
		tail = &tailBuffer{max: stderrLines}
		cmd.Stderr = tail
		cmd.WaitDelay = 2 * time.Second
		tr = &mcp.CommandTransport{Command: cmd}
	} else {
		tr = &mcp.StreamableClientTransport{Endpoint: t.URL, HTTPClient: httpClient(t), MaxRetries: -1, DisableStandaloneSSE: true}
	}
	fail := func(err error) (Info, error) {
		msg := scrub(err.Error())
		if tail != nil {
			if s := tail.String(); s != "" {
				msg += "\nstderr (last lines):\n" + scrub(s)
			}
		}
		// The original error stays wrapped so Classify still sees its kind.
		return Info{}, &sessionError{msg: msg, err: err}
	}
	cl := mcp.NewClient(&mcp.Implementation{Name: "cartographer-probe", Version: "1"}, nil)
	sess, err := cl.Connect(ctx, tr, nil)
	if err != nil {
		return fail(err)
	}
	defer sess.Close()
	var info Info
	if r := sess.InitializeResult(); r != nil {
		info.Protocol = r.ProtocolVersion
		if r.ServerInfo != nil {
			info.Server, info.Version = r.ServerInfo.Name, r.ServerInfo.Version
		}
	}
	for tool, err := range sess.Tools(ctx, nil) {
		if err != nil {
			return fail(fmt.Errorf("tools/list: %w", err))
		}
		info.Tools = append(info.Tools, tool.Name)
	}
	return info, nil
}

// sessionError carries the scrubbed text and the error behind it for Classify.
type sessionError struct {
	msg string
	err error
}

func (e *sessionError) Error() string { return e.msg }
func (e *sessionError) Unwrap() error { return e.err }

// SessionKind is Classify for a session error.
func SessionKind(err error) Kind {
	var se *sessionError
	if errors.As(err, &se) {
		return Classify(se.err)
	}
	return Classify(err)
}

// httpClient adds the descriptor's headers to every request and refuses a
// redirect to another host: the headers belong to the approved endpoint.
func httpClient(t Target) *http.Client {
	host := ""
	if req, err := http.NewRequest(http.MethodGet, t.URL, nil); err == nil {
		host = req.URL.Host
	}
	return &http.Client{
		Transport: headerTripper{h: t.Headers, next: http.DefaultTransport},
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			if !strings.EqualFold(req.URL.Host, host) {
				return errors.New("redirect to another host refused")
			}
			return nil
		},
	}
}

type headerTripper struct {
	h    map[string]string
	next http.RoundTripper
}

func (t headerTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range t.h {
		r.Header.Set(k, v)
	}
	return t.next.RoundTrip(r)
}

// tailBuffer keeps the last max lines written to it.
type tailBuffer struct {
	mu    sync.Mutex
	max   int
	lines []string
	part  bytes.Buffer
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.part.Write(p)
	for {
		s := b.part.String()
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			break
		}
		b.lines = append(b.lines, strings.TrimRight(s[:i], "\r"))
		b.part.Next(i + 1)
		if len(b.lines) > b.max {
			b.lines = b.lines[1:]
		}
	}
	if b.part.Len() > 4096 {
		b.part.Reset()
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	lines := b.lines
	if b.part.Len() > 0 {
		lines = append(append([]string(nil), lines...), b.part.String())
	}
	return strings.Join(lines, "\n")
}
