package landertest

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// readyCheckPlan runs scripts/ready-check-plan.sh on paths, from the repository root, and
// returns its name=value lines.
func readyCheckPlan(t *testing.T, paths ...string) map[string]string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH (affected-pkgs.sh runs go list)")
	}
	cmd := exec.Command("bash", "scripts/ready-check-plan.sh")
	cmd.Dir = repoRoot(t)
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\n") + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ready-check-plan.sh: %v\n%s", err, out)
	}
	got := map[string]string{}
	for l := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			t.Fatalf("not a name=value line: %q\n%s", l, out)
		}
		got[k] = v
	}
	for _, k := range []string{"pkgs", "count", "shards", "web"} {
		if _, ok := got[k]; !ok {
			t.Fatalf("no %s line:\n%s", k, out)
		}
	}
	return got
}

// What the hosted pre-check of a queued change runs (ready-check.yml): the Windows tests
// of the packages a code change can break, in as many shards as their weight needs, and the
// frontend's unit tests when it touches the frontend. The shard list feeds a job matrix, so
// it is a JSON array that is never empty, and it always matches the count.
func TestReadyCheckPlan(t *testing.T) {
	needBash(t)
	cases := []struct {
		name               string
		paths              []string
		pkgs, count, shard string
		web                string
	}{
		{"documents have no Windows leg", []string{"docs/testing.md", "CONTRIBUTING.md"}, "", "1", "[0]", "false"},
		{"nothing in, nothing to run", nil, "", "1", "[0]", "false"},
		{"the module selects everything, in four shards", []string{"go.sum"}, "./...", "4", "[0,1,2,3]", "false"},
		{"the frontend runs its unit tests", []string{"web/src/lib/health.ts"}, "-", "-", "-", "true"},
		{"the bundle runs them too", []string{"internal/webui/dist/provenance.json"}, "-", "-", "-", "true"},
		{"the control plane is the slow package", []string{"internal/mcpserver/server.go"}, "-", "4", "[0,1,2,3]", "false"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := readyCheckPlan(t, c.paths...)
			for k, want := range map[string]string{"pkgs": c.pkgs, "count": c.count, "shards": c.shard, "web": c.web} {
				if want != "-" && got[k] != want {
					t.Errorf("%s = %q, want %q (plan %v)", k, got[k], want, got)
				}
			}
			// The count follows the packages, whatever the change.
			n, slow := 0, false
			for p := range strings.FieldsSeq(got["pkgs"]) {
				n++
				slow = slow || p == "./..." || p == "./internal/mcpserver" || p == "./cmd/cartographer"
			}
			want := "1"
			if slow {
				want = "4"
			} else if n > 4 {
				want = "2"
			}
			if got["count"] != want {
				t.Errorf("count = %s for %d package(s) (slow: %v), want %s", got["count"], n, slow, want)
			}
			shards := map[string]string{"1": "[0]", "2": "[0,1]", "4": "[0,1,2,3]"}[got["count"]]
			if got["shards"] != shards {
				t.Errorf("shards = %s, want %s for count %s", got["shards"], shards, got["count"])
			}
		})
	}
}

// chainRepo is a bare upstream and a clone to commit in, for scripts/ready-check-base.sh.
type chainRepo struct {
	t             *testing.T
	up, work, tmp string
	env           []string
}

func newChainRepo(t *testing.T) *chainRepo {
	t.Helper()
	needBash(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	tmp := t.TempDir()
	r := &chainRepo{t: t, tmp: tmp, up: filepath.Join(tmp, "up.git"), work: filepath.Join(tmp, "work")}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") && !strings.HasPrefix(kv, "READY_CHECK_") {
			r.env = append(r.env, kv)
		}
	}
	// No identity anywhere: the script commits its replay with its own.
	r.env = append(r.env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	r.git(tmp, "init", "-q", "--bare", "-b", "main", r.up)
	r.git(tmp, "clone", "-q", r.up, r.work)
	r.git(r.work, "checkout", "-q", "-b", "main")
	r.commit("", "init", map[string]string{"a.txt": "a\n"})
	r.git(r.work, "push", "-q", "origin", "HEAD:refs/heads/main")
	return r
}

