// Package peers is the in-memory hub behind agent-to-agent messaging (D341):
// the agent sessions working on the same KB through the same server register
// here, see each other, and leave each other messages that a client-side
// delivery mechanism hands to the target agent.
//
// Nothing is persisted and nothing reaches git: presence and mailboxes live and
// die with the server process. A session is identified by the id its own agent
// client gave it (a Claude Code or Kiro session id, a Codex thread id, an
// OpenCode session id), so the hooks that register it and the delivery that
// targets it agree without a mapping of their own.
package peers

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"
)

// Limits. A hub is a shared, unauthenticated-by-default resource on a local
// server: every bound is there so one misbehaving client cannot grow it without
// limit.
const (
	MaxSessions      = 256
	MaxInbox         = 100
	MaxMessageBytes  = 8000
	MaxLabelBytes    = 128
	MaxWait          = 10 * time.Minute
	DefaultTTL       = 2 * time.Hour
	maxSessionIDLen  = 128
	maxCWDBytes      = 1024
	maxKBsPerSession = 32
)

// Providers are the agent clients a session may declare. The provider decides
// how a message is delivered, so an unknown one is refused rather than stored
// with no delivery path.
var Providers = []string{"claude", "codex", "kiro", "opencode"}

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)

// Errors a caller maps to a client-facing message.
var (
	ErrInvalid      = errors.New("invalid request")
	ErrUnknown      = errors.New("unknown session")
	ErrForbidden    = errors.New("session belongs to another principal")
	ErrNotInKB      = errors.New("session is not registered on this KB")
	ErrInboxFull    = errors.New("recipient inbox is full")
	ErrHubFull      = errors.New("too many registered sessions")
	ErrSelfMessage  = errors.New("a session cannot message itself")
	ErrNoRecipients = errors.New("no other session is registered on this KB")
)

// Session is one registered agent session.
type Session struct {
	ID       string    `json:"id"`
	Provider string    `json:"provider"`
	Label    string    `json:"label,omitempty"`
	Host     string    `json:"host,omitempty"`
	CWD      string    `json:"cwd,omitempty"`
	KBs      []string  `json:"kbs"`
	Since    time.Time `json:"since"`
	LastSeen time.Time `json:"last_seen"`
	Pending  int       `json:"pending"`
}

// Message is one message waiting in, or taken from, a session's inbox.
type Message struct {
	ID        string    `json:"id"`
	KB        string    `json:"kb"`
	From      string    `json:"from"`
	FromLabel string    `json:"from_label,omitempty"`
	FromAgent string    `json:"from_provider,omitempty"`
	To        string    `json:"to"`
	Text      string    `json:"text"`
	Sent      time.Time `json:"sent"`
}

type entry struct {
	session   Session
	principal string
	inbox     []Message
}

// Hub holds every registered session. The zero value is not usable: use New.
type Hub struct {
	mu       sync.Mutex
	now      func() time.Time
	ttl      time.Duration
	sessions map[string]*entry
	seq      uint64
	// changed is closed and replaced on every new message, waking every waiter
	// at once; each re-checks its own inboxes.
	changed chan struct{}
}

// New returns an empty hub whose sessions expire ttl after they were last
// seen. A non-positive ttl means DefaultTTL.
func New(ttl time.Duration) *Hub {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Hub{now: time.Now, ttl: ttl, sessions: map[string]*entry{}, changed: make(chan struct{})}
}

