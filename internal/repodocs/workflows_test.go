package repodocs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A tag is a pointer its owner can move; a commit SHA is not. Every workflow
// here holds a token or publishes something, so an action reference that is not
// a full SHA with its version in a comment is a supply-chain hole (D1020).
// Dependabot keeps the pins current and rewrites the comment with them.
var (
	usesLine    = regexp.MustCompile(`^\s*(?:-\s+)?uses:\s*(\S+)(.*)$`)
	pinnedUses  = regexp.MustCompile(`^[\w.-]+/[\w./-]+@[0-9a-f]{40}$`)
	versionNote = regexp.MustCompile(`^\s*#\s*v\d`)
)

// unpinnedUses returns one message per `uses:` line that is neither a local
// path nor a SHA pin followed by a `# vX` comment.
func unpinnedUses(name, content string) []string {
	var bad []string
	for i, line := range strings.Split(content, "\n") {
		m := usesLine.FindStringSubmatch(line)
		if m == nil || strings.HasPrefix(m[1], "./") {
			continue
		}
		switch {
		case !pinnedUses.MatchString(m[1]):
			bad = append(bad, fmt.Sprintf("%s:%d: %s is not pinned by a full commit SHA", name, i+1, m[1]))
		case !versionNote.MatchString(m[2]):
			bad = append(bad, fmt.Sprintf("%s:%d: %s has no `# vX.Y.Z` comment", name, i+1, m[1]))
		}
	}
	return bad
}

func TestWorkflowActionsArePinnedBySHA(t *testing.T) {
	cases := map[string]struct {
		content string
		want    int
	}{
		"sha with version":  {"      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1", 0},
		"local path":        {"    uses: ./.github/workflows/demo.yml", 0},
		"comment mentions":  {"#   - Every `uses:` is pinned to a full commit SHA", 0},
		"tag":               {"      - uses: actions/checkout@v7", 1},
		"branch":            {"      - uses: actions/checkout@main", 1},
		"short sha":         {"      - uses: actions/checkout@3d3c42e # v7.0.1", 1},
		"sha without note":  {"      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1", 1},
		"sha, wrong note":   {"      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # latest", 1},
		"subpath action":    {"      - uses: github/codeql-action/init@9b3254085494b55a0f7c4c2b3d2d52cc712ce370 # v4.39.1", 0},
		"subpath, tag only": {"      - uses: github/codeql-action/init@v4", 1},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := unpinnedUses("x.yml", c.content); len(got) != c.want {
				t.Errorf("got %v, want %d finding(s)", got, c.want)
			}
		})
	}

	root := repoRoot(t)
	files, err := filepath.Glob(filepath.Join(root, ".github/workflows/*.yml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflows found (%v)", err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, msg := range unpinnedUses(filepath.Base(f), string(raw)) {
			t.Error(msg)
		}
	}
}

