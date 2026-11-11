package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// These tests change the stream timings, which are package variables, so none
// of them is parallel.

func fastStreams(t *testing.T) {
	t.Helper()
	poll, hb, age := apiStreamPoll, apiStreamHeartbeat, apiStreamMaxAge
	apiStreamPoll, apiStreamHeartbeat = 15*time.Millisecond, 150*time.Millisecond
	t.Cleanup(func() { apiStreamPoll, apiStreamHeartbeat, apiStreamMaxAge = poll, hb, age })
}

var (
	wideToken   = auth.ScopedToken{Token: "wide", Principal: "wide", Policy: auth.Policy{Admin: true}}
	wide2Token  = auth.ScopedToken{Token: "wide2", Principal: "wide2", Policy: auth.Policy{Admin: true}}
	narrowToken = auth.ScopedToken{Token: "narrow", Principal: "narrow",
		Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"ops"}}}}}
)

type streamFixture struct {
	c     *changesLinksKB
	multi *MultiKBServer
	ts    *auth.TokenStore
	srv   *httptest.Server
}

func newStreamFixture(t *testing.T, cfg APIConfig, tokens ...auth.ScopedToken) *streamFixture {
	t.Helper()
	c := newChangesLinksKB(t)
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, c.k, Deps{}) })
	multi.EnableWeb(nil)
	multi.EnableAPI(cfg)
	ts := auth.NewScopedTokenStore(tokens).RequireAuth()
	f := &streamFixture{c: c, multi: multi, ts: ts, srv: httptest.NewServer(ts.Middleware(multi.Handler()))}
	t.Cleanup(func() {
		multi.CloseStreams()
		f.srv.Close()
		done := make(chan struct{})
		go func() { multi.streams.wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("a watcher goroutine outlived the server")
		}
	})
	return f
}

func (f *streamFixture) write(id string) {
	f.c.t.Helper()
	writeAs(f.c.t, f.c.s, authLocalContext(), id, nil)
}

func (f *streamFixture) head() string {
	h, err := gitx.HeadSHA(f.c.k.Root)
	if err != nil {
		f.c.t.Fatal(err)
	}
	return h
}

type sseEvent struct{ id, event, data string }

type sseStream struct {
	resp   *http.Response
	events chan sseEvent
	cancel context.CancelFunc
}

// open connects and parses the stream in a goroutine; a comment line `: hb`
// arrives as the event "hb". A non-200 answer has no events.
func (f *streamFixture) open(t *testing.T, token, lastID, query string) *sseStream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.srv.URL+APIPrefix+"/kbs/docs/events"+query, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	s := &sseStream{resp: resp, events: make(chan sseEvent, 256), cancel: cancel}
	go func() {
		defer close(s.events)
		defer resp.Body.Close()
		var cur sseEvent
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if cur.event != "" {
					s.events <- cur
				}
				cur = sseEvent{}
			case line == ": hb":
				s.events <- sseEvent{event: "hb"}
			case strings.HasPrefix(line, "id: "):
				cur.id = line[4:]
			case strings.HasPrefix(line, "event: "):
				cur.event = line[7:]
			case strings.HasPrefix(line, "data: "):
				cur.data = line[6:]
			}
		}
	}()
	t.Cleanup(cancel)
	return s
}

func (s *sseStream) next(t *testing.T, want string) sseEvent {
	t.Helper()
	for {
		select {
		case ev, ok := <-s.events:
			if !ok {
				t.Fatalf("stream closed while waiting for %q", want)
			}
			if ev.event == "hb" && want != "hb" {
				continue
			}
			if ev.event != want {
				t.Fatalf("got event %q (%s), want %q", ev.event, ev.data, want)
			}
			return ev
		case <-time.After(5 * time.Second):
			t.Fatalf("no %q event in 5s", want)
		}
	}
}

// quiet fails if the stream sends anything but heartbeats for d.
func (s *sseStream) quiet(t *testing.T, d time.Duration) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case ev, ok := <-s.events:
			if ok && ev.event != "hb" {
				t.Fatalf("unexpected event %q: %s", ev.event, ev.data)
			}
			if !ok {
				t.Fatal("stream closed")
			}
		case <-deadline:
			return
		}
	}
}

type changesData struct {
	From    string `json:"from"`
	Head    string `json:"head"`
	Changes struct {
		Concepts []struct {
			ID string `json:"id"`
		} `json:"concepts"`
	} `json:"changes"`
}

func (d changesData) ids() string {
	var out []string
	for _, c := range d.Changes.Concepts {
		out = append(out, c.ID)
	}
	sort.Strings(out) // the payload orders by commit second, then id
	return strings.Join(out, ",")
}

