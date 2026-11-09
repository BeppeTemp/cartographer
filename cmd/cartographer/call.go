package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/mcpbridge"
)

const callUsage = `usage: cartographer call --provider <name> <tool> [key=value ...] [--json '<object>'|-]
       cartographer call --provider <name> --list
       cartographer call --provider <name> --describe <tool>[,<tool>...]
       cartographer call --provider <name> --instructions
       cartographer call --provider <name> --batch -  [--output text|json]

Calls one MCP tool and exits, for agents that cannot use MCP. The KBs served
are the ones the stdio bridge would serve for the provider and the working
directory. A value that parses as JSON (5, true, ["a"], "2026") is passed
typed, anything else as a string.

--instructions prints the instructions the server sends an MCP client at
initialize. --batch - reads one JSON request per line from stdin
({"tool": "<name>", "arguments": {...}, "id": <any>}) and writes one JSON result
per line to stdout, in input order, through one connection; a failing line does
not stop the batch.

flags: --dir <path>  --output text|json  --timeout <duration>  --verbose
exit:  0 ok, 1 the tool returned an error, 2 usage, configuration or transport
       (--batch: the worst class among the lines)`

// maxCallJSON bounds --json - (same limit as a bridge line).
const maxCallJSON = 16 * 1024 * 1024

var callKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// cmdCall is `cartographer call`: one stateless request through the stdio
// bridge's scope and transport.
//
// It must not write: no file, directory, cache, log or lock, anywhere, and it
// works with a read-only $HOME. See TestCallWritesNothing.
func cmdCall(args []string) int {
	fs := flag.NewFlagSet("call", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	provider := fs.String("provider", "", "provider whose bindings apply (required)")
	dir := fs.String("dir", "", "folder to resolve (default: the working directory)")
	list := fs.Bool("list", false, "list the tools")
	describe := fs.String("describe", "", "print the description and input schema of a tool, or of several (a,b)")
	instructions := fs.Bool("instructions", false, "print the instructions the server sends at initialize")
	batch := fs.String("batch", "", "run JSONL requests read from stdin (the value -) through one connection")
	output := fs.String("output", "text", "text or json")
	timeout := fs.Duration("timeout", 5*time.Minute, "bound on the whole invocation")
	jsonArg := fs.String("json", "", "arguments as a JSON object, or - to read it from stdin")
	verbose := fs.Bool("verbose", false, "print the bridge log even on success")
	fs.Usage = func() { fmt.Fprintln(os.Stderr, callUsage) }

	// Flags may follow the tool name: parse again after each positional.
	var pos []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(os.Stderr, "Error: "+format+"\n", a...)
		return 2
	}
	modes := 0
	for _, on := range []bool{*list, *describe != "", *instructions, *batch != "", len(pos) > 0} {
		if on {
			modes++
		}
	}
	if *provider == "" || modes != 1 {
		fs.Usage()
		return 2
	}
	if *output != "text" && *output != "json" {
		return fail("--output must be text or json")
	}

	if *batch != "" && *batch != "-" {
		return fail("--batch takes only - (JSON lines on stdin)")
	}
	jsonSet := false
	fs.Visit(func(f *flag.Flag) { jsonSet = jsonSet || f.Name == "json" })
	if !isKnownProvider(*provider) {
		return fail("unknown provider %q (valid: %s)", *provider, strings.Join(knownProviderNames(), ", "))
	}
	var callArgs map[string]any
	var rawPairs map[string]string
	if len(pos) > 0 {
		var err error
		if callArgs, rawPairs, err = parseCallArgs(pos[1:], jsonSet, *jsonArg, mcpStdin); err != nil {
			return fail("%v", err)
		}
	} else if jsonSet && *batch != "" {
		return fail("--json does not go with --batch (each line carries its arguments)")
	} else if jsonSet {
		return fail("--json goes with a tool call")
	}

	_, cfg, err := loadWorkspaceConfig()
	if err != nil {
		return fail("%v", err)
	}
	if *dir == "" {
		// The process working directory, never $PWD (as cmdMCP).
		if *dir, err = os.Getwd(); err != nil {
			return fail("%v", err)
		}
	} else if st, serr := os.Stat(*dir); serr != nil || !st.IsDir() {
		return fail("--dir %q is not an existing directory", *dir)
	}
	token, err := resolveTokenErr(cfg)
	if err != nil {
		return fail("%v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	// One bridge per invocation, started at the first request: a batch with
	// no request at all never starts one.
	var sess *callSession
	session := func() *callSession {
		if sess == nil {
			sess = newCallSession(ctx, cfg, token, *provider, *dir, *instructions)
		}
		return sess
	}
	send := func(method string, params map[string]any) (json.RawMessage, string, error) {
		resp, err := session().Do(ctx, method, params)
		return resp, "", err
	}
	var blog string
	code := func() int {
		if *batch != "" {
			c, log := runBatch(ctx, session, mcpStdin, os.Stdout, os.Stderr, *output)
			sess = nil
			blog = log
			return c
		}
		if *instructions {
			return printInstructions(ctx, session(), *output)
		}
		if len(pos) == 0 {
			tools, log, err := listTools(send)
			blog = log
			if err != nil {
				return fail("%v", err)
			}
			if len(tools) == 0 {
				return fail("no KB is served for %s (the bridge lists no tool); see \"cartographer workspace resolve\"", *dir)
			}
			if *list {
				for _, t := range tools {
					fmt.Fprintf(os.Stdout, "%s\t%s\n", escapeControl(t.Name), escapeControl(firstSentence(t.Description)))
				}
				return 0
			}
			return describeTools(tools, *describe, fail)
		}
		if len(rawPairs) > 0 {
			// The schema types the key=value words (#1802). A bridge that
			// cannot list tools, or a tool it does not know, leaves the
			// JSON-typed values as parsed: the call's own error is the answer.
			if tools, log, lerr := listTools(send); lerr == nil {
				blog += log
				for _, t := range tools {
					if t.Name == pos[0] {
						coerceCallArgs(callArgs, rawPairs, t.InputSchema)
					}
				}
			} else {
				blog += log
			}
		}
		resp, log, err := send("tools/call", map[string]any{"name": pos[0], "arguments": callArgs})
		blog += log
		if err != nil {
			return fail("%v", err)
		}
		result, err := responseResult(resp)
		if err != nil {
			code := 2
			if errors.Is(err, errInvalidParams) {
				code = 1
			}
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return code
		}
		return printToolResult(pos[0], result, *output)
	}()
	if sess != nil {
		blog += sess.Close()
	}
	if blog != "" && (code != 0 || *verbose) {
		fmt.Fprint(os.Stderr, blog)
	}
	return code
}

type toolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// listTools follows nextCursor and returns the tools sorted by name.
func listTools(send func(string, map[string]any) (json.RawMessage, string, error)) ([]toolInfo, string, error) {
	var all []toolInfo
	var logs strings.Builder
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		resp, log, err := send("tools/list", params)
		logs.WriteString(log)
		if err != nil {
			return nil, logs.String(), err
		}
		result, err := responseResult(resp)
		if err != nil {
			return nil, logs.String(), err
		}
		var page struct {
			Tools      []toolInfo `json:"tools"`
			NextCursor string     `json:"nextCursor"`
		}
		if err := json.Unmarshal(result, &page); err != nil {
			return nil, logs.String(), fmt.Errorf("unreadable tool list: %w", err)
		}
		all = append(all, page.Tools...)
		if page.NextCursor == "" || page.NextCursor == cursor {
			break
		}
		cursor = page.NextCursor
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	return all, logs.String(), nil
}

// firstSentence is the description up to its first sentence end or line break.
func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\n\r"); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, ". "); i >= 0 {
		s = s[:i+1]
	}
	return s
}

