package mcpserver

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// hiddenEntries lists every file or folder below root whose name starts with a
// dot, relative to root.
func hiddenEntries(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if p != root && strings.HasPrefix(d.Name(), ".") {
			rel, _ := filepath.Rel(root, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// kbGitlinks lists the gitlinks (index mode 160000) the index of k records, or
// that HEAD does.
func kbGitlinks(t *testing.T, k *kb.KB, inHEAD bool) []string {
	t.Helper()
	args := []string{"-C", k.Root, "ls-files", "-s"}
	if inHEAD {
		args = []string{"-C", k.Root, "ls-tree", "-r", "HEAD"}
	}
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		t.Fatal(err)
	}
	var links []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "160000") {
			links = append(links, line)
		}
	}
	return links
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.test", "-c", "core.fsmonitor=false", "-C", dir}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// plantNestedRepo makes root/rel a repository of its own, with one commit. Its
// core.fsmonitor writes the file it returns, which exists only if git ran that
// repository's configuration (a POSIX shell line: on Windows the repository is
// planted unarmed, and the index checks are the ones that bite).
func plantNestedRepo(t *testing.T, root, rel string) (marker string) {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: nested\ndescription: x\n---\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "nested")
	marker = filepath.Join(t.TempDir(), "fsmonitor-ran")
	if runtime.GOOS != "windows" {
		gitIn(t, dir, "config", "core.fsmonitor", "echo ran > '"+filepath.ToSlash(marker)+"'")
	}
	return marker
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// An artifact path has no hidden segment, under any kind and whichever tool.
func TestArtifactTools_RefuseHiddenPathSegments(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	head := xferHead(t, k)

	paths := []string{
		"skills/foo/.git/config",
		"skills/foo/.git/HEAD",
		"skills/foo/.git/objects/pack/pack-1.idx",
		"skills/foo/.git/refs/heads/main",
		"skills/foo/.git",
		"skills/foo/.GIT/config",
		"skills/foo/GIT~1/config",
		"skills/foo/.gitattributes",
		"skills/foo/assets/.hidden",
		"skills/foo/sub/../.git/config",
		`skills\foo\.git\config`,
		"hooks/x/.git/HEAD",
		"hooks/x/.git/config",
		"hooks/.git/HEAD",
		"hooks/x/.hook.json",
		"agents/.helper.md",
		"mcp/.server.json",
		"templates/.note.md",
		".git/config",
		".cartographer/x",
	}
	for _, p := range paths {
		for _, call := range []struct {
			tool string
			args map[string]any
		}{
			{"artifact_write", map[string]any{"path": p, "content": "x"}},
			{"artifact_read", map[string]any{"path": p}},
			{"artifact_delete", map[string]any{"path": p, "if_match": sha256Hex([]byte("x"))}},
		} {
			b, _ := json.Marshal(call.args)
			res := s.callTool(authLocalContext(), call.tool, b)
			if !res.IsError || !strings.Contains(xferText(res), "path not allowed") {
				t.Errorf("%s %q: want a path not allowed refusal, got isError=%v %q", call.tool, p, res.IsError, xferText(res))
			}
		}
	}
	if got := hiddenEntries(t, filepath.Join(k.Root, "skills")); len(got) != 0 {
		t.Errorf("skills/ holds hidden entries after the refused writes: %v", got)
	}
	if got := hiddenEntries(t, filepath.Join(k.Root, "hooks")); len(got) != 0 {
		t.Errorf("hooks/ holds hidden entries after the refused writes: %v", got)
	}
	if xferHead(t, k) != head {
		t.Error("a refused write committed")
	}

	// What stays allowed: the same folders without the dot.
	for _, p := range []string{"skills/foo/assets/data.txt", "hooks/x/run.sh"} {
		b, _ := json.Marshal(map[string]any{"path": p, "content": "x"})
		if res := s.callTool(authLocalContext(), "artifact_write", b); res.IsError {
			t.Errorf("artifact_write %q: %s", p, xferText(res))
		}
	}
}

// A sequence of writes, the files of a nested repository among them: those are
// refused, the others land, and the KB never records a gitlink.
func TestArtifactWrite_ASequenceOfWritesNeverRecordsAGitlink(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	write := func(path, content string, wantOK bool) {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"path": path, "content": content})
		res := s.callTool(authLocalContext(), "artifact_write", b)
		if res.IsError == wantOK {
			t.Fatalf("artifact_write %q: isError=%v, want %v: %s", path, res.IsError, !wantOK, xferText(res))
		}
	}
	const skill = "---\nname: nested\ndescription: A skill.\n---\nbody\n"
	write("skills/nested/.git/HEAD", "ref: refs/heads/main\n", false)
	write("skills/nested/.git/refs/heads/main", "1111111111111111111111111111111111111111\n", false)
	write("skills/nested/.git/objects/info/packs", "\n", false)
	write("skills/nested/.git/config", "[core]\n\tbare = false\n", false)
	write("skills/nested/SKILL.md", skill, true)
	write("hooks/nested/.git/HEAD", "ref: refs/heads/main\n", false)
	write("hooks/nested/hook.json", `{"event":"SessionStart","command":"run.sh"}`, true)

	if got := kbGitlinks(t, k, false); len(got) != 0 {
		t.Errorf("the index records gitlinks: %v", got)
	}
	if got := kbGitlinks(t, k, true); len(got) != 0 {
		t.Errorf("HEAD records gitlinks: %v", got)
	}
	if got := hiddenEntries(t, filepath.Join(k.Root, "skills")); len(got) != 0 {
		t.Errorf("hidden entries under skills/: %v", got)
	}
	for _, p := range []string{"skills/nested/SKILL.md"} {
		out, err := exec.Command("git", "-C", k.Root, "ls-files", "--", p).Output()
		if err != nil || strings.TrimSpace(string(out)) != p {
			t.Errorf("%s is not tracked after its write: %q %v", p, out, err)
		}
	}
	xferClean(t, k)
}

