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
       cartographer call --provider <name> --describe <tool>

Calls one MCP tool and exits, for agents that cannot use MCP. The KBs served
are the ones the stdio bridge would serve for the provider and the working
directory. A value that parses as JSON (5, true, ["a"], "2026") is passed
typed, anything else as a string.

flags: --dir <path>  --output text|json  --timeout <duration>  --verbose
exit:  0 ok, 1 the tool returned an error, 2 usage, configuration or transport`

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
	describe := fs.String("describe", "", "print a tool's description and input schema")
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
	for _, on := range []bool{*list, *describe != "", len(pos) > 0} {
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

	jsonSet := false
	fs.Visit(func(f *flag.Flag) { jsonSet = jsonSet || f.Name == "json" })
	var callArgs map[string]any
	if len(pos) > 0 {
		var err error
		if callArgs, err = parseCallArgs(pos[1:], jsonSet, *jsonArg, mcpStdin); err != nil {
			return fail("%v", err)
		}
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
	}
	token, err := resolveTokenErr(cfg)
	if err != nil {
		return fail("%v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	send := func(method string, params map[string]any) (json.RawMessage, string, error) {
		params["_meta"] = client.RequestMeta(version)
		line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		return callBridge(ctx, cfg, token, *provider, *dir, line)
	}
	var blog string
	code := func() int {
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
					fmt.Fprintf(os.Stdout, "%s\t%s\n", t.Name, firstSentence(t.Description))
				}
				return 0
			}
			for _, t := range tools {
				if t.Name == *describe {
					out, _ := json.MarshalIndent(map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.InputSchema}, "", "  ")
					fmt.Fprintln(os.Stdout, string(out))
					return 0
				}
			}
			return fail("unknown tool %q; run \"cartographer call --list\"", *describe)
		}
		resp, log, err := send("tools/call", map[string]any{"name": pos[0], "arguments": callArgs})
		blog = log
		if err != nil {
			return fail("%v", err)
		}
		result, err := responseResult(resp)
		if err != nil {
			return fail("%v", err)
		}
		return printToolResult(pos[0], result, *output)
	}()
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

// responseResult is the result of a JSON-RPC response, or its error.
func responseResult(resp json.RawMessage) (json.RawMessage, error) {
	var m struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp, &m); err != nil {
		return nil, fmt.Errorf("unreadable response: %w", err)
	}
	if m.Error != nil {
		return nil, errors.New(m.Error.Message)
	}
	if len(m.Result) == 0 {
		return nil, errors.New("empty response")
	}
	return m.Result, nil
}

// printToolResult writes a tools/call result and returns the exit code.
func printToolResult(tool string, result json.RawMessage, output string) int {
	var r struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		fmt.Fprintf(os.Stderr, "Error: unreadable result: %v\n", err)
		return 2
	}
	if len(r.Content) == 0 {
		fmt.Fprintf(os.Stderr, "Error: tool %q returned no content\n", tool)
		return 2
	}
	var texts []string
	for _, c := range r.Content {
		if c.Type == "text" {
			texts = append(texts, c.Text)
		} else {
			fmt.Fprintf(os.Stderr, "[%s content omitted]\n", c.Type)
		}
	}
	text := strings.Join(texts, "\n")
	if r.IsError {
		fmt.Fprintln(os.Stderr, text)
		return 1
	}
	if output == "json" {
		var out bytes.Buffer
		_ = json.Indent(&out, result, "", "  ")
		fmt.Fprintln(os.Stdout, out.String())
		return 0
	}
	fmt.Fprintln(os.Stdout, text)
	return 0
}

// parseCallArgs builds the arguments object from key=value words and --json.
//
// jsonSet is whether --json was given at all: an empty value (an unset shell
// variable) is a usage error, never "no arguments".
func parseCallArgs(words []string, jsonSet bool, jsonArg string, stdin io.Reader) (map[string]any, error) {
	args := map[string]any{}
	if jsonSet {
		raw := []byte(jsonArg)
		if jsonArg == "-" {
			var err error
			if raw, err = io.ReadAll(io.LimitReader(stdin, maxCallJSON+1)); err != nil {
				return nil, fmt.Errorf("read --json from stdin: %w", err)
			}
			if len(raw) > maxCallJSON {
				return nil, errors.New("--json input is larger than 16 MiB")
			}
			if len(bytes.TrimSpace(raw)) == 0 {
				return nil, errors.New("--json - read an empty stdin")
			}
		}
		if err := json.Unmarshal(raw, &args); err != nil || args == nil {
			return nil, errors.New("--json must be a JSON object")
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
			return nil, fmt.Errorf("argument %q is not key=value", w)
		}
		if !callKeyRE.MatchString(k) {
			return nil, fmt.Errorf("argument name %q is not valid", k)
		}
		if seen[k] || fromJSON[k] {
			return nil, fmt.Errorf("argument %q is given twice", k)
		}
		seen[k] = true
		var typed any
		if err := json.Unmarshal([]byte(v), &typed); err == nil {
			args[k] = typed
		} else {
			args[k] = v
		}
	}
	return args, nil
}

// callBridge sends one JSON-RPC line through a mcpbridge.Bridge and returns the
// response with the same id, plus the bridge log.
//
// Trap: In must not reach EOF right after the request. Run aborts in-flight
// calls a grace after stdin closes, which would cut a long reindex or sync. The
// pipe stays open until the response is seen (TestCallBridgeKeepsThePipeOpen).
func callBridge(ctx context.Context, cfg *clientconfig.Config, token, provider, dir string, line []byte) (json.RawMessage, string, error) {
	pr, pw := io.Pipe()
	log := &lockedBuf{}
	w := &responseWatcher{done: func() { _ = pw.Close() }}
	b := &mcpbridge.Bridge{
		Cfg: cfg, Provider: provider, Dir: dir, Token: token, Version: version,
		In: pr, Out: w, Log: log,
	}
	go func() {
		_, _ = pw.Write(append(append([]byte(nil), line...), '\n'))
	}()
	err := b.Run(ctx)
	_ = pw.Close()
	_ = pr.Close()
	// A deadline wins over a response: the bridge answers an aborted request
	// with a transport error that would hide the cause.
	if resp := w.response(); resp != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return resp, log.String(), nil
	}
	if cerr := ctx.Err(); cerr != nil {
		if errors.Is(cerr, context.DeadlineExceeded) {
			return nil, log.String(), errors.New("timed out waiting for the server")
		}
		return nil, log.String(), cerr
	}
	if err != nil {
		return nil, log.String(), err
	}
	return nil, log.String(), errors.New("no response from the bridge")
}

// responseWatcher is the bridge's Out: it keeps the response with id 1 and
// signals done once it has it. The bridge writes one compact line per Write.
type responseWatcher struct {
	mu   sync.Mutex
	buf  []byte
	resp json.RawMessage
	done func()
}

func (w *responseWatcher) Write(p []byte) (int, error) {
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
		if json.Unmarshal(raw, &m) == nil && m.Method == "" && string(m.ID) == "1" && w.resp == nil {
			w.resp = append(json.RawMessage(nil), raw...)
			w.done()
		}
	}
	return len(p), nil
}

func (w *responseWatcher) response() json.RawMessage {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.resp
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
