package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/worker"
)

// Exit codes of `cartographer worker source test` (D1373): the listing ran and
// passed (0); the poll would write a feed_error for this Source or recipe (1);
// the test could not start (2); a system recipe's preflight refused (3): nothing
// was listed, fix the identity and not the script.
const (
	sourceTestFail    = 1
	sourceTestStart   = 2
	sourceTestRefused = 3
)

const sourceTestRows = 50

// cmdWorkerSource is `cartographer worker source <subcommand>`; test is the only one.
func cmdWorkerSource(args []string) int {
	if len(args) == 0 {
		printWorkerSourceUsage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "test":
		return cmdWorkerSourceTest(args[1:])
	case "help", "-h", "--help":
		printWorkerSourceUsage(os.Stdout)
		return 0
	}
	fmt.Fprintf(os.Stderr, "Error: unknown worker source subcommand %q\n\n", args[0])
	printWorkerSourceUsage(os.Stderr)
	return 2
}

func printWorkerSourceUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: cartographer worker source test --config <file> (--source <id> [--kb <kb>] | --recipe <name> --locator <loc> [--include <glob>]...)")
	fmt.Fprintln(w, "                                        [--kind <source_kind>] [--cursor <string>] [--show <key>]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  test   Run the listing the worker will run, with the real recipe, and print it. Writes nothing:")
	fmt.Fprintln(w, "         no lease, cursor, Source, unit or log entry; no model is called.")
	fmt.Fprintln(w, "Exit: 0 listed, 1 the poll would record a feed_error, 2 the test could not start, 3 a system recipe's preflight refused.")
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func cmdWorkerSourceTest(args []string) int {
	fs := flag.NewFlagSet("worker source test", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var include multiFlag
	configF := fs.String("config", "", "Path to the worker YAML config (required)")
	sourceF := fs.String("source", "", "ID of a registered feed Source")
	kbF := fs.String("kb", "", "KB of the Source (required with --source when the config names several)")
	recipeF := fs.String("recipe", "", "Recipe name, for a Source not yet registered")
	locatorF := fs.String("locator", "", "Locator, with --recipe")
	kindF := fs.String("kind", "", "source_kind the recipe receives, with --recipe")
	cursorF := fs.String("cursor", "", "Cursor to list from (default: empty for --recipe, the stored feed_cursor for --source)")
	showF := fs.String("show", "", "Print the content of this entry key as it would travel in its unit")
	fs.Var(&include, "include", "Glob for the recipe, with --recipe (repeatable)")
	if err := fs.Parse(args); err != nil {
		return sourceTestStart
	}
	usage := func(format string, a ...any) int {
		fmt.Fprintf(os.Stderr, "Error: "+format+"\n", a...)
		return sourceTestStart
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	switch {
	case fs.NArg() > 0:
		return usage("unexpected argument %q", fs.Arg(0))
	case *configF == "":
		return usage("--config is required")
	case (*sourceF == "") == (*recipeF == ""):
		return usage("give exactly one of --source and --recipe")
	case *sourceF != "" && (len(include) > 0 || set["kind"] || set["locator"]):
		return usage("--include, --kind and --locator come from the Source with --source")
	case *recipeF != "" && (set["kb"]):
		return usage("--kb applies to --source only")
	case *recipeF != "" && *locatorF == "":
		return usage("--locator is required with --recipe")
	}

	cfg, err := worker.LoadConfigScoped(*configF, worker.DefaultKinds(), worker.EnvScope{Token: *sourceF != ""})
	if err != nil {
		return usage("%v", err)
	}
	in := worker.SourceTestInput{Source: *sourceF, Recipe: *recipeF, Locator: *locatorF, Kind: *kindF, Include: include,
		Cursor: *cursorF, CursorSet: set["cursor"], Show: *showF}
	if *sourceF != "" {
		kb := *kbF
		switch {
		case kb == "" && len(cfg.KBs) == 1:
			kb = cfg.KBs[0]
		case kb == "":
			return usage("the config names several KBs (%s): give --kb", strings.Join(cfg.KBs, ", "))
		case !slices.Contains(cfg.KBs, kb):
			return usage("KB %q is not in the config's kbs (%s)", kb, strings.Join(cfg.KBs, ", "))
		}
		c := client.New(cfg.ServerURL, cfg.Token()).WithInsecureHTTP(cfg.AllowInsecureHTTP).WithTokenEnv(cfg.TokenEnv).WithKB(kb)
		if err := c.Ping(startupCallTimeout); err != nil {
			return usage("the server at %s did not answer or refused the token: %v", cfg.ServerURL, err)
		}
		in.Tools = c
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	rep, err := worker.SourceTest(ctx, cfg, in)
	if err != nil {
		if worker.IsSourceTestError(err) {
			return usage("%v", err)
		}
		fmt.Fprintf(os.Stderr, "interrupted: %v\n", err)
		return sourceTestFail
	}
	return printSourceTest(os.Stdout, os.Stderr, *configF, rep, in.Show)
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// printSourceTest writes the report: stdout is the stable contract the skill reads
// (plain text, tab-separated), stderr the warnings and the failure. A command's
// arguments, a secret and environment values are never printed.
func printSourceTest(out, errw io.Writer, config string, r *worker.SourceTestReport, show string) int {
	for _, w := range r.Warnings {
		fmt.Fprintf(errw, "warning: %s\n", w)
	}
	src := r.SourceID
	if src == "" {
		src = "(not registered)"
	}
	fmt.Fprintf(out, "recipe:\t%s\nshape:\t%s\nkind:\t%s\nsource:\t%s\nlocator:\t%s\ncursor_in:\t%s\nconfig:\t%s\n",
		r.Recipe, r.Shape, r.Kind, src, r.Locator, r.CursorIn, filepath.Base(config))
	if r.Failure != nil {
		fmt.Fprintf(errw, "the poll would record: feed_error %s: %s\n", r.Failure.Code, r.Failure.Detail)
		if r.PreflightRefused {
			return sourceTestRefused
		}
		return sourceTestFail
	}
	if r.Shape == "system" {
		fmt.Fprintf(out, "preflight:\tread_only, identity %s\nstate:\t%s\n", clip(r.Identity, 80), r.StateStatus)
	}
	for i, e := range r.Entries {
		if i == sourceTestRows {
			fmt.Fprintf(out, "(+%d more)\n", len(r.Entries)-i)
			break
		}
		fmt.Fprintf(out, "%s\t%s\t%s\t%d\t%s\n", e.Change, clip(e.Key, 80), clip(e.Revision, 80), e.ContentBytes, clip(e.Title, 80))
	}
	fmt.Fprintf(out, "counts:\tadded %d, modified %d, removed %d", r.Counts["added"], r.Counts["modified"], r.Counts["removed"])
	if r.Shape == "system" {
		fmt.Fprintf(out, ", unchanged %d", r.Counts["unchanged"])
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "next_cursor:\t%s\n", r.NextCursor)
	fmt.Fprintf(out, "summary:\t%d entries, %d content bytes, ~%d tokens, %s\n", len(r.Entries), r.ContentBytes, r.Tokens, r.Duration.Round(time.Millisecond))
	if r.Show != nil {
		if !r.Show.Found {
			fmt.Fprintf(errw, "Error: --show key %q is not in the listing\n", show)
			return sourceTestStart
		}
		fmt.Fprintf(out, "--- content of %s ---\n%s\n", clip(r.Show.Key, 80), r.Show.Content)
		if r.Show.Truncated {
			fmt.Fprintf(out, "[truncated at %d bytes]\n", worker.ShowCap)
		}
	}
	return 0
}
