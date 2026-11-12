package recipetest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/procattr"
	"github.com/BeppeTemp/cartographer/internal/surveyor/connector"
	"github.com/BeppeTemp/cartographer/internal/surveyor/recipe"
)

// Check is one automatic check: it runs whatever the scenario asserts.
type Check struct{ Name, Summary string }

// Checks is the list of automatic checks, in the order the report prints them.
// TRAP: docs/plugins.md §Conformance checks lists the same names and a test
// compares the two, so a check added here is documented in the same change.
var Checks = []Check{
	{"spec", "spec is valid, declares spec, check and list, and its env and settings are within what the manifest declares"},
	{"parser", "no verb call (the steps and the automatic calls alike) ended in a protocol_error the parser raises for another reason than the entries and system checks name: every output line passes the production parser and its caps"},
	{"entries", "the production parser accepted every listing's entries: keys are present and unique, a feed entry has a title and a revision, change is added or modified"},
	{"repeatable", "listing twice with the same cursor and fixtures gives the same entries and cursor"},
	{"incremental", "a listing from the returned cursor does not re-emit an entry whose revision did not change"},
	{"system", "a system recipe's check answers read_only, and the parser accepted its list: it ends with an empty cursor and has no deletes"},
	{"fetch", "fetch answers every key it is asked"},
	{"protocol", "an exec script exits 64 with no output on an unknown verb and on another protocol number"},
	{"secrets", "no env test value of 4 bytes or more appears in an entry, a content or an error detail"},
}

// automatic runs the checks of Checks, in order. The parser verdict is read
// last, so it covers every verb call (the steps, the spec, the second listing,
// the system check and the fetch), and is placed where Checks lists it.
func (x *run) automatic(ctx context.Context) {
	stage := ""
	if len(x.lists) > 0 {
		stage = x.lists[0].stage
	}
	x.checkSpec(ctx, stage)
	parserAt := len(x.out)
	x.out = append(x.out, Result{})
	x.checkEntries()
	x.label = "second listing: "
	x.checkRepeatable(ctx)
	x.label = ""
	x.checkIncremental()
	x.checkSystem(ctx, stage)
	x.checkFetch(ctx, stage)
	x.checkProtocol(ctx)
	x.checkSecrets()
	x.checkParser(parserAt)
}

// protoErr is one protocol_error a verb call ended in; class says which check
// owns it (classEntries, classSystem) or is "" for the parser check.
type protoErr struct{ class, text string }

const (
	classEntries = "entries"
	classSystem  = "system"
)

// classOf says which rule of the listing protocol a protocol_error broke. The
// production parser enforces them all and names the line; this reads its
// verdict, it does not judge the entries again.
// TRAP: it matches the parser's messages (internal/surveyor/recipe/exec.go:
// entryOf, systemEntryOf and parseLines for the entries, ListSystem for the
// system cursor); each pattern has a fixture in
// TestEveryCheckFailsOnItsNonConformingRecipe, so a changed message fails it.
func classOf(verb string, err error) string {
	var re *recipe.Error
	if verb != "list" || !errors.As(err, &re) {
		return ""
	}
	for _, m := range []string{"key is required", "title and revision are required", "change is added or modified", "duplicate key"} {
		if strings.Contains(re.Detail, m) {
			return classEntries
		}
	}
	for _, m := range []string{"a system listing has no delete lines", "a system listing ends with an empty next_cursor"} {
		if strings.Contains(re.Detail, m) {
			return classSystem
		}
	}
	return ""
}

// protoOf lists the protocol errors of one class.
func (x *run) protoOf(class string) []string {
	var out []string
	for _, p := range x.proto {
		if p.class == class {
			out = append(out, p.text)
		}
	}
	return out
}

func (x *run) checkParser(at int) {
	n := len(x.out)
	x.verdict("parser", x.protoOf(""))
	x.out[at] = x.out[n]
	x.out = x.out[:n]
}

func (x *run) checkSpec(ctx context.Context, stage string) {
	o := x.call(ctx, stage, "spec", "", nil)
	if o.err != nil {
		x.add("spec", fmt.Errorf("%s: a plugin recipe must answer spec (%v)", code(o.err), o.err), "")
		return
	}
	sp := x.spec
	var probs []string
	for _, v := range []string{recipe.VerbSpec, recipe.VerbCheck, recipe.VerbList} {
		if !sp.Declares(v) {
			probs = append(probs, "spec.verbs does not declare "+v)
		}
	}
	for _, n := range sp.Env {
		if x.in.Permissions == nil || !slices.Contains(x.in.Permissions.Env, n) {
			probs = append(probs, "spec.env names "+n+", which the manifest does not declare under permissions.env")
		}
	}
	for n := range sp.Settings {
		if _, ok := x.in.Declared[n]; !ok {
			probs = append(probs, "spec.settings has "+n+", which the manifest does not declare under settings")
		}
	}
	if err := sp.CheckSettings(x.in.Scenario.Settings); err != nil {
		probs = append(probs, "the scenario's settings: "+err.Error())
	}
	slices.Sort(probs)
	x.verdict("spec", probs)
}

// checkEntries reports the entry rules the production parser enforced.
func (x *run) checkEntries() { x.verdict("entries", x.protoOf(classEntries)) }

// verdict records a pass for no problem, else a failure naming the first few.
func (x *run) verdict(check string, probs []string) {
	if len(probs) == 0 {
		x.add(check, nil, "")
		return
	}
	if len(probs) > 3 {
		probs = append(probs[:3], fmt.Sprintf("and %d more", len(probs)-3))
	}
	x.add(check, errors.New(strings.Join(probs, "; ")), "")
}

