package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// The relay (D341) delivers to sessions whose client offers no hook that
// fires while the agent is idle but does accept input from outside: a Codex
// thread through `codex queue`, an OpenCode session through its local
// service. One per machine, held by a lock file; any Codex session start
// spawns it, and it exits once it has had nothing to serve for a while.

const (
	relayWait        = 20 * time.Second
	relayIdleExit    = 15 * time.Minute
	relayDeliverTime = time.Minute
	// openCodeRecent is how recently an OpenCode session must have been
	// active to count as open: the service keeps every past session and says
	// nothing about which ones a TUI still shows.
	openCodeRecent = 30 * time.Minute
)

type relaySession struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

func relayDir(dir string) string { return filepath.Join(peerStateDir(dir), "relay") }

func relayFile(dir string, s relaySession) string {
	return filepath.Join(relayDir(dir), s.Provider+"__"+s.ID+".json")
}

func addRelaySession(dir string, s relaySession) {
	if err := os.MkdirAll(relayDir(dir), 0o700); err != nil {
		return
	}
	data, _ := json.Marshal(s)
	_ = os.WriteFile(relayFile(dir, s), data, 0o600)
}

func readRelaySessions(dir string) []relaySession {
	entries, _ := os.ReadDir(relayDir(dir))
	var out []relaySession
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(relayDir(dir), e.Name()))
		if err != nil {
			continue
		}
		var s relaySession
		if json.Unmarshal(data, &s) == nil && s.ID != "" {
			out = append(out, s)
		}
	}
	return out
}

func relayLockPath(dir string) string { return filepath.Join(peerStateDir(dir), "relay.lock") }

func peerRelayRunning(dir string) bool {
	release, err := provisioning.TryLockPath(relayLockPath(dir))
	if err != nil {
		return errors.Is(err, provisioning.ErrLockHeld)
	}
	release()
	return false
}

// ensureRelay starts a detached relay unless one is running. The relay takes
// the lock itself, so two hooks racing here start at most one survivor.
func ensureRelay(dir string) {
	if peerRelayRunning(dir) {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "peer", "relay")
	detachProcess(cmd)
	if cmd.Start() == nil {
		_ = cmd.Process.Release()
	}
}

func peerRelay(args []string) int {
	env, err := loadPeerEnv()
	if err != nil || !env.cfg.Peers {
		return 0
	}
	if err := os.MkdirAll(peerStateDir(env.dir), 0o700); err != nil {
		return 1
	}
	release, err := provisioning.TryLockPath(relayLockPath(env.dir))
	if err != nil {
		return 0
	}
	defer release()
	logPath := filepath.Join(peerStateDir(env.dir), "relay.log")
	if fi, err := os.Stat(logPath); err == nil && fi.Size() > 1<<20 {
		_ = os.Remove(logPath)
	}
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		defer f.Close()
		log.SetOutput(f)
	}
	log.Printf("relay started (pid %d)", os.Getpid())
	r := &relay{env: env, introduced: map[string]bool{}}
	idleSince := time.Now()
	for {
		if cfg, err := loadPeerEnv(); err == nil {
			r.env = cfg
		}
		if !r.env.cfg.Peers {
			log.Print("peers disabled: relay exits")
			return 0
		}
		sessions := readRelaySessions(r.env.dir)
		if r.env.cfg.HasAgent(string(configurator.ProviderOpenCode)) {
			sessions = append(sessions, r.discoverOpenCode()...)
		}
		if len(sessions) == 0 {
			if time.Since(idleSince) > relayIdleExit {
				log.Print("nothing to serve: relay exits")
				return 0
			}
			time.Sleep(relayWait)
			continue
		}
		idleSince = time.Now()
		ids := make([]string, 0, len(sessions))
		byID := map[string]relaySession{}
		for _, s := range sessions {
			ids = append(ids, s.ID)
			byID[s.ID] = s
		}
		got, err := r.env.c.PeerWait(ids, relayWait, false)
		var he *client.PeerHTTPError
		switch {
		case errors.As(err, &he) && he.Status == http.StatusNotFound:
			r.prune(sessions)
			continue
		case err != nil:
			log.Printf("wait: %v", err)
			time.Sleep(relayWait)
			continue
		}
		for id, msgs := range got {
			s := byID[id]
			if err := r.deliver(s, peerMessagesText(id, msgs)); err != nil {
				log.Printf("deliver %d message(s) to %s %s: %v", len(msgs), s.Provider, id, err)
			}
		}
	}
}

