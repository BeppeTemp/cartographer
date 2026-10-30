package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/worker/feed"
	"github.com/BeppeTemp/cartographer/internal/worker/recipe"
	"github.com/BeppeTemp/cartographer/internal/worker/system"
)

// The dry run of a recipe (`cartographer worker source test`): what the poll
// does up to and including the listing, and nothing after it. It goes through
// NewRecipeRegistry, feedPreconditions and listFeed, the very functions the poll
// calls, so a script it passes is a script the poll accepts. It takes no lease,
// builds no model client, writes no cursor, Source, unit or log entry.
//
// A system recipe goes through observeSystem, the poll's own steps 4 to 6: the
// preflight, the state read through Load only (the store's Save is not in the
// interface the dry run receives, so no state asset is written), the listing, its
// validation and the diff. A refused preflight is exit class 3, nothing listed.

// Defaults of the poll task's options when the config has no poll entry.
const (
	sourceTestMinInterval = time.Hour
	sourceTestMaxEntries  = 200
	sourceTestDefInterval = 24 * time.Hour
	// ShowCap bounds the content --show prints.
	ShowCap = 20 << 10
)

// SourceTestInput is one dry run: a registered Source (Source and Tools) or a
// recipe not yet registered (Recipe and Locator).
type SourceTestInput struct {
	Source string
	Tools  ToolCaller // --source mode; wrapped so only read tools reach it

	Recipe, Locator, Kind string
	Include               []string

	Cursor    string
	CursorSet bool
	Show      string
	Clone     recipe.CloneFunc // nil = https; tests inject a fixture
}

// SourceTestEntry is one listed entry.
type SourceTestEntry struct {
	Change, Key, Title, Revision string
	ContentBytes                 int
}

// SourceTestReport is the result of a dry run whose listing could be attempted.
// Failure set means the poll would write a feed_error for this Source or recipe
// (exit class 1).
type SourceTestReport struct {
	Recipe, Shape, Kind, Locator string
	SourceID                     string // "" when not registered
	CursorIn, NextCursor         string
	Identity, StateStatus        string // system recipes: the preflight's label and how the stored state was found
	PreflightRefused             bool   // Failure came from the preflight: exit class 3
	MaxEntries                   int
	PollDefaults                 bool // no poll task in the config: the defaults above were used
	Entries                      []SourceTestEntry
	Counts                       map[string]int
	ContentBytes, Tokens         int
	Duration                     time.Duration
	Warnings                     []string
	Failure                      *SourceTestFailure
	Show                         *SourceTestShow
}

// SourceTestFailure is the feed_error the poll would write.
type SourceTestFailure struct{ Code, Detail string }

// SourceTestShow is the content of the --show entry as it would travel in its unit.
type SourceTestShow struct {
	Key, Content string
	Truncated    bool
	Found        bool
}

// SourceTestError is a failure to start the test (exit class 2): usage, an
// unreadable Source, an unset variable, an unknown recipe name.
type SourceTestError struct{ Msg string }

func (e *SourceTestError) Error() string { return e.Msg }

func startErr(format string, a ...any) error { return &SourceTestError{fmt.Sprintf(format, a...)} }

// dryRunTools are the only MCP tools a dry run may call.
var dryRunTools = map[string]bool{"concept_read": true, "concept_list": true, "asset_read": true, "asset_list": true}

// readOnly offers a ToolCaller only the read tools: a write is not expressible.
type readOnly struct{ t ToolCaller }

func (r readOnly) ListTools() ([]client.ToolInfo, error) { return r.t.ListTools() }
func (r readOnly) CallTool(name string, args any) (client.ToolOutput, error) {
	if !dryRunTools[name] {
		return client.ToolOutput{}, fmt.Errorf("source test: %s is not a read tool", name)
	}
	return r.t.CallTool(name, args)
}

// pollOptions reads the poll task's options, else the defaults.
func pollOptions(cfg *Config) (def, min time.Duration, maxEntries int, defaults bool) {
	for _, t := range cfg.Tasks {
		if t.Kind != pollKindName {
			continue
		}
		def, _ = t.Options["default_interval"].(time.Duration)
		min, _ = t.Options["min_interval"].(time.Duration)
		maxEntries, _ = t.Options["max_entries"].(int)
		return def, min, maxEntries, false
	}
	return sourceTestDefInterval, sourceTestMinInterval, sourceTestMaxEntries, true
}