// Register adds or refreshes a session. Re-registering an existing id keeps its
// inbox and replaces its description, but only for the principal that first
// registered it: an id is not a credential, and another token naming it must
// not take its mail.
func (h *Hub) Register(principal string, s Session) (Session, error) {
	if err := validateSession(&s); err != nil {
		return Session{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expireLocked()
	now := h.now()
	if e, ok := h.sessions[s.ID]; ok {
		if e.principal != principal {
			return Session{}, ErrForbidden
		}
		s.Since = e.session.Since
		s.LastSeen = now
		e.session = s
		return h.viewLocked(e), nil
	}
	if len(h.sessions) >= MaxSessions {
		return Session{}, ErrHubFull
	}
	s.Since, s.LastSeen = now, now
	e := &entry{session: s, principal: principal}
	h.sessions[s.ID] = e
	return h.viewLocked(e), nil
}

// Leave removes a session and drops its pending messages.
func (h *Hub) Leave(principal, id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, err := h.ownedLocked(principal, id)
	if err != nil {
		return err
	}
	delete(h.sessions, e.session.ID)
	return nil
}

// List returns the live sessions registered on kb, oldest first.
func (h *Hub) List(kb string) []Session {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expireLocked()
	out := []Session{}
	for _, e := range h.sessions {
		if slices.Contains(e.session.KBs, kb) {
			out = append(out, h.viewLocked(e))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Since.Equal(out[j].Since) {
			return out[i].Since.Before(out[j].Since)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Get returns one live session.
func (h *Hub) Get(id string) (Session, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expireLocked()
	e, ok := h.sessions[id]
	if !ok {
		return Session{}, false
	}
	return h.viewLocked(e), true
}

// Send leaves text in the inbox of to, or of every other session on kb when to
// is "*". The sender must be a session the principal registered on kb, and so
// must every recipient be registered on kb: the KB is the boundary of who may
// talk to whom.
func (h *Hub) Send(principal, kb, from, to, text string) ([]Message, error) {
	if kb == "" || to == "" {
		return nil, fmt.Errorf("%w: kb and to are required", ErrInvalid)
	}
	if text == "" || len(text) > MaxMessageBytes || !utf8.ValidString(text) {
		return nil, fmt.Errorf("%w: text must be valid UTF-8, 1 to %d bytes", ErrInvalid, MaxMessageBytes)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expireLocked()
	sender, err := h.ownedLocked(principal, from)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(sender.session.KBs, kb) {
		return nil, ErrNotInKB
	}
	var targets []*entry
	if to == "*" {
		for id, e := range h.sessions {
			if id != from && slices.Contains(e.session.KBs, kb) {
				targets = append(targets, e)
			}
		}
		if len(targets) == 0 {
			return nil, ErrNoRecipients
		}
		sort.Slice(targets, func(i, j int) bool { return targets[i].session.ID < targets[j].session.ID })
	} else {
		if to == from {
			return nil, ErrSelfMessage
		}
		e, ok := h.sessions[to]
		if !ok {
			return nil, ErrUnknown
		}
		if !slices.Contains(e.session.KBs, kb) {
			return nil, ErrNotInKB
		}
		targets = []*entry{e}
	}
	for _, e := range targets {
		if len(e.inbox) >= MaxInbox {
			return nil, fmt.Errorf("%w: %s", ErrInboxFull, e.session.ID)
		}
	}
	now := h.now()
	sender.session.LastSeen = now
	sent := make([]Message, 0, len(targets))
	for _, e := range targets {
		h.seq++
		m := Message{
			ID:        "m" + strconv.FormatUint(h.seq, 10),
			KB:        kb,
			From:      from,
			FromLabel: sender.session.Label,
			FromAgent: sender.session.Provider,
			To:        e.session.ID,
			Text:      text,
			Sent:      now,
		}
		e.inbox = append(e.inbox, m)
		sent = append(sent, m)
	}
	close(h.changed)
	h.changed = make(chan struct{})
	return sent, nil
}

// Take removes and returns every pending message of the given sessions, keyed
// by session id; sessions with nothing pending are absent from the result.
// touch refreshes the sessions' presence: an agent asking for its own mail is
// alive, a relay asking on its behalf says nothing about that.
func (h *Hub) Take(principal string, ids []string, touch bool) (map[string][]Message, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expireLocked()
	entries, err := h.ownedAllLocked(principal, ids)
	if err != nil {
		return nil, err
	}
	now := h.now()
	out := map[string][]Message{}
	for _, e := range entries {
		if touch {
			e.session.LastSeen = now
		}
		if len(e.inbox) > 0 {
			out[e.session.ID] = e.inbox
			e.inbox = nil
		}
	}
	return out, nil
}

// Wait is Take that blocks until at least one of the sessions has mail, the
// timeout elapses or ctx ends; the last two return an empty map and no error.
// A session that expires or leaves while waited on ends the wait with
// ErrUnknown, so a relay notices instead of waiting on nothing.
func (h *Hub) Wait(ctx context.Context, principal string, ids []string, timeout time.Duration, touch bool) (map[string][]Message, error) {
	if timeout <= 0 || timeout > MaxWait {
		timeout = MaxWait
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		got, err := h.Take(principal, ids, touch)
		if err != nil || len(got) > 0 {
			return got, err
		}
		h.mu.Lock()
		changed := h.changed
		h.mu.Unlock()
		select {
		case <-changed:
		case <-timer.C:
			return map[string][]Message{}, nil
		case <-ctx.Done():
			return map[string][]Message{}, nil
		}
	}
}

func (h *Hub) ownedLocked(principal, id string) (*entry, error) {
	e, ok := h.sessions[id]
	if !ok {
		return nil, ErrUnknown
	}
	if e.principal != principal {
		return nil, ErrForbidden
	}
	return e, nil
}

func (h *Hub) ownedAllLocked(principal string, ids []string) ([]*entry, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: at least one session id is required", ErrInvalid)
	}
	out := make([]*entry, 0, len(ids))
	for _, id := range ids {
		e, err := h.ownedLocked(principal, id)
		if err != nil {
			return nil, fmt.Errorf("%w: %s", err, id)
		}
		out = append(out, e)
	}
	return out, nil
}

func (h *Hub) viewLocked(e *entry) Session {
	s := e.session
	s.KBs = slices.Clone(s.KBs)
	s.Pending = len(e.inbox)
	return s
}

func (h *Hub) expireLocked() {
	cutoff := h.now().Add(-h.ttl)
	for id, e := range h.sessions {
		if e.session.LastSeen.Before(cutoff) {
			delete(h.sessions, id)
		}
	}
}

func validateSession(s *Session) error {
	if len(s.ID) > maxSessionIDLen || !sessionIDPattern.MatchString(s.ID) {
		return fmt.Errorf("%w: session id must be 1-%d characters of [A-Za-z0-9._:-]", ErrInvalid, maxSessionIDLen)
	}
	if !slices.Contains(Providers, s.Provider) {
		return fmt.Errorf("%w: provider must be one of %v", ErrInvalid, Providers)
	}
	if len(s.Label) > MaxLabelBytes || len(s.Host) > MaxLabelBytes || len(s.CWD) > maxCWDBytes {
		return fmt.Errorf("%w: label, host or cwd too long", ErrInvalid)
	}
	if len(s.KBs) == 0 || len(s.KBs) > maxKBsPerSession {
		return fmt.Errorf("%w: a session registers on 1 to %d KBs", ErrInvalid, maxKBsPerSession)
	}
	kbs := slices.Clone(s.KBs)
	slices.Sort(kbs)
	s.KBs = slices.Compact(kbs)
	if slices.Contains(s.KBs, "") {
		return fmt.Errorf("%w: empty KB name", ErrInvalid)
	}
	s.Pending = 0
	return nil
}