// A nested repository that is on disk anyway (a file system access, a KB that
// already records one) is never looked into by the server's git and never
// recorded by a commit of it.
func TestNestedRepositoryOnDisk_IsNeverRunAndNeverRecorded(t *testing.T) {
	t.Run("new to the history", func(t *testing.T) {
		t.Parallel()
		k, _ := setupGitKB(t)
		s := New("test")
		RegisterKBTools(s, k, Deps{})
		marker := plantNestedRepo(t, k.Root, "skills/nested")

		for _, entry := range []string{"first", "second"} {
			b, _ := json.Marshal(map[string]any{"entry": entry})
			s.callTool(authLocalContext(), "log_append", b)
		}
		if fileExists(marker) {
			t.Error("the nested repository's core.fsmonitor ran")
		}
		if got := kbGitlinks(t, k, false); len(got) != 0 {
			t.Errorf("the index records a gitlink: %v", got)
		}
		if got := kbGitlinks(t, k, true); len(got) != 0 {
			t.Errorf("HEAD records a gitlink: %v", got)
		}
	})

	t.Run("recorded by an earlier version", func(t *testing.T) {
		t.Parallel()
		k, _ := setupGitKB(t)
		s := New("test")
		RegisterKBTools(s, k, Deps{})
		marker := plantNestedRepo(t, k.Root, "skills/nested")
		gitIn(t, k.Root, "add", "skills/nested")
		gitIn(t, k.Root, "commit", "-qm", "recorded by an earlier version")
		recorded := kbGitlinks(t, k, true)
		if len(recorded) != 1 {
			t.Fatalf("setup: want one gitlink in HEAD, got %v", recorded)
		}
		head := xferHead(t, k)

		for _, entry := range []string{"first", "second"} {
			b, _ := json.Marshal(map[string]any{"entry": entry})
			if res := s.callTool(authLocalContext(), "log_append", b); res.IsError {
				t.Fatalf("log_append: %s", xferText(res))
			}
		}
		if fileExists(marker) {
			t.Error("the nested repository's core.fsmonitor ran")
		}
		if xferHead(t, k) == head {
			t.Error("the writes did not commit")
		}
		if got := kbGitlinks(t, k, true); len(got) != 1 || got[0] != recorded[0] {
			t.Errorf("HEAD gitlinks = %v, want %v unchanged", got, recorded)
		}
	})
}