func (r *chainRepo) git(dir string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=user", "-c", "user.email=user@example.com"}, args...)...)
	cmd.Dir, cmd.Env = dir, r.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit commits files on from (the current HEAD when empty) and returns the new sha.
func (r *chainRepo) commit(from, msg string, files map[string]string) string {
	r.t.Helper()
	if from != "" {
		r.git(r.work, "checkout", "-q", "--detach", from)
	}
	for p, body := range files {
		path := filepath.Join(r.work, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			r.t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			r.t.Fatal(err)
		}
	}
	r.git(r.work, "add", ".")
	r.git(r.work, "commit", "-q", "-m", msg)
	return r.git(r.work, "rev-parse", "HEAD")
}

func (r *chainRepo) push(sha, branch string) {
	r.t.Helper()
	r.git(r.work, "push", "-q", "--force", "origin", sha+":refs/heads/"+branch)
}

// replay checks out head as the pre-check's checkout does (every branch of origin) and runs
// ready-check-base.sh there: its stdout, its stderr, the checkout and whether it succeeded.
func (r *chainRepo) replay(head string) (stdout, stderr, dir string, ok bool) {
	r.t.Helper()
	dir, err := os.MkdirTemp(r.tmp, "check-")
	if err != nil {
		r.t.Fatal(err)
	}
	r.git(r.tmp, "clone", "-q", "--no-checkout", r.up, dir)
	r.git(dir, "checkout", "-q", "--detach", head)
	cmd := exec.Command("bash", filepath.Join(repoRoot(r.t), "scripts", "ready-check-base.sh"))
	var o, e bytes.Buffer
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = dir, append(append([]string{}, r.env...), "TMPDIR="+r.tmp), &o, &e
	err = cmd.Run()
	return o.String(), e.String(), dir, err == nil
}

// The pre-check tests a queued change's chain (it and the queued changes its Lander-After
// trailers name), replayed on the current main: the unit the lander would land, with what
// landed since its changes were queued. The paths the plan reads are those of each queued commit's own diff.
func TestReadyCheckBaseReplaysTheQueuedChainOnMain(t *testing.T) {
	// AC-P3-8: a change queued before a fix landed is tested with the fix, not rebuilt.
	// AC-P3-9: the tip's paths, so its Windows packages, are those of the whole chain.
	t.Run("a chain of two on a main that moved", func(t *testing.T) {
		r := newChainRepo(t)
		m0 := r.git(r.work, "rev-parse", "HEAD")
		a := r.commit(m0, "feat: a", map[string]string{"internal/kb/qa.go": "a\n"})
		r.push(a, "ready/a")
		// The full trailer block, as ready.sh writes it.
		b := r.commit(a, "feat: b\n\nCloses #7\n\nLander-After: a\nLander-Hold: yes\nReview: full, 2 rounds\nLander-Tip: "+a,
			map[string]string{"web/qb.ts": "b\n"})
		r.push(b, "ready/b")
		fix := r.commit(m0, "fix: the flake", map[string]string{"fix.txt": "fixed\n"})
		r.push(fix, "main")
		out, errOut, dir, ok := r.replay(b)
		if !ok {
			t.Fatalf("the chain must replay:\n%s", errOut)
		}
		if got := strings.Fields(out); !slices.Equal(got, []string{"internal/kb/qa.go", "web/qb.ts"}) {
			t.Errorf("paths = %q, want both changes' own (not main's fix.txt)", got)
		}
		if p := r.git(dir, "rev-parse", "HEAD~2"); p != fix {
			t.Errorf("the chain must sit on the current main %s, HEAD~2 is %s", fix, p)
		}
		for _, f := range []string{"fix.txt", "internal/kb/qa.go", "web/qb.ts"} {
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
				t.Errorf("the replayed tree lacks %s: %v", f, err)
			}
		}
		if subj := r.git(dir, "log", "-2", "--format=%s|%an"); subj != "feat: b|user\nfeat: a|user" {
			t.Errorf("each queued commit replays with its message and author, got %q", subj)
		}
	})
	t.Run("a change on a landed predecessor's branch head replays alone", func(t *testing.T) {
		r := newChainRepo(t)
		m0 := r.git(r.work, "rev-parse", "HEAD")
		p1 := r.commit(m0, "wip p 1", map[string]string{"p.txt": "p\n"})
		p2 := r.commit(p1, "wip p 2", map[string]string{"p2.txt": "p2\n"})
		landed := r.commit(m0, "feat: p", map[string]string{"p.txt": "p\n", "p2.txt": "p2\n"})
		r.push(landed, "main")
		c := r.commit(p2, "feat: c\n\nLander-After: p", map[string]string{"c.txt": "c\n"})
		r.push(c, "ready/c")
		out, errOut, dir, ok := r.replay(c)
		if !ok {
			t.Fatalf("the change must replay:\n%s", errOut)
		}
		if got := strings.Fields(out); !slices.Equal(got, []string{"c.txt"}) {
			t.Errorf("paths = %q, want c.txt only (main holds p squashed)", got)
		}
		if p := r.git(dir, "rev-parse", "HEAD^"); p != landed {
			t.Errorf("c must replay alone on main %s, HEAD^ is %s", landed, p)
		}
	})
	t.Run("a member main already holds replays empty", func(t *testing.T) {
		r := newChainRepo(t)
		m0 := r.git(r.work, "rev-parse", "HEAD")
		a := r.commit(m0, "feat: a", map[string]string{"qa.txt": "a\n"})
		r.push(a, "ready/a")
		b := r.commit(a, "feat: b\n\nLander-After: a", map[string]string{"qb.txt": "b\n"})
		r.push(b, "ready/b")
		r.push(r.commit(m0, "feat: a, landed", map[string]string{"qa.txt": "a\n"}), "main")
		if out, errOut, _, ok := r.replay(b); !ok || !slices.Equal(strings.Fields(out), []string{"qa.txt", "qb.txt"}) {
			t.Fatalf("ok=%v paths=%q\n%s", ok, out, errOut)
		}
	})
	// The chain is linked as the lander links a unit, by Lander-After: a predecessor queued again
	// at a new commit is replayed at that commit, not at the tip's parent.
	t.Run("a predecessor queued again replays at its new commit", func(t *testing.T) {
		r := newChainRepo(t)
		m0 := r.git(r.work, "rev-parse", "HEAD")
		a := r.commit(m0, "feat: a", map[string]string{"qa.txt": "a\n"})
		r.push(a, "ready/a")
		b := r.commit(a, "feat: b\n\nLander-After: a", map[string]string{"qb.txt": "b\n"})
		r.push(b, "ready/b")
		a2 := r.commit(m0, "feat: a, fixed", map[string]string{"qa.txt": "a2\n", "qa2.txt": "a2\n"})
		r.push(a2, "ready/a")
		out, errOut, dir, ok := r.replay(b)
		if !ok || !slices.Equal(strings.Fields(out), []string{"qa.txt", "qa2.txt", "qb.txt"}) {
			t.Fatalf("ok=%v paths=%q\n%s", ok, out, errOut)
		}
		if body, err := os.ReadFile(filepath.Join(dir, "qa2.txt")); err != nil || string(body) != "a2\n" {
			t.Errorf("a must replay at its new commit: qa2.txt %q, %v", body, err)
		}
		if p := r.git(dir, "rev-parse", "HEAD~2"); p != m0 {
			t.Errorf("HEAD~2 = %s, want main %s", p, m0)
		}
	})
	// Fail closed: a commit that is no ready/ tip in the checkout (refs missing, or the change
	// queued again since) is not tested alone.
	t.Run("a head that is no ready tip is red", func(t *testing.T) {
		r := newChainRepo(t)
		m0 := r.git(r.work, "rev-parse", "HEAD")
		x := r.commit(m0, "feat: x", map[string]string{"qx.txt": "x\n"})
		r.push(x, "ready/x")
		r.push(r.commit(m0, "feat: x, again", map[string]string{"qx.txt": "x2\n"}), "ready/x")
		out, errOut, _, ok := r.replay(x)
		if ok || out != "" || !strings.Contains(errOut, "ready-check: HEAD "+x+" is not the tip of a ready/ ref on origin") {
			t.Fatalf("ok=%v out=%q\n%s", ok, out, errOut)
		}
	})
	// The lander links by a plain slug match (scripts/land.sh units): a trailer that is a rev
	// expression, or that names the change itself, links nothing, and the tip replays alone.
	for _, after := range []string{"a~1", "a^", "a^{/init}", "b"} {
		t.Run("Lander-After: "+after+" links nothing", func(t *testing.T) {
			r := newChainRepo(t)
			m0 := r.git(r.work, "rev-parse", "HEAD")
			r.push(r.commit(m0, "feat: a", map[string]string{"qa.txt": "a\n"}), "ready/a")
			b := r.commit(m0, "feat: b\n\nLander-After: "+after+"\nLander-Tip: "+m0, map[string]string{"qb.txt": "b\n"})
			r.push(b, "ready/b")
			out, errOut, dir, ok := r.replay(b)
			if !ok || !slices.Equal(strings.Fields(out), []string{"qb.txt"}) {
				t.Fatalf("ok=%v paths=%q\n%s", ok, out, errOut)
			}
			if p := r.git(dir, "rev-parse", "HEAD^"); p != m0 {
				t.Errorf("b must replay alone on main %s, HEAD^ is %s", m0, p)
			}
		})
	}
	t.Run("Lander-After lines that loop are red", func(t *testing.T) {
		r := newChainRepo(t)
		m0 := r.git(r.work, "rev-parse", "HEAD")
		r.push(r.commit(m0, "feat: a\n\nLander-After: b", map[string]string{"qa.txt": "a\n"}), "ready/a")
		b := r.commit(m0, "feat: b\n\nLander-After: a", map[string]string{"qb.txt": "b\n"})
		r.push(b, "ready/b")
		out, errOut, _, ok := r.replay(b)
		if ok || out != "" || !strings.Contains(errOut, "ready-check: the Lander-After lines loop at a") {
			t.Fatalf("ok=%v out=%q\n%s", ok, out, errOut)
		}
	})
	t.Run("a chain that does not apply is red", func(t *testing.T) {
		r := newChainRepo(t)
		m0 := r.git(r.work, "rev-parse", "HEAD")
		x := r.commit(m0, "feat: x", map[string]string{"a.txt": "x\n"})
		r.push(x, "ready/x")
		r.push(r.commit(m0, "fix: a", map[string]string{"a.txt": "main\n"}), "main")
		out, errOut, _, ok := r.replay(x)
		if ok || out != "" {
			t.Fatalf("a conflicting replay must fail and print no path: ok=%v out=%q", ok, out)
		}
		if want := "ready-check: the chain does not apply on main: a.txt\n"; !strings.HasSuffix(errOut, want) {
			t.Errorf("stderr must end with %q, got:\n%s", want, errOut)
		}
	})
	// Two frontend changes always conflict in the committed bundle: a conflict there only is not
	// red. Main's bundle is kept, the sources are the change's, and the jobs learn the bundle is
	// stale from $GITHUB_OUTPUT; a conflict elsewhere beside it is still red.
	t.Run("a conflict in the bundle only is not red", func(t *testing.T) {
		r := newChainRepo(t)
		m0 := r.git(r.work, "rev-parse", "HEAD")
		// The change also adds a hashed asset main does not have: it goes with the change's bundle.
		x := r.commit(m0, "feat: x", map[string]string{"internal/webui/dist/index.js": "x\n", "internal/webui/dist/assets/x-1a2b.js": "x\n", "web/src/x.ts": "x\n"})
		r.push(x, "ready/x")
		r.push(r.commit(m0, "fix: other ui", map[string]string{"internal/webui/dist/index.js": "main\n"}), "main")
		ghOut := filepath.Join(r.tmp, "github-output")
		r.env = append(r.env, "GITHUB_OUTPUT="+ghOut)
		out, errOut, dir, ok := r.replay(x)
		if !ok || !slices.Equal(strings.Fields(out), []string{"internal/webui/dist/assets/x-1a2b.js", "internal/webui/dist/index.js", "web/src/x.ts"}) || !strings.Contains(errOut, "bundle=stale") {
			t.Fatalf("ok=%v paths=%q\n%s", ok, out, errOut)
		}
		if b, _ := os.ReadFile(filepath.Join(dir, "internal/webui/dist/index.js")); string(b) != "main\n" {
			t.Errorf("main's bundle is kept, got %q", b)
		}
		if _, err := os.Stat(filepath.Join(dir, "internal/webui/dist/assets/x-1a2b.js")); err == nil {
			t.Error("the change's new asset stays in main's bundle")
		}
		if b, _ := os.ReadFile(ghOut); string(b) != "bundle=stale\n" {
			t.Errorf("$GITHUB_OUTPUT = %q, want bundle=stale", b)
		}
		y := r.commit(m0, "feat: y", map[string]string{"internal/webui/dist/index.js": "y\n", "a.txt": "y\n"})
		r.push(y, "ready/y")
		r.push(r.commit(r.git(r.work, "rev-parse", "HEAD"), "fix: a", map[string]string{"a.txt": "main\n", "internal/webui/dist/index.js": "main2\n"}), "main")
		if _, errOut, _, ok := r.replay(y); ok || !strings.Contains(errOut, "does not apply on main: ") {
			t.Errorf("a conflict outside the bundle is red: ok=%v\n%s", ok, errOut)
		}
	})
	t.Run("a single change on the current main", func(t *testing.T) {
		r := newChainRepo(t)
		m0 := r.git(r.work, "rev-parse", "HEAD")
		d := r.commit(m0, "feat: d", map[string]string{"notes/d.txt": "d\n", "a.txt": "d\n"})
		r.push(d, "ready/d")
		out, errOut, dir, ok := r.replay(d)
		if !ok || !slices.Equal(strings.Fields(out), []string{"a.txt", "notes/d.txt"}) {
			t.Fatalf("ok=%v paths=%q\n%s", ok, out, errOut)
		}
		if p := r.git(dir, "rev-parse", "HEAD^"); p != m0 {
			t.Errorf("HEAD^ = %s, want main %s", p, m0)
		}
	})
}

