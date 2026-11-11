package recipe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/procattr"
	"github.com/BeppeTemp/cartographer/internal/surveyor/connector"
)

// ProtocolVersion is the version of the exec recipe protocol (docs/surveyor-recipes.md).
// A change to what is sent or accepted needs a new number (D1262). Version 2
// replaced version 1 outright: the Surveyor was not released with it.
const ProtocolVersion = 2

// exitUnsupported is the status of a script that does not implement the verb or
// the protocol number it was sent (EX_USAGE).
const exitUnsupported = 64

// CodeProtocolError is a listing the script produced but the protocol refuses.
const CodeProtocolError = "protocol_error"

// Limits of the protocol, in bytes; the per-recipe ones are ExecConfig's.
const (
	maxKey         = 512
	maxTitle       = 200
	maxRevision    = 200
	maxLocator     = 2048
	maxNextCursor  = 1024
	maxStderrKept  = 8 << 10
	maxStderrShown = 200
)

// ExecConfig is one recipes.<name> of kind exec, already validated by the
// surveyor config: the command comes from there and from nowhere else.
type ExecConfig struct {
	Name         string
	Command      string            // absolute path, or a bare name looked up on PATH
	Args         []string          // verbatim, no shell
	Env          map[string]string // NAME the script sees -> name of the surveyor's variable
	Timeout      time.Duration
	MaxOutput    int
	MaxContent   int
	Shape        string // "" or feed, or system (D1372)
	Connector    string // how the unit executor reads an entry's locator; "" = the Source's own
	Instructions string
	Settings     map[string]any // recipes.<name>.settings: typed scalars, sent to every verb as config
}

// execSlot lets one script run at a time per replica: feeds are handled one by
// one, and a second script would multiply the child processes a surveyor holds.
var execSlot = make(chan struct{}, 1)

type execRecipe struct{ c ExecConfig }

var _ SystemRecipe = (*execRecipe)(nil)

// NewExec returns the recipe that runs an operator-declared executable.
func NewExec(c ExecConfig) Recipe { return &execRecipe{c} }

func (e *execRecipe) Name() string { return e.c.Name }
func (e *execRecipe) Shape() string {
	if e.c.Shape == ShapeSystem {
		return ShapeSystem
	}
	return ShapeFeed
}
func (e *execRecipe) Instructions() string { return e.c.Instructions }

// Connector is the connector the unit executor uses to read an entry's locator,
// "" when the recipe names none (the poll then falls back to the Source's own).
func (e *execRecipe) Connector() string { return e.c.Connector }

// request is the stdin of every verb: protocol v2. A feed recipe is sent
// list, and spec, check and fetch when it declares them; a system recipe is sent
// check, then list after a clean answer.
type request struct {
	Protocol int            `json:"protocol"`
	Verb     string         `json:"verb"`
	Config   map[string]any `json:"config"`
	Source   *source        `json:"source,omitempty"`
}

// listRequest adds what only list carries; the cursor is sent as null on the
// first listing, so it has no omitempty.
type listRequest struct {
	request
	Cursor          *string `json:"cursor"`
	MaxEntries      int     `json:"max_entries"`
	MaxContentBytes int     `json:"max_content_bytes"`
}

// base is the request of a verb: config is always an object, never null.
func (e *execRecipe) base(verb string, src *source) request {
	cfg := e.c.Settings
	if cfg == nil {
		cfg = map[string]any{}
	}
	return request{Protocol: ProtocolVersion, Verb: verb, Config: cfg, Source: src}
}

type source struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Locator     string   `json:"locator"`
	SourceKind  string   `json:"source_kind"`
	SourceShape string   `json:"source_shape"`
	Include     []string `json:"include"`
}