// errInvalidParams marks a JSON-RPC -32602 answer.
var errInvalidParams = errors.New("invalid params")

// responseResult is the result of a JSON-RPC response, or its error.
func responseResult(resp json.RawMessage) (json.RawMessage, error) {
	var m struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp, &m); err != nil {
		return nil, fmt.Errorf("unreadable response: %w", err)
	}
	if m.Error != nil {
		msg := escapeControl(m.Error.Message)
		if m.Error.Code == -32602 && strings.HasPrefix(m.Error.Message, "invalid params:") {
			// An argument error (the server's "invalid params: ..."), like a
			// tool's own refusal: exit 1. The other -32602 answers, "unknown
			// tool" and "no Cartographer tool is available in this folder",
			// are not arguments and stay exit 2.
			return nil, fmt.Errorf("%w: %s", errInvalidParams, msg)
		}
		return nil, errors.New(msg)
	}
	if len(m.Result) == 0 {
		return nil, errors.New("empty response")
	}
	return m.Result, nil
}

// toolOutcome is a tools/call result read: its text (the text content joined),
// whether the tool reported an error, and the types of the content left out.
type toolOutcome struct {
	text    string
	isError bool
	omitted []string
}

// readToolResult is what printToolResult and a batch line share, so the two
// cannot differ on isError, an empty content or non-text content.
func readToolResult(tool string, result json.RawMessage) (toolOutcome, error) {
	var r struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		return toolOutcome{}, fmt.Errorf("unreadable result: %v", err)
	}
	if len(r.Content) == 0 {
		return toolOutcome{}, fmt.Errorf("tool %q returned no content", tool)
	}
	var texts []string
	var o toolOutcome
	for _, c := range r.Content {
		if c.Type == "text" {
			texts = append(texts, c.Text)
		} else {
			o.omitted = append(o.omitted, c.Type)
		}
	}
	o.text = strings.Join(texts, "\n")
	o.isError = r.IsError
	return o, nil
}

