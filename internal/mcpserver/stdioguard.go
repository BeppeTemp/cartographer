package mcpserver

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// maxStdioLine bounds one inbound line, as the SDK's own frame limit does. It
// is the one case that still ends the session: a line over it is a frame the
// scanner cannot skip without buffering it, and it is far above the HTTP body
// limit, so no real request reaches it.
const maxStdioLine = 16 << 20

// stdioDrainTimeout bounds how long the session outlives EOF on stdin while
// requests read before it are still unanswered (#1448): a handler that hangs
// cannot keep the process alive for ever.
const stdioDrainTimeout = 30 * time.Second

// lockedWriter serializes whole Write calls: the SDK writes each message in one
// call, and the guard's error lines share the same stdout. Every message the
// SDK writes that answers a pending request retires it, after the write.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
	p  *pendingIDs
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	n, err := l.w.Write(p)
	l.mu.Unlock()
	if l.p != nil {
		l.p.answered(p)
	}
	return n, err
}

// pendingIDs is the set of request ids read from stdin and not yet answered.
// The SDK cancels every in-flight request when its reader ends and then does
// not write their responses, so the guard keeps the reader open until the set
// is empty.
type pendingIDs struct {
	mu   sync.Mutex
	ids  map[string]struct{}
	wake chan struct{} // signalled (non-blocking) whenever the set shrinks
}

func newPendingIDs() *pendingIDs {
	return &pendingIDs{ids: map[string]struct{}{}, wake: make(chan struct{}, 1)}
}

type wireMsg struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		RequestID json.RawMessage `json:"requestId"`
	} `json:"params"`
}

func wireMsgs(b []byte) []wireMsg {
	b = bytes.TrimSpace(b)
	var batch []wireMsg
	if len(b) > 0 && b[0] == '[' {
		_ = json.Unmarshal(b, &batch)
		return batch
	}
	var m wireMsg
	if json.Unmarshal(b, &m) == nil {
		batch = append(batch, m)
	}
	return batch
}

func idKey(id json.RawMessage) string { return string(bytes.TrimSpace(id)) }

// started records the requests of one valid inbound line; a client's
// notifications/cancelled retires its target, which the SDK never answers.
func (p *pendingIDs) started(line []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, m := range wireMsgs(line) {
		switch {
		case m.Method == "notifications/cancelled" && len(m.Params.RequestID) > 0:
			delete(p.ids, idKey(m.Params.RequestID))
		case m.Method != "" && len(m.ID) > 0 && string(m.ID) != "null":
			p.ids[idKey(m.ID)] = struct{}{}
		}
	}
}

// answered retires the ids a written message responds to: it has an id and no
// method (one with a method is a server-to-client request).
func (p *pendingIDs) answered(msg []byte) {
	p.mu.Lock()
	for _, m := range wireMsgs(msg) {
		if m.Method == "" && len(m.ID) > 0 {
			delete(p.ids, idKey(m.ID))
		}
	}
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *pendingIDs) empty() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.ids) == 0
}

// drain waits until every pending request is answered or d passes.
func (p *pendingIDs) drain(d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	for !p.empty() {
		select {
		case <-p.wake:
		case <-t.C:
			return
		}
	}
}

// guardStdio sits between stdin and the SDK. The SDK's stdio reader decodes a
// JSON stream and ends the session on the first value it cannot take; here
// every line is checked first, a bad one is answered with the JSON-RPC error
// HTTP gives it (-32700 not JSON, -32600 not a valid request, id null unless
// the request carried a scalar one) and dropped, and the loop reads the next
// line. Only an I/O error on either side ends the session. Messages are one
// per line, as docs/transport-auth.md says.
//
// EOF on stdin is held back until the requests read before it are answered, at
// most stdioDrainTimeout (#1448): the SDK cancels every in-flight request the
// moment its reader ends, and drops their responses.
//
// The caller closes the returned reader when the session ends, which releases
// the goroutine if it is blocked handing a line over; done is closed when it
// has returned. A goroutine blocked reading in itself returns when it closes.
func guardStdio(in io.Reader, out io.Writer) (r *io.PipeReader, w io.Writer, done <-chan struct{}) {
	return guardStdioDrain(in, out, stdioDrainTimeout)
}

func guardStdioDrain(in io.Reader, out io.Writer, drain time.Duration) (r *io.PipeReader, w io.Writer, done <-chan struct{}) {
	pend := newPendingIDs()
	lw := &lockedWriter{w: out, p: pend}
	pr, pw := io.Pipe()
	fin := make(chan struct{})
	go func() {
		defer close(fin)
		sc := bufio.NewScanner(in)
		sc.Buffer(make([]byte, 0, 64<<10), maxStdioLine)
		for sc.Scan() {
			line := bytes.TrimSpace(sc.Bytes())
			if len(line) == 0 {
				continue
			}
			if code, msg := classifyStdioLine(line); code != 0 {
				var id json.RawMessage
				if code != ErrCodeParseError {
					id = requestID(line)
				}
				b, _ := json.Marshal(errorResponse(id, code, msg))
				if _, err := lw.Write(append(b, '\n')); err != nil {
					pw.CloseWithError(err)
					return
				}
				continue
			}
			pend.started(line)
			if _, err := pw.Write(append(append([]byte(nil), line...), '\n')); err != nil {
				return
			}
		}
		if err := sc.Err(); err == nil {
			pend.drain(drain)
		}
		pw.CloseWithError(sc.Err()) // nil is io.EOF for the reader
	}()
	return pr, lw, fin
}

// classifyStdioLine returns the JSON-RPC error code and text for a line that is
// not a valid message, or 0. The text is worded as the SDK words it and goes
// through protocolErrorCode, so both transports classify alike.
func classifyStdioLine(line []byte) (int, string) {
	if !json.Valid(line) {
		return protocolErrorCode("malformed payload: not valid JSON"), "malformed payload: not valid JSON"
	}
	var batch []json.RawMessage
	if json.Unmarshal(line, &batch) == nil {
		if len(batch) == 0 {
			return protocolErrorCode("empty batch"), "empty batch"
		}
	} else {
		batch = []json.RawMessage{line}
	}
	for _, raw := range batch {
		if _, err := jsonrpc.DecodeMessage(raw); err != nil {
			code := protocolErrorCode(err.Error())
			if code == 0 || code == ErrCodeMethodNotFound {
				code = ErrCodeInvalidRequest
			}
			return code, fmt.Sprint(err)
		}
	}
	return 0, ""
}
