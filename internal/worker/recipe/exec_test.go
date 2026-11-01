package recipe

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func helper(t *testing.T, mode string, args ...string) Recipe {
	t.Helper()
	return NewExec(ExecConfig{Name: "my-feed", Command: os.Args[0], Args: append([]string{execHelperFlag, mode}, args...),
		Timeout: 20 * time.Second, MaxOutput: 1 << 20, MaxContent: 1 << 10, Instructions: "text"})
}

func helperWith(t *testing.T, mutate func(*ExecConfig), mode string, args ...string) Recipe {
	t.Helper()
	c := helper(t, mode, args...).(*execRecipe).c
	mutate(&c)
	return NewExec(c)
}

func listErr(t *testing.T, r Recipe, wantCode, wantIn string) {
	t.Helper()
	_, err := r.List(context.Background(), Request{ID: "s", MaxEntries: 3, MaxContentBytes: 1 << 10})
	e, ok := err.(*Error)
	if !ok || e.Code != wantCode || !strings.Contains(e.Detail, wantIn) {
		t.Errorf("err = %v, want %s containing %q", err, wantCode, wantIn)
	}
}

func TestExecListsEntriesAndTheCursor(t *testing.T) {
	r := helper(t, "ok")
	if r.Name() != "my-feed" || r.Shape() != ShapeFeed || r.Instructions() != "text" {
		t.Fatalf("identity: %s %s %s", r.Name(), r.Shape(), r.Instructions())
	}
	l, err := r.List(context.Background(), Request{ID: "s", MaxEntries: 5})
	if err != nil {
		t.Fatal(err)
	}
	if l.NextCursor != "cur-2" || len(l.Entries) != 3 {
		t.Fatalf("listing = %+v", l)
	}
	if a := l.Entries[0]; a.Key != "a.md" || a.Content != "hello" || a.Change != Added || a.Revision != "r1" {
		t.Errorf("entry 0 = %+v", a)
	}
	if b := l.Entries[1]; b.Locator != "https://example.com/b" || b.Change != Modified {
		t.Errorf("entry 1 = %+v", b)
	}
	if c := l.Entries[2]; c.Change != Removed || c.Title != "" {
		t.Errorf("entry 2 = %+v", c)
	}
}

