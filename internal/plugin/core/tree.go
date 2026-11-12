package core

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

const (
	// MaxFiles and MaxBytes bound the stored files of one plugin.
	MaxFiles = 2000
	MaxBytes = 8 << 20
)

// TreeFile is a file of a plugin tree in memory: what an acquisition produces
// and the store reads back.
type TreeFile struct {
	Path       string // canonical slash form, relative to the plugin root
	Content    []byte
	Executable bool
	// Special marks a symlink or any non-regular file: always an error.
	Special bool
}

// Plugin is a validated plugin tree.
type Plugin struct {
	Manifest      Manifest
	Contributions map[string][]Contribution // by point Dir
	Artifacts     []contract.Artifact
	Files         []TreeFile // every stored file, manifest included, sorted by path
	Digest        string     // identifies the validated tree
	// Sides are the sides the manifest declares, in canonical order (a schema 1
	// plugin is client only); Unsupported says why a side cannot load in this
	// build; Unknown lists what a schema 2 manifest or tree holds that this
	// build knows no family or side for (`entry x`, `side y`): stored and
	// digested, never loaded.
	Sides       []Side
	Unsupported map[Side]string
	Unknown     []string
}

// Ignored reports a top-level name that is never stored.
func Ignored(first string) bool {
	u := strings.ToUpper(first)
	return strings.HasPrefix(u, "README") || strings.HasPrefix(u, "LICENSE") ||
		strings.HasPrefix(u, "CHANGELOG") || u == "DOCS" || strings.HasPrefix(first, ".git")
}

// ReadTree validates a plugin tree and returns every problem it finds, sorted.
// The result does not depend on the order of files.
func ReadTree(files []TreeFile, opts Options) (Plugin, []string) {
	var p Plugin
	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	names := []string{ManifestFile, "plugin.star", "lib/"}
	for _, pt := range RegisteredPoints() {
		if pt.Side == SideClient {
			names = append(names, pt.Dir+"/")
		}
	}
	held := strings.Join(names, ", ") + " (README*, LICENSE*, CHANGELOG*, docs/ and .git* are ignored)"

	byPoint := map[string][]TreeFile{}
	var stored []TreeFile
	var manifest []byte
	seen := map[string]bool{}
	var total int
	var scripts, unknownFiles []TreeFile
	for _, f := range files {
		first, _, nested := strings.Cut(f.Path, "/")
		if !contract.ValidArtifactFilePath(f.Path) || strings.IndexFunc(f.Path, unprintable) >= 0 {
			add("%q: not a canonical relative path of printable characters", f.Path)
			continue
		}
		if seen[strings.ToLower(f.Path)] {
			add("%q: duplicate path", f.Path)
			continue
		}
		seen[strings.ToLower(f.Path)] = true
		switch {
		case Ignored(first):
			continue
		case f.Special:
			add("%q: a symlink or non-regular file", f.Path)
			continue
		case len(f.Content) > contract.ArtifactMaxFileSize:
			add("%q: %d bytes, over the %d cap of a file", f.Path, len(f.Content), contract.ArtifactMaxFileSize)
			continue
		case !nested && f.Path == ManifestFile:
			manifest = f.Content
		case IsScriptPath(f.Path):
			if msg := scriptProblem(f.Path, len(f.Content)); msg != "" {
				add("%q: %s", f.Path, msg)
				continue
			}
			scripts = append(scripts, f)
		case first == "mcp" || (!nested && f.Path == "instructions.md"):
			add("%q: not a plugin contribution point in this version", f.Path)
			continue
		case nested && isPointDir(first):
			byPoint[first] = append(byPoint[first], f)
		default:
			// Whether it is fatal, ignored or reported depends on the manifest's
			// schema, read below.
			unknownFiles = append(unknownFiles, f)
			continue
		}
		stored = append(stored, f)
		total += len(f.Content)
	}
	problems = append(problems, scriptTreeProblems(scripts)...)

	if manifest == nil {
		add("%s: missing", ManifestFile)
	} else {
		m, probs := ParseManifest(manifest, opts)
		p.Manifest = m
		problems = append(problems, probs...)
	}
	// Under schema 1 an unknown top-level entry is the fatal error 0.28 gave.
	// Under schema 2 it is stored (and digested) and reported; a family this
	// build registers is claimed, not unknown, but its side must declare it.
	schema2 := p.Manifest.Schema != nil && *p.Manifest.Schema == SchemaVersion
	if !schema2 {
		// A schema 1 plugin is client only: a folder of another side's point is
		// the unknown entry 0.28 reported, not a contribution.
		for dir, fs := range byPoint {
			if fam, _ := FamilyOf(dir); fam.Side != SideClient {
				unknownFiles = append(unknownFiles, fs...)
				for _, f := range fs {
					stored = slices.DeleteFunc(stored, func(s TreeFile) bool { return s.Path == f.Path })
					total -= len(f.Content)
				}
				delete(byPoint, dir)
			}
		}
	}
	unknown := map[string]bool{}
	for _, f := range unknownFiles {
		first, _, nested := strings.Cut(f.Path, "/")
		if !schema2 {
			add("unknown entry `%s`: a plugin holds %s", first, held)
			continue
		}
		if nested && first == "tests" {
			continue // fixtures of the plugin's own tests: never stored, never digested
		}
		if fam, ok := FamilyOf(first); !ok {
			unknown["entry "+first] = true
		} else if !strings.Contains(f.Path, "/") && slices.Contains(fam.Dirs, first) {
			// A flat file named like a family folder is neither unknown nor loadable.
			add("%q: a file where the %s/ folder of sdk family %s is expected", f.Path, first, fam.Name)
			continue
		}
		stored = append(stored, f)
		total += len(f.Content)
	}
	if len(stored) > MaxFiles {
		add("%d files, over the %d cap", len(stored), MaxFiles)
	}
	if total > MaxBytes {
		add("%d bytes in all, over the %d cap", total, MaxBytes)
	}
	if schema2 {
		for _, s := range p.Manifest.UnknownSides {
			unknown["side "+s] = true
		}
		p.Unknown = slices.Sorted(maps.Keys(unknown))
		p.Unsupported = map[Side]string{}
		for _, side := range Sides() {
			if c, ok := p.Manifest.Components[side]; ok {
				p.Sides = append(p.Sides, side)
				if why := unsupportedReason(side, c); why != "" {
					p.Unsupported[side] = why
				}
			}
		}
		if len(problems) == 0 {
			problems = append(problems, undeclaredFamilies(stored, p.Manifest)...)
		}
	} else if p.Manifest.Components != nil {
		p.Sides = []Side{SideClient}
	}

	p.Contributions = map[string][]Contribution{}
	for _, pt := range RegisteredPoints() {
		if !opts.loads(pt.Side) || p.Unsupported[pt.Side] != "" {
			continue
		}
		cs, probs := pt.Load(byPoint[pt.Dir])
		problems = append(problems, probs...)
		if pt.Check != nil && p.Manifest.Name != "" {
			for _, c := range cs {
				problems = append(problems, pt.Check(p.Manifest.Name, c)...)
			}
		}
		if len(cs) > 0 {
			p.Contributions[pt.Dir] = cs
		}
		for _, c := range cs {
			if c.Provider != nil || pt.Side != SideClient {
				continue // registered by the host, or data of another component: not an artifact
			}
			p.Artifacts = append(p.Artifacts, contract.Artifact{
				Kind: c.Kind, Name: c.Name, Source: "plugin:" + p.Manifest.Name,
				ContentHash: contract.HashArtifactFiles(c.Files), Files: c.Files,
			})
		}
	}
	// The scripts run only on a tree with no other problem: a tree over a cap
	// or with a bad manifest never reaches the interpreter.
	if len(scripts) > 0 && len(problems) == 0 && opts.CheckScripts != nil && opts.loads(SideClient) && p.Unsupported[SideClient] == "" {
		problems = append(problems, opts.CheckScripts(SideClient, scripts)...)
	}
	sort.Slice(p.Artifacts, func(i, j int) bool {
		a, b := p.Artifacts[i], p.Artifacts[j]
		return a.Kind+"\x00"+a.Name < b.Kind+"\x00"+b.Name
	})
	sort.Slice(stored, func(i, j int) bool { return stored[i].Path < stored[j].Path })
	p.Files = stored
	p.Digest = digest(stored)

	sort.Strings(problems)
	return p, problems
}

