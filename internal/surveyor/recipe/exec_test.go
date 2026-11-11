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

func TestExecStdinCarriesTheDocumentedFieldsAndVerbList(t *testing.T) {
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
		if len(m) != 7 || m["protocol"] != float64(2) || m["verb"] != "list" || m["max_entries"] != float64(7) || m["max_content_bytes"] != float64(100) {
			t.Fatalf("stdin = %s", b)
		}
		if cfg, ok := m["config"].(map[string]any); !ok || len(cfg) != 0 {
			t.Errorf("config = %v, want an empty object (never null)", m["config"])
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
		"unknown line":  {helper(t, "unknownline"), CodeProtocolError, "neither entry, delete nor next_cursor"},
		"content cap":   {helper(t, "badentry", `{"key":"a","title":"A","revision":"1","change":"added","content":"`+strings.Repeat("x", 1025)+`"}`), CodeProtocolError, "content"},
		"removed":       {helper(t, "badentry", `{"key":"a","title":"A","revision":"1","change":"removed"}`), CodeProtocolError, "delete line"},
		"after cursor":  {helper(t, "lines", `{"next_cursor":"x"}`, `{"delete":{"key":"a"}}`), CodeProtocolError, "nothing may follow"},
		"del and entry": {helper(t, "lines", `{"entry":{"key":"a","title":"A","revision":"1","change":"added"}}`, `{"delete":{"key":"a"}}`, `{"next_cursor":"x"}`), CodeProtocolError, "duplicate key"},
		"del twice":     {helper(t, "lines", `{"delete":{"key":"a"}}`, `{"delete":{"key":"a"}}`, `{"next_cursor":"x"}`), CodeProtocolError, "duplicate key"},
		"del extra":     {helper(t, "lines", `{"delete":{"key":"a","reason":"x"}}`, `{"next_cursor":"x"}`), CodeProtocolError, "delete is"},
		"del no key":    {helper(t, "lines", `{"delete":{}}`, `{"next_cursor":"x"}`), CodeProtocolError, "delete is"},
		"del empty key": {helper(t, "lines", `{"delete":{"key":""}}`, `{"next_cursor":"x"}`), CodeProtocolError, "delete key"},
		"del long key":  {helper(t, "lines", `{"delete":{"key":"`+strings.Repeat("k", 513)+`"}}`, `{"next_cursor":"x"}`), CodeProtocolError, "delete key"},
		"del too many":  {helper(t, "lines", `{"delete":{"key":"a"}}`, `{"delete":{"key":"b"}}`, `{"delete":{"key":"c"}}`, `{"delete":{"key":"d"}}`, `{"next_cursor":"x"}`), CodeProtocolError, "max_entries"},
		"two keys":      {helper(t, "lines", `{"delete":{"key":"a"},"entry":{}}`, `{"next_cursor":"x"}`), CodeProtocolError, "one key"},
		"no list verb":  {helper(t, "exit64"), CodeListFailed, "does not implement list"},
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

func verbs_(t *testing.T, log string) []string {
	b, _ := os.ReadFile(log)
	return strings.Fields(string(b))
}

const cleanCheck = `{"check":{"ok":true,"read_only":true,"identity":"viewer\u001b[31m","detail":"ok\nline","extra":1}}`

func TestSystemRecipeIsListedOnlyAfterACleanCheck(t *testing.T) {
	log := filepath.Join(t.TempDir(), "verbs")
	r := sysRecipe(t, log, cleanCheck)
	ctx, req := context.Background(), Request{ID: "src/s", Locator: "kubernetes://dev", MaxContentBytes: 500}
	if r.Shape() != ShapeSystem {
		t.Fatal("shape")
	}
	if _, err := r.List(ctx, req); err != ErrNeedsCheck {
		t.Fatalf("generic List = %v", err)
	}
	if _, err := r.ListSystem(ctx, Verified{}, req); err != ErrNeedsCheck {
		t.Fatalf("zero Verified = %v", err)
	}
	if len(verbs_(t, log)) != 0 {
		t.Fatal("a script ran before the check")
	}
	v, err := r.Check(ctx, req)
	if err != nil || v.Identity() != "viewer [31m" || v.Detail() != "ok line" {
		t.Fatalf("check = %+v, %v", v, err)
	}
	l, err := r.ListSystem(ctx, v, req)
	if err != nil || len(l.Entries) != 2 || l.NextCursor != "" {
		t.Fatalf("list = %+v, %v", l, err)
	}
	if e := l.Entries[0]; e.Content != "line1\nline2" || e.Revision != "" || e.Change != "" || e.Title != "namespace/a" {
		t.Errorf("entry = %+v", e)
	}
	// The check runs again at the next poll: nothing is cached.
	if _, err := r.Check(ctx, req); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(verbs_(t, log), ","); got != "check,list,check" {
		t.Errorf("verbs = %s", got)
	}
	// A Verified of another recipe is not accepted.
	other := NewExec(ExecConfig{Name: "other", Shape: ShapeSystem, Command: os.Args[0], Timeout: time.Second, MaxOutput: 100, MaxContent: 100}).(SystemRecipe)
	if _, err := other.ListSystem(ctx, v, req); err != ErrNeedsCheck {
		t.Errorf("foreign Verified = %v", err)
	}
}

func TestSystemCheckRefusalsNeverList(t *testing.T) {
	for name, tc := range map[string]struct {
		answer string
		extra  []string
		code   string
		want   string
	}{
		"read_only false": {`{"check":{"ok":true,"read_only":false,"identity":"admin","detail":"can read secrets"}}`, nil, CodeIdentityNotReadOnly, "admin — can read secrets"},
		"read_only gone":  {`{"check":{"ok":true,"identity":"x"}}`, nil, CodeIdentityNotReadOnly, "read_only"},
		"ok false":        {`{"check":{"ok":false,"read_only":true,"identity":"x","detail":"token expired"}}`, nil, CodeCheckFailed, "x — token expired"},
		"ok gone":         {`{"check":{"read_only":true}}`, nil, CodeCheckFailed, "no boolean ok"},
		"string boolean":  {`{"check":{"ok":true,"read_only":"true"}}`, nil, CodeCheckFailed, "wrong type"},
		"empty":           {``, nil, CodeCheckFailed, "not exactly one"},
		"two objects":     {`{"check":{"ok":true,"read_only":true}}{"check":{"ok":true,"read_only":true}}`, nil, CodeCheckFailed, "not exactly one"},
		"not json":        {`yes`, nil, CodeCheckFailed, "not exactly one"},
		"not wrapped":     {`{"ok":true,"read_only":true}`, nil, CodeCheckFailed, "not exactly one"},
		"true but exit 4": {`{"check":{"ok":true,"read_only":true}}`, []string{"exit4"}, CodeCheckFailed, "exit 4: no kubeconfig"},
	} {
		log := filepath.Join(t.TempDir(), "verbs")
		r := sysRecipe(t, log, tc.answer, tc.extra...)
		_, err := r.Check(context.Background(), Request{ID: "s"})
		e, ok := err.(*Error)
		if !ok || e.Code != tc.code || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %s containing %q", name, err, tc.code, tc.want)
		}
		if got := verbs_(t, log); len(got) != 1 || got[0] != "check" {
			t.Errorf("%s: verbs = %v, want the check only", name, got)
		}
	}
	// The codes are a contract (the Atlas shows feed_error).
	if CodeIdentityNotReadOnly != "identity_not_read_only" || CodeCheckFailed != "check_failed" {
		t.Error("code changed")
	}
	// Start failure and a timeout are failures of the check too.
	r := NewExec(ExecConfig{Name: "n", Shape: ShapeSystem, Command: filepath.Join(t.TempDir(), "absent"), Timeout: time.Second, MaxOutput: 100, MaxContent: 100}).(SystemRecipe)
	if _, err := r.Check(context.Background(), Request{}); err == nil || err.(*Error).Code != CodeCheckFailed {
		t.Errorf("start failure = %v", err)
	}
	r = NewExec(ExecConfig{Name: "n", Shape: ShapeSystem, Command: os.Args[0], Args: []string{execHelperFlag, "sleep"}, Timeout: 300 * time.Millisecond, MaxOutput: 100, MaxContent: 100}).(SystemRecipe)
	if _, err := r.Check(context.Background(), Request{}); err == nil || err.(*Error).Code != CodeCheckFailed {
		t.Errorf("timeout = %v", err)
	}
	// A system script with no check cannot prove its identity: a failure, never a pass.
	r = helperWith(t, func(c *ExecConfig) { c.Shape = ShapeSystem }, "exit64").(SystemRecipe)
	if _, err := r.Check(context.Background(), Request{}); err == nil || err.(*Error).Code != CodeCheckFailed || !strings.Contains(err.Error(), "does not implement check") {
		t.Errorf("unsupported system check = %v", err)
	}
}

func TestSystemRequestsCarryTheSystemShape(t *testing.T) {
	f := filepath.Join(t.TempDir(), "stdin.json")
	r := helperWith(t, func(c *ExecConfig) { c.Shape = ShapeSystem }, "stdin", f).(SystemRecipe)
	// The helper answers a next_cursor line only, which a check refuses; the
	// stdin it saved is what is checked.
	_, _ = r.Check(context.Background(), Request{ID: "src/s", Include: []string{"ns/*"}})
	b, _ := os.ReadFile(f)
	if !strings.Contains(string(b), `"verb":"check"`) || !strings.Contains(string(b), `"source_shape":"system"`) || !strings.Contains(string(b), `"include":["ns/*"]`) {
		t.Errorf("check stdin = %s", b)
	}
	_, _ = r.ListSystem(context.Background(), verifiedFor(r), Request{ID: "src/s"}) // refused for its cursor; the stdin is what counts
	b, _ = os.ReadFile(f)
	if !strings.Contains(string(b), `"verb":"list"`) || !strings.Contains(string(b), `"cursor":null`) || !strings.Contains(string(b), `"max_entries":1000`) {
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
	// A system listing has no delete line: a group that is gone is absent.
	r := helperWith(t, func(c *ExecConfig) { c.Shape = ShapeSystem }, "lines", `{"delete":{"key":"a"}}`, `{"next_cursor":""}`).(SystemRecipe)
	if _, err := r.ListSystem(context.Background(), verifiedFor(r), Request{}); err == nil || !strings.Contains(err.Error(), "no delete lines") {
		t.Errorf("delete in a system listing: %v", err)
	}
}

func TestSystemListingWithANextCursorIsRefusedNotReadAsComplete(t *testing.T) {
	log := filepath.Join(t.TempDir(), "verbs")
	r := sysRecipe(t, log, `{"check":{"ok":true,"read_only":true}}`, "cursor")
	v, err := r.Check(context.Background(), Request{ID: "s"})
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

func TestExecDeleteLinesBecomeRemovedEntries(t *testing.T) {
	l, err := helper(t, "ok").List(context.Background(), Request{ID: "s", MaxEntries: 5})
	if err != nil || len(l.Entries) != 3 || l.Entries[2].Key != "c.md" || l.Entries[2].Change != Removed {
		t.Fatalf("listing = %+v, %v", l, err)
	}
}

func TestExecVerbsThatAreNotImplementedExitSixtyFour(t *testing.T) {
	r := helper(t, "exit64")
	if _, err := r.(Checker).Check(context.Background(), Request{ID: "s"}); err != ErrUnsupported {
		t.Errorf("feed check = %v", err)
	}
	listErr(t, r, CodeListFailed, "does not implement list")
}

func TestExecFeedCheckAnswersAndRedacts(t *testing.T) {
	t.Setenv("WIKI_TOKEN", "s3cr3t-token-value")
	mk := func(answer string) Checker {
		return helperWith(t, func(c *ExecConfig) { c.Env = map[string]string{"API_TOKEN": "WIKI_TOKEN"} },
			"byverb", filepath.Join(t.TempDir(), "v"), "-", answer).(Checker)
	}
	v, err := mk(`{"check":{"ok":true,"identity":"svc-reader","detail":"reads 12 spaces"}}`).Check(context.Background(), Request{ID: "s"})
	if err != nil || v.Identity() != "svc-reader" || v.Detail() != "reads 12 spaces" {
		t.Fatalf("check = %+v, %v", v, err)
	}
	// read_only is not asked of a feed.
	if _, err := mk(`{"check":{"ok":true}}`).Check(context.Background(), Request{ID: "s"}); err != nil {
		t.Errorf("feed check without read_only: %v", err)
	}
	_, err = mk(`{"check":{"ok":false,"detail":"401 for s3cr3t-token-value"}}`).Check(context.Background(), Request{ID: "s"})
	e, ok := err.(*Error)
	if !ok || e.Code != CodeCheckFailed || strings.Contains(e.Detail, "s3cr3t") || !strings.Contains(e.Detail, "401 for ***") {
		t.Errorf("a failed check = %v", err)
	}
	// ok: false with nothing to show still says why the check failed.
	if _, err := mk(`{"check":{"ok":false}}`).Check(context.Background(), Request{ID: "s"}); err == nil || err.Error() != "check_failed: the check answered ok: false" {
		t.Errorf("empty failure = %v", err)
	}
	// An identity or a detail past its limit is cut, not refused.
	v, err = mk(`{"check":{"ok":true,"identity":"`+strings.Repeat("i", 200)+`","detail":"`+strings.Repeat("d", 400)+`"}}`).Check(context.Background(), Request{ID: "s"})
	if err != nil || len([]rune(v.Identity())) != 120 || len([]rune(v.Detail())) != 300 {
		t.Errorf("limits: %d %d %v", len(v.Identity()), len(v.Detail()), err)
	}
}
