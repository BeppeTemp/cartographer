package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// The per-KB event stream, GET /api/v1/kbs/{kb}/events (server-sent events).
//
// Protocol. `id` is a commit sha and the cursor: a client that reconnects with
// Last-Event-ID (or ?cursor=) is sent what came after it. Events: hello (no
// cursor given), changes (the changes_since payload of cursor..head, filtered
// with this connection's principal), reset (the cursor is unknown or the gap
// too large: refetch /changes), end (the server closes: reason says why).
//
// TRAP (filtered cursor): the watcher's wake-up carries no data, and each
// connection computes its own payload. A commit that touches nothing the
// principal may see produces no event, but the cursor still advances past it,
// so the next visible event has a `from` that is not the previous event's id and
// its payload holds only visible concepts. A narrowed token therefore never
// learns that a hidden collection moved. TestAPIEvents_NarrowedCursorAdvances
// pins it.

// Stream limits; vars so tests shorten them.
var (
	apiStreamPoll      = 2 * time.Second
	apiStreamPollMax   = 30 * time.Second
	apiStreamHeartbeat = 20 * time.Second
	apiStreamMaxAge    = 30 * time.Minute
	apiStreamWrite     = 10 * time.Second
	// apiStreamRetry is the reconnect delay the client is told to wait (ms).
	apiStreamRetry = 5000
)

// apiStreamBatch is the most commits one changes event carries: more is a reset.
const apiStreamBatch = 500

const (
	uiCodeTooManyStreams = "too_many_streams"
	uiCodeUnsupported    = "unsupported"
	uiCodeUnavailable    = "unavailable"
)

// The data of each event, for the OpenAPI document.
type (
	apiStreamHello struct {
		Head string `json:"head"`
		KB   string `json:"kb"`
	}
	apiStreamChanges struct {
		From    string             `json:"from"`
		Head    string             `json:"head"`
		Changes changesSinceResult `json:"changes"`
	}
	apiStreamReset struct {
		Head   string `json:"head"`
		Reason string `json:"reason"`
	}
	apiStreamEnd struct {
		Reason string `json:"reason"`
	}
)

// streamHub counts the open streams, shares one watcher per KB and ends every
// stream at shutdown. Its zero value is ready.
type streamHub struct {
	mu       sync.Mutex
	perToken map[string]int
	total    int
	watchers map[string]*kbWatcher
	closed   bool
	once     sync.Once
	done     chan struct{}
	wg       sync.WaitGroup
}

func (h *streamHub) init() {
	h.once.Do(func() { h.done = make(chan struct{}) })
}

// admit takes a slot for token or says why not. The check and the increment are
// one critical section: two requests cannot both take the last slot.
func (h *streamHub) admit(token string, perToken, max int) (release func(), why string) {
	h.init()
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case h.closed:
		return nil, "shutdown"
	case h.perToken[token] >= perToken || h.total >= max:
		return nil, "cap"
	}
	if h.perToken == nil {
		h.perToken = map[string]int{}
	}
	h.perToken[token]++
	h.total++
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if h.perToken[token]--; h.perToken[token] <= 0 {
				delete(h.perToken, token)
			}
			h.total--
		})
	}, ""
}

// closeAll ends every stream (each writes `end` shutdown) and refuses new ones.
func (h *streamHub) closeAll() {
	h.init()
	h.mu.Lock()
	if !h.closed {
		h.closed = true
		close(h.done)
	}
	h.mu.Unlock()
}

// wait blocks until every watcher goroutine has exited (tests).
func (h *streamHub) wait() { h.wg.Wait() }

// kbWatcher polls one KB's HEAD and wakes its subscribers when it moves. Started
// by the first subscriber, stopped with the last.
type kbWatcher struct {
	ctx    context.Context
	cancel context.CancelFunc
	subs   map[chan struct{}]struct{}
	// ready closes once the watcher holds the head it compares against.
	ready chan struct{}
}

// apiStreamHead reads a KB's HEAD; a variable so a test can make git hang.
var apiStreamHead = gitx.HeadSHA

// subscribe registers for wake-ups on k and returns once the watcher holds a
// head: a commit that lands after the caller's own read of the head, which it
// makes after subscribe returns, is then a change the watcher sees, and none
// falls between the two. The head is read outside streamHub.mu (git is an
// exec, and every admit, release and fan-out takes the lock); the first
// subscriber of a KB reads it, the others wait for it, or for ctx. ok false:
// ctx ended first.
func (h *streamHub) subscribe(ctx context.Context, name string, k *kb.KB) (wake <-chan struct{}, unsub func(), ok bool) {
	h.init()
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	w := h.watchers[name]
	creator := w == nil
	if creator {
		wctx, cancel := context.WithCancel(context.Background())
		w = &kbWatcher{ctx: wctx, cancel: cancel, subs: map[chan struct{}]struct{}{}, ready: make(chan struct{})}
		if h.watchers == nil {
			h.watchers = map[string]*kbWatcher{}
		}
		h.watchers[name] = w
		h.wg.Add(1)
	}
	w.subs[ch] = struct{}{}
	h.mu.Unlock()

	unsub = func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(w.subs, ch)
		if len(w.subs) == 0 {
			w.cancel()
			if h.watchers[name] == w {
				delete(h.watchers, name)
			}
		}
	}
	if creator {
		last, _ := apiStreamHead(k.Root)
		go func() {
			defer h.wg.Done()
			h.watch(w.ctx, w, k, last)
		}()
		close(w.ready)
		return ch, unsub, true
	}
	select {
	case <-w.ready:
		return ch, unsub, true
	case <-ctx.Done():
		unsub()
		return nil, nil, false
	}
}

