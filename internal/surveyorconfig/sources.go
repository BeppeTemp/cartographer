package surveyorconfig

import (
	"regexp"
	"strings"
	"time"
)

// Sources are the recipes and connectors a Surveyor serves, reported next to
// its scalar entries so the Atlas can list them. An element is a name, where it
// came from, what kind it is and the outcome of its last check: never a
// command, an argument, a URL, an environment name or a header (D1259: no value
// is sent, and here no setting of a source is either).

// The origins of a source. Core is the binary's own (git-docs, http-pages).
const (
	OriginOperator = "operator"
	OriginPlugin   = "plugin"
	OriginCore     = "core"
)

// The kinds of a source.
const (
	KindExec     = "exec"
	KindStarlark = "starlark"
	KindMCP      = "mcp"
	KindBuiltin  = "builtin"
)

// The outcomes of a check. Unsupported is a recipe or connector with no check
// verb; Never is one that has not been checked since this process started.
const (
	CheckOK          = "ok"
	CheckFailed      = "failed"
	CheckUnsupported = "unsupported"
	CheckNever       = "never"
)

// MaxSources caps the array on both sides, so a report stays inside the body cap
// of the settings route next to its entries.
const MaxSources = 200

// Source is one recipe or connector.
type Source struct {
	// Name is the one a Source names: qualified (<plugin>/<name>) for a plugin's.
	Name   string `json:"name"`
	Origin string `json:"origin"`
	// Plugin and Digest name the pinned plugin of an origin-plugin source.
	Plugin string `json:"plugin,omitempty"`
	Digest string `json:"digest,omitempty"`
	Kind   string `json:"kind"`
	// Shape is feed or system, for a recipe; a connector has none.
	Shape string      `json:"shape,omitempty"`
	Check SourceCheck `json:"check"`
}

// SourceCheck is the last check outcome. The Surveyor says how long ago it ran
// (AgeSeconds), never when: a time it declared would be read against the clock
// of the server, which turns it into At on receipt (ages use the server clock).
type SourceCheck struct {
	Outcome string `json:"outcome"`
	// Code is the failure code of a failed check (check_failed,
	// identity_not_read_only...), nothing else.
	Code       string `json:"code,omitempty"`
	AgeSeconds int    `json:"age_seconds,omitempty"`
	// At is set by the server on receipt and ignored on the wire.
	At time.Time `json:"at,omitzero"`
}

var (
	qualifiedNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}/[a-z0-9-]{1,40}$`)
	checkCodeRe     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
)

// maxCheckAge is ten years, far more than a process lives.
const maxCheckAge = 10 * 365 * 24 * 3600

// ScrubSources makes a sources array fit to keep and show, whatever the sender
// did, as Scrub does for the entries. An element is dropped when it is malformed
// (an unknown origin, kind, shape or outcome, a name that is not an operator or
// a qualified name, a plugin that does not match its name, a digest that is not
// 64 hex digits, a code on a check that did not fail), when its name repeats
// among the recipes or among the connectors (a recipe and a connector of the
// same name are two sources) or when it is past MaxSources. Nothing the sender wrote outside the fields is
// kept, and At is the receipt time minus the age.
func ScrubSources(in []Source, receivedAt time.Time) []Source {
	out := make([]Source, 0, min(len(in), MaxSources))
	type key struct {
		name      string
		connector bool
	}
	seen := map[key]bool{}
	for _, s := range in {
		if len(out) >= MaxSources {
			break
		}
		k := key{s.Name, s.Kind == KindMCP}
		if seen[k] || !sourceShape(s) {
			continue
		}
		seen[k] = true
		c := SourceCheck{Outcome: s.Check.Outcome, Code: s.Check.Code}
		if c.Outcome == CheckOK || c.Outcome == CheckFailed {
			c.At = receivedAt.Add(-time.Duration(s.Check.AgeSeconds) * time.Second).UTC()
		}
		out = append(out, Source{Name: s.Name, Origin: s.Origin, Plugin: s.Plugin, Digest: s.Digest, Kind: s.Kind, Shape: s.Shape, Check: c})
	}
	return out
}

// sourceShape reports whether every field of s is one the table allows.
func sourceShape(s Source) bool {
	switch s.Origin {
	case OriginOperator:
		if !nameRe.MatchString(s.Name) || s.Plugin != "" || s.Digest != "" || (s.Kind != KindExec && s.Kind != KindMCP) {
			return false
		}
	case OriginCore:
		if !nameRe.MatchString(s.Name) || s.Plugin != "" || s.Digest != "" || s.Kind != KindBuiltin {
			return false
		}
	case OriginPlugin:
		if !qualifiedNameRe.MatchString(s.Name) || !strings.HasPrefix(s.Name, s.Plugin+"/") || !digestRe.MatchString(s.Digest) ||
			(s.Kind != KindExec && s.Kind != KindStarlark && s.Kind != KindMCP) {
			return false
		}
	default:
		return false
	}
	switch s.Shape {
	case "", "feed", "system":
	default:
		return false
	}
	if s.Kind == KindMCP && s.Shape != "" {
		return false // a connector has no shape
	}
	c := s.Check
	if c.AgeSeconds < 0 || c.AgeSeconds > maxCheckAge {
		return false
	}
	switch c.Outcome {
	case CheckFailed:
		return checkCodeRe.MatchString(c.Code)
	case CheckOK, CheckUnsupported, CheckNever:
		return c.Code == ""
	}
	return false
}
