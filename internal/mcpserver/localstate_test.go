package mcpserver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The server's local state of a KB (.cartographer/) is written only inside the
// KB: a link in place of the directory, of the file or of a temporary beside
// it leaves what it points at exactly as it was, and the state is still kept
// where it can be.

const victimBody = "victim content, not a log\n"

// victim is a file outside the KB that must come out of a test unchanged.
func victim(t *testing.T) (path string, unchanged func()) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(path, []byte(victimBody), 0o644); err != nil {
		t.Fatal(err)
	}
	return path, func() {
		t.Helper()
		if data, err := os.ReadFile(path); err != nil || string(data) != victimBody {
			t.Errorf("a file outside the KB changed: %q %v", data, err)
		}
	}
}

func symlinkOrSkipT(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func isRegular(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode().IsRegular()
}

func dirEntries(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return strings.Join(names, ",")
}

func TestReadAccessLog_TemporaryThatIsALinkIsNotWrittenThrough(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	v, unchanged := victim(t)
	touchConcepts(t, k, "a/b")
	l := newReadAccessLog(k)
	symlinkOrSkipT(t, v, l.path+".tmp")

	l.record("a/b")
	l.save()

	unchanged()
	if fi, err := os.Lstat(l.path + ".tmp"); err != nil || fi.Mode().IsRegular() {
		t.Errorf("the link in place of the temporary was replaced: %v %v", fi, err)
	}
	if !isRegular(l.path) {
		t.Fatalf("the log was not written as a regular file")
	}
	if _, ok := newReadAccessLog(k).lastReadTime("a/b"); !ok {
		t.Error("the read was not kept")
	}
}

func TestReadAccessLog_DirectoryThatIsALinkIsNeitherReadNorWritten(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	outside := t.TempDir()
	body := `{"since":"2026-01-01T00:00:00Z","read":{"seen/outside":"2026-01-02T00:00:00Z"},"surfaced":{}}`
	if err := os.WriteFile(filepath.Join(outside, readAccessFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkipT(t, outside, filepath.Join(k.Root, ".cartographer"))

	l := newReadAccessLog(k)
	if _, ok := l.lastReadTime("seen/outside"); ok {
		t.Error("the log read a file through a linked .cartographer")
	}
	l.record("a/b")
	l.save()

	if got := dirEntries(t, outside); got != readAccessFile {
		t.Errorf("the directory behind the link changed: %s", got)
	}
	if data, _ := os.ReadFile(filepath.Join(outside, readAccessFile)); string(data) != body {
		t.Errorf("the file behind the link changed: %q", data)
	}
}

func TestReadAccessLog_FileThatIsALinkIsNeitherReadNorReplaced(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	v, unchanged := victim(t)
	l := newReadAccessLog(k)
	symlinkOrSkipT(t, v, l.path)
	// What the link points at would parse as a log: it must not be read.
	if err := os.WriteFile(v, []byte(`{"since":"2026-01-01T00:00:00Z","read":{"seen/outside":"2026-01-02T00:00:00Z"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(v)

	l = newReadAccessLog(k)
	if _, ok := l.lastReadTime("seen/outside"); ok {
		t.Error("the log read the file behind the link")
	}
	l.record("a/b")
	l.save()

	if data, _ := os.ReadFile(v); string(data) != string(before) {
		t.Errorf("the file behind the link changed: %q", data)
	}
	if fi, err := os.Lstat(l.path); err != nil || fi.Mode().IsRegular() {
		t.Errorf("the link was replaced: %v %v", fi, err)
	}
	_ = unchanged
}

func TestSearchMissLog_TemporaryThatIsALinkIsNotWrittenThrough(t *testing.T) {
	t.Parallel()
	k, _, l := missFixture(t)
	v, unchanged := victim(t)
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := 0; i < searchMissMax; i++ {
		fmt.Fprintf(&b, `{"at":"2026-01-01T00:00:00Z","query":"q%d"}`+"\n", i)
	}
	if err := os.WriteFile(l.path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkipT(t, v, l.path+".tmp")

	l.record("newest") // past the cap: the log is rewritten to its tail

	unchanged()
	if fi, err := os.Lstat(l.path + ".tmp"); err != nil || fi.Mode().IsRegular() {
		t.Errorf("the link in place of the temporary was replaced: %v %v", fi, err)
	}
	if got := missLines(t, l); len(got) != searchMissKeep || got[len(got)-1].Query != "newest" {
		t.Fatalf("after trim: %d lines", len(got))
	}
	if !isRegular(l.path) {
		t.Error("the log is not a regular file")
	}
	_ = k
}

func TestSearchMissLog_DirectoryThatIsALinkIsNeitherReadNorWritten(t *testing.T) {
	t.Parallel()
	k, _, l := missFixture(t)
	outside := t.TempDir()
	body := `{"at":"` + time.Now().UTC().Format(time.RFC3339) + `","query":"seen outside"}` + "\n"
	if err := os.WriteFile(filepath.Join(outside, searchMissFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkipT(t, outside, filepath.Join(k.Root, ".cartographer"))

	l.record("a new miss")
	if got := l.top(); len(got) != 0 {
		t.Errorf("the log read through a linked .cartographer: %+v", got)
	}
	if got := dirEntries(t, outside); got != searchMissFile {
		t.Errorf("the directory behind the link changed: %s", got)
	}
	if data, _ := os.ReadFile(filepath.Join(outside, searchMissFile)); string(data) != body {
		t.Errorf("the file behind the link changed: %q", data)
	}
}

func TestSearchMissLog_FileThatIsALinkIsNotReadEither(t *testing.T) {
	t.Parallel()
	_, _, l := missFixture(t)
	v, unchanged := victim(t)
	body := `{"at":"` + time.Now().UTC().Format(time.RFC3339) + `","query":"seen outside"}` + "\n"
	if err := os.WriteFile(v, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkipT(t, v, l.path)

	l.record("a new miss")
	if got := l.top(); len(got) != 0 {
		t.Errorf("the log read the file behind the link: %+v", got)
	}
	if data, _ := os.ReadFile(v); string(data) != body {
		t.Errorf("the file behind the link changed: %q", data)
	}
	_ = unchanged
}

// The other files of .cartographer/ kept by this package obey the same rule.
func TestLocalState_LintHistoryAndRepairLogRefuseALinkedDirectory(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	outside := t.TempDir()
	symlinkOrSkipT(t, outside, filepath.Join(k.Root, ".cartographer"))

	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	if err := recordLintSample(k, lintSample{At: now.Format(time.RFC3339), Total: 1, ByHandler: map[string]int{}}, now); err == nil {
		t.Error("recordLintSample wrote through a linked .cartographer")
	}
	if err := appendAutoRepairRun(k, autoRepairRun{At: now.Format(time.RFC3339)}); err == nil {
		t.Error("appendAutoRepairRun wrote through a linked .cartographer")
	}
	if got := dirEntries(t, outside); got != "" {
		t.Errorf("the directory behind the link gained %q", got)
	}
}