// SourceTest runs one dry listing. The error is a *SourceTestError (class 2) or
// the context ending; a poll-level failure is in the report (class 1).
func SourceTest(ctx context.Context, cfg *Config, in SourceTestInput) (*SourceTestReport, error) {
	reg, err := NewRecipeRegistry(cfg, in.Clone)
	if err != nil {
		return nil, startErr("%v", err)
	}
	def, min, maxEntries, defaults := pollOptions(cfg)
	rep := &SourceTestReport{MaxEntries: maxEntries, PollDefaults: defaults, Counts: map[string]int{}}
	if defaults {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("the config has no poll task: min_interval %s and max_entries %d are the defaults", min, maxEntries))
	}
	var f feed.Feed
	var perr error
	var tools ToolCaller
	isSystem := false // the Source's shape in --source mode, the recipe's in --recipe mode
	if in.Source != "" {
		if in.Tools == nil {
			return nil, startErr("no server to read the Source from")
		}
		tools = readOnly{in.Tools}
		c, err := readConcept(tools, in.Source)
		if err != nil {
			return nil, startErr("cannot read Source %s: %v", in.Source, err)
		}
		if str(c.Frontmatter, "type") != "Source" {
			return nil, startErr("%s is not a Source", in.Source)
		}
		// A shape the poll does not offer (document) is not a Source it polls.
		shape := str(c.Frontmatter, "source_shape")
		if shape != "feed" && shape != "system" {
			return nil, startErr("%s has source_shape %q: the poll only lists feeds and systems", in.Source, shape)
		}
		isSystem = shape == "system"
		if isSystem && str(c.Frontmatter, "ingest_status") == "skipped" {
			rep.Warnings = append(rep.Warnings, "ingest_status is skipped: the poll never runs this system Source (the operator's off switch)")
		}
		f, perr = feed.Parse(feed.Entry{ID: in.Source, Fields: plainFields(c.Frontmatter), ContentHash: c.ContentHash}, def, min)
		rep.SourceID = in.Source
	} else {
		if _, ok := reg.Get(in.Recipe); !ok {
			return nil, startErr("recipe %q is not in the registry; known: %s", in.Recipe, strings.Join(reg.Names(), ", "))
		}
		if r, _ := reg.Get(in.Recipe); r.Shape() == recipe.ShapeSystem {
			isSystem = true
		}
		f = feed.Feed{ID: "dry-run", Recipe: in.Recipe, Locator: in.Locator, SourceKind: in.Kind, Include: in.Include}
	}
	switch {
	case in.CursorSet && isSystem:
		rep.Warnings = append(rep.Warnings, "a system recipe lists the whole current state: --cursor is ignored")
	case in.CursorSet:
		f.Cursor = in.Cursor
	}
	rep.Recipe, rep.Locator, rep.Kind, rep.CursorIn = f.Recipe, f.Locator, f.SourceKind, f.Cursor

	rec, pf := feedPreconditions(reg, builtinHosts(cfg), f, perr, isSystem)
	if pf != nil {
		rep.Failure = (*SourceTestFailure)(pf)
		return rep, nil
	}
	rep.Shape = rec.Shape()
	if err := execEnvCheck(cfg, f.Recipe); err != nil {
		return nil, err
	}
	if tools != nil {
		if w := planWarning(tools, f.ID); w != "" {
			rep.Warnings = append(rep.Warnings, w)
		}
	}

	if isSystem {
		return rep, rep.observeSystem(ctx, rec, f, tools, in.Show)
	}
	start := time.Now()
	listing, pf, lerr := listFeed(ctx, rec, f, maxEntries)
	rep.Duration = time.Since(start)
	if lerr != nil {
		return nil, lerr
	}
	if pf != nil {
		rep.Failure = (*SourceTestFailure)(pf)
		return rep, nil
	}
	rep.NextCursor = listing.NextCursor
	for _, e := range listing.Entries {
		rep.Entries = append(rep.Entries, SourceTestEntry{e.Change, e.Key, e.Title, e.Revision, len(e.Content)})
		rep.Counts[e.Change]++
		rep.ContentBytes += len(e.Content)
		if in.Show != "" && e.Key == in.Show {
			s := &SourceTestShow{Key: e.Key, Content: e.Content, Found: true}
			if len(s.Content) > ShowCap {
				s.Content, s.Truncated = truncateBytes(s.Content, ShowCap), true
			}
			rep.Show = s
		}
	}
	rep.Tokens = (rep.ContentBytes + 3) / 4
	if rep.Counts[recipe.Removed] > 0 {
		rep.Warnings = append(rep.Warnings, "removed entries are listed; the poll only counts them")
	}
	if in.Show != "" && rep.Show == nil {
		rep.Show = &SourceTestShow{Key: in.Show}
	}
	return rep, nil
}

