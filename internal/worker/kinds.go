package worker

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/settingschema"
)

// KindOption is one option a task kind accepts in its tasks[] entry. Key.Path is
// "tasks[].<name>"; Key.Type is one of the types optionTypes decodes and
// Key.Default, a YAML literal, is applied when the option is absent.
type KindOption struct {
	Key      settingschema.Key
	Required bool
}

// name is the last segment of the option's path.
func (o KindOption) name() string { return strings.TrimPrefix(o.Key.Path, "tasks[].") }

// Kind is a task kind: the declaration half (its name, its options and a hook
// for the rules that span several options) and the execution half (Build).
type Kind struct {
	Name    string
	Options []KindOption
	// Validate receives the decoded options of one tasks[] entry (defaults
	// applied) and returns an error naming the option at fault; the loader adds
	// the key path and the line.
	Validate func(opts map[string]any) error
	// Load runs after Validate with what only the loader knows (the config
	// directory, the perimeter, the entry's profile). It may read files and add
	// derived values to opts, under keys no declared option uses; Build then
	// finds them there.
	Load func(env LoadEnv, opts map[string]any) error
	// Last means the kind may only be the last entry of tasks: it lives on what
	// the entries before it left (synthesis), and an entry listed first would
	// shadow them. The loader refuses any other position.
	Last bool
	// Build is the execution half: it makes one Instance from the decoded
	// options of one tasks[] entry (so two entries of a repeatable kind are two
	// independent instances). A kind without Build can be loaded and described
	// but not run; NewRunner refuses it.
	Build func(opts map[string]any) (Instance, error)
}

// LoadEnv is what Kind.Load receives for one tasks[] entry.
type LoadEnv struct {
	Dir       string // directory of the config file; "" when the config came from memory
	Perimeter string
	KBs       []string // the perimeter's kbs
	Profile   Profile
	Given     map[string]bool // options the entry sets explicitly
	Warn      func(format string, args ...any)
}

// repeatable reports whether the kind may appear more than once in tasks (D23):
// only when it declares a name option to tell its entries apart.
func (k Kind) repeatable() bool {
	_, ok := k.option("name")
	return ok
}

func (k Kind) option(name string) (KindOption, bool) {
	for _, o := range k.Options {
		if o.name() == name {
			return o, true
		}
	}
	return KindOption{}, false
}

// Kinds is the registry of task kinds a worker binary knows.
type Kinds struct {
	list []Kind
}

// optionTypes are the types a kind option can have, as the loader decodes them:
// string, int, bool, duration (a Go duration string) and list (of strings).
var optionTypes = []string{"string", "int", "bool", "duration", "list"}

// NewKinds builds a registry. Declaring a kind wrongly is a programming error and
// panics: a reserved option key (kind and model belong to the entry), an option
// outside "tasks[]." or of an unknown type, a default that does not decode, an
// option declared twice by one kind, or a kind registered twice.
func NewKinds(kinds ...Kind) *Kinds {
	r := &Kinds{}
	for _, k := range kinds {
		if k.Name == "" {
			panic("worker: a task kind needs a name")
		}
		if _, dup := r.Lookup(k.Name); dup {
			panic(fmt.Sprintf("worker: task kind %q registered twice", k.Name))
		}
		seen := map[string]bool{}
		for _, o := range k.Options {
			n, ok := strings.CutPrefix(o.Key.Path, "tasks[].")
			switch {
			case !ok || n == "" || strings.ContainsAny(n, ".[]"):
				panic(fmt.Sprintf("worker: kind %q: option path %q must be tasks[].<name>", k.Name, o.Key.Path))
			case n == "kind" || n == "model":
				panic(fmt.Sprintf("worker: kind %q declares the reserved option %q", k.Name, n))
			case seen[n]:
				panic(fmt.Sprintf("worker: kind %q declares option %q twice", k.Name, n))
			case !slices.Contains(optionTypes, o.Key.Type):
				panic(fmt.Sprintf("worker: kind %q option %q: type %q is not one of %v", k.Name, n, o.Key.Type, optionTypes))
			}
			seen[n] = true
			if !o.Required {
				if _, err := defaultOf(o); err != nil {
					panic(fmt.Sprintf("worker: kind %q option %q: default %q: %v", k.Name, n, o.Key.Default, err))
				}
			}
		}
		r.list = append(r.list, k)
	}
	return r
}

// DefaultKinds is the production registry: each plan that adds a task kind
// registers it here, one line apiece.
func DefaultKinds() *Kinds {
	return NewKinds(IngestKind(), DoctorKind(), PollKind(), promptKind(), SynthesisKind())
}