// undeclaredFamilies reports a top-level folder or file of a registered family
// whose side does not declare that family under sdk: a schema 2 plugin states
// what it was written for, so nothing is loaded by its folder name alone.
func undeclaredFamilies(files []TreeFile, m Manifest) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		first, nested, _ := strings.Cut(f.Path, "/")
		fam, ok := FamilyOf(first)
		if !ok || seen[first] {
			continue
		}
		seen[first] = true
		if _, declared := m.Components[fam.Side].SDK[fam.Name]; !declared {
			name := first
			if nested != "" {
				name += "/"
			}
			out = append(out, fmt.Sprintf("%s: declare sdk.%s under components.%s", name, fam.Name, fam.Side))
		}
	}
	return out
}

// unprintable reports a control or format rune (bidi override, zero-width): a
// path that carries one would mislead the install preview.
func unprintable(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }

// isPointDir reports whether dir is a folder of a registered family. It
// classifies by family, not by registered point, so a tree reads the same
// (digest, problems, sides, unknown entries) in a binary that links no loaders.
func isPointDir(dir string) bool {
	fam, ok := FamilyOf(dir)
	return ok && slices.Contains(fam.Dirs, dir)
}

// digest hashes the sorted (path, executable, sha256 of content) of every file.
// Changing this invalidates every approval ever made, and every digest pinned
// by a component config: TestSchema1Compat pins it against the v0.28.0 values.
func digest(files []TreeFile) string {
	h := sha256.New()
	h.Write([]byte("cartographer:plugin-tree:v1\x00"))
	for _, f := range files {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(f.Path)))
		h.Write(n[:])
		h.Write([]byte(f.Path))
		if f.Executable {
			h.Write([]byte{1})
		} else {
			h.Write([]byte{0})
		}
		c := sha256.Sum256(f.Content)
		h.Write(c[:])
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}
