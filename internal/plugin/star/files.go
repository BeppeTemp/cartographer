package star

import (
	"errors"
	"fmt"
	"github.com/BeppeTemp/cartographer/internal/plugin/starcore"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/plugin"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/safewrite"
	"github.com/BeppeTemp/cartographer/internal/service"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// Files are reached through placeholder paths only ({home}/notes/a.md), the
// form the manifest grants them in, so a script is OS-independent and never
// holds an absolute path. A path is checked in this order, and each step can
// only narrow the next: lexical form, the manifest's grant for the operation,
// the hard deny list (regardless of the grant), the walk from home through no
// link (safewrite), the same walk after symlink resolution, then the access
// itself, which does not follow a final link. The check-then-open race is the
// repository's accepted one (safewrite): a process that can swap a directory
// for a link between the check and the open is already running as the user.

// filesState is the file state of one call: where home is and what Cartographer
// keeps below it.
type filesState struct {
	cc       *starcore.CallCtx
	host     Host
	readOnly bool
}

func filesModule() *starlarkstruct.Module {
	return &starlarkstruct.Module{Name: "files", Members: starlark.StringDict{
		"read":   starlark.NewBuiltin("files.read", filesRead),
		"write":  starlark.NewBuiltin("files.write", filesWrite),
		"remove": starlark.NewBuiltin("files.remove", filesRemove),
		"glob":   starlark.NewBuiltin("files.glob", filesGlob),
	}}
}

// fileErr is a failure of the access itself (not a missing grant): the OS
// reason without its absolute path, naming the placeholder path.
func fileErr(op, p string, err error) error {
	if errors.Is(err, safewrite.ErrSymlinkDestination) {
		err = errors.New("goes through a link")
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		err = pe.Err
	}
	return errors.New(starcore.EscapeLines(fmt.Sprintf("files.%s %s: %v", op, p, err)))
}

// fpath is a parsed placeholder path: its anchor and the segments below it.
type fpath struct {
	anchor string
	segs   []string
}

func (p fpath) String() string { return "{" + p.anchor + "}/" + strings.Join(p.segs, "/") }

var windowsDevices = []string{"CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$"}

// segProblem is why a segment is refused on every OS, so a plugin that works
// here cannot name a device or a stream on another: empty, dot, dot-dot, a
// separator or colon, a control character, a trailing dot or space, a Windows
// device name. A pattern segment may hold * ? [ ] and the segment ** alone.
func segProblem(seg string, pattern bool) string {
	switch {
	case seg == "" || seg == "." || seg == "..":
		return "has an empty, dot or .. segment"
	case !utf8.ValidString(seg):
		return "is not valid UTF-8"
	case strings.ContainsAny(seg, `\:{}`) || strings.ContainsFunc(seg, unicode.IsControl):
		return `has a backslash, colon, brace or control character: use / separators`
	case seg != strings.TrimRight(seg, " ."):
		return "has a segment ending in a dot or a space"
	case !pattern && strings.ContainsAny(seg, "*?["):
		return "has a wildcard: files.glob takes patterns"
	case pattern && strings.Contains(seg, "**") && seg != "**":
		return "has ** inside a segment: it stands alone"
	}
	name := strings.ToUpper(seg)
	if i := strings.IndexByte(name, '.'); i >= 0 {
		name = name[:i]
	}
	if slices.Contains(windowsDevices, name) {
		return "names a Windows device"
	}
	if len(name) == 4 && (strings.HasPrefix(name, "COM") || strings.HasPrefix(name, "LPT")) && name[3] >= '0' && name[3] <= '9' {
		return "names a Windows device"
	}
	return ""
}

// parseFilePath accepts only {anchor}/seg/seg: no absolute path, no ~.
func parseFilePath(s string, pattern bool) (fpath, error) {
	var anchor, rest string
	for _, a := range []string{"home", "xdg_config", "app_data", "local_app_data", "documents"} {
		if strings.HasPrefix(s, "{"+a+"}/") {
			anchor, rest = a, s[len(a)+3:]
			break
		}
	}
	if anchor == "" {
		return fpath{}, starcore.DeniedErr("files: %q must start with {home}/, {xdg_config}/, {app_data}/, {local_app_data}/ or {documents}/", s)
	}
	segs := strings.Split(rest, "/")
	for _, seg := range segs {
		if why := segProblem(seg, pattern); why != "" {
			return fpath{}, starcore.DeniedErr("files: %q %s", s, why)
		}
	}
	return fpath{anchor, segs}, nil
}

// fileTarget is a checked path: where it is and under which grant.
type fileTarget struct {
	fp       fpath
	home     string   // native home
	dir      string   // native directory of the anchor
	dirRel   []string // the anchor's segments below home
	abs      string
	display  string
	homeReal string
}

// home returns the user's home directory, or why it is unknown.
func (s *filesState) homeDir() (string, error) {
	h := s.host.Home
	if h == "" {
		var err error
		if h, err = os.UserHomeDir(); err != nil {
			return "", err
		}
	}
	if !filepath.IsAbs(h) {
		return "", errors.New("the home directory is unknown")
	}
	return filepath.Clean(h), nil
}

func (s *filesState) env(home string) core.ProviderEnv {
	return core.ProviderEnv{GOOS: runtime.GOOS, Home: home, Getenv: func(k string) string {
		v, _ := s.cc.LookupEnv(k)
		return v
	}}
}

func relSegs(base, p string) ([]string, bool) {
	rel, err := filepath.Rel(base, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, false
	}
	if rel == "." {
		return nil, true
	}
	return strings.Split(filepath.ToSlash(rel), "/"), true
}

// deniedComponents are never reachable, whatever the manifest says: key and
// credential stores, a repository's metadata (a write into .git/hooks is code
// execution, which "no process execution" rules out) and every ".cartographer*"
// name (the client config, its lock files, token, state and approvals, the
// variant cache, a KB's own directory). Compared case-insensitively on every OS.
func deniedComponent(seg string) bool {
	seg = strings.ToLower(strings.TrimRight(seg, " ."))
	return seg == ".ssh" || seg == ".gnupg" || seg == ".git" || strings.HasPrefix(seg, ".cartographer")
}

// stateDirs are the directories named cartographer under the places a config or
// a cache lives (the server config, the update cache and the plugin store) and
// the cron backend's state directory, below home, lowercased.
func (s *filesState) stateDirs(home, homeReal string) [][]string {
	bases := []string{filepath.Join(home, ".config")}
	if s.host.ConfigDir != "" {
		bases = append(bases, s.host.ConfigDir)
	} else if d, err := os.UserConfigDir(); err == nil {
		bases = append(bases, d)
	}
	if s.host.CacheDir != "" {
		bases = append(bases, s.host.CacheDir)
	} else if d, err := os.UserCacheDir(); err == nil {
		bases = append(bases, d)
	}
	for _, a := range []string{"xdg_config", "app_data", "local_app_data"} {
		if d, err := core.ExpandAnchor(s.env(home), a); err == nil {
			bases = append(bases, filepath.FromSlash(d))
		}
	}
	dirs := make([]string, 0, len(bases)+2)
	for _, b := range bases {
		dirs = append(dirs, filepath.Join(b, "cartographer"))
	}
	// The cron backend's pidfile, lock and logs: $XDG_STATE_HOME/cartographer,
	// which a custom XDG_STATE_HOME can put anywhere below home.
	dirs = append(dirs, filepath.Join(home, ".local", "state", "cartographer"))
	if d, err := service.StateDir(); err == nil {
		dirs = append(dirs, d)
	}
	var out [][]string
	for _, d := range dirs {
		for _, h := range []string{home, homeReal} {
			if segs, ok := relSegs(h, d); ok {
				for i := range segs {
					segs[i] = strings.ToLower(segs[i])
				}
				out = append(out, segs)
			}
		}
	}
	return out
}

// deny says why segs, the path below home, is never reachable; "" if it is.
func (s *filesState) deny(home, homeReal string, segs []string) string {
	for _, seg := range segs {
		if deniedComponent(seg) {
			return fmt.Sprintf("%q is Cartographer's or a credential store's own state", seg)
		}
	}
	if configurator.DeniedHomePath(segs) {
		return "a place no plugin may reach (credentials, shell start-up files, autostart, Cartographer's data)"
	}
	low := make([]string, len(segs))
	for i, seg := range segs {
		low[i] = strings.ToLower(strings.TrimRight(seg, " ."))
	}
	for _, d := range s.stateDirs(home, homeReal) {
		if len(low) >= len(d) && slices.Equal(low[:len(d)], d) {
			return "Cartographer's own configuration, cache or plugin store"
		}
	}
	return ""
}

// target runs the checks every operation shares, in order.
func (s *filesState) target(op, raw string, list func(*core.Files) []string, write bool) (*fileTarget, error) {
	perms := s.cc.Perms
	if perms == nil || perms.Files == nil || len(list(perms.Files)) == 0 {
		kind := "read"
		if write {
			kind = "write"
		}
		return nil, starcore.DeniedErr("files.%s needs permissions.files.%s in the manifest", op, kind)
	}
	if write && s.readOnly {
		return nil, starcore.DeniedErr("files.%s: this session is read-only", op)
	}
	fp, err := parseFilePath(raw, false)
	if err != nil {
		return nil, err
	}
	return s.locate(op, fp, list(perms.Files), true)
}

// locate expands the anchor and applies the deny list, the grant (when match),
// and the link checks.
func (s *filesState) locate(op string, fp fpath, grants []string, match bool) (*fileTarget, error) {
	home, err := s.homeDir()
	if err != nil {
		return nil, fileErr(op, fp.String(), err)
	}
	dir, err := core.ExpandAnchor(s.env(home), fp.anchor)
	if err != nil {
		return nil, starcore.DeniedErr("files.%s: %s", op, err)
	}
	t := &fileTarget{fp: fp, home: home, dir: filepath.FromSlash(dir), display: fp.String()}
	t.homeReal = home
	if r, err := filepath.EvalSymlinks(home); err == nil {
		t.homeReal = r
	}
	var ok bool
	if t.dirRel, ok = relSegs(home, t.dir); !ok {
		return nil, starcore.DeniedErr("files.%s: {%s} resolves outside the home directory", op, fp.anchor)
	}
	t.abs = filepath.Join(append([]string{t.dir}, fp.segs...)...)
	if why := s.deny(home, t.homeReal, append(slices.Clone(t.dirRel), fp.segs...)); why != "" {
		return nil, starcore.DeniedErr("files.%s: %s is denied: %s", op, t.display, why)
	}
	if match && !slices.ContainsFunc(grants, func(g string) bool { return plugin.GlobMatch(g, t.display) }) {
		return nil, starcore.DeniedErr("files.%s: %s is outside the paths the manifest grants", op, t.display)
	}
	if err := s.noLinks(op, t, t.abs); err != nil {
		return nil, err
	}
	return t, nil
}

// noLinks refuses a path with a link, a device or any irregular component
// between home and it (safewrite, ignoring the user's allow list: a plugin
// follows no link), then resolves the deepest part that exists and checks the
// result again: still below home, still not denied.
func (s *filesState) noLinks(op string, t *fileTarget, abs string) error {
	if err := safewrite.CheckPathStrict(t.home, abs); err != nil {
		// safewrite's text holds absolute paths, a link target and advice for a
		// user; the script gets none of it.
		return starcore.DeniedErr("files.%s: %s goes through a link or a file that is not plain", op, t.display)
	}
	existing, tail := abs, []string{}
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return nil
		}
		tail = append([]string{filepath.Base(existing)}, tail...)
		existing = parent
	}
	real, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return fileErr(op, t.display, err)
	}
	segs, ok := relSegs(t.homeReal, real)
	if !ok {
		return starcore.DeniedErr("files.%s: %s resolves outside the home directory", op, t.display)
	}
	if why := s.deny(t.home, t.homeReal, append(segs, tail...)); why != "" {
		return starcore.DeniedErr("files.%s: %s resolves into a denied place: %s", op, t.display, why)
	}
	return nil
}