// Every job of ready-check.yml tests the replayed chain: its checkout holds every branch
// (fetch-depth: 0, for origin/main and the ready/ refs) and the step right after it is the
// replay. The plan job reads the paths from a file, never through a pipe, whose exit status
// would hide a replay that failed.
func TestReadyCheckJobsReplayTheChain(t *testing.T) {
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Uses string         `yaml:"uses"`
				Run  string         `yaml:"run"`
				With map[string]any `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(readFile(t, repoRoot(t), ".github/workflows/ready-check.yml")), &wf); err != nil {
		t.Fatal(err)
	}
	checkouts := 0
	for name, job := range wf.Jobs {
		for i, st := range job.Steps {
			if !strings.HasPrefix(st.Uses, "actions/checkout@") {
				continue
			}
			checkouts++
			if fmt.Sprint(st.With["fetch-depth"]) != "0" {
				t.Errorf("%s: the checkout must fetch every branch (fetch-depth: 0), got %v", name, st.With["fetch-depth"])
			}
			if i+1 >= len(job.Steps) || !strings.HasPrefix(job.Steps[i+1].Run, "scripts/ready-check-base.sh >") {
				t.Errorf("%s: the step after the checkout must replay the chain (scripts/ready-check-base.sh)", name)
			}
			if name == "plan" {
				// The plan reads the very file the replay wrote: another one would be empty,
				// so no Windows leg and a green verdict.
				const paths = `"$RUNNER_TEMP/paths"`
				if run := strings.TrimSpace(job.Steps[i+1].Run); run != "scripts/ready-check-base.sh > "+paths {
					t.Errorf("plan: the replay must write the paths to %s, unpiped: %q", paths, run)
				}
				read := false
				for _, later := range job.Steps[i+2:] {
					read = read || strings.Contains(later.Run, "scripts/ready-check-plan.sh < "+paths)
				}
				if !read {
					t.Errorf("plan: no later step runs scripts/ready-check-plan.sh < %s", paths)
				}
			}
		}
	}
	if j, ok := wf.Jobs["readers"]; !ok || !strings.Contains(j.Steps[len(j.Steps)-1].Run, "./internal/repodocs/... ./internal/docskb ./internal/boundaries ./internal/webui") {
		t.Errorf("the readers job runs the tests that read the repository on the replayed chain")
	}
	if checkouts != 4 {
		t.Errorf("ready-check.yml has %d checkouts, want 4 (plan, windows, web, readers)", checkouts)
	}
}
