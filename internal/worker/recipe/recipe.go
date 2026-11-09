// Package recipe lists what changed in a feed Source.
//
// A recipe is Go code shipped in the worker (the operator can replace its
// instructions text and nothing else), or an executable the operator declares in
// the worker config (exec.go, protocol v1). A Source can only name a recipe that
// the registry holds: nothing a KB contains selects code, a command or a path.
package recipe

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strings"
)

// The Source shapes a recipe serves: feed lists what changed in a document set
// (the recipe holds the revisions), system lists the current state of a live
// system grouped (the worker hashes and diffs it, D1372).
const (
	ShapeFeed   = "feed"
	ShapeSystem = "system"
)

// Change values of an Entry.
const (
	Added    = "added"
	Modified = "modified"
	Removed  = "removed"
)

// Request is one listing: the feed Source and where the last one stopped.
type Request struct {
	ID, Title, Locator, SourceKind string
	Include                        []string // feed_include; the recipe interprets it
	Cursor                         string   // empty = first poll
	MaxEntries                     int      // 0 = DefaultMaxEntries
	MaxContentBytes                int
}

// Entry is one thing that changed. Key, Title and Revision are untrusted text.
type Entry struct {
	Key, Title, Revision, Change string
	Locator                      string // what the unit executor fetches, when not the Source's own
	Content                      string // inline content, for recipes that have it
}

// Listing is the entries and the cursor that covers exactly what was emitted.
type Listing struct {
	Entries    []Entry
	NextCursor string
}

// DefaultMaxEntries bounds a listing when the request names none.
const DefaultMaxEntries = 200

// Recipe lists one kind of feed.
type Recipe interface {
	Name() string
	Shape() string
	// Instructions is the text the poll's model call receives: what a unit for
	// this kind of source looks like and how to name locators.
	Instructions() string
	List(ctx context.Context, req Request) (Listing, error)
}

// Error is a listing failure with the code that goes to the Source's
// feed_error ("<code>: <detail>"). Detail never holds a credential: the
// built-in recipes use none.
type Error struct{ Code, Detail string }

func (e *Error) Error() string { return e.Code + ": " + e.Detail }

// Codes of Error.
const (
	CodeHostNotAllowed = "host_not_allowed"
	CodeBadLocator     = "bad_locator"
	CodeCursorInvalid  = "cursor_invalid"
	CodeListFailed     = "list_failed"
	// CodeIdentityNotReadOnly is the feed_error code of a failed preflight: a
	// contract, the Atlas shows it (D1372).
	CodeIdentityNotReadOnly = "identity_not_read_only"
)

func fail(code, format string, a ...any) error {
	return &Error{Code: code, Detail: fmt.Sprintf(format, a...)}
}

//go:embed recipes/*.md
var texts embed.FS

// Options configure the registry from the worker config.
type Options struct {
	GitHosts     []string          // git.allow_hosts
	Instructions map[string]string // recipes.<name>.instructions_file, already read
	Clone        CloneFunc         // nil = gitx over https; tests inject a fixture
	Exec         []ExecConfig      // recipes.<name>.exec, already validated (D1262)
	FetchHosts   []string          // fetch.allow_hosts
	InsecureHTTP bool              // fetch.allow_insecure_http
	Get          GetFunc           // http-pages' fetch; nil = every request fails
}

// Registry holds the built-ins, always available under their own names.
type Registry struct{ byName map[string]Recipe }

// Builtins are the names of the recipes shipped in the binary.
func Builtins() []string { return []string{"git-docs", "http-pages"} }

// NewRegistry builds the built-ins; it fails when one has no embedded text.
func NewRegistry(o Options) (*Registry, error) {
	r := &Registry{byName: map[string]Recipe{}}
	for _, name := range Builtins() {
		text, ok := o.Instructions[name]
		if !ok {
			b, err := texts.ReadFile("recipes/" + name + ".md")
			if err != nil || strings.TrimSpace(string(b)) == "" {
				return nil, fmt.Errorf("recipe %s: no embedded instructions", name)
			}
			text = string(b)
		}
		switch name {
		case "git-docs":
			r.byName[name] = newGitDocs(text, o)
		case "http-pages":
			get := o.Get
			if get == nil {
				get = func(context.Context, string) (Page, error) { return Page{}, fmt.Errorf("no fetch is configured") }
			}
			r.byName[name] = &httpPages{text: text, hosts: o.FetchHosts, insecure: o.InsecureHTTP, get: get}
		}
	}
	for _, c := range o.Exec {
		if _, dup := r.byName[c.Name]; dup {
			return nil, fmt.Errorf("recipe %s: the name is taken", c.Name)
		}
		r.byName[c.Name] = NewExec(c)
	}
	return r, nil
}

// Get returns the recipe a Source names.
func (r *Registry) Get(name string) (Recipe, bool) { x, ok := r.byName[name]; return x, ok }

// Names lists the registry in order.
func (r *Registry) Names() []string {
	n := make([]string, 0, len(r.byName))
	for k := range r.byName {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}