func decodeChanges(t *testing.T, ev sseEvent) changesData {
	t.Helper()
	var d changesData
	if err := json.Unmarshal([]byte(ev.data), &d); err != nil {
		t.Fatalf("changes data %q: %v", ev.data, err)
	}
	return d
}

func TestAPIEvents_HelloThenChange(t *testing.T) {
	fastStreams(t)
	f := newStreamFixture(t, APIConfig{MaxStreamsPerToken: 4, MaxStreams: 8}, wideToken)
	f.write("ops/first")
	base := f.head()
	s := f.open(t, "wide", "", "")
	if ct := s.resp.Header.Get("Content-Type"); ct != "text/event-stream" || s.resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("headers: %v", s.resp.Header)
	}
	hello := s.next(t, "hello")
	if hello.id != base || !strings.Contains(hello.data, `"kb":"docs"`) {
		t.Fatalf("hello = %+v, head %s", hello, base)
	}
	f.write("ops/second")
	ch := s.next(t, "changes")
	d := decodeChanges(t, ch)
	if ch.id != f.head() || d.Head != ch.id || d.From != base || d.ids() != "ops/second" {
		t.Fatalf("changes = %+v id %s head %s", d, ch.id, f.head())
	}
}

func TestAPIEvents_ResumeReplaysOnceInOrder(t *testing.T) {
	fastStreams(t)
	f := newStreamFixture(t, APIConfig{MaxStreamsPerToken: 4, MaxStreams: 8}, wideToken)
	f.write("ops/first")
	last := f.head()
	f.write("ops/second")
	f.write("other/third")
	head := f.head()

	// Header and ?cursor= both continue from the sha; the header wins.
	s := f.open(t, "wide", last, "")
	d := decodeChanges(t, s.next(t, "changes"))
	if d.From != last || d.Head != head || d.ids() != "ops/second,other/third" {
		t.Fatalf("replay = %+v", d)
	}
	s.quiet(t, 200*time.Millisecond) // once: nothing repeats
	s2 := f.open(t, "wide", "", "?cursor="+last)
	if d2 := decodeChanges(t, s2.next(t, "changes")); d2.Head != head {
		t.Fatalf("?cursor= replay = %+v", d2)
	}
	// A commit after the reconnect arrives with the previous head as from.
	f.write("ops/fourth")
	if d3 := decodeChanges(t, s.next(t, "changes")); d3.From != head || d3.ids() != "ops/fourth" {
		t.Fatalf("after replay = %+v", d3)
	}
}

func TestAPIEvents_UnknownCursorIsAReset(t *testing.T) {
	fastStreams(t)
	f := newStreamFixture(t, APIConfig{MaxStreamsPerToken: 8, MaxStreams: 16}, wideToken)
	f.write("ops/first")
	for _, bad := range []string{strings.Repeat("a", 40), "HEAD", "--all", "../../x", strings.Repeat("f", 500)} {
		s := f.open(t, "wide", bad, "")
		ev := s.next(t, "reset")
		if ev.id != f.head() || !strings.Contains(ev.data, "unknown_cursor") {
			t.Errorf("cursor %.20q: reset = %+v", bad, ev)
		}
	}
}

// A commit the principal may not see moves the cursor and emits nothing; the
// next visible commit carries only visible concepts and a from that is not an
// id the client was ever sent.
func TestAPIEvents_NarrowedCursorAdvances(t *testing.T) {
	fastStreams(t)
	f := newStreamFixture(t, APIConfig{MaxStreamsPerToken: 4, MaxStreams: 8}, wideToken, narrowToken)
	s := f.open(t, "narrow", "", "")
	hello := s.next(t, "hello")
	f.write("hidden/secret")
	hiddenHead := f.head()
	s.quiet(t, 300*time.Millisecond)
	f.write("ops/visible")
	ch := s.next(t, "changes")
	d := decodeChanges(t, ch)
	if d.From != hiddenHead || d.From == hello.id || d.ids() != "ops/visible" {
		t.Fatalf("narrowed changes = %+v (hello %s, hidden %s)", d, hello.id, hiddenHead)
	}
	if strings.Contains(ch.data, "secret") {
		t.Fatalf("a hidden concept leaked: %s", ch.data)
	}
}

func TestAPIEvents_Heartbeat(t *testing.T) {
	fastStreams(t)
	f := newStreamFixture(t, APIConfig{MaxStreamsPerToken: 4, MaxStreams: 8}, wideToken)
	s := f.open(t, "wide", "", "")
	s.next(t, "hello")
	s.next(t, "hb")
	s.next(t, "hb")
}