func (x *run) checkRepeatable(ctx context.Context) {
	if len(x.lists) == 0 {
		x.add("repeatable", nil, "no list step succeeded")
		return
	}
	first := x.lists[0]
	again := x.call(ctx, first.stage, "list", first.cursor, nil)
	switch {
	case again.err != nil:
		x.add("repeatable", fmt.Errorf("the second listing failed: %v", again.err), "")
	case !sameListing(first.o, again):
		x.add("repeatable", errors.New("two listings from the same cursor and fixtures differ"), "")
	default:
		x.add("repeatable", nil, "")
	}
}

func (x *run) checkIncremental() {
	if x.r.Shape() != recipe.ShapeFeed {
		x.add("incremental", nil, "a system listing is diffed by the Surveyor, not by the recipe")
		return
	}
	var probs []string
	pairs := 0
	for i := 1; i < len(x.lists); i++ {
		if !x.lists[i].fromNext {
			continue
		}
		pairs++
		before := map[string]string{}
		for _, e := range x.lists[i-1].o.entries {
			before[e.Key] = e.Revision
		}
		for _, e := range x.lists[i].o.entries {
			if rev, ok := before[e.Key]; ok && rev == e.Revision {
				probs = append(probs, fmt.Sprintf("list %d re-emits key %q at the revision list %d gave", i+1, e.Key, i))
			}
		}
	}
	if pairs == 0 {
		x.add("incremental", nil, `needs a list step whose request.cursor is "@next" (and a later stage of the recordings)`)
		return
	}
	x.verdict("incremental", probs)
}

func (x *run) checkSystem(ctx context.Context, stage string) {
	if x.r.Shape() != recipe.ShapeSystem {
		x.add("system", nil, "not a system recipe")
		return
	}
	var probs []string
	if o := x.call(ctx, stage, "check", "", nil); o.err != nil {
		probs = append(probs, fmt.Sprintf("check: %v", o.err))
	}
	probs = append(probs, x.protoOf(classSystem)...)
	x.verdict("system", probs)
}

func (x *run) checkFetch(ctx context.Context, stage string) {
	if x.spec == nil || !x.spec.Declares(recipe.VerbFetch) {
		x.add("fetch", nil, "the recipe does not declare fetch")
		return
	}
	var keys []string
	if len(x.lists) == 0 {
		x.add("fetch", nil, "no list step succeeded")
		return
	}
	for _, e := range x.lists[0].o.entries {
		if e.Content == "" && len(keys) < recipe.MaxFetchKeys {
			keys = append(keys, e.Key)
		}
	}
	if len(keys) == 0 {
		x.add("fetch", nil, "every entry of the first list came with its content")
		return
	}
	o := x.call(ctx, stage, "fetch", "", keys)
	x.add("fetch", o.err, "")
}

// checkProtocol probes an exec script with an unknown verb and with another
// protocol number: both must exit 64 and print nothing. A Starlark recipe has
// no process, and the runtime answers a verb it did not register itself.
func (x *run) checkProtocol(ctx context.Context) {
	if x.in.Exec == nil {
		x.add("protocol", nil, "a Starlark recipe has no process to probe")
		return
	}
	var probs []string
	for label, body := range map[string]string{
		"an unknown verb":         fmt.Sprintf(`{"protocol":%d,"verb":"conformance-unknown","config":{}}`, recipe.ProtocolVersion),
		"another protocol number": fmt.Sprintf(`{"protocol":%d,"verb":"list","config":{}}`, recipe.ProtocolVersion+97),
	} {
		if err := probe(ctx, *x.in.Exec, label, body); err != nil {
			probs = append(probs, err.Error())
		}
	}
	slices.Sort(probs)
	x.verdict("protocol", probs)
}

func probe(ctx context.Context, c recipe.ExecConfig, label, body string) error {
	env, err := connector.ChildEnv(c.Env)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "cartographer-conformance-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var out bytes.Buffer
	cmd := procattr.Hide(exec.CommandContext(ctx, c.Command, c.Args...))
	cmd.Dir, cmd.Env, cmd.Stdin, cmd.Stdout = dir, env, strings.NewReader(body), &out
	cmd.WaitDelay = 2 * time.Second
	connector.KillGroupOnCancel(cmd)
	err = cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 64 && out.Len() == 0 {
		return nil
	}
	return fmt.Errorf("on %s the script must exit 64 with no output, not %s with %d bytes on stdout", label, exitOf(err), out.Len())
}

func exitOf(err error) string {
	var ee *exec.ExitError
	switch {
	case err == nil:
		return "exit 0"
	case errors.As(err, &ee):
		return fmt.Sprintf("exit %d", ee.ExitCode())
	}
	return "a failure to run"
}

func (x *run) checkSecrets() {
	var probs []string
	names := make([]string, 0, len(x.in.Scenario.Env))
	for n := range x.in.Scenario.Env {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		v := x.in.Scenario.Env[n]
		if len(v) < 4 || x.in.Scenario.fixturePaths[n] {
			continue
		}
		for _, s := range x.seen {
			if strings.Contains(s[1], v) {
				probs = append(probs, fmt.Sprintf("the value of %s appears in %s", n, s[0]))
				break
			}
		}
	}
	x.verdict("secrets", probs)
}

// sameListing compares two runs of one list for the repeatable check.
func sameListing(a, b outcome) bool {
	return reflect.DeepEqual(a.entries, b.entries) && reflect.DeepEqual(a.deletes, b.deletes) && a.next == b.next
}