// List runs the script once, for a feed. argv with no shell, a per-call
// working directory, the environment of connector.ChildEnv (exactly the
// variables listed plus PATH/HOME, so the model key never reaches operator
// code), the whole process group killed on timeout, cancel or an output past
// the cap. Every detail that leaves this function is redacted of the listed
// variables' values. A system recipe is never listed from here (ListSystem).
func (e *execRecipe) List(ctx context.Context, req Request) (Listing, error) {
	if e.c.Shape == ShapeSystem {
		return Listing{}, ErrNeedsCheck
	}
	maxEntries, maxContent := req.MaxEntries, e.contentCap(req)
	if maxEntries <= 0 {
		maxEntries = DefaultMaxEntries
	}
	body, err := json.Marshal(listRequest{request: e.base("list", &source{req.ID, req.Title, req.Locator, req.SourceKind, ShapeFeed, nonNil(req.Include)}),
		Cursor: cursorOf(req.Cursor), MaxEntries: maxEntries, MaxContentBytes: maxContent})
	if err != nil {
		return Listing{}, fail(CodeListFailed, "cannot encode the request")
	}
	var l Listing
	err = e.runScript(ctx, CodeListFailed, body, func(r io.Reader) (bool, error) {
		var atEOF bool
		var perr error
		l, atEOF, perr = e.parse(r, maxEntries, maxContent, e.entry)
		return atEOF, perr
	})
	return l, unsupportedIsFailure(err, CodeListFailed, "list")
}

// unsupportedIsFailure turns the exit-64 answer into a failure of the verb for
// the verbs that cannot go without (list); spec and check keep ErrUnsupported.
func unsupportedIsFailure(err error, code, verb string) error {
	if errors.Is(err, ErrUnsupported) {
		return fail(code, "the script does not implement %s (it exited %d)", verb, exitUnsupported)
	}
	return err
}

// contentCap is the per-entry cap: the recipe's, lowered by the request's.
func (e *execRecipe) contentCap(req Request) int {
	if req.MaxContentBytes > 0 && req.MaxContentBytes < e.c.MaxContent {
		return req.MaxContentBytes
	}
	return e.c.MaxContent
}

// runScript is the process half of every mode, one script at a time per
// replica: it feeds body to the script and hands its stdout to read, which
// returns atEOF true when the stream ended cleanly but unusable (the exit status
// then says why). Verb-agnostic: every verb shares it; fc is the code of a failure
// of the process itself (exit status, timeout, start), and exit 64 with an
// unusable stream is ErrUnsupported.
func (e *execRecipe) runScript(ctx context.Context, fc string, body []byte, read func(io.Reader) (bool, error)) error {
	select {
	case execSlot <- struct{}{}:
		defer func() { <-execSlot }()
	case <-ctx.Done():
		return ctx.Err()
	}
	env, err := connector.ChildEnv(e.c.Env)
	if err != nil {
		return fail(fc, "%v", err) // names the variable, never a value
	}
	dir, err := os.MkdirTemp("", "cartographer-recipe-")
	if err != nil {
		return fail(fc, "no temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	runCtx, cancel := context.WithTimeout(ctx, e.c.Timeout)
	defer cancel()
	cmd := procattr.Hide(exec.CommandContext(runCtx, e.c.Command, e.c.Args...))
	cmd.Dir, cmd.Env, cmd.Stdin = dir, env, bytes.NewReader(body)
	cmd.WaitDelay = 2 * time.Second
	connector.KillGroupOnCancel(cmd)
	stderr := &capped{max: maxStderrKept}
	cmd.Stderr = stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return fail(fc, "cannot start: %v", err)
	}
	if err := cmd.Start(); err != nil {
		return fail(fc, "cannot start %s: %s", baseName(e.c.Command), e.redact(err.Error()))
	}
	// The script may leave grandchildren that outlive it (and hold the pipes).
	defer connector.KillGroup(cmd)

	// Read in a goroutine and close the pipe when the deadline or a cancel hits:
	// a descendant that left the process group (setsid) and keeps stdout open
	// would otherwise block this read forever and hold execSlot.
	type parsed struct {
		atEOF bool
		err   error
	}
	done := make(chan parsed, 1)
	go func() {
		var p parsed
		p.atEOF, p.err = read(out)
		done <- p
	}()
	var p parsed
	select {
	case p = <-done:
	case <-runCtx.Done():
		_ = out.Close()
		p = <-done
		if p.err == nil {
			p.err = fail(fc, "stopped before the listing ended")
		}
		p.atEOF = false
	}
	atEOF, perr := p.atEOF, p.err
	if perr != nil && !atEOF {
		timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded) // before cancel() below sets Err itself
		cancel()                                                      // kills the group; the exit status no longer matters
		_ = cmd.Wait()
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case timedOut:
			return fail(fc, "timeout after %s", e.c.Timeout)
		}
		return perr
	}
	werr := cmd.Wait()
	if perr != nil { // the stream ended without a next_cursor: the exit status says why
		var ee *exec.ExitError
		if errors.As(werr, &ee) && runCtx.Err() == nil {
			if ee.ExitCode() == exitUnsupported {
				return ErrUnsupported
			}
			return fail(fc, "exit %d%s", ee.ExitCode(), e.excerpt(stderr.String()))
		}
		if runCtx.Err() == nil {
			return perr
		}
	}
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case runCtx.Err() != nil:
		return fail(fc, "timeout after %s", e.c.Timeout)
	case werr != nil:
		var ee *exec.ExitError
		if errors.As(werr, &ee) {
			return fail(fc, "exit %d%s", ee.ExitCode(), e.excerpt(stderr.String()))
		}
		return fail(fc, "%s", e.redact(werr.Error()))
	}
	return nil
}