func filesRead(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var p string
	var binary bool
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "path", &p, "binary?", &binary); err != nil {
		return nil, err
	}
	c, err := starcore.CallPhase(thread, b.Name())
	if err != nil {
		return nil, err
	}
	t, err := filesOf(c).target("read", p, func(f *core.Files) []string { return f.Read }, false)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(t.abs, os.O_RDONLY|openNoFollow, 0)
	if err != nil {
		return nil, fileErr("read", t.display, err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, fileErr("read", t.display, err)
	}
	if !st.Mode().IsRegular() {
		return nil, fileErr("read", t.display, errors.New("not a regular file"))
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return nil, fileErr("read", t.display, err)
	}
	if len(data) > MaxFileBytes {
		return nil, starcore.LimitErr("files.read: %s is over the %d MiB cap", t.display, MaxFileBytes>>20)
	}
	if binary {
		return starlark.Bytes(data), nil
	}
	if !utf8.Valid(data) {
		return nil, fileErr("read", t.display, errors.New("not valid UTF-8: read it with binary=True"))
	}
	return starlark.String(data), nil
}

func filesWrite(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var p string
	var v starlark.Value
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "path", &p, "data", &v); err != nil {
		return nil, err
	}
	var data []byte
	switch x := v.(type) {
	case starlark.String:
		data = []byte(x)
	case starlark.Bytes:
		data = []byte(x)
	default:
		return nil, fmt.Errorf("files.write: data must be a string or bytes, not %s", v.Type())
	}
	c, err := starcore.CallPhase(thread, b.Name())
	if err != nil {
		return nil, err
	}
	t, err := filesOf(c).target("write", p, func(f *core.Files) []string { return f.Write }, true)
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFileBytes {
		return nil, starcore.LimitErr("files.write: %d bytes is over the %d MiB cap", len(data), MaxFileBytes>>20)
	}
	if err := safewrite.MkdirAllNoFollow(t.home, filepath.Dir(t.abs), 0o700); err != nil {
		return nil, fileErr("write", t.display, err)
	}
	// A new file is 0600; an existing one keeps its mode.
	if err := safewrite.WriteFileAtomicNoFollow(t.home, t.abs, data, 0o600); err != nil {
		return nil, fileErr("write", t.display, err)
	}
	return starlark.None, nil
}

