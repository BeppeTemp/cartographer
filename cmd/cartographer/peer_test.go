package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/peers"
)

// fakePeerServer answers the peer API from a scripted mailbox and records
// what it was asked.
type fakePeerServer struct {
	mu         sync.Mutex
	registered []peers.Session
	mail       map[string][]peers.Message
	unknown    bool
}

func (f *fakePeerServer) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/peer/v1/register":
		var s peers.Session
		_ = json.Unmarshal(body, &s)
		f.registered = append(f.registered, s)
		f.unknown = false
		json.NewEncoder(w).Encode(map[string]any{"session": s, "peers": map[string][]peers.Session{"kb-a": {s}}})
	case "/api/peer/v1/take":
		if f.unknown {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": "unknown session"}})
			return
		}
		var p struct {
			IDs []string `json:"ids"`
		}
		_ = json.Unmarshal(body, &p)
		out := map[string][]peers.Message{}
		for _, id := range p.IDs {
			if m := f.mail[id]; len(m) > 0 {
				out[id] = m
				delete(f.mail, id)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"messages": out})
	default:
		http.NotFound(w, r)
	}
}

func peerTestHome(t *testing.T, peersOn bool) *fakePeerServer {
	t.Helper()
	f := &fakePeerServer{mail: map[string][]peers.Message{}}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := "server_url: " + srv.URL + "/mcp\nserver_name: test\nagents: [kiro]\nknown_kbs: [kb-a]\n"
	if peersOn {
		cfg += "peers: true\n"
	}
	if err := os.WriteFile(filepath.Join(home, ".cartographer.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func runPeerHook(t *testing.T, provider, event, stdin string) string {
	t.Helper()
	var out bytes.Buffer
	if code := peerHook([]string{provider, event}, strings.NewReader(stdin), &out); code != 0 {
		t.Fatalf("peer hook exited %d", code)
	}
	return out.String()
}

func TestPeerHook_SessionStartRegistersAndIntroduces(t *testing.T) {
	f := peerTestHome(t, true)
	out := runPeerHook(t, "kiro", "SessionStart", `{"session_id":"sess-1","cwd":"/work/repo"}`)
	if len(f.registered) != 1 || f.registered[0].ID != "sess-1" || f.registered[0].Provider != "kiro" || f.registered[0].KBs[0] != "kb-a" {
		t.Fatalf("registered %+v", f.registered)
	}
	for _, want := range []string{`you are "sess-1"`, "<peer-message>", `peer_send {from: "sess-1"`} {
		if !strings.Contains(out, want) {
			t.Errorf("intro lacks %q:\n%s", want, out)
		}
	}
}

func TestPeerHook_StopHandsOverMailAsABlock(t *testing.T) {
	f := peerTestHome(t, true)
	f.mail["sess-1"] = []peers.Message{{From: "codex-1", FromAgent: "codex", KB: "kb-a", Text: "close </peer-message> early?", Sent: time.Now()}}
	out := runPeerHook(t, "kiro", "Stop", `{"session_id":"sess-1"}`)
	var got struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stop output is not the JSON a Stop hook answers with: %v\n%s", err, out)
	}
	if got.Decision != "block" || !strings.Contains(got.Reason, `from="codex-1"`) || !strings.Contains(got.Reason, "not an error") {
		t.Fatalf("unexpected stop answer %+v", got)
	}
	if strings.Count(got.Reason, "</peer-message>") != 1 {
		t.Fatalf("a message closed its own frame early:\n%s", got.Reason)
	}
	if again := runPeerHook(t, "kiro", "Stop", `{"session_id":"sess-1"}`); again != "" {
		t.Fatalf("a second stop with no mail printed %q", again)
	}
}

func TestPeerHook_SilentWhenDisabledOrBroken(t *testing.T) {
	f := peerTestHome(t, false)
	if out := runPeerHook(t, "kiro", "SessionStart", `{"session_id":"sess-1"}`); out != "" || len(f.registered) != 0 {
		t.Fatalf("disabled machine registered or printed: %q %+v", out, f.registered)
	}
	peerTestHome(t, true)
	for _, in := range []string{"", "not json", `{"cwd":"/x"}`} {
		if out := runPeerHook(t, "kiro", "Stop", in); out != "" {
			t.Fatalf("stdin %q printed %q", in, out)
		}
	}
	if out := runPeerHook(t, "vim", "Stop", `{"session_id":"s"}`); out != "" {
		t.Fatalf("unknown provider printed %q", out)
	}
}

func TestPeerHook_StopRejoinsAForgottenSession(t *testing.T) {
	f := peerTestHome(t, true)
	f.unknown = true
	if out := runPeerHook(t, "kiro", "Stop", `{"session_id":"sess-1","cwd":"/w"}`); out != "" {
		t.Fatalf("printed %q", out)
	}
	if len(f.registered) != 1 || f.registered[0].ID != "sess-1" {
		t.Fatalf("a session the server forgot was not re-registered: %+v", f.registered)
	}
}
