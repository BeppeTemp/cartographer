package starcore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"sort"
	"sync"

	"go.starlark.net/starlark"

	"github.com/BeppeTemp/cartographer/internal/plugin/starcore/internal/seam"
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
	// Pos is where the Registering call stands, "file:line".
	Pos string

	// Impl is the function the script registered.
	Impl *starlark.Function
}

// Options say what the host adds to the language: the registrars only
// plugin.star has (the kinds of a registration) and the capability modules
// beyond the runtime's own (settings, log, http, env).
type Options struct {
	Registrars starlark.StringDict
	Extra      starlark.StringDict
}

// check refuses an option that would replace a builtin of the runtime: the
// host adds modules and registrars, it does not override the pure modules,
// settings, log, http or env, whose checks are the runtime's contract.
func (o Options) check() error {
	reserved := Merge(pureModules(), capabilityModules(&settingsValue{}))
	for _, d := range []starlark.StringDict{o.Registrars, o.Extra} {
		for name := range d {
			if _, ok := reserved[name]; ok {
				return fmt.Errorf("the host may not replace the builtin %q", name)
			}
		}
	}
	return nil
}

// Script is a loaded plugin script: its files and what it registered, in the
// order the script registered them. Its functions are frozen.
type Script struct {
	Files         []File
	Registrations []Registration

	globals starlark.StringDict
	mu      sync.RWMutex
	srcs    map[string][]byte // the sources, to run again for a call
	opts    Options
}

// Sorted returns the registrations by kind, then name: a stable listing.
func (p *Script) Sorted() []Registration {
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

// sources is the map of sources a call runs again; it is never written, only
// swapped whole (ReplaceSource), so a call keeps the one it read.
func (s *Script) sources() map[string][]byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.srcs
}

// ReplaceSource swaps a source the next call runs again: a test seam, gated by
// the token only startest holds.
func (s *Script) ReplaceSource(_ seam.Gate, path string, src []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := maps.Clone(s.srcs)
	next[path] = src
	s.srcs = next
}