// parse reads the JSON lines as a stream and stops at the first violation, so a
// script cannot make the surveyor hold more than max_output_bytes.
// atEOF is true when the stream ended cleanly but without a next_cursor.
func (e *execRecipe) parse(r io.Reader, maxEntries, maxContent int, entry entryFunc) (l Listing, atEOF bool, err error) {
	l, atEOF, err = e.parseLines(r, maxEntries, maxContent, entry)
	return
}

// entryFunc decodes one entry line; the shape picks it (entryOf, systemEntryOf).
type entryFunc func(raw json.RawMessage, maxContent int) (Entry, error)

func (e *execRecipe) entry(raw json.RawMessage, maxContent int) (Entry, error) {
	if e.c.Shape == ShapeSystem {
		return systemEntryOf(raw, maxContent)
	}
	return entryOf(raw, maxContent)
}

func (e *execRecipe) parseLines(r io.Reader, maxEntries, maxContent int, entryOf entryFunc) (Listing, bool, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	var l Listing
	seen := map[string]bool{}
	total, n, done := 0, 0, false
	for {
		line, rerr := br.ReadSlice('\n')
		var long []byte
		for errors.Is(rerr, bufio.ErrBufferFull) { // a line longer than the buffer
			long = append(long, line...)
			total += len(line)
			if total > e.c.MaxOutput {
				return Listing{}, false, fail(CodeListFailed, "output over %d bytes", e.c.MaxOutput)
			}
			line, rerr = br.ReadSlice('\n')
		}
		line = append(long, line...)
		total += len(line) - len(long)
		if total > e.c.MaxOutput {
			return Listing{}, false, fail(CodeListFailed, "output over %d bytes", e.c.MaxOutput)
		}
		if t := bytes.TrimSpace(line); len(t) > 0 {
			n++
			if done {
				return Listing{}, false, fail(CodeProtocolError, "line %d: nothing may follow next_cursor", n)
			}
			var m map[string]json.RawMessage
			if err := json.Unmarshal(t, &m); err != nil || len(m) != 1 {
				return Listing{}, false, fail(CodeProtocolError, "line %d is not a JSON object with one key, entry, delete or next_cursor", n)
			}
			switch {
			case m["entry"] != nil:
				if len(l.Entries) >= maxEntries {
					return Listing{}, false, fail(CodeProtocolError, "more than max_entries (%d) entries", maxEntries)
				}
				en, err := entryOf(m["entry"], maxContent)
				if err != nil {
					return Listing{}, false, fail(CodeProtocolError, "line %d: %v", n, err)
				}
				if seen[en.Key] {
					return Listing{}, false, fail(CodeProtocolError, "line %d: duplicate key", n)
				}
				seen[en.Key] = true
				l.Entries = append(l.Entries, en)
			case m["delete"] != nil:
				if e.c.Shape == ShapeSystem {
					return Listing{}, false, fail(CodeProtocolError, "line %d: a system listing has no delete lines (a group that is gone is absent)", n)
				}
				if len(l.Entries) >= maxEntries {
					return Listing{}, false, fail(CodeProtocolError, "more than max_entries (%d) entries", maxEntries)
				}
				key, err := deleteKey(m["delete"])
				if err != nil {
					return Listing{}, false, fail(CodeProtocolError, "line %d: %v", n, err)
				}
				if seen[key] {
					return Listing{}, false, fail(CodeProtocolError, "line %d: duplicate key (a key is an entry or a delete, once)", n)
				}
				seen[key] = true
				l.Entries = append(l.Entries, Entry{Key: key, Change: Removed})
			case m["next_cursor"] != nil:
				var c string
				if string(m["next_cursor"]) == "null" || json.Unmarshal(m["next_cursor"], &c) != nil || len(c) > maxNextCursor || !utf8.ValidString(c) {
					return Listing{}, false, fail(CodeProtocolError, "line %d: next_cursor is a string of at most %d bytes", n, maxNextCursor)
				}
				l.NextCursor, done = c, true
			default:
				return Listing{}, false, fail(CodeProtocolError, "line %d is neither entry, delete nor next_cursor", n)
			}
		}
		if rerr != nil {
			if rerr != io.EOF {
				return Listing{}, false, fail(CodeListFailed, "reading the output: %v", rerr)
			}
			break
		}
	}
	if !done {
		return Listing{}, true, fail(CodeProtocolError, "no final next_cursor")
	}
	return l, false, nil
}

