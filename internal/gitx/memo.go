package gitx

import (
	"os"
	"path/filepath"
	"sync"
)

// headMemo remembers `git symbolic-ref --short HEAD` for as long as .git/HEAD
// holds the same bytes, which is all the answer depends on. kb_status and the
// Atlas ask for the branch on every call, and a git process costs tens of
// milliseconds where reading a 30-byte file costs microseconds (D736).
//
// Only a plain repository is memoised: when .git is a file (a worktree or a
// submodule) HEAD lives elsewhere and the command always runs.
type headMemo struct {
	mu      sync.Mutex
	entries map[string]headEntry
}

type headEntry struct {
	head string
	out  string
	err  error
}

var branchMemo headMemo

func (m *headMemo) do(dir string, run func() (string, error)) (string, error) {
	gitDir := filepath.Join(dir, ".git")
	if fi, err := os.Stat(gitDir); err != nil || !fi.IsDir() {
		return run()
	}
	raw, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return run()
	}
	head := string(raw)
	m.mu.Lock()
	e, ok := m.entries[dir]
	m.mu.Unlock()
	if ok && e.head == head {
		return e.out, e.err
	}
	out, runErr := run()
	m.mu.Lock()
	if m.entries == nil {
		m.entries = map[string]headEntry{}
	}
	m.entries[dir] = headEntry{head: head, out: out, err: runErr}
	m.mu.Unlock()
	return out, runErr
}
