package peers

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func sess(id, provider string, kbs ...string) Session {
	return Session{ID: id, Provider: provider, Label: "user@host", KBs: kbs}
}

func mustRegister(t *testing.T, h *Hub, principal string, s Session) {
	t.Helper()
	if _, err := h.Register(principal, s); err != nil {
		t.Fatalf("register %s: %v", s.ID, err)
	}
}

func TestSendAndTakeDeliverOnce(t *testing.T) {
	h := New(0)
	mustRegister(t, h, "", sess("a", "codex", "kb-a"))
	mustRegister(t, h, "", sess("b", "kiro", "kb-a"))

	sent, err := h.Send("", "kb-a", "a", "b", "hello")
	if err != nil || len(sent) != 1 {
		t.Fatalf("send: %v %v", sent, err)
	}
	if got, _ := h.Get("b"); got.Pending != 1 {
		t.Fatalf("pending = %d, want 1", got.Pending)
	}
	got, err := h.Take("", []string{"b"}, true)
	if err != nil {
		t.Fatal(err)
	}
	msgs := got["b"]
	if len(msgs) != 1 || msgs[0].Text != "hello" || msgs[0].From != "a" || msgs[0].FromAgent != "codex" || msgs[0].KB != "kb-a" {
		t.Fatalf("unexpected messages: %+v", msgs)
	}
	again, _ := h.Take("", []string{"b"}, true)
	if len(again) != 0 {
		t.Fatalf("a message was delivered twice: %+v", again)
	}
}

func TestKBIsTheBoundary(t *testing.T) {
	h := New(0)
	mustRegister(t, h, "", sess("a", "codex", "kb-a"))
	mustRegister(t, h, "", sess("b", "kiro", "kb-b"))
	if _, err := h.Send("", "kb-a", "a", "b", "x"); !errors.Is(err, ErrNotInKB) {
		t.Fatalf("send across KBs: err = %v, want ErrNotInKB", err)
	}
	if _, err := h.Send("", "kb-b", "a", "b", "x"); !errors.Is(err, ErrNotInKB) {
		t.Fatalf("send from a session not on the KB: err = %v, want ErrNotInKB", err)
	}
	if got := h.List("kb-a"); len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("List(kb-a) = %+v", got)
	}
}

func TestBroadcastSkipsSender(t *testing.T) {
	h := New(0)
	mustRegister(t, h, "", sess("a", "codex", "kb-a"))
	if _, err := h.Send("", "kb-a", "a", "*", "x"); !errors.Is(err, ErrNoRecipients) {
		t.Fatalf("lonely broadcast: err = %v", err)
	}
	mustRegister(t, h, "", sess("b", "kiro", "kb-a"))
	mustRegister(t, h, "", sess("c", "claude", "kb-a", "kb-b"))
	sent, err := h.Send("", "kb-a", "a", "*", "x")
	if err != nil || len(sent) != 2 || sent[0].To != "b" || sent[1].To != "c" {
		t.Fatalf("broadcast: %+v %v", sent, err)
	}
	if _, err := h.Send("", "kb-a", "a", "a", "x"); !errors.Is(err, ErrSelfMessage) {
		t.Fatalf("self message: err = %v", err)
	}
}

func TestPrincipalOwnsItsSessions(t *testing.T) {
	h := New(0)
	mustRegister(t, h, "alice", sess("a", "codex", "kb-a"))
	mustRegister(t, h, "bob", sess("b", "kiro", "kb-a"))
	if _, err := h.Register("bob", sess("a", "codex", "kb-a")); !errors.Is(err, ErrForbidden) {
		t.Fatalf("re-register under another principal: err = %v", err)
	}
	if _, err := h.Send("bob", "kb-a", "a", "b", "x"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("send as someone else's session: err = %v", err)
	}
	if _, err := h.Send("alice", "kb-a", "a", "b", "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Take("alice", []string{"b"}, false); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reading someone else's inbox: err = %v", err)
	}
	if err := h.Leave("alice", "b"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("removing someone else's session: err = %v", err)
	}
}