// deleteKey decodes {"delete": {"key": "..."}}: the key and nothing else.
func deleteKey(raw json.RawMessage) (string, error) {
	var d map[string]json.RawMessage
	var key string
	if json.Unmarshal(raw, &d) != nil || len(d) != 1 || json.Unmarshal(d["key"], &key) != nil {
		return "", errors.New(`delete is {"key": "<string>"} and nothing else`)
	}
	if key == "" || len(key) > maxKey || !utf8.ValidString(key) {
		return "", errors.New("the delete key is empty, over its limit or not UTF-8")
	}
	return key, nil
}

type wireEntry struct {
	Key      string  `json:"key"`
	Title    string  `json:"title"`
	Revision string  `json:"revision"`
	Change   string  `json:"change"`
	Locator  string  `json:"locator"`
	Content  *string `json:"content"`
}

// entryOf decodes and checks one entry; unknown fields are ignored (the protocol
// may grow optional ones), every known one is bounded. A deletion is a delete
// line, not a change value.
func entryOf(raw json.RawMessage, maxContent int) (Entry, error) {
	var w wireEntry
	if err := json.Unmarshal(raw, &w); err != nil {
		return Entry{}, errors.New("entry has a field of the wrong type")
	}
	for _, f := range []struct {
		name, v string
		max     int
	}{{"key", w.Key, maxKey}, {"title", w.Title, maxTitle}, {"revision", w.Revision, maxRevision}, {"locator", w.Locator, maxLocator}} {
		if len(f.v) > f.max || !utf8.ValidString(f.v) {
			return Entry{}, errors.New(f.name + " is over its limit or not UTF-8")
		}
	}
	if w.Key == "" {
		return Entry{}, errors.New("key is required")
	}
	switch w.Change {
	case Added, Modified:
		if w.Title == "" || w.Revision == "" {
			return Entry{}, errors.New("title and revision are required")
		}
	default:
		return Entry{}, errors.New("change is added or modified (a deletion is a delete line)")
	}
	en := Entry{Key: w.Key, Title: w.Title, Revision: w.Revision, Change: w.Change, Locator: w.Locator}
	if w.Content != nil {
		if len(*w.Content) > maxContent || !utf8.ValidString(*w.Content) {
			return Entry{}, errors.New("content is over max_content_bytes or not UTF-8")
		}
		en.Content = *w.Content
	}
	return en, nil
}