func TestAPIEvents_MaxAgeEndsTheStream(t *testing.T) {
	fastStreams(t)
	apiStreamMaxAge = 100 * time.Millisecond
	f := newStreamFixture(t, APIConfig{MaxStreamsPerToken: 4, MaxStreams: 8}, wideToken)
	s := f.open(t, "wide", "", "")
	s.next(t, "hello")
	if ev := s.next(t, "end"); !strings.Contains(ev.data, "max_age") {
		t.Fatalf("end = %+v", ev)
	}
}

func TestAPIEvents_Caps(t *testing.T) {
	fastStreams(t)
	f := newStreamFixture(t, APIConfig{MaxStreamsPerToken: 1, MaxStreams: 2}, wideToken, wide2Token, narrowToken)
	first := f.open(t, "wide", "", "")
	first.next(t, "hello")

	over := f.open(t, "wide", "", "")
	if over.resp.StatusCode != http.StatusTooManyRequests || over.resp.Header.Get("Retry-After") != "5" {
		t.Fatalf("per-token cap: %d %v", over.resp.StatusCode, over.resp.Header)
	}

	// Another token has its own count.
	other := f.open(t, "wide2", "", "")
	other.next(t, "hello")
	// The global cap (2) is reached.
	if g := f.open(t, "narrow", "", ""); g.resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("global cap: %d", g.resp.StatusCode)
	}

	// The slot is freed when the client leaves.
	first.cancel()
	deadline := time.Now().Add(5 * time.Second)
	for {
		again := f.open(t, "wide", "", "")
		if again.resp.StatusCode == http.StatusOK {
			again.next(t, "hello")
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the slot was not released after disconnect")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Admission is atomic: N concurrent requests over a cap of 3 admit exactly 3.
func TestAPIEvents_ConcurrentAdmission(t *testing.T) {
	fastStreams(t)
	f := newStreamFixture(t, APIConfig{MaxStreamsPerToken: 3, MaxStreams: 3}, wideToken)
	var ok, over atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.srv.URL+APIPrefix+"/kbs/docs/events", nil)
			req.Header.Set("Authorization", "Bearer wide")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			switch resp.StatusCode {
			case http.StatusOK:
				ok.Add(1)
				time.Sleep(300 * time.Millisecond) // hold the slot while the others try
			case http.StatusTooManyRequests:
				over.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 3 || over.Load() != 21 {
		t.Fatalf("admitted %d, refused %d; want 3 and 21", ok.Load(), over.Load())
	}
}

// Streams come and go while commits land: the watcher starts and stops with
// the subscribers, and nothing is left running (checked by the cleanup).
func TestAPIEvents_ConcurrentSubscribeUnsubscribe(t *testing.T) {
	fastStreams(t)
	f := newStreamFixture(t, APIConfig{MaxStreamsPerToken: 64, MaxStreams: 64}, wideToken)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				ctx, cancel := context.WithTimeout(context.Background(), time.Duration(30+10*((i+j)%5))*time.Millisecond)
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.srv.URL+APIPrefix+"/kbs/docs/events", nil)
				req.Header.Set("Authorization", "Bearer wide")
				if resp, err := http.DefaultClient.Do(req); err == nil {
					_, _ = bufio.NewReader(resp.Body).ReadString('\n')
					resp.Body.Close()
				}
				cancel()
			}
		}(i)
	}
	for i := 0; i < 3; i++ {
		f.write(fmt.Sprintf("ops/churn%d", i))
	}
	wg.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.multi.streams.mu.Lock()
		left, total := len(f.multi.streams.watchers), f.multi.streams.total
		f.multi.streams.mu.Unlock()
		if left == 0 && total == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after every client left: %d watchers, %d slots", left, total)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAPIEvents_RevokedTokenEndsTheStream(t *testing.T) {
	fastStreams(t)
	f := newStreamFixture(t, APIConfig{MaxStreamsPerToken: 4, MaxStreams: 8}, wideToken, wide2Token)
	s := f.open(t, "wide", "", "")
	s.next(t, "hello")
	if err := f.ts.Replace([]auth.ScopedToken{wide2Token}); err != nil {
		t.Fatal(err)
	}
	if ev := s.next(t, "end"); !strings.Contains(ev.data, "token_expired") {
		t.Fatalf("end = %+v", ev)
	}
}

// A reload that narrows the token narrows the stream: the next event holds
// only what the new policy shows, and a token that loses the KB ends.
func TestAPIEvents_ReloadNarrowsTheStream(t *testing.T) {
	fastStreams(t)
	f := newStreamFixture(t, APIConfig{MaxStreamsPerToken: 4, MaxStreams: 8}, wideToken)
	s := f.open(t, "wide", "", "")
	s.next(t, "hello")
	narrowed := narrowToken
	narrowed.Token, narrowed.Principal = "wide", "wide"
	if err := f.ts.Replace([]auth.ScopedToken{narrowed}); err != nil {
		t.Fatal(err)
	}
	f.write("hidden/secret")
	f.write("ops/visible")
	d := decodeChanges(t, s.next(t, "changes"))
	if d.ids() != "ops/visible" {
		t.Fatalf("after narrowing = %+v", d)
	}
	gone := auth.ScopedToken{Token: "wide", Principal: "wide",
		Policy: auth.Policy{Permissions: []auth.Permission{{KB: "other-kb"}}}}
	if err := f.ts.Replace([]auth.ScopedToken{gone}); err != nil {
		t.Fatal(err)
	}
	if ev := s.next(t, "end"); !strings.Contains(ev.data, "access_changed") {
		t.Fatalf("end = %+v", ev)
	}
}

