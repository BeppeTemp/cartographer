package gitx

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

// statMemo remembers the output of a read-only git command for as long as the
// file under .git that decides it is unchanged. kb_status and the Atlas ask
// for the branch and the remote URL on every call, and a git process costs
// tens of milliseconds where a stat costs microseconds.
//
// Only a plain repository is memoised: when .git is a file (a worktree or a
// submodule) the files live elsewhere and the command always runs.
type statMemo struct {
	mu      sync.Mutex
	entries map[string]memoEntry
}

type memoEntry struct {
	mod  time.Time
	size int64
	out  string
	err  error
}

var gitMemo statMemo

// do returns run's result, cached under key while dir/.git/<file> keeps its
// modification time and size.
func (m *statMemo) do(dir, file, key string, run func() (string, error)) (string, error) {
	gitDir := filepath.Join(dir, ".git")
	if fi, err := os.Stat(gitDir); err != nil || !fi.IsDir() {
		return run()
	}
	fi, err := os.Stat(filepath.Join(gitDir, file))
	if err != nil {
		return run()
	}
	k := dir + "\x00" + key
	m.mu.Lock()
	e, ok := m.entries[k]
	m.mu.Unlock()
	if ok && e.mod.Equal(fi.ModTime()) && e.size == fi.Size() {
		return e.out, e.err
	}
	out, runErr := run()
	m.mu.Lock()
	if m.entries == nil {
		m.entries = map[string]memoEntry{}
	}
	m.entries[k] = memoEntry{mod: fi.ModTime(), size: fi.Size(), out: out, err: runErr}
	m.mu.Unlock()
	return out, runErr
}
