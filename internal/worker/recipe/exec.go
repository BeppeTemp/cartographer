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
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/worker/connector"
)

// ProtocolVersion is the version of the exec recipe protocol (docs/worker-recipes.md).
// A change to what is sent or accepted needs a new number (D1262).
const ProtocolVersion = 1

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
// worker config: the command comes from there and from nowhere else.
type ExecConfig struct {
	Name         string
	Command      string            // absolute path, or a bare name looked up on PATH
	Args         []string          // verbatim, no shell
	Env          map[string]string // NAME the script sees -> name of the worker's variable
	Timeout      time.Duration
	MaxOutput    int
	MaxContent   int
	Connector    string // how the unit executor reads an entry's locator; "" = the Source's own
	Instructions string
}

// execSlot lets one script run at a time per replica: feeds are handled one by
// one, and a second script would multiply the child processes a worker holds.
var execSlot = make(chan struct{}, 1)

type execRecipe struct{ c ExecConfig }

// NewExec returns the recipe that runs an operator-declared executable.
func NewExec(c ExecConfig) Recipe { return &execRecipe{c} }

func (e *execRecipe) Name() string         { return e.c.Name }
func (e *execRecipe) Shape() string        { return ShapeFeed }
func (e *execRecipe) Instructions() string { return e.c.Instructions }

// Connector is the connector the unit executor uses to read an entry's locator,
// "" when the recipe names none (the poll then falls back to the Source's own).
func (e *execRecipe) Connector() string { return e.c.Connector }

// request is the stdin of the script: protocol v1, mode "list" only. The
// reserved mode "preflight" and shape "system" belong to #1372 and are never sent.
type request struct {
	Protocol        int     `json:"protocol"`
	Mode            string  `json:"mode"`
	Source          source  `json:"source"`
	Cursor          *string `json:"cursor"`
	MaxEntries      int     `json:"max_entries"`
	MaxContentBytes int     `json:"max_content_bytes"`
}

type source struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Locator     string   `json:"locator"`
	SourceKind  string   `json:"source_kind"`
	SourceShape string   `json:"source_shape"`
	Include     []string `json:"include"`
}