// watch polls HEAD until ctx ends. A failing read backs the poll off, doubling
// up to apiStreamPollMax, and the next good read resets it.
func (h *streamHub) watch(ctx context.Context, w *kbWatcher, k *kb.KB, last string) {
	delay := apiStreamPoll
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		head, err := gitx.HeadSHA(k.Root)
		switch {
		case err != nil:
			if delay *= 2; delay > apiStreamPollMax {
				delay = apiStreamPollMax
			}
		default:
			delay = apiStreamPoll
			if head != last {
				last = head
				h.mu.Lock()
				for ch := range w.subs {
					select { // a slow subscriber coalesces: one pending tick is enough
					case ch <- struct{}{}:
					default:
					}
				}
				h.mu.Unlock()
			}
		}
		timer.Reset(delay)
	}
}

// CloseStreams ends every open event stream with `end` {"reason":"shutdown"}
// and refuses new ones. serve calls it before http.Server.Shutdown, which would
// otherwise wait for streams that never finish on their own.
func (m *MultiKBServer) CloseStreams() { m.streams.closeAll() }

// sseWriter writes frames and flushes each one. Every write has a deadline: a
// client that stops reading must not pin the handler (and a slot) forever.
type sseWriter struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

func (s *sseWriter) write(text string) error {
	// A deadline that cannot be set is fatal: without it a client that stops
	// reading would pin the handler and its slot until max_age.
	if err := s.rc.SetWriteDeadline(time.Now().Add(apiStreamWrite)); err != nil {
		return err
	}
	if _, err := s.w.Write([]byte(text)); err != nil {
		return err
	}
	return s.rc.Flush()
}

// frame writes one event. data is single-line JSON, so it can never carry the
// blank line that ends a frame.
func (s *sseWriter) frame(id, event string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	var b strings.Builder
	if id != "" {
		b.WriteString("id: " + id + "\n")
	}
	b.WriteString("event: " + event + "\ndata: " + string(raw) + "\n\n")
	return s.write(b.String())
}

func canFlush(w http.ResponseWriter) bool {
	for {
		if _, ok := w.(http.Flusher); ok {
			return true
		}
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return false
		}
		w = u.Unwrap()
	}
}

// apiEvents serves GET /kbs/{kb}/events. Only the public mount serves it: the
// Atlas polls (D337) and its mount has no stream caps.
func (m *MultiKBServer) apiEvents(w http.ResponseWriter, r *http.Request, name string, srv *Server) {
	if m.api == nil || !strings.HasPrefix(r.URL.Path, APIPrefix+"/") {
		writeUINotFound(w)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeUIError(w, http.StatusMethodNotAllowed, uiCodeMethodNotAllow, "the event stream is GET only", "")
		return
	}
	// Verify up front that write deadlines work: see sseWriter.write.
	rc := http.NewResponseController(w)
	if !canFlush(w) || rc.SetWriteDeadline(time.Now().Add(apiStreamWrite)) != nil {
		writeUIError(w, http.StatusInternalServerError, uiCodeUnsupported, "this connection cannot stream", "")
		return
	}
	k := srv.kbRef
	principal := auth.PrincipalFromContext(r.Context())
	release, why := m.streams.admit(principal.ID, m.api.MaxStreamsPerToken, m.api.MaxStreams)
	if release == nil {
		if why == "shutdown" {
			writeUIError(w, http.StatusServiceUnavailable, uiCodeUnavailable, "the server is shutting down", "")
			return
		}
		w.Header().Set("Retry-After", "5")
		writeUIError(w, http.StatusTooManyRequests, uiCodeTooManyStreams, "too many open event streams", "")
		return
	}
	defer release()

	// Subscribe before reading the head: see subscribe.
	wake, unsub, ok := m.streams.subscribe(r.Context(), name, k)
	if !ok {
		return // the client left while the watcher was starting
	}
	defer unsub()

	// The cursor is client input: only a full sha reaches git (gitx validates).
	cursor := r.Header.Get("Last-Event-ID")
	if cursor == "" {
		cursor = r.URL.Query().Get("cursor")
	}
	if len(cursor) > 128 {
		cursor = "x" // longer than any object name: unknown, whatever it holds
	}

	c := &apiStream{
		m: m, name: name, srv: srv, k: k, r: r, cursor: cursor,
		sse: &sseWriter{w: w, rc: rc},
		ctx: r.Context(), recheck: auth.RecheckFromContext(r.Context()),
	}
	c.run(wake)
}