// artifact_move carries a folder whole into a KB that commits it: a hidden
// entry in it, or a hidden name for it, refuses the move.
func TestArtifactMove_RefusesHiddenEntries(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	amWriteBytes(t, f.a, "skills/query-rete/SKILL.md", []byte(amSkillMD), 0o644)
	amWriteBytes(t, f.a, "hooks/guard/hook.json", []byte(`{"event":"SessionStart","command":"run.sh"}`), 0o644)
	amWriteBytes(t, f.a, "hooks/guard/run.sh", []byte("#!/bin/sh\n"), 0o755)
	xferCommit(t, f.a)

	cases := []struct {
		name  string
		plant func()
		args  string
		want  string
	}{
		{"a nested .git in a skill", func() {
			amWriteBytes(t, f.a, "skills/query-rete/.git/config", []byte("[core]\n"), 0o644)
		}, `{"target_kb":"kb-b","paths":["skills/query-rete"]}`, ".git"},
		{"a hidden file in a hook", func() {
			amWriteBytes(t, f.a, "hooks/guard/.env", []byte("x"), 0o644)
		}, `{"target_kb":"kb-b","paths":["hooks/guard"]}`, ".env"},
		{"a nested .git under a junk directory", func() {
			// Alone: the plain .git planted above would be reported first.
			if err := os.RemoveAll(filepath.Join(f.a.Root, "skills/query-rete/.git")); err != nil {
				t.Fatal(err)
			}
			amWriteBytes(t, f.a, "skills/query-rete/__pycache__/.git/config", []byte("[core]\n"), 0o644)
		}, `{"target_kb":"kb-b","paths":["skills/query-rete"]}`, "__pycache__/.git"},
		{"a hidden skill name", func() {}, `{"target_kb":"kb-b","paths":["skills/.git"]}`, "hidden segment"},
		{"a hidden hook name", func() {}, `{"target_kb":"kb-b","paths":["hooks/.git"]}`, "hidden segment"},
	}
	for _, tc := range cases {
		tc.plant()
		// The server commits what it finds uncommitted before it plans a move:
		// what is compared is the state the refusal found.
		xferCommit(t, f.a)
		headA, headB := xferHead(t, f.a), xferHead(t, f.b)
		res := f.move(t, tc.args)
		if !res.IsError || !strings.Contains(xferText(res), tc.want) {
			t.Errorf("%s: want a refusal naming %q, got isError=%v %q", tc.name, tc.want, res.IsError, xferText(res))
		}
		if xferHead(t, f.a) != headA || xferHead(t, f.b) != headB {
			t.Errorf("%s: a refused move committed", tc.name)
		}
	}
	if amExists(f.b, "skills/query-rete") || amExists(f.b, "hooks/guard") {
		t.Error("a refused move wrote to the target")
	}
	if !amExists(f.a, "skills/query-rete/__pycache__/.git/config") {
		t.Error("a refused move touched the source")
	}
	if err := os.RemoveAll(filepath.Join(f.a.Root, "skills/query-rete/__pycache__")); err != nil {
		t.Fatal(err)
	}

	// Junk goes with the folder as before (#1377); it is not a hidden entry.
	amWriteBytes(t, f.a, "skills/query-rete/.DS_Store", []byte("junk"), 0o644)
	if res := f.move(t, `{"target_kb":"kb-b","paths":["skills/query-rete"]}`); res.IsError {
		t.Fatalf("a folder with only junk dotfiles must move: %s", xferText(res))
	}
}

// The legacy-name repair moves a whole skills/ folder: a hidden entry in it
// stops the move, junk does not.
func TestRepairLegacySkillName_RefusesAHiddenEntry(t *testing.T) {
	t.Parallel()
	k, s := repairKB(t, 0)
	writeKBSkill(t, k, "kb-create", legacySkillText)
	nested := filepath.Join(k.Root, "skills", "kb-create", ".git")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "config"), []byte("[core]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := commitCount(t, k)

	out := repairApply(t, s, "legacy_skill_name")
	if applied, _ := out["applied"].(float64); applied != 0 {
		t.Fatalf("a folder with a hidden entry must not move: %v", out)
	}
	skipped, _ := out["skipped"].([]any)
	if len(skipped) != 1 || !strings.Contains(skipped[0].(map[string]any)["reason"].(string), ".git") {
		t.Fatalf("want one skip naming the hidden entry: %v", out["skipped"])
	}
	if _, err := os.Stat(filepath.Join(k.Root, "skills", "cartographer-kb-create")); !os.IsNotExist(err) {
		t.Errorf("the new folder exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(nested, "config")); err != nil {
		t.Errorf("the folder was touched: %v", err)
	}
	if commitCount(t, k) != before {
		t.Error("a refused repair committed")
	}

	// Junk goes with the folder.
	if err := os.RemoveAll(nested); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(k.Root, "skills", "kb-create", ".DS_Store"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = repairApply(t, s, "legacy_skill_name")
	if applied, _ := out["applied"].(float64); applied != 1 {
		t.Fatalf("a folder with only junk dotfiles must move: %v", out)
	}
}

// A junk directory excuses nothing below it: a hidden folder under
// __pycache__/ stops the legacy-name move as a top-level one does.
func TestRepairLegacySkillName_RefusesAHiddenEntryUnderJunkDir(t *testing.T) {
	t.Parallel()
	k, s := repairKB(t, 0)
	writeKBSkill(t, k, "kb-create", legacySkillText)
	nested := filepath.Join(k.Root, "skills", "kb-create", "__pycache__", ".git")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "config"), []byte("[core]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := repairApply(t, s, "legacy_skill_name")
	if applied, _ := out["applied"].(float64); applied != 0 {
		t.Fatalf("a folder with a hidden entry under __pycache__ must not move: %v", out)
	}
	if _, err := os.Stat(filepath.Join(nested, "config")); err != nil {
		t.Errorf("the folder was touched: %v", err)
	}
}
