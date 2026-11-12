package surveyor

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/surveyor/recipe"
	"github.com/BeppeTemp/cartographer/internal/surveyorconfig"
)

// CheckLog keeps, in memory, the last `check` outcome of each recipe by name.
// The system poll records into it where it already calls Check (a feed poll
// sends list only, and `source test` is a process with no beater: neither
// records); the beater reads it to report the sources (D1259). A nil log
// records nothing and reports every check as never, so a Config built without
// one stays usable.
//
// TRAP: Record runs on the runner's goroutine and Snapshot on the beater's:
// both hold the mutex for a map access only, never across a call, so the beater
// cannot block the runner.
type CheckLog struct {
	mu  sync.Mutex
	m   map[string]checkRecord
	gen uint64
}

type checkRecord struct {
	outcome, code string
	at            time.Time
}

// NewCheckLog returns an empty log.
func NewCheckLog() *CheckLog { return &CheckLog{m: map[string]checkRecord{}} }

// Record keeps the outcome of a check of the named recipe: err nil is ok, a
// recipe that does not implement the verb is unsupported, any other error is a
// failure with its code (check_failed when the error carries none). Only the
// code is kept, never the detail, which may quote the recipe's output.
func (l *CheckLog) Record(name string, err error, at time.Time) {
	if l == nil {
		return
	}
	rec := checkRecord{outcome: surveyorconfig.CheckOK, at: at}
	var re *recipe.Error
	switch {
	case err == nil:
	case errors.Is(err, recipe.ErrUnsupported):
		rec.outcome = surveyorconfig.CheckUnsupported
	case errors.As(err, &re):
		rec.outcome, rec.code = surveyorconfig.CheckFailed, re.Code
	default:
		rec.outcome, rec.code = surveyorconfig.CheckFailed, recipe.CodeCheckFailed
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]checkRecord{}
	}
	l.m[name] = rec
	l.gen++
}

// Generation grows with every recorded check: the beater compares it with the
// one of its last report, because the age of a check is no change but a new check is.
func (l *CheckLog) Generation() uint64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.gen
}

// last returns the check recorded for a name, as the wire shape.
func (l *CheckLog) last(name string, now time.Time) surveyorconfig.SourceCheck {
	if l == nil {
		return surveyorconfig.SourceCheck{Outcome: surveyorconfig.CheckNever}
	}
	l.mu.Lock()
	rec, ok := l.m[name]
	l.mu.Unlock()
	if !ok {
		return surveyorconfig.SourceCheck{Outcome: surveyorconfig.CheckNever}
	}
	c := surveyorconfig.SourceCheck{Outcome: rec.outcome, Code: rec.code}
	if rec.outcome == surveyorconfig.CheckOK || rec.outcome == surveyorconfig.CheckFailed {
		c.AgeSeconds = max(0, int(now.Sub(rec.at)/time.Second))
	}
	return c
}

// Sources lists the recipes and connectors of the loaded config for the settings
// report: names, origins, kinds and the last check, nothing else (no command,
// arguments, URL, environment name or header ever reaches the report). A name
// with a "/" is a plugin's, and the plugin's pinned digest comes from its pin.
// The array is sorted by name (a recipe before a connector of the same name) and cut at surveyorconfig.MaxSources, so a report
// is stable between two beats that changed nothing.
//
// TRAP: a connector reports unsupported (it has no check verb) and a built-in
// recipe too (it implements none); a recipe of the config reports what the log
// holds, never when it has not run.
func (c *Config) Sources(now time.Time) []surveyorconfig.Source {
	out := []surveyorconfig.Source{}
	for _, name := range recipe.Builtins() {
		out = append(out, surveyorconfig.Source{Name: name, Origin: surveyorconfig.OriginCore, Kind: surveyorconfig.KindBuiltin, Shape: recipe.ShapeFeed,
			Check: surveyorconfig.SourceCheck{Outcome: surveyorconfig.CheckUnsupported}})
	}
	add := func(name, kind, shape string, check surveyorconfig.SourceCheck) {
		s := surveyorconfig.Source{Name: name, Origin: surveyorconfig.OriginOperator, Kind: kind, Shape: shape, Check: check}
		if plugin, _, ok := recipe.Split(name); ok {
			s.Origin, s.Plugin, s.Digest = surveyorconfig.OriginPlugin, plugin, c.PluginPins[plugin].Digest
		}
		out = append(out, s)
	}
	shape := func(x recipe.ExecConfig) string {
		if x.Shape == recipe.ShapeSystem {
			return recipe.ShapeSystem
		}
		return recipe.ShapeFeed
	}
	for _, x := range c.ExecRecipes {
		add(x.Name, surveyorconfig.KindExec, shape(x), c.Checks.last(x.Name, now))
	}
	for _, x := range c.StarRecipes {
		add(x.Name, surveyorconfig.KindStarlark, shape(x.ExecConfig), c.Checks.last(x.Name, now))
	}
	for _, x := range c.Connectors {
		add(x.Name, surveyorconfig.KindMCP, "", surveyorconfig.SourceCheck{Outcome: surveyorconfig.CheckUnsupported})
	}
	// A recipe and a connector may share a name (a plugin's recipes/wiki and
	// connectors/wiki.yaml, or an operator's): both are reported, the recipe first.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Kind != surveyorconfig.KindMCP && out[j].Kind == surveyorconfig.KindMCP
	})
	if len(out) > surveyorconfig.MaxSources {
		out = out[:surveyorconfig.MaxSources]
	}
	return out
}

// recordSystem keeps the check outcome of a system poll, from what
// observeSystem returned: refused says the failure came from the check itself, so
// a listing or state failure after a clean check still records ok; an error is
// the context ending, which says nothing about the recipe.
func (l *CheckLog) recordSystem(name string, pf *pollFailure, refused bool, err error, at time.Time) {
	switch {
	case err != nil:
	case pf != nil && refused:
		l.Record(name, &recipe.Error{Code: pf.Code, Detail: pf.Detail}, at)
	default:
		l.Record(name, nil, at)
	}
}