func TestAPIEvents_CloseStreamsEndsThemAndShutdownIsPrompt(t *testing.T) {
	fastStreams(t)
	f := newStreamFixture(t, APIConfig{MaxStreamsPerToken: 4, MaxStreams: 8}, wideToken)
	a, b := f.open(t, "wide", "", ""), f.open(t, "wide", "", "")
	a.next(t, "hello")
	b.next(t, "hello")
	f.multi.CloseStreams()
	for _, s := range []*sseStream{a, b} {
		if ev := s.next(t, "end"); !strings.Contains(ev.data, "shutdown") {
			t.Fatalf("end = %+v", ev)
		}
	}
	closed := make(chan struct{})
	go func() { f.srv.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the test server did not close promptly")
	}
	f.multi.streams.wait()
}

func TestAPIEvents_RefusalsAndMounts(t *testing.T) {
	fastStreams(t)
	f := newStreamFixture(t, APIConfig{MaxStreamsPerToken: 4, MaxStreams: 8}, wideToken)
	do := func(method, path, token string) int {
		req, _ := http.NewRequest(method, f.srv.URL+path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if c := do(http.MethodGet, APIPrefix+"/kbs/docs/events", ""); c != http.StatusUnauthorized {
		t.Errorf("no token: %d", c)
	}
	if c := do(http.MethodHead, APIPrefix+"/kbs/docs/events", "wide"); c != http.StatusMethodNotAllowed {
		t.Errorf("HEAD: %d", c)
	}
	if c := do(http.MethodGet, APIPrefix+"/kbs/nope/events", "wide"); c != http.StatusNotFound {
		t.Errorf("unknown KB: %d", c)
	}
	// The Atlas mount does not stream: it polls (D337).
	if c := do(http.MethodGet, UIAPIPrefix+"/kbs/docs/events", "wide"); c != http.StatusNotFound {
		t.Errorf("atlas mount: %d", c)
	}
}

// A commit made after subscribe returns wakes the subscriber: the head the
// watcher compares against is read before subscribe returns.
func TestAPIEvents_SubscribeLeavesNoGapBeforeTheWatcher(t *testing.T) {
	fastStreams(t)
	f := newStreamFixture(t, APIConfig{MaxStreamsPerToken: 4, MaxStreams: 8}, wideToken)
	wake, unsub, ok := f.multi.streams.subscribe(context.Background(), "docs", f.c.k)
	if !ok {
		t.Fatal("subscribe refused")
	}
	defer unsub()
	f.write("ops/after-subscribe")
	select {
	case <-wake:
	case <-time.After(5 * time.Second):
		t.Fatal("a commit after subscribe did not wake the subscriber")
	}
}

// The head is read outside the hub lock: a git that hangs on one KB must not
// block admission, release or another KB's subscribe, and a second subscriber
// of the hanging KB gives up with its context.
func TestAPIEvents_HungGitDoesNotHoldTheHubLock(t *testing.T) {
	fastStreams(t)
	f := newStreamFixture(t, APIConfig{MaxStreamsPerToken: 4, MaxStreams: 8}, wideToken)
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	orig := apiStreamHead
	apiStreamHead = func(dir string, env ...string) (string, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return orig(dir, env...)
	}
	t.Cleanup(func() { apiStreamHead = orig })

	first := make(chan func(), 1)
	go func() {
		_, unsub, _ := f.multi.streams.subscribe(context.Background(), "docs", f.c.k)
		first <- unsub
	}()
	<-entered

	admitted := make(chan struct{})
	go func() {
		rel, _ := f.multi.streams.admit("x", 4, 8)
		rel()
		close(admitted)
	}()
	select {
	case <-admitted:
	case <-time.After(2 * time.Second):
		t.Fatal("admit blocked behind a hung git")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, _, ok := f.multi.streams.subscribe(ctx, "docs", f.c.k); ok {
		t.Fatal("a second subscriber was admitted before the watcher was ready")
	}

	close(release)
	select {
	case unsub := <-first:
		unsub()
	case <-time.After(5 * time.Second):
		t.Fatal("the first subscribe never returned")
	}
}