// Lookup returns the kind with the given name.
func (r *Kinds) Lookup(name string) (Kind, bool) {
	if r == nil {
		return Kind{}, false
	}
	for _, k := range r.list {
		if k.Name == name {
			return k, true
		}
	}
	return Kind{}, false
}

// Names lists the registered kinds in registration order.
func (r *Kinds) Names() []string {
	var out []string
	if r != nil {
		for _, k := range r.list {
			out = append(out, k.Name)
		}
	}
	return out
}

// CheckSchema verifies the registry against the key table: every option of
// every kind must be a row of schema, and an option shared by two kinds must be
// declared identically (the table holds one row per path). The start-up path
// and a test over DefaultKinds both run it, so a kind plan that forgets its
// rows, or drifts from a sibling's declaration, fails there.
func (r *Kinds) CheckSchema(schema settingschema.Schema) error {
	if r == nil {
		return nil
	}
	first := map[string]settingschema.Key{}
	owner := map[string]string{}
	for _, k := range r.list {
		for _, o := range k.Options {
			if prev, ok := first[o.Key.Path]; ok && prev != o.Key {
				return fmt.Errorf("worker: kinds %q and %q declare %s differently; share one declaration", owner[o.Key.Path], k.Name, o.Key.Path)
			}
			first[o.Key.Path], owner[o.Key.Path] = o.Key, k.Name
			row, ok := schema.Lookup(o.Key.Path)
			if !ok {
				return fmt.Errorf("worker: kind %q option %s has no row in the worker key table (internal/workerconfig/schema.go)", k.Name, o.Key.Path)
			}
			if row != o.Key {
				return fmt.Errorf("worker: kind %q declares %s differently from the key table", k.Name, o.Key.Path)
			}
		}
	}
	return nil
}

// defaultOf decodes the default of an optional option; a required one has none.
func defaultOf(o KindOption) (any, error) {
	if o.Required {
		return nil, nil
	}
	if o.Key.Default == "" {
		return nil, fmt.Errorf("an optional option needs a default")
	}
	return decodeLiteral(o.Key.Type, o.Key.Default)
}

// durationOf parses a duration option; kept next to the types it decodes.
func durationOf(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration such as 30m or 24h", s)
	}
	return d, nil
}

// WorkItem is one unit of work a kind found on a KB. Key identifies it among the
// units of that kind on that KB, so the runner never offers the same one twice
// in a run; Data is the kind's own payload, opaque to the runner.
type WorkItem struct {
	Key  string
	Data any
}

// Runtime is what the runner hands to Instance.Run: the profile of the entry
// with its Model, the grant (a ceiling in tokens the loop must not exceed, to be
// passed as Limits.MaxTotalTokens), the identity of this replica and the MCP
// caller scoped to the KB.
type Runtime struct {
	KB        string
	Perimeter string // the config's name
	Replica   string // random per process, never persisted: a restarted replica is a new claimant
	Profile   Profile
	Model     Model
	Grant     int
	// DailyLeft is what the profile may still spend today, the run's own
	// spending already taken off: unlike Grant it is not capped by run_tokens,
	// so a task that keeps a reserve of the daily budget compares with this.
	DailyLeft int
	Tools     ToolCaller
}

// Result is what a finished (or failed) task reports. Tokens are charged to the
// task's profile even when Run also returns an error.
type Result struct {
	Tokens  int
	Units   int
	Commits int
	Summary string // one line

	CacheReadTokens int // the part of Tokens read from the provider's cache
}

// Binder is implemented by an Instance that needs the loaded config and the
// model profile of its tasks[] entry; the runner calls it once, after Build.
type Binder interface {
	Bind(cfg *Config, profile Profile)
}

// Instance is a kind built for one tasks[] entry. The runner owns the registry,
// the order and the loop; the kind owns what a task is.
type Instance interface {
	// Next finds one unit of work on the KB; (nil, nil) means nothing to do.
	Next(ctx context.Context, kb string, tools ToolCaller) (*WorkItem, error)
	// Run drives the agent loop (NewAgent) for the item with rt.Model and a
	// token ceiling of rt.Grant.
	Run(ctx context.Context, item *WorkItem, rt Runtime) (Result, error)
}

// MaxWritesKey is tasks[].max_writes of the doctor kind, declared once here and
// as a row of the key table.
var MaxWritesKey = settingschema.Key{Path: "tasks[].max_writes", Scope: settingschema.ScopeWorker, Type: "int", Values: "at least 1", Default: "20",
	Description: "The most writes one doctor session may apply to a KB; past it the session is told to close."}