// wfFile is the part of a workflow the token rules below read.
type wfFile struct {
	Permissions map[string]string `yaml:"permissions"`
	Jobs        map[string]struct {
		Permissions map[string]string `yaml:"permissions"`
		Env         map[string]any    `yaml:"env"`
		Steps       []struct {
			Name string         `yaml:"name"`
			Uses string         `yaml:"uses"`
			Run  string         `yaml:"run"`
			Env  map[string]any `yaml:"env"`
			With map[string]any `yaml:"with"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func loadWorkflows(t *testing.T) map[string]wfFile {
	t.Helper()
	root := repoRoot(t)
	files, err := filepath.Glob(filepath.Join(root, ".github/workflows/*.yml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflows found (%v)", err)
	}
	out := map[string]wfFile{}
	for _, f := range files {
		var wf wfFile
		if err := yaml.Unmarshal([]byte(readFile(t, root, filepath.Join(".github/workflows", filepath.Base(f)))), &wf); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out[filepath.Base(f)] = wf
	}
	return out
}

var setupAction = regexp.MustCompile(`^actions/setup-(go|node|python|java)@`)

// writeTokenCacheInput returns what a setup step of a write-token job gets wrong
// about its cache input. setup-node v7 refuses `cache: false` (the input names a
// package manager) and switches off its automatic cache with
// `package-manager-cache: false`; the other setup actions take `cache: false`.
func writeTokenCacheInput(uses string, with map[string]any) []string {
	var out []string
	v, has := with["cache"]
	isNode := strings.HasPrefix(uses, "actions/setup-node@")
	if has {
		s := fmt.Sprint(v)
		switch {
		case isNode && s == "false":
			out = append(out, "cache: false is refused by setup-node v7; use package-manager-cache: false")
		case s != "false" && s != "":
			out = append(out, fmt.Sprintf("cache: %v on a job with a write token", v))
		}
	}
	if isNode {
		if fmt.Sprint(with["package-manager-cache"]) != "false" {
			out = append(out, "a setup-node step with a write token needs an explicit package-manager-cache: false")
		}
	} else if setupAction.MatchString(uses) && !has {
		// The setup actions cache by default: the input must be there, and off.
		out = append(out, "a setup action with a write token needs an explicit cache: false")
	}
	return out
}

func TestWriteTokenCacheInput(t *testing.T) {
	const node = "actions/setup-node@sha"
	for name, c := range map[string]struct {
		uses string
		with map[string]any
		bad  bool
	}{
		"node cache false":         {node, map[string]any{"cache": false}, true},
		"node package-manager off": {node, map[string]any{"package-manager-cache": false}, false},
		"node neither":             {node, map[string]any{}, true},
		"node cache npm":           {node, map[string]any{"cache": "npm", "package-manager-cache": false}, true},
		"go cache false":           {"actions/setup-go@sha", map[string]any{"cache": false}, false},
		"go neither":               {"actions/setup-go@sha", map[string]any{}, true},
	} {
		if got := len(writeTokenCacheInput(c.uses, c.with)) > 0; got != c.bad {
			t.Errorf("%s: flagged = %v, want %v", name, got, c.bad)
		}
	}
}

// The film jobs run the checked-out ref's code (`npm ci`, Playwright, vite), so
// setup-node's automatic package-manager cache must be decided explicitly, never
// left on by default: film-check (a pull request, untrusted) turns it off, and
// film.yml's render job turns it on only when the `trusted` output says main
// (D1068). A setup-node step without `package-manager-cache` would restore and save
// whatever the untrusted ref gets from the package.json auto-detection. The `cache`
// input is pinned too: absent or empty in film-check, and exactly the `trusted`
// expression in film.yml, so an unconditional `cache: npm` fails.
func TestFilmJobsSetPackageManagerCache(t *testing.T) {
	const trusted = "${{ steps.needed.outputs.trusted == 'true' }}"
	wfs := loadWorkflows(t)
	const cacheOnMain = "${{ steps.needed.outputs.trusted == 'true' && 'npm' || '' }}"
	for _, c := range []struct{ file, job, want, wantCache string }{
		{"film-check.yml", "render", "false", ""},
		{"film.yml", "render", trusted, cacheOnMain},
	} {
		job, ok := wfs[c.file].Jobs[c.job]
		if !ok {
			t.Fatalf("%s has no job %s", c.file, c.job)
		}
		nodes := 0
		for i, st := range job.Steps {
			if !strings.HasPrefix(st.Uses, "actions/setup-node@") {
				continue
			}
			nodes++
			gotCache := ""
			if cv := st.With["cache"]; cv != nil {
				gotCache = fmt.Sprint(cv)
			}
			if gotCache != c.wantCache {
				t.Errorf("%s job %s step %d: cache = %q, want %q (empty means the input is absent)", c.file, c.job, i+1, gotCache, c.wantCache)
			}
			v, has := st.With["package-manager-cache"]
			if !has {
				t.Errorf("%s job %s step %d: setup-node has no package-manager-cache (the automatic cache stays on for an untrusted ref)", c.file, c.job, i+1)
				continue
			}
			if got := fmt.Sprint(v); got != c.want {
				t.Errorf("%s job %s step %d: package-manager-cache = %q, want %q", c.file, c.job, i+1, got, c.want)
			}
		}
		if nodes == 0 {
			t.Errorf("%s job %s: no setup-node step found, the test checks nothing", c.file, c.job)
		}
	}
}

// A job that holds a write token restores no cache a past run, or a run of another
// ref, left behind, and builds from none (D1020): no `cache:` input other than off,
// no actions/cache step, and a `type=gha` image cache only when scoped to the run.
func TestWriteTokenJobsRestoreNoSharedCache(t *testing.T) {
	for name, wf := range loadWorkflows(t) {
		for id, job := range wf.Jobs {
			perms := job.Permissions
			if perms == nil {
				perms = wf.Permissions
			}
			if perms["contents"] != "write" && perms["packages"] != "write" {
				continue
			}
			for i, st := range job.Steps {
				where := fmt.Sprintf("%s job %s step %d (%s)", name, id, i+1, st.Name+st.Uses)
				if strings.HasPrefix(st.Uses, "actions/cache") {
					t.Errorf("%s: a job with a write token restores a cache", where)
				}
				for _, msg := range writeTokenCacheInput(st.Uses, st.With) {
					t.Errorf("%s: %s", where, msg)
				}
				for _, k := range []string{"cache-from", "cache-to"} {
					if v, ok := st.With[k].(string); ok && strings.Contains(v, "type=gha") && !strings.Contains(v, "scope=${{ github.run_id }}") {
						t.Errorf("%s: %s: %s is not scoped to the run", where, k, v)
					}
				}
			}
		}
	}
}

// The branch's own code (make, go, a repository script) never runs in a step that
// holds the release PAT: it is set where it is needed, and only gh commands use it.
func TestReleasePATStepsRunNoRepositoryCode(t *testing.T) {
	code := regexp.MustCompile(`(^|[\s;&|(])(make|go|npm|npx|bash|sh)\s|(^|[\s;&|(])(\./|scripts/|web/|test/)`)
	seen := 0
	for name, wf := range loadWorkflows(t) {
		for id, job := range wf.Jobs {
			for i, st := range job.Steps {
				holds := false
				for _, env := range []map[string]any{job.Env, st.Env} {
					for _, v := range env {
						if strings.Contains(fmt.Sprint(v), "secrets.RELEASE_PLEASE_TOKEN") {
							holds = true
						}
					}
				}
				if !holds {
					continue
				}
				seen++
				if code.MatchString(st.Run) {
					t.Errorf("%s job %s step %d holds RELEASE_PLEASE_TOKEN and runs repository code:\n%s", name, id, i+1, st.Run)
				}
			}
		}
	}
	if seen == 0 {
		t.Error("no step holding RELEASE_PLEASE_TOKEN found: the test reads nothing")
	}
}

// A Python package installed by a workflow or the Makefile is checked against a
// hash lock: `pip install` names `--require-hashes`, and installs from a file.
func TestPipInstallsRequireHashes(t *testing.T) {
	root := repoRoot(t)
	files, _ := filepath.Glob(filepath.Join(root, ".github/workflows/*.yml"))
	files = append(files, filepath.Join(root, "Makefile"))
	install := regexp.MustCompile(`\bpipx? (run|install)\b`)
	for _, f := range files {
		for i, line := range strings.Split(readFile(t, filepath.Dir(f), filepath.Base(f)), "\n") {
			l := strings.TrimSpace(line)
			if strings.HasPrefix(l, "#") || !install.MatchString(l) {
				continue
			}
			if !strings.Contains(l, "--require-hashes") || !strings.Contains(l, "--only-binary=:all:") || !strings.Contains(l, " -r ") {
				t.Errorf("%s:%d: %q installs without --require-hashes --only-binary=:all: -r <lock>", filepath.Base(f), i+1, l)
			}
		}
	}
	for _, lock := range []string{"docs", "zizmor"} {
		if !strings.Contains(readFile(t, root, ".github/pip/"+lock+".txt"), "--hash=sha256:") {
			t.Errorf(".github/pip/%s.txt carries no hashes", lock)
		}
	}
}

// scripts/dev.sh keeps the generated test KBs under the user's own cache
// directory, mode 700, whatever TMPDIR is.
func TestTestKBsCacheDirIsPrivate(t *testing.T) {
	bash := bashOrSkip(t)
	home := t.TempDir()
	cmd := exec.Command(bash, "scripts/testkbs-cache-dir.sh")
	cmd.Dir = repoRoot(t)
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")} // no TMPDIR, no XDG_CACHE_HOME
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("testkbs-cache-dir.sh: %v", err)
	}
	dir := strings.TrimSpace(string(out))
	if !strings.HasPrefix(dir, home+string(filepath.Separator)) {
		t.Errorf("cache dir %q is not under HOME %q", dir, home)
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("cache dir mode = %v (%v), want 0700", fi, err)
	}
	if strings.Contains(readFile(t, repoRoot(t), "scripts/dev.sh"), "/tmp/cartographer-testkbs") {
		t.Error("scripts/dev.sh still names a cache directory in /tmp")
	}
}