type relay struct {
	env        *peerEnv
	introduced map[string]bool
}

// prune drops the sessions the server no longer knows: an expired Codex
// thread's file is removed, so a closed session stops being waited on.
func (r *relay) prune(sessions []relaySession) {
	for _, s := range sessions {
		_, err := r.env.c.PeerTake([]string{s.ID}, false, peerHTTPTimeout)
		var he *client.PeerHTTPError
		if errors.As(err, &he) && he.Status == http.StatusNotFound {
			_ = os.Remove(relayFile(r.env.dir, s))
			delete(r.introduced, s.ID)
		}
	}
}

func (r *relay) deliver(s relaySession, text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), relayDeliverTime)
	defer cancel()
	switch s.Provider {
	case "codex":
		out, err := exec.CommandContext(ctx, "codex", "queue", "--thread", s.ID, "--message", text).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	case "opencode":
		svc, err := readOpenCodeService()
		if err != nil {
			return err
		}
		return svc.post(ctx, "/api/session/"+url.PathEscape(s.ID)+"/prompt", map[string]any{"text": text, "delivery": "queue"}, nil)
	}
	return fmt.Errorf("no relay delivery for provider %q", s.Provider)
}

// openCodeService is the registration file OpenCode's own clients discover the
// local service through ($XDG_STATE_HOME/opencode/service.json).
type openCodeService struct {
	URL      string `json:"url"`
	Password string `json:"password"`
}

func readOpenCodeService() (*openCodeService, error) {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		state = filepath.Join(home, ".local", "state")
	}
	data, err := os.ReadFile(filepath.Join(state, "opencode", "service.json"))
	if err != nil {
		return nil, err
	}
	var s openCodeService
	if err := json.Unmarshal(data, &s); err != nil || s.URL == "" {
		return nil, errors.New("opencode service.json has no url")
	}
	return &s, nil
}

func (s *openCodeService) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(s.URL, "/")+path, body)
	if err != nil {
		return err
	}
	req.SetBasicAuth("opencode", s.Password)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("opencode %s %s: HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (s *openCodeService) post(ctx context.Context, path string, in, out any) error {
	return s.do(ctx, http.MethodPost, path, in, out)
}

// discoverOpenCode registers the root sessions OpenCode's service shows as
// recently active in a workspace bound for OpenCode, and returns them. A session is introduced once per relay
// run, through a synthetic message that does not start a turn: the intro is
// what an OpenCode agent reads instead of a session-start hook's output.
func (r *relay) discoverOpenCode() []relaySession {
	svc, err := readOpenCodeService()
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), peerHTTPTimeout)
	defer cancel()
	var list struct {
		Data []struct {
			ID       string `json:"id"`
			ParentID string `json:"parentID"`
			Time     struct {
				Updated int64 `json:"updated"`
				Idle    int64 `json:"idle"`
			} `json:"time"`
			Location struct {
				Directory string `json:"directory"`
			} `json:"location"`
		} `json:"data"`
	}
	if err := svc.do(ctx, http.MethodGet, "/api/session?limit=20&order=desc&parentID=null", nil, &list); err != nil {
		return nil
	}
	var out []relaySession
	for _, s := range list.Data {
		last := time.UnixMilli(max(s.Time.Updated, s.Time.Idle))
		if s.ParentID != "" || time.Since(last) > openCodeRecent {
			continue
		}
		// Only a session in a workspace bound for OpenCode joins: the service
		// lists every session on the machine, and one the operator never put
		// in a KB's perimeter must not be registered, nor written to.
		if _, ok, err := r.env.cfg.ResolveWorkspace(string(configurator.ProviderOpenCode), s.Location.Directory); err != nil || !ok {
			continue
		}
		in := hookInput{SessionID: s.ID, CWD: s.Location.Directory}
		roster, err := peerRegister(r.env, "opencode", in)
		if err != nil {
			continue
		}
		if !r.introduced[s.ID] {
			intro := map[string]any{"text": peerIntro(s.ID, roster), "description": "Cartographer peers", "resume": false}
			if err := svc.post(ctx, "/api/session/"+url.PathEscape(s.ID)+"/synthetic", intro, nil); err != nil {
				log.Printf("introduce opencode %s: %v", s.ID, err)
			}
			r.introduced[s.ID] = true
		}
		out = append(out, relaySession{Provider: "opencode", ID: s.ID})
	}
	return out
}