// List runs the script once. argv with no shell, a per-call working directory,
// the environment of connector.ChildEnv (exactly the variables listed plus
// PATH/HOME, so the model key never reaches operator code), the whole process
// group killed on timeout, cancel or an output past the cap. Every detail that
// leaves this function is redacted of the listed variables' values.
func (e *execRecipe) List(ctx context.Context, req Request) (Listing, error) {
	select {
	case execSlot <- struct{}{}:
		defer func() { <-execSlot }()
	case <-ctx.Done():
		return Listing{}, ctx.Err()
	}
	maxEntries, maxContent := req.MaxEntries, e.c.MaxContent
	if maxEntries <= 0 {
		maxEntries = DefaultMaxEntries
	}
	if req.MaxContentBytes > 0 && req.MaxContentBytes < maxContent {
		maxContent = req.MaxContentBytes
	}
	body, err := json.Marshal(request{Protocol: ProtocolVersion, Mode: "list",
		Source: source{req.ID, req.Title, req.Locator, req.SourceKind, ShapeFeed, nonNil(req.Include)},
		Cursor: cursorOf(req.Cursor), MaxEntries: maxEntries, MaxContentBytes: maxContent})
	if err != nil {
		return Listing{}, fail(CodeListFailed, "cannot encode the request")
	}
	env, err := connector.ChildEnv(e.c.Env)
	if err != nil {
		return Listing{}, fail(CodeListFailed, "%v", err) // names the variable, never a value
	}
	dir, err := os.MkdirTemp("", "cartographer-recipe-")
	if err != nil {
		return Listing{}, fail(CodeListFailed, "no temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	runCtx, cancel := context.WithTimeout(ctx, e.c.Timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, e.c.Command, e.c.Args...)
	cmd.Dir, cmd.Env, cmd.Stdin = dir, env, bytes.NewReader(body)
	cmd.WaitDelay = 2 * time.Second
	connector.KillGroupOnCancel(cmd)
	stderr := &capped{max: maxStderrKept}
	cmd.Stderr = stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return Listing{}, fail(CodeListFailed, "cannot start: %v", err)
	}
	if err := cmd.Start(); err != nil {
		return Listing{}, fail(CodeListFailed, "cannot start %s: %s", baseName(e.c.Command), e.redact(err.Error()))
	}
	// The script may leave grandchildren that outlive it (and hold the pipes).
	defer connector.KillGroup(cmd)

	l, atEOF, perr := e.parse(out, maxEntries, maxContent)
	if perr != nil && !atEOF {
		timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded) // before cancel() below sets Err itself
		cancel()                                                      // kills the group; the exit status no longer matters
		_ = cmd.Wait()
		switch {
		case ctx.Err() != nil:
			return Listing{}, ctx.Err()
		case timedOut:
			return Listing{}, fail(CodeListFailed, "timeout after %s", e.c.Timeout)
		}
		return Listing{}, perr
	}
	werr := cmd.Wait()
	if perr != nil { // the stream ended without a next_cursor: the exit status says why
		var ee *exec.ExitError
		if errors.As(werr, &ee) && runCtx.Err() == nil {
			return Listing{}, fail(CodeListFailed, "exit %d%s", ee.ExitCode(), e.excerpt(stderr.String()))
		}
		if runCtx.Err() == nil {
			return Listing{}, perr
		}
	}
	switch {
	case ctx.Err() != nil:
		return Listing{}, ctx.Err()
	case runCtx.Err() != nil:
		return Listing{}, fail(CodeListFailed, "timeout after %s", e.c.Timeout)
	case werr != nil:
		var ee *exec.ExitError
		if errors.As(werr, &ee) {
			return Listing{}, fail(CodeListFailed, "exit %d%s", ee.ExitCode(), e.excerpt(stderr.String()))
		}
		return Listing{}, fail(CodeListFailed, "%s", e.redact(werr.Error()))
	}
	return l, nil
}

// parse reads the JSON lines as a stream and stops at the first violation, so a
// script cannot make the worker hold more than max_output_bytes.
// atEOF is true when the stream ended cleanly but without a next_cursor.
func (e *execRecipe) parse(r io.Reader, maxEntries, maxContent int) (l Listing, atEOF bool, err error) {
	l, atEOF, err = e.parseLines(r, maxEntries, maxContent)
	return
}

func (e *execRecipe) parseLines(r io.Reader, maxEntries, maxContent int) (Listing, bool, error) {
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
				return Listing{}, false, fail(CodeProtocolError, "line %d is not a JSON object with one key, entry or next_cursor", n)
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
			case m["next_cursor"] != nil:
				var c string
				if err := json.Unmarshal(m["next_cursor"], &c); err != nil || len(c) > maxNextCursor || !utf8.ValidString(c) {
					return Listing{}, false, fail(CodeProtocolError, "line %d: next_cursor is a string of at most %d bytes", n, maxNextCursor)
				}
				l.NextCursor, done = c, true
			default:
				return Listing{}, false, fail(CodeProtocolError, "line %d is neither entry nor next_cursor", n)
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

type wireEntry struct {
	Key      string  `json:"key"`
	Title    string  `json:"title"`
	Revision string  `json:"revision"`
	Change   string  `json:"change"`
	Locator  string  `json:"locator"`
	Content  *string `json:"content"`
}

// entryOf decodes and checks one entry; unknown fields are ignored (protocol v1
// may grow optional ones), every known one is bounded.
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
			return Entry{}, errors.New("title and revision are required for added and modified")
		}
	case Removed:
		if w.Content != nil {
			return Entry{}, errors.New("content is not allowed on removed")
		}
	default:
		return Entry{}, errors.New("change is added, modified or removed")
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