// printToolResult writes a tools/call result and returns the exit code.
func printToolResult(tool string, result json.RawMessage, output string) int {
	o, err := readToolResult(tool, result)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 2
	}
	for _, typ := range o.omitted {
		fmt.Fprintf(os.Stderr, "[%s content omitted]\n", typ)
	}
	// A failed result is a diagnostic from the server, escaped like every other
	// server-chosen message. The success path below is the tool's normal output
	// (KB content the caller asked for) and is printed as it is.
	if o.isError {
		fmt.Fprintln(os.Stderr, escapeControl(o.text))
		return 1
	}
	if output == "json" {
		var out bytes.Buffer
		_ = json.Indent(&out, result, "", "  ")
		fmt.Fprintln(os.Stdout, out.String())
		return 0
	}
	fmt.Fprintln(os.Stdout, o.text)
	return 0
}

// parseCallArgs builds the arguments object from key=value words and --json.
//
// jsonSet is whether --json was given at all: an empty value (an unset shell
// variable) is a usage error, never "no arguments".
func parseCallArgs(words []string, jsonSet bool, jsonArg string, stdin io.Reader) (map[string]any, map[string]string, error) {
	args := map[string]any{}
	raw := map[string]string{}
	if jsonSet {
		raw := []byte(jsonArg)
		if jsonArg == "-" {
			var err error
			if raw, err = io.ReadAll(io.LimitReader(stdin, maxCallJSON+1)); err != nil {
				return nil, nil, fmt.Errorf("read --json from stdin: %w", err)
			}
			if len(raw) > maxCallJSON {
				return nil, nil, errors.New("--json input is larger than 16 MiB")
			}
			if len(bytes.TrimSpace(raw)) == 0 {
				return nil, nil, errors.New("--json - read an empty stdin")
			}
		}
		if err := json.Unmarshal(raw, &args); err != nil || args == nil {
			return nil, nil, errors.New("--json must be a JSON object")
		}
	}
	fromJSON := map[string]bool{}
	for k := range args {
		fromJSON[k] = true
	}
	seen := map[string]bool{}
	for _, w := range words {
		k, v, ok := strings.Cut(w, "=")
		if !ok {
			return nil, nil, fmt.Errorf("argument %q is not key=value", w)
		}
		if !callKeyRE.MatchString(k) {
			return nil, nil, fmt.Errorf("argument name %q is not valid", k)
		}
		if seen[k] || fromJSON[k] {
			return nil, nil, fmt.Errorf("argument %q is given twice", k)
		}
		seen[k] = true
		var typed any
		if err := json.Unmarshal([]byte(v), &typed); err == nil {
			args[k] = typed
			raw[k] = v
		} else {
			args[k] = v
			raw[k] = v
		}
	}
	return args, raw, nil
}

// coerceCallArgs retypes the key=value words against the tool's input schema
// (#1802): a string property keeps the raw text (`query=2026`, `query=null`;
// a quoted value is already a string),
// an array property wraps a value that is not already an array (`where=a=b`).
// Every key=value word asks for the schema, since a plain string may need wrapping. A key the
// schema does not declare, or a type it does not know, keeps the parsed JSON.
func coerceCallArgs(args map[string]any, raw map[string]string, schema json.RawMessage) {
	var sc struct {
		Properties map[string]struct {
			Type  json.RawMessage `json:"type"`
			Items struct {
				Type json.RawMessage `json:"type"`
			} `json:"items"`
		} `json:"properties"`
	}
	if json.Unmarshal(schema, &sc) != nil {
		return
	}
	for k, text := range raw {
		p, ok := sc.Properties[k]
		if !ok {
			continue
		}
		switch schemaType(p.Type) {
		case "string":
			// A quoted value (title="2026") is already the string.
			if _, isStr := args[k].(string); !isStr {
				args[k] = text
			}
		case "array":
			if _, isArr := args[k].([]any); !isArr {
				if _, isStr := args[k].(string); isStr {
					args[k] = []any{args[k]}
				} else if schemaType(p.Items.Type) == "string" {
					args[k] = []any{text}
				} else {
					args[k] = []any{args[k]}
				}
			}
		}
	}
}