func TestExecStdinCarriesTheDocumentedFieldsAndModeListOnly(t *testing.T) {
	f := filepath.Join(t.TempDir(), "stdin.json")
	r := helper(t, "stdin", f)
	for _, cursor := range []string{"", "abc"} {
		_, err := r.List(context.Background(), Request{ID: "src/a", Title: "T", Locator: "https://example.com/x", SourceKind: "wiki",
			Include: []string{"a/**"}, Cursor: cursor, MaxEntries: 7, MaxContentBytes: 100})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(f)
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		// Only these keys: a new one is a protocol change (D1262: a new protocol number).
		if len(m) != 6 || m["protocol"] != float64(1) || m["mode"] != "list" || m["max_entries"] != float64(7) || m["max_content_bytes"] != float64(100) {
			t.Fatalf("stdin = %s", b)
		}
		if (cursor == "") != (m["cursor"] == nil) || (cursor != "" && m["cursor"] != cursor) {
			t.Errorf("cursor = %v for %q", m["cursor"], cursor)
		}
		src := m["source"].(map[string]any)
		if len(src) != 6 || src["id"] != "src/a" || src["source_shape"] != "feed" || src["source_kind"] != "wiki" || src["locator"] != "https://example.com/x" || src["title"] != "T" {
			t.Errorf("source = %v", src)
		}
	}
	// A request without include sends an empty list, not null.
	if _, err := r.List(context.Background(), Request{ID: "s"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(f); !strings.Contains(string(b), `"include":[]`) || !strings.Contains(string(b), `"max_entries":200`) {
		t.Errorf("defaults: %s", b)
	}
}

func TestExecProtocolViolationsAreRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		r    Recipe
		code string
		in   string
	}{
		"exit code":     {helper(t, "exit3"), CodeListFailed, "exit 3"},
		"no cursor":     {helper(t, "nocursor"), CodeProtocolError, "no final next_cursor"},
		"cursor first":  {helper(t, "cursornotlast"), CodeProtocolError, "nothing may follow"},
		"too many":      {helper(t, "many"), CodeProtocolError, "max_entries"},
		"null cursor":   {helper(t, "nullcursor"), CodeProtocolError, "next_cursor is a string"},
		"duplicate":     {helper(t, "dup"), CodeProtocolError, "duplicate key"},
		"malformed":     {helper(t, "malformed"), CodeProtocolError, "not a JSON object"},
		"unknown line":  {helper(t, "unknownline"), CodeProtocolError, "neither entry nor next_cursor"},
		"content cap":   {helper(t, "badentry", `{"key":"a","title":"A","revision":"1","change":"added","content":"`+strings.Repeat("x", 1025)+`"}`), CodeProtocolError, "content"},
		"content/rm":    {helper(t, "badentry", `{"key":"a","change":"removed","content":"x"}`), CodeProtocolError, "removed"},
		"no title":      {helper(t, "badentry", `{"key":"a","revision":"1","change":"added"}`), CodeProtocolError, "title and revision"},
		"no revision":   {helper(t, "badentry", `{"key":"a","title":"A","change":"modified"}`), CodeProtocolError, "title and revision"},
		"bad change":    {helper(t, "badentry", `{"key":"a","title":"A","revision":"1","change":"renamed"}`), CodeProtocolError, "change"},
		"key too long":  {helper(t, "badentry", `{"key":"`+strings.Repeat("k", 513)+`","title":"A","revision":"1","change":"added"}`), CodeProtocolError, "key"},
		"title too big": {helper(t, "badentry", `{"key":"a","title":"`+strings.Repeat("t", 201)+`","revision":"1","change":"added"}`), CodeProtocolError, "title"},
		"loc too long":  {helper(t, "badentry", `{"key":"a","title":"A","revision":"1","change":"added","locator":"`+strings.Repeat("l", 2049)+`"}`), CodeProtocolError, "locator"},
		"empty key":     {helper(t, "badentry", `{"key":"","title":"A","revision":"1","change":"added"}`), CodeProtocolError, "key is required"},
		"missing":       {NewExec(ExecConfig{Name: "x", Command: filepath.Join(t.TempDir(), "nope"), Timeout: time.Second, MaxOutput: 1 << 10, MaxContent: 10}), CodeListFailed, "cannot start nope"},
	} {
		t.Run(name, func(t *testing.T) { listErr(t, tc.r, tc.code, tc.in) })
	}
}

func TestExecOutputPastTheCapKillsTheProcess(t *testing.T) {
	r := helperWith(t, func(c *ExecConfig) { c.MaxOutput = 64 << 10 }, "flood")
	start := time.Now()
	listErr(t, r, CodeListFailed, "output over 65536 bytes")
	if time.Since(start) > 10*time.Second {
		t.Errorf("took %s: the process was not killed", time.Since(start))
	}
}