// filesRemove removes a file, never a directory; False when it was not there.
func filesRemove(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var p string
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &p); err != nil {
		return nil, err
	}
	c, err := starcore.CallPhase(thread, b.Name())
	if err != nil {
		return nil, err
	}
	t, err := filesOf(c).target("remove", p, func(f *core.Files) []string { return f.Write }, true)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(t.abs)
	if errors.Is(err, fs.ErrNotExist) {
		return starlark.False, nil
	}
	if err != nil {
		return nil, fileErr("remove", t.display, err)
	}
	if info.IsDir() {
		return nil, fileErr("remove", t.display, errors.New("is a directory: remove deletes files only"))
	}
	if err := safewrite.Remove(t.home, t.abs); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return starlark.False, nil
		}
		return nil, fileErr("remove", t.display, err)
	}
	return starlark.True, nil
}

// filesGlob lists the regular files a pattern matches, as placeholder paths,
// sorted. Only files a read grant covers are returned; a denied directory is
// never entered, a link is never followed, and the walk is bounded.
func filesGlob(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var pat string
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &pat); err != nil {
		return nil, err
	}
	c, err := starcore.CallPhase(thread, b.Name())
	if err != nil {
		return nil, err
	}
	s := filesOf(c)
	if c.Perms == nil || c.Perms.Files == nil || len(c.Perms.Files.Read) == 0 {
		return nil, starcore.DeniedErr("files.glob needs permissions.files.read in the manifest")
	}
	fp, err := parseFilePath(pat, true)
	if err != nil {
		return nil, err
	}
	// The pattern's own literal head is checked as a path would be; the
	// matches are checked one by one against the grant.
	head := fpath{fp.anchor, nil}
	for _, seg := range fp.segs {
		if strings.ContainsAny(seg, "*?[") || len(head.segs) == len(fp.segs)-1 {
			break
		}
		head.segs = append(head.segs, seg)
	}
	t, err := s.locate("glob", head, c.Perms.Files.Read, false)
	if err != nil {
		return nil, err
	}
	w := &globWalk{s: s, c: c, t: t, grants: c.Perms.Files.Read}
	w.walk(t.abs, head.segs, fp.segs[len(head.segs):])
	if w.err != nil {
		return nil, w.err
	}
	sort.Strings(w.out)
	out := make([]starlark.Value, len(w.out))
	for i, o := range w.out {
		out[i] = starlark.String(o)
	}
	return starlark.NewList(out), nil
}