// excerpt is the stderr of a failed script as one short line for feed_error:
// redacted of the listed variables' values (before and after the control
// characters go, so a value split by one is still caught), one line, 200 bytes.
func (e *execRecipe) excerpt(s string) string {
	s = e.redact(s)
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = e.redact(strings.Join(strings.Fields(s), " "))
	if len(s) > maxStderrShown {
		s = s[:maxStderrShown]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	if s == "" {
		return ""
	}
	return ": " + s
}

func (e *execRecipe) redact(s string) string { return connector.RedactEnv(s, e.c.Env) }

func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func cursorOf(c string) *string {
	if c == "" {
		return nil
	}
	return &c
}

// capped keeps the first max bytes written and drops the rest, so a chatty
// stderr costs nothing; it never fails a write (the script must not block).
type capped struct {
	max int
	b   bytes.Buffer
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.b.Len(); room > 0 {
		c.b.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

func (c *capped) String() string { return c.b.String() }

// systemEntryOf is the entry rule of a system listing: the content is the whole
// entry. revision and change are ignored (the surveyor hashes the content itself,
// D1372) and the title defaults to the key.
func systemEntryOf(raw json.RawMessage, maxContent int) (Entry, error) {
	var w wireEntry
	if err := json.Unmarshal(raw, &w); err != nil {
		return Entry{}, errors.New("entry has a field of the wrong type")
	}
	for _, f := range []struct {
		name, v string
		max     int
	}{{"key", w.Key, maxKey}, {"title", w.Title, maxTitle}, {"locator", w.Locator, maxLocator}} {
		if len(f.v) > f.max || !utf8.ValidString(f.v) {
			return Entry{}, errors.New(f.name + " is over its limit or not UTF-8")
		}
	}
	switch {
	case w.Key == "":
		return Entry{}, errors.New("key is required")
	case w.Content == nil || *w.Content == "":
		return Entry{}, errors.New("content is required")
	case len(*w.Content) > maxContent || !utf8.ValidString(*w.Content) || strings.ContainsRune(*w.Content, 0):
		return Entry{}, errors.New("content is over max_content_bytes, not UTF-8 or holds a NUL")
	}
	if w.Title == "" {
		w.Title = w.Key
	}
	return Entry{Key: w.Key, Title: w.Title, Locator: w.Locator, Content: *w.Content}, nil
}

// Limits of the check answer, in runes, and of a spec.
const (
	maxIdentity    = 120
	maxDetail      = 300
	maxAnswer      = 16 << 10
	maxSpecTitle   = 120
	maxSpecText    = 500
	maxSpecEntries = 32
	maxSpecDefault = 200
)

// label is a script-printed text as one clean line: redacted, control
// characters stripped, cut to max runes.
func (e *execRecipe) label(s string, max int) string {
	s = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, e.redact(s))), " ")
	if rs := []rune(s); len(rs) > max {
		s = string(rs[:max])
	}
	return s
}

// readAnswer reads the one-object answer of spec and check: exactly one JSON
// object with the single key, at most maxAnswer bytes, nothing after it.
func readAnswer(r io.Reader, key string, v any) (bool, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxAnswer+1))
	if err != nil || len(b) > maxAnswer {
		return false, fail(CodeProtocolError, "the %s answer is unreadable or over %d bytes", key, maxAnswer)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	var m map[string]json.RawMessage
	if dec.Decode(&m) != nil || dec.More() || len(m) != 1 || m[key] == nil {
		return true, fail(CodeProtocolError, "the %s answer is not exactly one JSON object {%q: {...}}", key, key)
	}
	if json.Unmarshal(m[key], v) != nil {
		return true, fail(CodeProtocolError, "the %s answer has a field of the wrong type", key)
	}
	return true, nil
}

