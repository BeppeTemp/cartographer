package plugin

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
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
}

// ignored reports a top-level name that is never stored.
func ignored(first string) bool {
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
	var scripts []TreeFile
	for _, f := range files {
		first, _, nested := strings.Cut(f.Path, "/")
		if !provisioning.ValidArtifactFilePath(f.Path) || strings.IndexFunc(f.Path, unprintable) >= 0 {
			add("%q: not a canonical relative path of printable characters", f.Path)
			continue
		}
		if seen[strings.ToLower(f.Path)] {
			add("%q: duplicate path", f.Path)
			continue
		}
		seen[strings.ToLower(f.Path)] = true
		switch {
		case ignored(first):
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
		case nested && pointOf(first) != nil:
			byPoint[first] = append(byPoint[first], f)
		default:
			add("unknown entry `%s`: a plugin holds %s", first, held)
			continue
		}
		stored = append(stored, f)
		total += len(f.Content)
	}
	problems = append(problems, scriptTreeProblems(scripts)...)
	if len(stored) > MaxFiles {
		add("%d files, over the %d cap", len(stored), MaxFiles)
	}
	if total > MaxBytes {
		add("%d bytes in all, over the %d cap", total, MaxBytes)
	}

	if manifest == nil {
		add("%s: missing", ManifestFile)
	} else {
		m, probs := ParseManifest(manifest, opts)
		p.Manifest = m
		problems = append(problems, probs...)
	}

	p.Contributions = map[string][]Contribution{}
	for _, pt := range RegisteredPoints() {
		cs, probs := pt.Load(byPoint[pt.Dir])
		problems = append(problems, probs...)
		if len(cs) > 0 {
			p.Contributions[pt.Dir] = cs
		}
		for _, c := range cs {
			if c.Provider != nil {
				continue // registered by the host, not materialized as a file set
			}
			p.Artifacts = append(p.Artifacts, contract.Artifact{
				Kind: c.Kind, Name: c.Name, Source: "plugin:" + p.Manifest.Name,
				ContentHash: provisioning.ContentHashFiles(c.Files), Files: c.Files,
			})
		}
	}
	// The scripts run only on a tree with no other problem: a tree over a cap
	// or with a bad manifest never reaches the interpreter.
	if len(scripts) > 0 && len(problems) == 0 && opts.CheckScripts != nil {
		problems = append(problems, opts.CheckScripts(scripts)...)
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

// unprintable reports a control or format rune (bidi override, zero-width): a
// path that carries one would mislead the install preview.
func unprintable(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }

func pointOf(dir string) *Point {
	if pt, ok := points[dir]; ok && pt.Side == SideClient {
		return &pt
	}
	return nil
}

// digest hashes the sorted (path, executable, sha256 of content) of every file.
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