type globWalk struct {
	s      *filesState
	c      *starcore.CallCtx
	t      *fileTarget
	grants []string
	out    []string
	seen   map[string]bool
	visits int
	err    error
}

func (w *globWalk) done() bool {
	if w.err != nil || len(w.out) >= MaxGlobResults || w.visits >= MaxGlobVisits {
		return true
	}
	if e := w.c.Ctx.Err(); e != nil {
		w.err = e
		return true
	}
	return false
}

// add records a regular file reached through segs below the anchor.
func (w *globWalk) add(rel []string) {
	p := fpath{w.t.fp.anchor, rel}.String()
	if w.seen[p] || !slices.ContainsFunc(w.grants, func(g string) bool { return plugin.GlobMatch(g, p) }) {
		return
	}
	if w.seen == nil {
		w.seen = map[string]bool{}
	}
	w.seen[p] = true
	w.out = append(w.out, p)
}

// walk matches pat below dir, whose segments below the anchor are rel.
func (w *globWalk) walk(dir string, rel, pat []string) {
	if w.done() || len(pat) == 0 || len(rel) > starcore.MaxGlobDepth {
		return
	}
	seg, last := pat[0], len(pat) == 1
	step := func(name string, typ fs.FileMode) {
		w.visits++
		child := append(slices.Clone(rel), name)
		if why := w.s.deny(w.t.home, w.t.homeReal, append(slices.Clone(w.t.dirRel), child...)); why != "" {
			return
		}
		switch {
		case typ.IsRegular() && last && seg != "**":
			w.add(child)
		case typ.IsRegular() && seg == "**" && last:
			w.add(child)
		case typ.IsDir() && seg == "**":
			w.walk(filepath.Join(dir, name), child, pat)
		case typ.IsDir() && !last:
			w.walk(filepath.Join(dir, name), child, pat[1:])
		}
	}
	if seg == "**" && !last {
		// Zero directories, then each directory with ** still in force.
		w.walk(dir, rel, pat[1:])
	}
	if !strings.ContainsAny(seg, "*?[") {
		if info, err := os.Lstat(filepath.Join(dir, seg)); err == nil {
			step(seg, info.Mode())
		}
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if w.done() {
			return
		}
		if seg != "**" {
			if ok, err := path.Match(seg, e.Name()); err != nil || !ok {
				continue
			}
		}
		info, err := e.Info()
		if err != nil || info.Mode()&fs.ModeSymlink != 0 {
			continue
		}
		step(e.Name(), info.Mode())
	}
}
