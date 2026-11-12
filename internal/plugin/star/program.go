package star

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"sort"

	"go.starlark.net/starlark"
)

// File is a script file of the tree.
type File struct {
	Path   string `json:"path"`
	Lines  int    `json:"lines"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Registration is one contribution the script made while loading.
type Registration struct {
	Kind string // the registrar that made it: its host names them
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

// Options say what the host adds to the language: the registrars only
// plugin.star has (the kinds of a registration) and the capability modules
// beyond the runtime's own (settings, log, http, env).
type Options struct {
	Registrars starlark.StringDict
	Extra      starlark.StringDict
}

// script is a loaded plugin script: its files and what it registered, in the
// order the script registered them. Its functions are frozen.
type script struct {
	Files         []File
	Registrations []Registration

	globals starlark.StringDict
	srcs    map[string][]byte // the sources, to run again for a call
	opts    Options
}

// Sorted returns the registrations by kind, then name: a stable listing.
func (p *script) Sorted() []Registration {
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
