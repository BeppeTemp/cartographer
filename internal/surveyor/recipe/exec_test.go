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

func sysRecipe(t *testing.T, log, answer string, extra ...string) SystemRecipe {
	t.Helper()
	r := helperWith(t, func(c *ExecConfig) { c.Shape = ShapeSystem }, "sys", append([]string{log, answer}, extra...)...)
	return r.(SystemRecipe)
}

func modes(t *testing.T, log string) []string {
	b, _ := os.ReadFile(log)
	return strings.Fields(string(b))
}

func TestSystemRecipeIsListedOnlyAfterACleanPreflight(t *testing.T) {
	log := filepath.Join(t.TempDir(), "modes")
	r := sysRecipe(t, log, `{"read_only":true,"identity":"viewer\u001b[31m","detail":"ok\nline","extra":1}`)
	ctx, req := context.Background(), Request{ID: "src/s", Locator: "kubernetes://dev", MaxContentBytes: 500}
	if r.Shape() != ShapeSystem {
		t.Fatal("shape")
	}
	if _, err := r.List(ctx, req); err != ErrNeedsPreflight {
		t.Fatalf("generic List = %v", err)
	}
	if _, err := r.ListSystem(ctx, Verified{}, req); err != ErrNeedsPreflight {
		t.Fatalf("zero Verified = %v", err)
	}
	if len(modes(t, log)) != 0 {
		t.Fatal("a script ran before the preflight")
	}
	v, err := r.Preflight(ctx, req)
	if err != nil || v.Identity() != "viewer [31m" || v.Detail() != "ok line" {
		t.Fatalf("preflight = %+v, %v", v, err)
	}
	l, err := r.ListSystem(ctx, v, req)
	if err != nil || len(l.Entries) != 2 || l.NextCursor != "" {
		t.Fatalf("list = %+v, %v", l, err)
	}
	if e := l.Entries[0]; e.Content != "line1\nline2" || e.Revision != "" || e.Change != "" || e.Title != "namespace/a" {
		t.Errorf("entry = %+v", e)
	}
	// The preflight runs again at the next poll: nothing is cached.
	if _, err := r.Preflight(ctx, req); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(modes(t, log), ","); got != "preflight,list,preflight" {
		t.Errorf("modes = %s", got)
	}
	// A Verified of another recipe is not accepted.
	other := NewExec(ExecConfig{Name: "other", Shape: ShapeSystem, Command: os.Args[0], Timeout: time.Second, MaxOutput: 100, MaxContent: 100}).(SystemRecipe)
	if _, err := other.ListSystem(ctx, v, req); err != ErrNeedsPreflight {
		t.Errorf("foreign Verified = %v", err)
	}
}

func TestSystemPreflightRefusalsNeverList(t *testing.T) {
	for name, tc := range map[string]struct {
		answer string
		extra  []string
		want   string
	}{
		"false":           {`{"read_only":false,"identity":"admin","detail":"can read secrets"}`, nil, "identity_not_read_only: admin — can read secrets"},
		"missing":         {`{"identity":"x"}`, nil, "identity_not_read_only"},
		"string boolean":  {`{"read_only":"true"}`, nil, "identity_not_read_only"},
		"empty":           {``, nil, "identity_not_read_only"},
		"two objects":     {`{"read_only":true}{"read_only":true}`, nil, "identity_not_read_only"},
		"not json":        {`yes`, nil, "identity_not_read_only"},
		"true but exit 4": {`{"read_only":true}`, []string{"exit4"}, "exit 4: no kubeconfig"},
	} {
		log := filepath.Join(t.TempDir(), "modes")
		r := sysRecipe(t, log, tc.answer, tc.extra...)
		_, err := r.Preflight(context.Background(), Request{ID: "s"})
		e, ok := err.(*Error)
		if !ok || e.Code != CodeIdentityNotReadOnly || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
		if got := modes(t, log); len(got) != 1 || got[0] != "preflight" {
			t.Errorf("%s: modes = %v, want the preflight only", name, got)
		}
	}
	// The code is a contract (the Atlas shows feed_error).
	if CodeIdentityNotReadOnly != "identity_not_read_only" {
		t.Error("code changed")
	}
	// Start failure and a timeout are refusals too.
	r := NewExec(ExecConfig{Name: "n", Shape: ShapeSystem, Command: filepath.Join(t.TempDir(), "absent"), Timeout: time.Second, MaxOutput: 100, MaxContent: 100}).(SystemRecipe)
	if _, err := r.Preflight(context.Background(), Request{}); err == nil || err.(*Error).Code != CodeIdentityNotReadOnly {
		t.Errorf("start failure = %v", err)
	}
	r = NewExec(ExecConfig{Name: "n", Shape: ShapeSystem, Command: os.Args[0], Args: []string{execHelperFlag, "sleep"}, Timeout: 300 * time.Millisecond, MaxOutput: 100, MaxContent: 100}).(SystemRecipe)
	if _, err := r.Preflight(context.Background(), Request{}); err == nil || err.(*Error).Code != CodeIdentityNotReadOnly {
		t.Errorf("timeout = %v", err)
	}
	// A feed recipe has no preflight.
	if _, err := helper(t, "ok").(SystemRecipe).Preflight(context.Background(), Request{}); err == nil {
		t.Error("a feed recipe answered a preflight")
	}
}