func TestReRegisterKeepsInbox(t *testing.T) {
	h := New(0)
	mustRegister(t, h, "", sess("a", "codex", "kb-a"))
	mustRegister(t, h, "", sess("b", "kiro", "kb-a"))
	if _, err := h.Send("", "kb-a", "a", "b", "x"); err != nil {
		t.Fatal(err)
	}
	mustRegister(t, h, "", Session{ID: "b", Provider: "kiro", KBs: []string{"kb-a", "kb-a"}, Label: "renamed"})
	got, _ := h.Get("b")
	if got.Pending != 1 || got.Label != "renamed" || len(got.KBs) != 1 {
		t.Fatalf("after re-register: %+v", got)
	}
}

func TestSessionsExpire(t *testing.T) {
	h := New(time.Hour)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return now }
	mustRegister(t, h, "", sess("a", "codex", "kb-a"))
	mustRegister(t, h, "", sess("b", "kiro", "kb-a"))
	now = now.Add(50 * time.Minute)
	if _, err := h.Take("", []string{"a"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Take("", []string{"b"}, true); err != nil {
		t.Fatal(err)
	}
	now = now.Add(20 * time.Minute)
	if got := h.List("kb-a"); len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("a relay-only take kept a session alive, or a touch did not: %+v", got)
	}
}

func TestValidation(t *testing.T) {
	h := New(0)
	bad := []Session{
		{ID: "", Provider: "codex", KBs: []string{"kb-a"}},
		{ID: "has space", Provider: "codex", KBs: []string{"kb-a"}},
		{ID: "a", Provider: "vim", KBs: []string{"kb-a"}},
		{ID: "a", Provider: "codex"},
		{ID: "a", Provider: "codex", KBs: []string{""}},
		{ID: "a", Provider: "codex", KBs: []string{"kb-a"}, Label: strings.Repeat("x", MaxLabelBytes+1)},
	}
	for _, s := range bad {
		if _, err := h.Register("", s); !errors.Is(err, ErrInvalid) {
			t.Errorf("Register(%+v) err = %v, want ErrInvalid", s, err)
		}
	}
	mustRegister(t, h, "", sess("a", "codex", "kb-a"))
	mustRegister(t, h, "", sess("b", "kiro", "kb-a"))
	for _, text := range []string{"", strings.Repeat("x", MaxMessageBytes+1), "\xff"} {
		if _, err := h.Send("", "kb-a", "a", "b", text); !errors.Is(err, ErrInvalid) {
			t.Errorf("Send(%q...) err = %v, want ErrInvalid", text[:min(len(text), 4)], err)
		}
	}
}

func TestInboxIsBounded(t *testing.T) {
	h := New(0)
	mustRegister(t, h, "", sess("a", "codex", "kb-a"))
	mustRegister(t, h, "", sess("b", "kiro", "kb-a"))
	for i := 0; i < MaxInbox; i++ {
		if _, err := h.Send("", "kb-a", "a", "b", "x"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.Send("", "kb-a", "a", "b", "x"); !errors.Is(err, ErrInboxFull) {
		t.Fatalf("over the bound: err = %v", err)
	}
}

func TestWaitWakesOnSend(t *testing.T) {
	h := New(0)
	mustRegister(t, h, "", sess("a", "codex", "kb-a"))
	mustRegister(t, h, "", sess("b", "kiro", "kb-a"))
	done := make(chan map[string][]Message, 1)
	go func() {
		got, err := h.Wait(context.Background(), "", []string{"b"}, 5*time.Second, true)
		if err != nil {
			t.Error(err)
		}
		done <- got
	}()
	time.Sleep(50 * time.Millisecond)
	if _, err := h.Send("", "kb-a", "a", "b", "wake up"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if len(got["b"]) != 1 || got["b"][0].Text != "wake up" {
			t.Fatalf("woke with %+v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Wait did not wake on Send")
	}
}

func TestWaitTimesOutEmpty(t *testing.T) {
	h := New(0)
	mustRegister(t, h, "", sess("b", "kiro", "kb-a"))
	got, err := h.Wait(context.Background(), "", []string{"b"}, 20*time.Millisecond, true)
	if err != nil || len(got) != 0 {
		t.Fatalf("Wait = %+v, %v; want empty, nil", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := h.Wait(ctx, "", []string{"b"}, time.Minute, true); err != nil || len(got) != 0 {
		t.Fatalf("cancelled Wait = %+v, %v", got, err)
	}
	if _, err := h.Wait(context.Background(), "", []string{"gone"}, time.Second, true); !errors.Is(err, ErrUnknown) {
		t.Fatalf("Wait on an unknown session: err = %v", err)
	}
}
