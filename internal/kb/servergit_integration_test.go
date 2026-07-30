package kb

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// newServerGitMount is deliberately HTTPS-backed (served from httptest by the
// shared helper) while all Git objects remain local temp fixtures.
func newServerGitMount(t *testing.T) (*KB, string) {
	t.Helper()
	root := t.TempDir()
	k, err := Init(filepath.Join(root, "kb"))
	if err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(root, "remote", "example", "wiki.git")
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	serverGitInitBare(t, bare)
	serverGitRemote(t, k, bare)
	return k, bare
}

func TestServerGitTwoCloneBaseAdvanceAndConflictRegistry(t *testing.T) {
	t.Run("clean base advance never updates protected base", func(t *testing.T) {
		k, bare := newServerGitMount(t)
		if err := k.ConfigureServerGit(serverConfig()); err != nil {
			t.Fatal(err)
		}
		other := filepath.Join(t.TempDir(), "other")
		serverGitRun(t, filepath.Dir(other), "clone", bare, other)
		if err := os.WriteFile(filepath.Join(other, "data", "from-base.md"), []byte("---\ntype: Note\n---\nbase\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		serverGitRun(t, other, "add", "data/from-base.md")
		serverGitRun(t, other, "-c", "user.name=test", "-c", "user.email=test@example.test", "commit", "-m", "base advance")
		serverGitRun(t, other, "push", "origin", "main")
		serverGitRun(t, bare, "update-server-info")
		baseBefore, _ := gitx.HeadSHAAt(k.Root, "origin/main")
		k.GitSync = true
		if _, err := k.SyncIn(); err != nil {
			t.Fatal(err)
		}
		baseAfter, _ := gitx.HeadSHAAt(k.Root, "origin/main")
		if baseBefore == baseAfter {
			t.Fatal("fixture did not fetch advanced base")
		}
		if changed, _ := gitx.HeadSHAAt(k.Root, "origin/main"); changed != baseAfter {
			t.Fatal("protected base ref was changed locally")
		}
	})

	t.Run("concept and unrelated conflicts remain fail closed", func(t *testing.T) {
		k, bare := newServerGitMount(t)
		// Seed a real concept before creating the server working branch.
		if err := os.WriteFile(filepath.Join(k.Root, "data", "shared.md"), []byte("---\ntype: Note\n---\nbase\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(k.Root, "README.md"), []byte("base\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		serverGitRun(t, k.Root, "add", "data/shared.md", "README.md")
		serverGitRun(t, k.Root, "commit", "-m", "seed")
		serverGitRun(t, k.Root, "push", "origin", "main")
		serverGitRun(t, bare, "update-server-info")
		if err := k.ConfigureServerGit(serverConfig()); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(k.Root, "data", "shared.md"), []byte("---\ntype: Note\n---\nworking\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		serverGitRun(t, k.Root, "add", "data/shared.md")
		if err := os.WriteFile(filepath.Join(k.Root, "README.md"), []byte("working\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		serverGitRun(t, k.Root, "add", "README.md")
		serverGitRun(t, k.Root, "commit", "-m", "working edit")
		other := filepath.Join(t.TempDir(), "other")
		serverGitRun(t, filepath.Dir(other), "clone", bare, other)
		if err := os.WriteFile(filepath.Join(other, "data", "shared.md"), []byte("---\ntype: Note\n---\nbase edit\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(other, "README.md"), []byte("base edit\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		serverGitRun(t, other, "add", "data/shared.md", "README.md")
		serverGitRun(t, other, "-c", "user.name=test", "-c", "user.email=test@example.test", "commit", "-m", "base conflict")
		serverGitRun(t, other, "push", "origin", "main")
		serverGitRun(t, bare, "update-server-info")
		k.GitSync = true
		if _, err := k.SyncIn(); err == nil {
			t.Fatal("expected rebase conflict")
		}
		conflicts, err := k.ListConflicts()
		if err != nil || len(conflicts) != 2 {
			t.Fatalf("concept conflict registry = %#v, %v", conflicts, err)
		}
		ids := map[string]bool{}
		for _, c := range conflicts {
			ids[c.ConceptID] = true
		}
		if !ids["shared"] || !ids["git-path:README.md"] {
			t.Fatalf("missing fail-closed conflict entries: %#v", conflicts)
		}
		// The state is local-only and survives a fresh KB object.
		restarted, err := Open(k.Root)
		if err != nil {
			t.Fatal(err)
		}
		conflicts, err = restarted.ListConflicts()
		if err != nil || len(conflicts) != 2 {
			t.Fatalf("restart registry = %#v, %v", conflicts, err)
		}
	})
}

func TestServerConflictResolutionStrategiesMaterializeExpectedContent(t *testing.T) {
	root := t.TempDir()
	k, err := Init(filepath.Join(root, "kb"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(k.Root, "data", "choice.md")
	if err := os.WriteFile(path, []byte("ours\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	serverGitRun(t, k.Root, "add", "data/choice.md")
	serverGitRun(t, k.Root, "commit", "-m", "ours")
	ours, _ := gitx.HeadSHA(k.Root)
	if err := os.WriteFile(path, []byte("theirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	serverGitRun(t, k.Root, "add", "data/choice.md")
	serverGitRun(t, k.Root, "commit", "-m", "theirs")
	theirs, _ := gitx.HeadSHA(k.Root)
	for _, tc := range []struct{ strategy, body, want string }{
		{"ours", "", "ours\n"}, {"theirs", "", "theirs\n"}, {"edit", "edited\n", "edited\n"},
	} {
		t.Run(tc.strategy, func(t *testing.T) {
			got, err := k.resolvedContent(Conflict{ConceptID: "choice", Path: "data/choice.md", LocalSHA: ours, RemoteSHA: theirs, ResolutionStrategy: tc.strategy, ResolutionBody: tc.body})
			if err != nil || got != tc.want {
				t.Fatalf("resolvedContent = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestServerGitConcurrentWritersAreSerialized(t *testing.T) {
	k, _ := newServerGitMount(t)
	var mu sync.Mutex
	active, maxActive := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := k.WithGitLock(func() error {
				mu.Lock()
				active++
				if active > maxActive {
					maxActive = active
				}
				mu.Unlock()
				time.Sleep(time.Millisecond)
				mu.Lock()
				active--
				mu.Unlock()
				return nil
			}); err != nil {
				t.Errorf("WithGitLock: %v", err)
			}
		}()
	}
	wg.Wait()
	if maxActive != 1 {
		t.Fatalf("concurrent server writers entered git section: %d", maxActive)
	}
}

func TestServerGitIfMatchTracksRebasedAndExternallyUpdatedWorkingContent(t *testing.T) {
	k, bare := newServerGitMount(t)
	seed := "---\ntype: Note\ntitle: Item\n---\nseed\n"
	if err := os.WriteFile(filepath.Join(k.Root, "data", "item.md"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	serverGitRun(t, k.Root, "add", "data/item.md")
	serverGitRun(t, k.Root, "commit", "-m", "seed item")
	serverGitRun(t, k.Root, "push", "origin", "main")
	serverGitRun(t, bare, "update-server-info")
	if err := k.ConfigureServerGit(serverConfig()); err != nil {
		t.Fatal(err)
	}
	k.GitSync, k.AutoCommit = true, true

	// A base advance is incorporated before a normal write. The hash read after
	// that rebase is accepted by the real WriteConcept/CommitOp path.
	baseClone := filepath.Join(t.TempDir(), "base")
	serverGitRun(t, filepath.Dir(baseClone), "clone", bare, baseClone)
	if err := os.WriteFile(filepath.Join(baseClone, "data", "base.md"), []byte("---\ntype: Note\n---\nbase\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	serverGitRun(t, baseClone, "add", "data/base.md")
	serverGitRun(t, baseClone, "-c", "user.name=test", "-c", "user.email=test@example.test", "commit", "-m", "base advance")
	serverGitRun(t, baseClone, "push", "origin", "main")
	serverGitRun(t, bare, "update-server-info")
	if _, err := k.SyncIn(); err != nil {
		t.Fatal(err)
	}
	item, err := k.ReadConcept("item")
	if err != nil {
		t.Fatal(err)
	}
	fm, err := okf.ParseFrontmatter(item.FrontmatterRaw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.WriteConcept("item", fm, "after base rebase\n", item.ContentHash); err != nil {
		t.Fatalf("post-rebase if_match write: %v", err)
	}
	if err := k.CommitOp("post rebase update"); err != nil {
		t.Fatal(err)
	}
	if err := k.SyncOut(); err != nil {
		t.Fatal(err)
	}
	postBase, err := k.ReadConcept("item")
	if err != nil {
		t.Fatal(err)
	}

	// A second writer updates the dedicated working branch. SyncOut takes its
	// real non-fast-forward rebase path; the prior hash is then stale.
	workClone := filepath.Join(t.TempDir(), "work")
	serverGitRun(t, filepath.Dir(workClone), "clone", bare, workClone)
	serverGitRun(t, workClone, "checkout", "cartographer/kb")
	if err := os.WriteFile(filepath.Join(workClone, "data", "item.md"), []byte("---\ntype: Note\ntitle: Item\n---\nexternal working update\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	serverGitRun(t, workClone, "add", "data/item.md")
	serverGitRun(t, workClone, "-c", "user.name=test", "-c", "user.email=test@example.test", "commit", "-m", "external work")
	serverGitRun(t, workClone, "push", "origin", "cartographer/kb")
	serverGitRun(t, bare, "update-server-info")
	if err := os.WriteFile(filepath.Join(k.Root, "data", "local.md"), []byte("---\ntype: Note\n---\nlocal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := k.CommitOp("local concurrent write"); err != nil {
		t.Fatal(err)
	}
	if err := k.SyncOut(); err != nil {
		t.Fatal(err)
	}
	if _, err := k.WriteConcept("item", fm, "must reject\n", postBase.ContentHash); err == nil || !strings.Contains(err.Error(), "stale write") {
		t.Fatalf("old if_match after remote working update = %v", err)
	}
}

func serverConfig() ServerGitConfig {
	return ServerGitConfig{BaseBranch: "main", WorkingBranch: "cartographer/kb", Owner: "example", Repository: "wiki", Forge: fakeForge{}}
}

func TestConfigureServerGitStartupFailures(t *testing.T) {
	t.Run("origin missing", func(t *testing.T) {
		k, err := Init(filepath.Join(t.TempDir(), "kb"))
		if err != nil {
			t.Fatal(err)
		}
		if err := k.ConfigureServerGit(serverConfig()); err == nil || !strings.Contains(err.Error(), "origin") {
			t.Fatalf("ConfigureServerGit error = %v", err)
		}
	})
	t.Run("detached", func(t *testing.T) {
		k, _ := newServerGitMount(t)
		serverGitRun(t, k.Root, "checkout", "--detach")
		if err := k.ConfigureServerGit(serverConfig()); err == nil || !strings.Contains(err.Error(), "detached") {
			t.Fatalf("ConfigureServerGit error = %v", err)
		}
	})
	t.Run("base missing", func(t *testing.T) {
		k, _ := newServerGitMount(t)
		cfg := serverConfig()
		cfg.BaseBranch = "absent"
		if err := k.ConfigureServerGit(cfg); err == nil || !strings.Contains(err.Error(), "base branch") {
			t.Fatalf("ConfigureServerGit error = %v", err)
		}
	})
	t.Run("same branch", func(t *testing.T) {
		k, _ := newServerGitMount(t)
		cfg := serverConfig()
		cfg.WorkingBranch = cfg.BaseBranch
		if err := k.ConfigureServerGit(cfg); err == nil || !strings.Contains(err.Error(), "must differ") {
			t.Fatalf("ConfigureServerGit error = %v", err)
		}
	})
	t.Run("local only working branch", func(t *testing.T) {
		k, _ := newServerGitMount(t)
		serverGitRun(t, k.Root, "checkout", "-b", "cartographer/kb")
		serverGitRun(t, k.Root, "checkout", "main")
		if err := k.ConfigureServerGit(serverConfig()); err == nil || !strings.Contains(err.Error(), "no remote counterpart") {
			t.Fatalf("ConfigureServerGit error = %v", err)
		}
	})
}

func TestConfigureServerGitRemoteResumeAndRejectsNonDescendant(t *testing.T) {
	k, bare := newServerGitMount(t)
	if err := k.ConfigureServerGit(serverConfig()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(k.Root, "data", "resume.md"), []byte("---\ntype: Note\n---\nresume\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	serverGitRun(t, k.Root, "add", "data/resume.md")
	serverGitRun(t, k.Root, "commit", "-m", "resume")
	serverGitRun(t, k.Root, "push", "origin", "cartographer/kb")
	serverGitRun(t, bare, "update-server-info")
	// A restart must mount the existing remote working branch, not create a
	// second local branch or reset the published work.
	restarted, err := Open(k.Root)
	if err != nil {
		t.Fatal(err)
	}
	restarted.GitEnv = append(restarted.GitEnv, "GIT_SSL_NO_VERIFY=true")
	if err := restarted.ConfigureServerGit(serverConfig()); err != nil {
		t.Fatalf("resume ConfigureServerGit: %v", err)
	}
	if branch, _ := gitx.Branch(restarted.Root); branch != "cartographer/kb" {
		t.Fatalf("branch = %q", branch)
	}

	// Replace only the remote working ref with unrelated history. Configure
	// must refuse rather than resetting/merging it into the protected base.
	other := filepath.Join(t.TempDir(), "other")
	serverGitRun(t, filepath.Dir(other), "clone", bare, other)
	serverGitRun(t, other, "checkout", "--orphan", "cartographer/kb")
	serverGitRun(t, other, "rm", "-rf", ".")
	if err := os.WriteFile(filepath.Join(other, "unrelated.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	serverGitRun(t, other, "add", "unrelated.txt")
	serverGitRun(t, other, "-c", "user.name=test", "-c", "user.email=test@example.test", "commit", "-m", "unrelated")
	serverGitRun(t, other, "push", "--force", "origin", "cartographer/kb")
	serverGitRun(t, bare, "update-server-info")
	serverGitRun(t, restarted.Root, "checkout", "main")
	if err := restarted.ConfigureServerGit(serverConfig()); err == nil || !strings.Contains(err.Error(), "does not descend") {
		t.Fatalf("non-descendant working branch accepted: %v", err)
	}
}