// Check asks the script to check its credentials and reachability, in a process
// of its own. For a system recipe it also proves the identity read-only before
// every listing: anything but ok: true with read_only: true, exit 0, is a
// refusal (identity_not_read_only for a read_only that is false or missing,
// check_failed for the rest). It guards against a misconfigured identity and is
// not a security boundary: the script is operator code (docs/surveyor-recipes.md).
// A feed script that does not implement check answers ErrUnsupported.
func (e *execRecipe) Check(ctx context.Context, req Request) (Verified, error) {
	system := e.Shape() == ShapeSystem
	body, err := json.Marshal(e.base("check", &source{req.ID, req.Title, req.Locator, req.SourceKind, e.Shape(), nonNil(req.Include)}))
	if err != nil {
		return Verified{}, fail(CodeCheckFailed, "cannot encode the request")
	}
	var ans struct {
		OK       *bool  `json:"ok"`
		ReadOnly *bool  `json:"read_only"`
		Identity string `json:"identity"`
		Detail   string `json:"detail"`
	}
	err = e.runScript(ctx, CodeCheckFailed, body, func(r io.Reader) (bool, error) { return readAnswer(r, "check", &ans) })
	var re *Error
	switch {
	case ctx.Err() != nil:
		return Verified{}, ctx.Err()
	case errors.Is(err, ErrUnsupported) && system:
		return Verified{}, fail(CodeCheckFailed, "the recipe does not implement check, which a system recipe needs to prove its identity read-only")
	case errors.As(err, &re):
		return Verified{}, fail(CodeCheckFailed, "%s", re.Detail)
	case err != nil:
		return Verified{}, err
	}
	identity, detail := e.label(ans.Identity, maxIdentity), e.label(ans.Detail, maxDetail)
	said := strings.Join(slices.DeleteFunc([]string{identity, detail}, func(x string) bool { return x == "" }), " — ")
	switch {
	case ans.OK == nil:
		return Verified{}, fail(CodeCheckFailed, "the check answer has no boolean ok")
	case !*ans.OK:
		if said == "" {
			said = "the check answered ok: false"
		}
		return Verified{}, fail(CodeCheckFailed, "%s", said)
	case system && ans.ReadOnly == nil:
		return Verified{}, fail(CodeIdentityNotReadOnly, "the check of a system recipe must answer a boolean read_only")
	case system && !*ans.ReadOnly:
		return Verified{}, fail(CodeIdentityNotReadOnly, "%s", said)
	}
	return Verified{recipe: e.c.Name, ok: true, identity: identity, detail: detail}, nil
}

// SystemMaxGroups is the most groups one listing may hold (D1372).
const SystemMaxGroups = 1000

// ListSystem lists the current state of the system. It cannot be reached
// without a Verified from this recipe's Check: the value has no exported
// constructor, so no code path lists a live system without a clean check
// first. There is no cache: the poll runs Check again before each listing.
func (e *execRecipe) ListSystem(ctx context.Context, v Verified, req Request) (Listing, error) {
	if e.c.Shape != ShapeSystem || !v.ok || v.recipe != e.c.Name {
		return Listing{}, ErrNeedsCheck
	}
	maxContent := e.contentCap(req)
	body, err := json.Marshal(listRequest{request: e.base("list", &source{req.ID, req.Title, req.Locator, req.SourceKind, ShapeSystem, nonNil(req.Include)}),
		Cursor: nil, MaxEntries: SystemMaxGroups, MaxContentBytes: maxContent})
	if err != nil {
		return Listing{}, fail(CodeListFailed, "cannot encode the request")
	}
	var l Listing
	err = e.runScript(ctx, CodeListFailed, body, func(r io.Reader) (bool, error) {
		var atEOF bool
		var perr error
		l, atEOF, perr = e.parse(r, SystemMaxGroups, maxContent, e.entry)
		return atEOF, perr
	})
	err = unsupportedIsFailure(err, CodeListFailed, "list")
	if err == nil && l.NextCursor != "" {
		// A system listing is complete or it is nothing: every group it leaves out
		// becomes a removal, so a lister that stopped early (a cursor to resume
		// from) must not be read as having listed the whole system.
		return Listing{}, fail(CodeProtocolError, "a system listing ends with an empty next_cursor: it is always complete, there is no cursor to resume from")
	}
	return l, err
}

// fetchRequest adds what only fetch carries: the keys whose content is wanted.
type fetchRequest struct {
	request
	Keys            []string `json:"keys"`
	MaxContentBytes int      `json:"max_content_bytes"`
}