func TestExecChildEnvIsTheListedVariablesPlusTheBaseSet(t *testing.T) {
	t.Setenv("MODEL_API_KEY", "provider-key")
	t.Setenv("WIKI_TOKEN", "wiki-token-value")
	f := filepath.Join(t.TempDir(), "env")
	r := helperWith(t, func(c *ExecConfig) { c.Env = map[string]string{"API_TOKEN": "WIKI_TOKEN"} }, "env", f)
	if _, err := r.List(context.Background(), Request{ID: "s"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(f)
	for _, n := range strings.Fields(string(b)) {
		switch n {
		case "API_TOKEN", "PATH", "HOME", "SystemRoot", "TEMP", "TMP", "USERPROFILE", "SYSTEMROOT", "SystemDrive", "WINDIR", "COMSPEC":
		default:
			t.Errorf("unexpected variable %s in the child env", n)
		}
	}
	if !strings.Contains(string(b), "API_TOKEN") || strings.Contains(string(b), "MODEL_API_KEY") {
		t.Errorf("env = %q", b)
	}
}

func TestExecUnsetVariableRefusesNamingItNeverStartingTheScript(t *testing.T) {
	f := filepath.Join(t.TempDir(), "ran")
	r := helperWith(t, func(c *ExecConfig) { c.Env = map[string]string{"TOKEN": "NOT_SET_ANYWHERE"} }, "env", f)
	listErr(t, r, CodeListFailed, "NOT_SET_ANYWHERE")
	if _, err := os.Stat(f); err == nil {
		t.Error("the script ran with a variable unset")
	}
}

func TestExecArgumentsArriveVerbatimWithNoShell(t *testing.T) {
	f := filepath.Join(t.TempDir(), "argv")
	odd := []string{"a;b", "$(touch pwned)", "with space", "`id`", "*", "x && y"}
	r := helper(t, "argv", append([]string{f}, odd...)...)
	if _, err := r.List(context.Background(), Request{ID: "s"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(f)
	if got := strings.Split(string(b), "\x00"); strings.Join(got, "|") != strings.Join(odd, "|") {
		t.Errorf("argv = %q", got)
	}
}

func TestExecRunsInATempDirRemovedAfterwards(t *testing.T) {
	f := filepath.Join(t.TempDir(), "cwd")
	if _, err := helper(t, "cwd", f).List(context.Background(), Request{ID: "s"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(f)
	if _, err := os.Stat(string(b)); err == nil || len(b) == 0 {
		t.Errorf("working dir %q still exists", b)
	}
}

func TestExecStderrExcerptHidesListedValuesAndIsOneShortLine(t *testing.T) {
	t.Setenv("WIKI_TOKEN", "s3cr3t-token-value")
	r := helperWith(t, func(c *ExecConfig) { c.Env = map[string]string{"API_TOKEN": "WIKI_TOKEN"} }, "exit3")
	_, err := r.List(context.Background(), Request{ID: "s"})
	e, ok := err.(*Error)
	if !ok || e.Code != CodeListFailed {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(e.Detail, "s3cr3t") || !strings.Contains(e.Detail, "***") {
		t.Errorf("secret not redacted: %q", e.Detail)
	}
	if strings.ContainsAny(e.Detail, "\n\x1b") || len(e.Detail) > len("exit 3: ")+200 || !strings.Contains(e.Detail, "more") {
		t.Errorf("not one clean line: %q", e.Detail)
	}
}

func TestExecTimeoutKillsTheProcessAndItsChild(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	r := helperWith(t, func(c *ExecConfig) { c.Timeout = 1500 * time.Millisecond }, "hang", pidFile)
	start := time.Now()
	listErr(t, r, CodeListFailed, "timeout")
	if time.Since(start) > 15*time.Second {
		t.Errorf("took %s", time.Since(start))
	}
	b, _ := os.ReadFile(pidFile)
	pid, _ := strconv.Atoi(string(b))
	assertGone(t, pid)
}

func TestExecCancelledContextReturnsItsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(500 * time.Millisecond); cancel() }()
	if _, err := helper(t, "hang").List(ctx, Request{ID: "s"}); err != context.Canceled {
		t.Errorf("err = %v", err)
	}
}

func TestExecOneScriptAtATimeAndABusySlotHonoursContext(t *testing.T) {
	execSlot <- struct{}{}
	defer func() { <-execSlot }()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := helper(t, "ok").List(ctx, Request{ID: "s"}); err != context.DeadlineExceeded {
		t.Errorf("err = %v", err)
	}
}

func TestRegistryHoldsExecRecipesAndRefusesABuiltinName(t *testing.T) {
	r, err := NewRegistry(Options{Exec: []ExecConfig{{Name: "my-feed", Command: "x", Instructions: "t"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := r.Get("my-feed"); !ok || got.Name() != "my-feed" {
		t.Errorf("Get = %v %v", got, ok)
	}
	if _, err := NewRegistry(Options{Exec: []ExecConfig{{Name: "git-docs", Command: "x"}}}); err == nil {
		t.Error("an exec recipe took a built-in's name")
	}
}
