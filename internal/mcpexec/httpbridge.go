package mcpexec

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
)

const (
	headerSession  = "Mcp-Session-Id"
	headerProtocol = "MCP-Protocol-Version"
	maxLine        = 16 << 20 // one JSON-RPC message
)

// Bridge serves a `type: http` descriptor to a client that only speaks stdio:
// every newline-delimited JSON-RPC message read from in is POSTed to l.URL with
// the resolved Headers, and each message of the answer (JSON or an SSE stream)
// is written to out as one line. It returns nil on EOF of in, after a
// best-effort session DELETE. errw gets one-line diagnostics; neither it, out
// nor any error carries a header value.
func (l *Launch) Bridge(ctx context.Context, in io.Reader, out, errw io.Writer) error {
	if l.Spec.Type != "http" || l.URL == "" {
		return errors.New("the descriptor is not an http server")
	}
	if err := client.CheckServerURL(l.URL, false); err != nil {
		return fmt.Errorf("%s", client.Escape(err.Error()))
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 2 * time.Minute
	b := &bridge{l: l, out: out, errw: errw, ctx: ctx, hc: &http.Client{
		Transport: tr,
		// A redirect would carry the descriptor's headers to a host nobody approved.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	lines := make(chan []byte)
	go func() {
		defer close(lines)
		r := bufio.NewReaderSize(in, 64<<10)
		for {
			line, err := r.ReadBytes('\n')
			if len(line) > maxLine {
				return
			}
			if t := bytes.TrimSpace(line); len(t) > 0 {
				select {
				case lines <- t:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			b.closeSession()
			return nil
		case line, ok := <-lines:
			if !ok {
				b.closeSession()
				return nil
			}
			b.forward(line)
		}
	}
}

type bridge struct {
	l        *Launch
	out      io.Writer
	errw     io.Writer
	ctx      context.Context
	hc       *http.Client
	mu       sync.Mutex
	session  string
	protocol string
}

// scrub removes every resolved value from a message.
func (b *bridge) scrub(s string) string {
	for _, v := range b.l.secrets {
		s = strings.ReplaceAll(s, v, redactedText)
	}
	for k, v := range b.l.Headers {
		if b.l.redactHdr[k] && v != "" {
			s = strings.ReplaceAll(s, v, redactedText)
		}
	}
	return client.Escape(s)
}

func (b *bridge) setHeaders(req *http.Request) {
	for k, v := range b.l.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if b.session != "" {
		req.Header.Set(headerSession, b.session)
	}
	if b.protocol != "" {
		req.Header.Set(headerProtocol, b.protocol)
	}
}

// secretHeaders names the headers whose value held a reference.
func (b *bridge) secretHeaders() string {
	var names []string
	for k := range b.l.redactHdr {
		if b.l.redactHdr[k] {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func (b *bridge) write(msg []byte) {
	var c bytes.Buffer
	if json.Compact(&c, msg) != nil {
		return
	}
	c.WriteByte('\n')
	b.mu.Lock()
	b.out.Write(c.Bytes())
	b.mu.Unlock()
}

func (b *bridge) fail(id json.RawMessage, msg string) {
	fmt.Fprintln(b.errw, "mcp exec:", msg)
	if len(id) == 0 || string(id) == "null" {
		return
	}
	e, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32000, "message": msg}})
	b.write(e)
}

func (b *bridge) forward(line []byte) {
	var head struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	_ = json.Unmarshal(line, &head)
	isRequest := len(head.ID) > 0 && string(head.ID) != "null" && head.Method != ""
	var id json.RawMessage
	if isRequest {
		id = head.ID
	}
	req, err := http.NewRequestWithContext(b.ctx, http.MethodPost, b.l.URL, bytes.NewReader(line))
	if err != nil {
		b.fail(id, "cannot build the request to the MCP server")
		return
	}
	b.setHeaders(req)
	resp, err := b.hc.Do(req)
	if err != nil {
		cause := err
		if u := errors.Unwrap(err); u != nil {
			cause = u
		}
		b.fail(id, "request to the MCP server failed: "+b.scrub(cause.Error()))
		return
	}
	defer resp.Body.Close()
	if head.Method == "initialize" {
		if s := resp.Header.Get(headerSession); s != "" {
			b.session = s
		}
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		msg := "the MCP server answered 401"
		if n := b.secretHeaders(); n != "" {
			msg += "; check the secret behind the header " + n
		}
		b.fail(id, msg)
		return
	case resp.StatusCode == http.StatusAccepted:
		return
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		b.fail(id, fmt.Sprintf("the MCP server answered HTTP %d", resp.StatusCode))
		return
	}
	got := 0
	emit := func(msg []byte) {
		if head.Method == "initialize" {
			var r struct {
				Result struct {
					ProtocolVersion string `json:"protocolVersion"`
				} `json:"result"`
			}
			if json.Unmarshal(msg, &r) == nil && r.Result.ProtocolVersion != "" {
				b.protocol = r.Result.ProtocolVersion
			}
		}
		got++
		b.write(msg)
	}
	if mt, _, _ := strings.Cut(resp.Header.Get("Content-Type"), ";"); strings.EqualFold(strings.TrimSpace(mt), "text/event-stream") {
		readSSE(resp.Body, emit)
	} else {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxLine))
		if t := bytes.TrimSpace(body); len(t) > 0 {
			emit(t)
		}
	}
	if got == 0 && isRequest {
		b.fail(id, "the MCP server sent no response")
	}
}

// readSSE calls emit with the data of each event of a text/event-stream body.
func readSSE(r io.Reader, emit func([]byte)) {
	br := bufio.NewReaderSize(r, 64<<10)
	var data [][]byte
	flush := func() {
		if len(data) > 0 {
			emit(bytes.Join(data, []byte("\n")))
		}
		data = nil
	}
	for {
		line, err := br.ReadBytes('\n')
		line = bytes.TrimRight(line, "\r\n")
		switch {
		case len(line) == 0 && err == nil:
			flush()
		case bytes.HasPrefix(line, []byte("data:")):
			data = append(data, bytes.TrimPrefix(bytes.TrimPrefix(line, []byte("data:")), []byte(" ")))
		}
		if err != nil {
			flush()
			return
		}
	}
}

// closeSession ends the server session, best effort, within two seconds.
func (b *bridge) closeSession() {
	if b.session == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, b.l.URL, nil)
	if err != nil {
		return
	}
	b.setHeaders(req)
	req.Header.Del("Content-Type")
	if resp, err := b.hc.Do(req); err == nil {
		resp.Body.Close()
	}
}