// Fetch asks the script for the content of keys it listed without any (at most
// MaxFetchKeys), in a process of its own. The answer is one
// {"content": {"key", "content"}} line per key and then exactly one
// {"done": true}: a key not asked, asked twice or left unanswered, a content that
// is empty, over max_content_bytes or not UTF-8, or a line that is anything else,
// is protocol_error; a failing process is fetch_failed. What the script prints is
// untrusted: the content is data for a unit and never appears in an error, and a
// key quoted in one goes through label. Only a feed recipe has fetch.
func (e *execRecipe) Fetch(ctx context.Context, req Request, keys []string) (map[string]string, error) {
	if e.c.Shape == ShapeSystem {
		return nil, ErrUnsupported
	}
	if len(keys) == 0 || len(keys) > MaxFetchKeys {
		return nil, fail(CodeFetchFailed, "a fetch carries 1 to %d keys", MaxFetchKeys)
	}
	maxContent := e.contentCap(req)
	body, err := json.Marshal(fetchRequest{request: e.base(VerbFetch, &source{req.ID, req.Title, req.Locator, req.SourceKind, ShapeFeed, nonNil(req.Include)}),
		Keys: keys, MaxContentBytes: maxContent})
	if err != nil {
		return nil, fail(CodeFetchFailed, "cannot encode the request")
	}
	var got map[string]string
	var printed countingReader
	err = e.runScript(ctx, CodeFetchFailed, body, func(r io.Reader) (bool, error) {
		var atEOF bool
		var perr error
		printed.r = r
		got, atEOF, perr = e.readFetch(&printed, keys, maxContent)
		return atEOF, perr
	})
	if errors.Is(err, ErrUnsupported) && printed.n.Load() > 0 {
		// Exit 64 means "no fetch" only when nothing was printed: after some output it
		// is a script that broke off, and a half answer is not "nothing to fetch".
		return nil, fail(CodeProtocolError, "the script printed part of an answer and then exited %d", exitUnsupported)
	}
	if err != nil {
		return nil, err
	}
	return got, nil
}

// readFetch reads the answer of fetch as a stream of JSON values, bounded by
// max_output_bytes, and stops at the first violation. atEOF is true when the
// stream ended before done: the exit status then says why.
func (e *execRecipe) readFetch(r io.Reader, keys []string, maxContent int) (map[string]string, bool, error) {
	lr := &io.LimitedReader{R: r, N: int64(e.c.MaxOutput) + 1}
	dec := json.NewDecoder(lr)
	want := make(map[string]bool, len(keys))
	for _, k := range keys {
		want[k] = true
	}
	got := make(map[string]string, len(keys))
	done := false
	for n := 1; ; n++ {
		var m map[string]json.RawMessage
		err := dec.Decode(&m)
		switch {
		case lr.N <= 0:
			return nil, false, fail(CodeFetchFailed, "output over %d bytes", e.c.MaxOutput)
		case errors.Is(err, io.EOF) && done:
			if len(got) != len(keys) {
				return nil, false, fail(CodeProtocolError, "%d of the %d keys asked were not answered", len(keys)-len(got), len(keys))
			}
			return got, false, nil
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
			return nil, true, fail(CodeProtocolError, "no final {\"done\": true}")
		case err != nil:
			return nil, false, fail(CodeProtocolError, "answer %d is not a JSON object", n)
		case done:
			return nil, false, fail(CodeProtocolError, "answer %d: nothing may follow done", n)
		}
		switch {
		case len(m) != 1:
			return nil, false, fail(CodeProtocolError, "answer %d is not a JSON object with one key, content or done", n)
		case m["content"] != nil:
			var c struct {
				Key     string  `json:"key"`
				Content *string `json:"content"`
			}
			if json.Unmarshal(m["content"], &c) != nil || c.Content == nil {
				return nil, false, fail(CodeProtocolError, "answer %d: content is {\"key\", \"content\"} of two strings", n)
			}
			_, twice := got[c.Key]
			switch {
			case !want[c.Key]:
				return nil, false, fail(CodeProtocolError, "answer %d: the key %q was not asked", n, e.label(c.Key, maxKey))
			case twice:
				return nil, false, fail(CodeProtocolError, "answer %d: the key %q is answered twice", n, e.label(c.Key, maxKey))
			case *c.Content == "" || len(*c.Content) > maxContent || !utf8.ValidString(*c.Content):
				return nil, false, fail(CodeProtocolError, "answer %d: the content of %q is empty, over max_content_bytes (%d) or not UTF-8", n, e.label(c.Key, maxKey), maxContent)
			}
			got[c.Key] = *c.Content
		case m["done"] != nil:
			if string(m["done"]) != "true" {
				return nil, false, fail(CodeProtocolError, "answer %d: done is true", n)
			}
			done = true
		default:
			return nil, false, fail(CodeProtocolError, "answer %d is neither content nor done", n)
		}
	}
}

// countingReader counts what the script printed, read from another goroutine.
type countingReader struct {
	r io.Reader
	n atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}
