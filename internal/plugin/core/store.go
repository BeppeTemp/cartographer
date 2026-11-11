package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/execbit"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
	"github.com/BeppeTemp/cartographer/internal/safewrite"
	"github.com/BeppeTemp/cartographer/internal/updatecheck"
)

// The store is a re-fetchable cache under the user's cache directory:
//
//	plugins/<name>/<digest>/   exactly the files ReadTree validated, manifest included
//	plugins/.tmp-<name>-<n>/   a write in progress
//
// A digest directory is immutable: its name is the digest of what it holds, and
// Open recomputes it. Put and GC are writers: on the user cache they run under
// the client lock (provisioning.LockClientState), held by the caller; a vendored
// store (plugin vendor) is written by one process, the temp-dir-and-rename write
// being race safe. Open takes no lock, so a read racing a write sees either
// state or ErrNotFetched. A ReadOnly store, which headless components open,
// refuses both writers.
var (
	// ErrNotFetched: no tree is stored under that name and digest.
	ErrNotFetched = errors.New("plugin: not fetched")
	// ErrModified: the stored tree no longer has the digest it is named by.
	ErrModified = errors.New("plugin: stored tree was modified")
	// ErrInvalid: the stored tree has its digest but fails ReadTree.
	ErrInvalid = errors.New("plugin: stored tree is invalid")
	// ErrReadOnly: Put or GC on a read-only store.
	ErrReadOnly = errors.New("plugin: the store is read-only")
)

var digestRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

const tmpPrefix = ".tmp-"

// tmpMaxAge is how old an interrupted write must be before a store operation sweeps it.
const tmpMaxAge = time.Hour

// Store is the plugin cache rooted at Root.
type Store struct {
	Root     string
	ReadOnly bool
}

// DefaultStore is <cache dir>/plugins, the cache dir being the update check's.
func DefaultStore() (Store, error) {
	d, err := updatecheck.CacheDir()
	if err != nil {
		return Store{}, err
	}
	return Store{Root: filepath.Join(d, "plugins")}, nil
}

// checkName is applied at every path join: a name that is not a valid plugin
// name (`../x`, an absolute path) never reaches filepath.Join.
func checkName(name string) error {
	if !nameRe.MatchString(name) || !contract.ValidArtifactName(name) {
		return fmt.Errorf("plugin: %q is not a valid plugin name", name)
	}
	return nil
}

func (s Store) dir(name, digest string) (string, error) {
	if err := checkName(name); err != nil {
		return "", err
	}
	if !digestRe.MatchString(digest) {
		return "", fmt.Errorf("plugin: %q is not a tree digest", digest)
	}
	return filepath.Join(s.Root, name, digest), nil
}

// Put stores files under name after validating them: ReadTree must report no
// problem and the digest must be want. The tree is written to a temp directory,
// read back through ReadTree, checked against want again (the bytes validated
// are the bytes stored) and renamed into place, never over a directory that
// exists. An existing digest directory that verifies is kept; one that does not
// is a corrupt cache and is replaced. The caller holds the client lock.
func (s Store) Put(name string, files []TreeFile, want string, opts Options) error {
	if s.ReadOnly {
		return ErrReadOnly
	}
	final, err := s.dir(name, want)
	if err != nil {
		return err
	}
	p, problems := ReadTree(files, opts)
	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalid, strings.Join(problems, "; "))
	}
	if p.Manifest.Name != name {
		return fmt.Errorf("plugin: the manifest names %q, not %q", p.Manifest.Name, name)
	}
	if p.Digest != want {
		return fmt.Errorf("plugin: the tree has digest %s, not %s", p.Digest, want)
	}
	if !execbit.Supported {
		for _, f := range p.Files {
			if f.Executable {
				return fmt.Errorf("plugin: %s is executable, and this host cannot record the executable bit", f.Path)
			}
		}
	}
	if err := safewrite.MkdirAllNoFollow(filepath.Dir(s.Root), s.Root, 0o700); err != nil {
		return err
	}
	s.sweep()
	if _, err := s.Open(name, want, opts); err == nil {
		return nil
	} else if !errors.Is(err, ErrNotFetched) {
		if err := removeTree(s.Root, final); err != nil {
			return err
		}
	}

	tmp, err := os.MkdirTemp(s.Root, tmpPrefix+name+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for _, f := range p.Files {
		dst := filepath.Join(tmp, filepath.FromSlash(f.Path))
		if err := safewrite.MkdirAllNoFollow(tmp, filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		if err := safewrite.WriteFile(tmp, dst, f.Content, 0o644); err != nil {
			return err
		}
		if f.Executable {
			if err := os.Chmod(dst, 0o755); err != nil {
				return err
			}
		}
	}
	if err := checkDir(tmp, want, opts); err != nil {
		return err
	}
	if err := safewrite.MkdirAllNoFollow(s.Root, filepath.Join(s.Root, name), 0o700); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		// Another writer won the race, or the directory exists: keep it if it verifies.
		if _, openErr := s.Open(name, want, opts); openErr == nil {
			return nil
		}
		return err
	}
	return nil
}