func TestSystemRequestsCarryTheSystemShape(t *testing.T) {
	f := filepath.Join(t.TempDir(), "stdin.json")
	r := helperWith(t, func(c *ExecConfig) { c.Shape = ShapeSystem }, "stdin", f).(SystemRecipe)
	// The helper answers a next_cursor line only, which a preflight refuses; the
	// stdin it saved is what is checked.
	_, _ = r.Preflight(context.Background(), Request{ID: "src/s", Include: []string{"ns/*"}})
	b, _ := os.ReadFile(f)
	if !strings.Contains(string(b), `"mode":"preflight"`) || !strings.Contains(string(b), `"source_shape":"system"`) || !strings.Contains(string(b), `"include":["ns/*"]`) {
		t.Errorf("preflight stdin = %s", b)
	}
	_, _ = r.ListSystem(context.Background(), verifiedFor(r), Request{ID: "src/s"}) // refused for its cursor; the stdin is what counts
	b, _ = os.ReadFile(f)
	if !strings.Contains(string(b), `"mode":"list"`) || !strings.Contains(string(b), `"cursor":null`) || !strings.Contains(string(b), `"max_entries":1000`) {
		t.Errorf("list stdin = %s", b)
	}
}

func TestSystemListingRulesAreRelaxedForContentOnly(t *testing.T) {
	for name, entry := range map[string]string{
		"no content":    `{"key":"a"}`,
		"empty content": `{"key":"a","content":""}`,
		"nul":           `{"key":"a","content":"x\u0000y"}`,
		"over cap":      `{"key":"a","content":"` + strings.Repeat("x", 2000) + `"}`,
		"no key":        `{"content":"x"}`,
	} {
		r := helperWith(t, func(c *ExecConfig) { c.Shape = ShapeSystem }, "badentry", entry).(SystemRecipe)
		_, err := r.ListSystem(context.Background(), verifiedFor(r), Request{})
		if e, ok := err.(*Error); !ok || e.Code != CodeProtocolError {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestSystemListingWithANextCursorIsRefusedNotReadAsComplete(t *testing.T) {
	log := filepath.Join(t.TempDir(), "modes")
	r := sysRecipe(t, log, `{"read_only":true}`, "cursor")
	v, err := r.Preflight(context.Background(), Request{ID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := r.ListSystem(context.Background(), v, Request{ID: "s"})
	if e, ok := err.(*Error); !ok || e.Code != CodeProtocolError || !strings.Contains(e.Detail, "empty next_cursor") || len(l.Entries) != 0 {
		t.Errorf("listing = %+v, %v", l, err)
	}
}

// verifiedFor builds a Verified for a test of the listing alone.
func verifiedFor(r SystemRecipe) Verified { return Verified{recipe: r.Name(), ok: true} }