// execEnvCheck requires only the variables the chosen exec recipe lists: they are
// read when the script runs, and an unset one is named here, never its value.
func execEnvCheck(cfg *Config, name string) error {
	for _, x := range cfg.ExecRecipes {
		if x.Name != name {
			continue
		}
		if err := x.CheckEnv(); err != nil {
			return startErr("recipe %s: %v", name, err)
		}
	}
	return nil
}

// planWarning is the poll's step 3 lookup, read-only: a warning, never a failure.
func planWarning(tools ToolCaller, id string) string {
	plans, err := conceptList(tools, map[string]any{"where": []string{"type=IngestPlan", "sources=" + id}, "fields": []string{"status"}})
	if err != nil {
		return "cannot look up the IngestPlan: " + oneLineClip(err.Error(), 120)
	}
	switch len(plans.Results) {
	case 0:
		return codeNoPlan + ": no IngestPlan lists this feed in sources; the poll will not run it"
	case 1:
		return ""
	}
	return fmt.Sprintf("%s: %d IngestPlans list this feed in sources", codeAmbiguousPlan, len(plans.Results))
}

// IsSourceTestError reports a class 2 error.
func IsSourceTestError(err error) bool {
	var e *SourceTestError
	return errors.As(err, &e)
}

// noState is the stored state of a recipe not yet registered: there is none.
type noState struct{}

func (noState) Load(string) (system.Loaded, error) {
	return system.Loaded{State: system.State{}, Trusted: true}, nil
}

// observeSystem fills the report from the poll's own steps 4 to 6.
func (rep *SourceTestReport) observeSystem(ctx context.Context, rec recipe.Recipe, f feed.Feed, tools ToolCaller, show string) error {
	sr, ok := rec.(recipe.SystemRecipe)
	if !ok {
		rep.Failure = &SourceTestFailure{codeRecipeShape, fmt.Sprintf("recipe %q does not list a system", f.Recipe)}
		return nil
	}
	var st stateLoader = noState{}
	if tools != nil {
		st = &system.Store{Call: func(tool string, args map[string]any) (string, error) {
			text, err := callOK(tools, tool, args)
			var te *toolError
			if errors.As(err, &te) {
				return "", &system.Refusal{Text: te.text}
			}
			return text, err
		}, Source: f.ID}
	}
	start := time.Now()
	obs, pf, preflight, err := observeSystem(ctx, sr, systemRequest(f), st, f.Cursor)
	rep.Duration = time.Since(start)
	if err != nil {
		return err
	}
	if pf != nil {
		rep.Failure, rep.PreflightRefused = (*SourceTestFailure)(pf), preflight
		return nil
	}
	rep.Identity = obs.Identity
	l := obs.Loaded
	switch {
	case l.Trusted && l.Digest == "":
		rep.StateStatus = "no stored state"
	case l.Trusted:
		rep.StateStatus = fmt.Sprintf("trusted, %d groups", len(l.State))
	default:
		rep.StateStatus = "untrusted (" + l.Reason + "): the diff is against an empty state, as the poll's baseline rebuild would be"
	}
	now := time.Now()
	var handled []system.Handled
	for _, c := range obs.Changes.Ordered() {
		title, rev := c.Group.Title, c.Group.Revision
		if c.Kind == recipe.Removed {
			title, rev = c.Previous.Title, c.Previous.Revision
		}
		rep.Entries = append(rep.Entries, SourceTestEntry{c.Kind, c.Key, title, rev, len(c.Group.Content)})
		rep.Counts[c.Kind]++
		rep.ContentBytes += len(c.Group.Content)
		handled = append(handled, system.Handled{Key: c.Key, Removed: c.Kind == recipe.Removed, Group: c.Group, At: now.UTC().Format(time.RFC3339)})
		if show != "" && c.Key == show {
			body := systemBody("", c, rev, now.UTC().Format("2006-01-02"), nil)
			sh := &SourceTestShow{Key: c.Key, Content: body, Found: true}
			if len(body) > ShowCap {
				sh.Content, sh.Truncated = truncateBytes(body, ShowCap), true
			}
			rep.Show = sh
		}
	}
	rep.Counts["unchanged"] = obs.Changes.Unchanged
	// The cursor the poll would write if it handled every group: the state's digest.
	if _, digest, err := l.State.Apply(handled).Encode(); err == nil {
		rep.NextCursor = digest
	}
	rep.Tokens = (rep.ContentBytes + 3) / 4
	if show != "" && rep.Show == nil {
		rep.Show = &SourceTestShow{Key: show}
	}
	return nil
}