// Open reads the stored tree back through ReadTree and compares its digest.
func (s Store) Open(name, digest string, opts Options) (Plugin, error) {
	dir, err := s.dir(name, digest)
	if err != nil {
		return Plugin{}, err
	}
	if fi, err := os.Lstat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Plugin{}, ErrNotFetched
		}
		return Plugin{}, err
	} else if !fi.IsDir() {
		return Plugin{}, fmt.Errorf("%w: %s is not a directory", ErrModified, dir)
	}
	if err := safewrite.CheckPath(s.Root, dir); err != nil {
		return Plugin{}, fmt.Errorf("%w: %v", ErrModified, err)
	}
	files, err := ReadFolder(dir)
	if err != nil {
		return Plugin{}, fmt.Errorf("%w: %v", ErrModified, err)
	}
	if sp := specialIn(files); sp != "" {
		return Plugin{}, fmt.Errorf("%w: %s: a symlink or non-regular file", ErrModified, sp)
	}
	p, problems := ReadTree(files, opts)
	if p.Digest != digest {
		return Plugin{}, fmt.Errorf("%w: digest is %s", ErrModified, p.Digest)
	}
	if len(problems) > 0 {
		return Plugin{}, fmt.Errorf("%w: %s", ErrInvalid, strings.Join(problems, "; "))
	}
	return p, nil
}

// specialIn names the first symlink or non-regular file of a tree read back from
// the store, or "": a stored tree never holds one, so it was changed after the
// write (ErrModified), whatever its digest says.
func specialIn(files []TreeFile) string {
	for _, f := range files {
		if f.Special {
			return f.Path
		}
	}
	return ""
}

func checkDir(dir, want string, opts Options) error {
	files, err := ReadFolder(dir)
	if err != nil {
		return err
	}
	if sp := specialIn(files); sp != "" {
		return fmt.Errorf("plugin: %s in the tree written to the store is a symlink or non-regular file", sp)
	}
	p, problems := ReadTree(files, opts)
	if len(problems) > 0 || p.Digest != want {
		return errors.New("plugin: the tree written to the store does not read back as validated")
	}
	return nil
}

// Keep is what GC preserves for one configured plugin.
type Keep struct{ Installed, Approved string }

// GC removes every digest directory but the two of a configured plugin, and every
// plugin directory that is not configured. It is best effort: a failure is
// reported and the rest goes on. It never removes the approved tree, and it
// leaves alone a name that is not a valid plugin name. The caller holds the
// client lock.
func (s Store) GC(keep map[string]Keep) error {
	if s.ReadOnly {
		return ErrReadOnly
	}
	s.sweep()
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var errs []error
	for _, e := range entries {
		name := e.Name()
		if checkName(name) != nil || !e.IsDir() {
			continue
		}
		dir := filepath.Join(s.Root, name)
		k, ok := keep[name]
		if !ok {
			errs = append(errs, removeTree(s.Root, dir))
			continue
		}
		subs, err := os.ReadDir(dir)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, d := range subs {
			if d.Name() != k.Installed && d.Name() != k.Approved {
				errs = append(errs, removeTree(s.Root, filepath.Join(dir, d.Name())))
			}
		}
	}
	return errors.Join(errs...)
}

// sweep removes the leftovers of an interrupted write older than tmpMaxAge.
func (s Store) sweep() {
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), tmpPrefix) {
			continue
		}
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > tmpMaxAge {
			_ = removeTree(s.Root, filepath.Join(s.Root, e.Name()))
		}
	}
}

// removeTree removes dir (inside base) unless the path to it goes through a link.
func removeTree(base, dir string) error {
	if err := safewrite.CheckPath(base, dir); err != nil {
		return err
	}
	return os.RemoveAll(dir)
}
