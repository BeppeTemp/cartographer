package star

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"go.starlark.net/starlark"
)

// File is a script file of the tree.
type File struct {
	Path   string
	Lines  int
	Bytes  int
	SHA256 string
}

// Kind of a registration.
const (
	KindCommand = "command"
	KindTool    = "tool"
	KindEvent   = "event"
)

// Registration is one contribution the script made while loading.
type Registration struct {
	Kind string // KindCommand, KindTool or KindEvent
	Name string // command or tool name, or the event
	// Help is a command's help, a tool's description; Usage a command's usage.
	Help, Usage string
	// Params is a tool's JSON schema (an object schema), as JSON.
	Params []byte
	Writes bool
	// Pos is where the registering call stands, "file:line".
	Pos string

	impl *starlark.Function
}

// Line is the registration as the preview and `plugin validate` print it,
// without its position: it is what two versions are compared on.
func (r Registration) Line(plugin string) string {
	switch r.Kind {
	case KindCommand:
		return fmt.Sprintf("command %s %s", plugin, r.Name)
	case KindTool:
		s := fmt.Sprintf("tool %s_%s", plugin, r.Name)
		if r.Writes {
			s += " (writes)"
		}
		return s
	}
	return "event " + r.Name
}

// Program is a loaded plugin script: its files and what it registered, in the
// order the script registered them. Its functions are frozen.
type Program struct {
	Files         []File
	Registrations []Registration

	globals starlark.StringDict
}

// Lines are the registrations in the order Sorted lists them.
func (p *Program) Lines(plugin string) []string {
	regs := p.Sorted()
	out := make([]string, len(regs))
	for i, r := range regs {
		out[i] = r.Line(plugin)
	}
	return out
}

// Sorted returns the registrations by kind, then name: a stable listing.
func (p *Program) Sorted() []Registration {
	regs := append([]Registration(nil), p.Registrations...)
	sort.SliceStable(regs, func(i, j int) bool {
		a, b := regs[i], regs[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Name < b.Name
	})
	return regs
}

func fileInfo(path string, content []byte) File {
	n := bytes.Count(content, []byte{'\n'})
	if len(content) > 0 && content[len(content)-1] != '\n' {
		n++
	}
	sum := sha256.Sum256(content)
	return File{Path: path, Lines: n, Bytes: len(content), SHA256: hex.EncodeToString(sum[:])}
}
