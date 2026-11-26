package configurator

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

// CreatedLedgerName is the small record, in a client base dir, of the config
// files Cartographer itself created there (#1269). A Claude Code config or
// settings file is deleted once it is `{}` only when it is listed in it: a user's
// own `{}` file is reduced and kept. The ledger is deleted when its last entry
// is, so a connect/disconnect round trip on an empty HOME leaves no trace of it.
const CreatedLedgerName = ".cartographer-created.json"

type createdLedger struct {
	// Files maps a created file (slash path relative to baseDir) to the names
	// of the Cartographer-owned entries it holds: MCP server names, hook names.
	Files map[string][]string `json:"files"`
	// Dirs lists the provisioning root directories Cartographer created (slash
	// paths relative to baseDir, sorted, unique), only members of rootDirs. A
	// directory that existed before, or that another process made first, is
	// never listed; one created by an older version is unknown, so never removed.
	Dirs []string `json:"dirs,omitempty"`
}

func readCreated(baseDir string) createdLedger {
	var l createdLedger
	data, err := os.ReadFile(filepath.Join(baseDir, CreatedLedgerName))
	if err == nil {
		_ = json.Unmarshal(data, &l)
	}
	if l.Files == nil {
		l.Files = map[string][]string{}
	}
	// Untrusted input: drop what is not a valid root, keep sorted and unique.
	set := map[string]bool{}
	for _, d := range l.Dirs {
		if validLedgerDir(d) {
			set[d] = true
		}
	}
	l.Dirs = nil
	for d := range set {
		l.Dirs = append(l.Dirs, d)
	}
	sort.Strings(l.Dirs)
	return l
}

func writeCreated(baseDir string, l createdLedger) error {
	path := filepath.Join(baseDir, CreatedLedgerName)
	if len(l.Files) == 0 && len(l.Dirs) == 0 {
		if err := safewrite.Remove(baseDir, path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	for k := range l.Files {
		sort.Strings(l.Files[k])
	}
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	return safewrite.WriteFile(baseDir, path, append(data, '\n'), 0o644)
}

// WasCreated reports whether rel (relative to baseDir) is a file Cartographer
// created, per the ledger.
func WasCreated(baseDir, rel string) bool {
	_, ok := readCreated(baseDir).Files[filepath.ToSlash(rel)]
	return ok
}

// NoteCreatedDirs records the root directories among rels (slash or OS paths
// relative to baseDir) as created by Cartographer; the others are ignored.
func NoteCreatedDirs(baseDir string, rels []string) error {
	l := readCreated(baseDir)
	have := map[string]bool{}
	for _, d := range l.Dirs {
		have[d] = true
	}
	changed := false
	for _, r := range rels {
		r = filepath.ToSlash(r)
		if validLedgerDir(r) && !have[r] {
			have[r] = true
			l.Dirs = append(l.Dirs, r)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return writeCreated(baseDir, l)
}

// WasDirCreated reports whether rel is a root directory Cartographer created,
// per the ledger.
func WasDirCreated(baseDir, rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, d := range readCreated(baseDir).Dirs {
		if d == rel {
			return true
		}
	}
	return false
}

// ForgetCreatedDir drops rel from the ledger's directories.
func ForgetCreatedDir(baseDir, rel string) error {
	rel = filepath.ToSlash(rel)
	l := readCreated(baseDir)
	kept := l.Dirs[:0]
	for _, d := range l.Dirs {
		if d != rel {
			kept = append(kept, d)
		}
	}
	if len(kept) == len(l.Dirs) {
		return nil
	}
	l.Dirs = kept
	return writeCreated(baseDir, l)
}

// ReconcileCreatedDirs forgets the entries whose path is no longer a directory
// by Lstat (missing, replaced by a file or by a link): the one we created is
// gone, so whatever is there now is the user's.
func ReconcileCreatedDirs(baseDir string) error {
	l := readCreated(baseDir)
	kept := l.Dirs[:0]
	for _, d := range l.Dirs {
		if fi, err := os.Lstat(filepath.Join(baseDir, filepath.FromSlash(d))); err == nil && fi.IsDir() {
			kept = append(kept, d)
		}
	}
	if len(kept) == len(l.Dirs) {
		return nil
	}
	l.Dirs = kept
	return writeCreated(baseDir, l)
}

// ReconcileCreated is called before a register writes rel, which already
// exists: when the ledger lists it but none of the entries it names is in the
// file any more, the file we created is gone and this one is somebody else's
// (deleted and recreated by the user), so the provenance ends. present says
// whether an owner is in the file as it stands.
func ReconcileCreated(baseDir, rel string, present func(owner string) bool) error {
	rel = filepath.ToSlash(rel)
	l := readCreated(baseDir)
	owners, ok := l.Files[rel]
	if !ok {
		return nil
	}
	for _, o := range owners {
		if present(o) {
			return nil
		}
	}
	delete(l.Files, rel)
	return writeCreated(baseDir, l)
}

// NoteOwner records, after a register wrote rel, that owner's entry is in it.
// created says the register created the file; for a file that existed, the
// owner is added only if the ledger already lists it (we created it earlier).
func NoteOwner(baseDir, rel, owner string, created bool) error {
	rel = filepath.ToSlash(rel)
	l := readCreated(baseDir)
	owners, ok := l.Files[rel]
	if !ok && !created {
		return nil
	}
	for _, o := range owners {
		if o == owner {
			return nil
		}
	}
	l.Files[rel] = append(owners, owner)
	return writeCreated(baseDir, l)
}

// DropOwner records that owner's entry left rel, which is kept. The provenance
// ends with the last owner: a file holding nothing of ours is no longer one we
// are responsible for deleting.
func DropOwner(baseDir, rel, owner string) error {
	rel = filepath.ToSlash(rel)
	l := readCreated(baseDir)
	owners, ok := l.Files[rel]
	if !ok {
		return nil
	}
	kept := owners[:0]
	for _, o := range owners {
		if o != owner {
			kept = append(kept, o)
		}
	}
	if len(kept) == 0 {
		delete(l.Files, rel)
	} else {
		l.Files[rel] = kept
	}
	return writeCreated(baseDir, l)
}

// ForgetCreated drops rel from the ledger (the file was deleted).
func ForgetCreated(baseDir, rel string) error {
	rel = filepath.ToSlash(rel)
	l := readCreated(baseDir)
	if _, ok := l.Files[rel]; !ok {
		return nil
	}
	delete(l.Files, rel)
	return writeCreated(baseDir, l)
}

// ServerNamesIn returns the MCP server names a provider config's JSON holds
// under any of the server-map keys: the owners a register looks for.
func ServerNamesIn(root map[string]any, provider Provider) map[string]bool {
	out := map[string]bool{}
	for _, name := range MCPServerNames(root, provider) {
		out[name] = true
	}
	return out
}

// MayDeleteEmpty reports whether the config file rel of provider, left empty,
// may be deleted: DeletableWhenEmpty, and for a provider whose file is shared
// state (DeleteOnlyIfCreated) only when Cartographer created it.
func MayDeleteEmpty(d Descriptor, baseDir, rel string) bool {
	if !d.DeletableWhenEmpty {
		return false
	}
	return !d.DeleteOnlyIfCreated || WasCreated(baseDir, rel)
}