// apiStream is one open connection.
type apiStream struct {
	m      *MultiKBServer
	name   string
	srv    *Server
	k      *kb.KB
	r      *http.Request
	sse    *sseWriter
	cursor string
	// ctx carries the principal now in force; recheck refreshes it.
	ctx     context.Context
	recheck auth.Recheck
}

func (c *apiStream) run(wake <-chan struct{}) {
	h := c.sse.w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Accel-Buffering", "no")
	// TRAP: the server's ReadTimeout is a read deadline on the connection that
	// stays armed while a handler runs; its background read would fail at the
	// deadline and cancel the request context, killing every stream after a
	// minute. A stream has no more to read.
	_ = c.sse.rc.SetReadDeadline(time.Time{})
	c.sse.w.WriteHeader(http.StatusOK)
	// retry: the reconnect back-off the client is told to use.
	if c.sse.write("retry: "+strconv.Itoa(apiStreamRetry)+"\n\n") != nil {
		return
	}

	if c.cursor == "" {
		head, err := gitx.HeadSHA(c.k.Root)
		if err != nil {
			head = "" // an unborn KB: the first commit is announced as a reset
		}
		if c.sse.frame(head, "hello", apiStreamHello{Head: head, KB: c.name}) != nil {
			return
		}
		c.cursor = head
	} else if !c.step() {
		return
	}

	heartbeat := time.NewTicker(apiStreamHeartbeat)
	defer heartbeat.Stop()
	maxAge := time.NewTimer(apiStreamMaxAge)
	defer maxAge.Stop()
	for {
		select {
		case <-c.r.Context().Done():
			return
		case <-c.m.streams.done:
			c.end("shutdown")
			return
		case <-maxAge.C:
			c.end("max_age")
			return
		case <-heartbeat.C:
			if c.sse.write(": hb\n\n") != nil {
				return
			}
			// A missed wake-up costs at most one heartbeat.
			if !c.alive() || !c.step() {
				return
			}
		case <-wake:
			if !c.alive() || !c.step() {
				return
			}
		}
	}
}

func (c *apiStream) end(reason string) {
	_ = c.sse.frame("", "end", apiStreamEnd{Reason: reason})
}

// alive re-checks what the connection was admitted on, at every wake-up and
// heartbeat: the token (revoked, expired, narrowed by a reload: the narrowed
// principal is used from here on), the KB still mounted and still visible. It
// ends the stream when any fails, with a reason that says no more than the 401
// or 404 the same request would have got.
func (c *apiStream) alive() bool {
	if c.recheck != nil {
		p, ok := c.recheck()
		if !ok {
			c.end("token_expired")
			return false
		}
		c.ctx = auth.ContextWithPrincipal(c.r.Context(), p)
	}
	if c.m.servers[c.name] != c.srv || !uiKBVisible(c.ctx, c.k) {
		c.end("access_changed")
		return false
	}
	return true
}

// step sends what happened between the cursor and HEAD, filtered for the
// connection's principal, and moves the cursor to the head it read: events and
// cursor come from one snapshot (gitx.LogNameStatusRange), so a commit landing
// meanwhile is after both and the next step sends it. False: stop.
func (c *apiStream) step() bool {
	commits, head, err := gitx.LogNameStatusRange(c.k.Root, c.cursor)
	switch {
	case errors.Is(err, gitx.ErrUnknownRev), errors.Is(err, gitx.ErrRangeTooLarge):
		reason := "unknown_cursor"
		if errors.Is(err, gitx.ErrRangeTooLarge) {
			reason = "too_many_commits"
		}
		return c.reset(reason)
	case err != nil:
		return true // git failed: keep the cursor, try at the next wake-up
	case len(commits) == 0:
		return true
	case len(commits) > apiStreamBatch:
		return c.reset("too_many_commits")
	}
	since, err := gitx.CommitTime(c.k.Root, c.cursor)
	if err != nil {
		return true
	}
	res, err := buildChangesSince(c.ctx, c.k, commits, since, head, apiStreamBatch, false, true)
	if err != nil {
		return true
	}
	from := c.cursor
	c.cursor = head
	if len(res.Concepts) == 0 {
		return true // nothing this principal may see: the cursor moved, silently
	}
	return c.sse.frame(head, "changes", apiStreamChanges{From: from, Head: head, Changes: res}) == nil
}

// reset tells the client to refetch /changes and continues from the head.
func (c *apiStream) reset(reason string) bool {
	head, err := gitx.HeadSHA(c.k.Root)
	if err != nil {
		return true
	}
	c.cursor = head
	return c.sse.frame(head, "reset", apiStreamReset{Head: head, Reason: reason}) == nil
}
