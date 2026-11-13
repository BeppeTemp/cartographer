package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	iofs "io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/BeppeTemp/cartographer/internal/docskb"
)

const docsUsage = `usage: cartographer docs <tool> [key=value ...] [--json '<object>'|-]
       cartographer docs --list
       cartographer docs --describe <tool>[,<tool>...]
       cartographer docs --batch -  [--output text|json]
       cartographer docs export <dir>

Reads the documentation of this version of Cartographer, built into the
binary, with the read tools of an MCP server (search, graph_context,
concept_read, ...) run in this process: no server, provider, network or client
configuration. Arguments and exit codes are those of "cartographer call".

Start here:
  cartographer docs graph_context query="<question>"
  cartographer docs concept_read id=<id>

export writes the documentation as a KB tree into <dir> (missing or empty).

flags: --output text|json  --timeout <duration>  --verbose
exit:  0 ok, 1 the tool returned an error, 2 usage or transport
       (--batch: the worst class among the lines)`

// cmdDocs is `cartographer docs`: the real read-min tools over the embedded
// documentation (D1184's profile), in this process.
//
// Unlike `call` it writes: one private temp directory per invocation (the KB
// opens on a directory and the tools keep state beside it), removed on exit.
// It never touches the network, the client config, a user KB or $HOME. See
// TestDocsWritesOnlyItsTempDir.
func cmdDocs(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stdout, docsUsage)
		return 0
	}
	if args[0] == "export" {
		return cmdDocsExport(args[1:])
	}
	fs := flag.NewFlagSet("docs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	list := fs.Bool("list", false, "list the tools")
	describe := fs.String("describe", "", "print the description and input schema of a tool, or of several (a,b)")
	batch := fs.String("batch", "", "run JSONL requests read from stdin (the value -) through one runner")
	output := fs.String("output", "text", "text or json")
	timeout := fs.Duration("timeout", 5*time.Minute, "bound on the whole invocation")
	jsonArg := fs.String("json", "", "arguments as a JSON object, or - to read it from stdin")
	verbose := fs.Bool("verbose", false, "print the runner log even on success")
	fs.Usage = func() { fmt.Fprintln(os.Stderr, docsUsage) }

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
	for _, on := range []bool{*list, *describe != "", *batch != "", len(pos) > 0} {
		if on {
			modes++
		}
	}
	if modes != 1 {
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
	var callArgs map[string]any
	var rawPairs map[string]string
	if len(pos) > 0 {
		var err error
		if callArgs, rawPairs, err = parseCallArgs(pos[1:], jsonSet, *jsonArg, mcpStdin); err != nil {
			return fail("%v", err)
		}
	} else if jsonSet {
		return fail("--json goes with a tool call")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	// One runner per invocation, started at the first request: a batch with
	// no request at all never materializes the tree.
	var sess *docsSession
	var startErr error
	session := func() callTransport {
		if sess == nil && startErr == nil {
			r, err := newDocsRunner(version)
			if err != nil {
				startErr = err
				return failedSession{err}
			}
			sess = &docsSession{r: r}
		}
		if startErr != nil {
			return failedSession{startErr}
		}
		return sess
	}
	send := func(method string, params map[string]any) (json.RawMessage, string, error) {
		resp, err := session().Do(ctx, method, params)
		return resp, "", err
	}
	code := func() int {
		if *batch != "" {
			c, _ := runBatch(ctx, session, mcpStdin, os.Stdout, os.Stderr, *output)
			sess = nil // runBatch closed it
			return c
		}
		if len(pos) == 0 {
			tools, _, err := listTools(send)
			if err != nil {
				return fail("%v", err)
			}
			if *list {
				for _, t := range tools {
					fmt.Fprintf(os.Stdout, "%s\t%s\n", escapeControl(t.Name), escapeControl(firstSentence(t.Description)))
				}
				return 0
			}
			return describeTools(tools, *describe, "cartographer docs --list", fail)
		}
		if len(rawPairs) > 0 {
			if tools, _, lerr := listTools(send); lerr == nil {
				for _, t := range tools {
					if t.Name == pos[0] {
						coerceCallArgs(callArgs, rawPairs, t.InputSchema)
					}
				}
			}
		}
		resp, _, err := send("tools/call", map[string]any{"name": pos[0], "arguments": callArgs})
		if err != nil {
			return fail("%v", err)
		}
		result, err := responseResult(resp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			if errors.Is(err, errInvalidParams) {
				return 1
			}
			return 2
		}
		return printToolResult(pos[0], result, *output)
	}()
	if sess != nil {
		if log := sess.Close(); log != "" && (code != 0 || *verbose) {
			fmt.Fprint(os.Stderr, log)
		}
	}
	return code
}

// cmdDocsExport is `cartographer docs export <dir>`.
func cmdDocsExport(args []string) int {
	if len(args) != 1 || args[0] == "" || args[0][0] == '-' {
		fmt.Fprintln(os.Stderr, "usage: cartographer docs export <dir>")
		return 2
	}
	if err := docskb.Export(args[0], version); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 2
	}
	n := 0
	_ = filepath.WalkDir(args[0], func(_ string, d iofs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	fmt.Fprintf(os.Stdout, "wrote %d files to %s\n", n, args[0])
	return 0
}

// docsSession adapts the in-process runner to callTransport.
type docsSession struct{ r docsRunner }

// docsRunner is what a session needs of a runner; newDocsRunner is the seam
// the tests replace with a shared one, so that only one test pays for the
// materialization on every invocation.
type docsRunner interface {
	Raw(ctx context.Context, method string, params map[string]any) (json.RawMessage, error)
	Close() error
}

var newDocsRunner = func(version string) (docsRunner, error) { return docskb.NewRunner(version) }

func (s *docsSession) Do(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	return s.r.Raw(ctx, method, params)
}

func (s *docsSession) Close() string {
	if err := s.r.Close(); err != nil {
		return fmt.Sprintf("removing the temporary documentation tree: %v\n", err)
	}
	return ""
}

func (s *docsSession) stopped() (bool, error) { return false, nil }

// failedSession answers every request with the error that kept the runner
// from starting, so a batch reports it per line.
type failedSession struct{ err error }

func (f failedSession) Do(context.Context, string, map[string]any) (json.RawMessage, error) {
	return nil, f.err
}
func (f failedSession) Close() string          { return "" }
func (f failedSession) stopped() (bool, error) { return true, f.err }