// schemaType is the one type of a JSON Schema "type", or "" for none or a union.
func schemaType(raw json.RawMessage) string {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one
	}
	return ""
}

// callSession is one mcpbridge.Bridge serving the requests of one invocation:
// one for a single call, many for --batch.
//
// Trap: In must not reach EOF between requests, nor right after the last one.
// Run aborts in-flight calls a grace after stdin closes, which would cut a long
// reindex or sync. The pipe stays open until the last response is seen, and
// Close closes it (TestCallBridgeKeepsThePipeOpen).
//
// One request is outstanding at a time: the bridge dispatches each line on its
// own goroutine, so overlapping requests could be answered out of order, and a
// batch is a script (a write must be seen by the read after it), not a set.
type callSession struct {
	pw   *io.PipeWriter
	pr   *io.PipeReader
	out  *responseRouter
	log  *lockedBuf
	done chan struct{} // closed when Run returned
	err  error         // Run's error, valid after done
	next int
}

// newCallSession starts the bridge. guide asks the server for its operating
// guide in initialize (mcpbridge.Bridge.Guide).
func newCallSession(ctx context.Context, cfg *clientconfig.Config, token, provider, dir string, guide bool) *callSession {
	pr, pw := io.Pipe()
	s := &callSession{pr: pr, pw: pw, out: &responseRouter{}, log: &lockedBuf{}, done: make(chan struct{})}
	b := &mcpbridge.Bridge{
		Cfg: cfg, Provider: provider, Dir: dir, Token: token, Version: version, OneShot: true, Guide: guide,
		In: pr, Out: s.out, Log: s.log,
	}
	b.Plugins = bridgePluginTools(cfg, s.log)
	go func() {
		s.err = b.Run(ctx)
		close(s.done)
		// A write to a bridge that stopped must fail, not block.
		_ = pr.CloseWithError(io.ErrClosedPipe)
	}()
	return s
}

// Do sends one JSON-RPC request and returns the response with the same id.
func (s *callSession) Do(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	s.next++
	id := s.next
	params["_meta"] = client.RequestMeta(version)
	line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	ch := s.out.expect(id)
	// A failed write means the bridge stopped: done is closed then.
	go func() { _, _ = s.pw.Write(append(line, '\n')) }()
	var resp json.RawMessage
	select {
	case resp = <-ch:
	case <-ctx.Done():
	case <-s.done:
		select { // the answer may have raced the end of Run
		case resp = <-ch:
		default:
		}
	}
	// A deadline wins over a response: the bridge answers an aborted request
	// with a transport error that would hide the cause.
	if resp != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return resp, nil
	}
	if cerr := ctx.Err(); cerr != nil {
		if errors.Is(cerr, context.DeadlineExceeded) {
			return nil, errors.New("timed out waiting for the server")
		}
		return nil, cerr
	}
	select {
	case <-s.done:
		if s.err != nil {
			return nil, s.err
		}
	default:
	}
	return nil, errors.New("no response from the bridge")
}

// stopped reports whether Run has returned, and why.
func (s *callSession) stopped() (bool, error) {
	select {
	case <-s.done:
		if s.err != nil {
			return true, s.err
		}
		return true, errors.New("no response from the bridge")
	default:
		return false, nil
	}
}

// Close ends the session: it closes the pipe, waits for Run (the closing
// DELETE and the in-flight grace happen there) and returns the bridge log.
func (s *callSession) Close() string {
	_ = s.pw.Close()
	<-s.done
	_ = s.pr.Close()
	return s.log.String()
}

// responseRouter is the bridge's Out: it hands the response with an awaited id
// to the caller waiting for it. The bridge writes one compact line per Write.
type responseRouter struct {
	mu      sync.Mutex
	buf     []byte
	waiters map[int]chan json.RawMessage
}

// expect registers a wait for the response with this id.
func (w *responseRouter) expect(id int) <-chan json.RawMessage {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.waiters == nil {
		w.waiters = map[int]chan json.RawMessage{}
	}
	ch := make(chan json.RawMessage, 1)
	w.waiters[id] = ch
	return ch
}

func (w *responseRouter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		raw := bytes.TrimSpace(w.buf[:i])
		w.buf = w.buf[i+1:]
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(raw, &m) != nil || m.Method != "" {
			continue
		}
		var id int
		if json.Unmarshal(m.ID, &id) != nil {
			continue
		}
		if ch, ok := w.waiters[id]; ok {
			delete(w.waiters, id)
			ch <- append(json.RawMessage(nil), raw...)
		}
	}
	return len(p), nil
}

// lockedBuf is a bytes.Buffer safe for the bridge's goroutines.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
