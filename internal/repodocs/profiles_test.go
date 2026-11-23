package repodocs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// profileRows reads scripts/profiles.tsv of the repository: the data every profile reader shares.
func profileRows(t *testing.T) [][]string {
	t.Helper()
	var rows [][]string
	for i, line := range strings.Split(readFile(t, repoRoot(t), "scripts/profiles.tsv"), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 7 {
			t.Fatalf("scripts/profiles.tsv:%d: want seven tab-separated fields, got %d", i+1, len(f))
		}
		rows = append(rows, f)
	}
	return rows
}

// The file parses: seven fields, unique names, only the values the readers know, and a path
// pattern never beside a required trailer (the message is checked without the paths).
func TestProfilesFileParses(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range profileRows(t) {
		if !regexp.MustCompile(`^[a-z][a-z0-9-]*$`).MatchString(f[0]) || seen[f[0]] {
			t.Errorf("profile name %q is not a lowercase word, or is listed twice", f[0])
		}
		seen[f[0]] = true
		if _, err := regexp.Compile(f[2]); err != nil {
			t.Errorf("%s: the path pattern %q: %v", f[0], f[2], err)
		}
		if f[3] != "full" && f[3] != "-" || f[5] != "held" && f[5] != "-" || f[6] != "no" && f[6] != "-" {
			t.Errorf("%s: unknown tier %q, freeze %q or standing %q", f[0], f[3], f[5], f[6])
		}
		if f[4] != "-" {
			for _, tn := range strings.Split(f[4], ",") {
				if tn != "Found-by" && tn != "Introduced-by" {
					t.Errorf("%s: %q is not a trailer scripts/trailers-lint.sh checks", f[0], tn)
				}
			}
		}
		if f[4] != "-" && f[2] != "-" {
			t.Errorf("%s: a path pattern and required trailers", f[0])
		}
	}
	for _, want := range []string{"security", "process", "fix", "feature", "live"} {
		if !seen[want] {
			t.Errorf("the profile %s is missing", want)
		}
	}
}

// AC-P3-19: no script holds a second copy of the invariant-area or supply-chain pattern.
func TestNoScriptHoldsASecondCopyOfTheProfilePatterns(t *testing.T) {
	root := repoRoot(t)
	files, _ := filepath.Glob(filepath.Join(root, "scripts", "*"))
	for _, f := range files {
		if strings.HasSuffix(f, "profiles.tsv") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, frag := range []string{"internal/(mcpserver|provisioning", "(AGENTS|CLAUDE)\\.md$", "INVARIANT_AREAS=", "SUPPLY_CHAIN="} {
			if strings.Contains(string(b), frag) {
				t.Errorf("%s holds %q: that pattern is a row of scripts/profiles.tsv", filepath.Base(f), frag)
			}
		}
	}
}

// AC-P3-23: a change that matches a path row and a type row gets the strictest value of every
// column. Every other fixture keeps its tier and its warnings.
func TestPRTriageAppliesEveryMatchingProfile(t *testing.T) {
	e := newLandEnv(t)
	run := func(branch, body string, files map[string]string) string {
		t.Helper()
		e.git("checkout", "-q", "-B", branch, "main")
		e.commitFiles("wip", files)
		out, errb, code := e.acScript("pr-triage.sh", []string{"TRIAGE_BODY=" + body})
		if code != 0 {
			t.Fatalf("pr-triage.sh: exit %d\n%s%s", code, out, errb)
		}
		return out
	}
	out := run("feat/a", "fix(mcp): x\n\nFound-by: gate\n", map[string]string{"internal/mcpserver/x.go": "package mcpserver\n"})
	for _, want := range []string{"Profile: security + fix\n", "Tier: full — security: internal/mcpserver/x.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("a fix in an invariant area is both, full tier: lacks %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "supply-chain") {
		t.Errorf("no supply-chain warning for an invariant area\n%s", out)
	}
	out = run("feat/b", "feat(dev): x\n", map[string]string{"scripts/new.sh": "#!/bin/sh\n"})
	if !strings.Contains(out, "Profile: process + feature\n") || !strings.Contains(out, "Tier: full") || !strings.Contains(out, "WARN: supply-chain files changed") {
		t.Errorf("a feat under scripts/ is process + feature, full tier, with the warning\n%s", out)
	}
	out = run("feat/c", "chore(kb): x\n", map[string]string{"internal/kb/x.go": "package kb\n"})
	if !strings.Contains(out, "Profile: other\n") || !strings.Contains(out, "Tier: light\n") || strings.Contains(out, "supply-chain") {
		t.Errorf("a change no row matches is `other`, tiered by size\n%s", out)
	}
	out = run("feat/d", "feat(kb)!: x\n", map[string]string{"internal/kb/y.go": "package kb\n"})
	if !strings.Contains(out, "Profile: feature\n") {
		t.Errorf("a breaking change is a feature row\n%s", out)
	}
	out = run("feat/e", "chore(kb): x\n\nProfile: live\n", map[string]string{"internal/kb/z.go": "package kb\n"})
	if !strings.Contains(out, "Profile: live\n") {
		t.Errorf("a declared profile applies\n%s", out)
	}
}

// AC-P3-18: changing a tier floor or a required trailer is a one-row diff: with the `feature`
// row requiring the trailers of a fix, a feat owes them, and nothing else changed.
func TestProfilesARequiredTrailerIsOneRow(t *testing.T) {
	e := newLandEnv(t)
	msg := "feat(kb): x\n\nWhy.\n"
	if out, ok := e.trailersLint(msg); !ok {
		t.Fatalf("a feat owes no trailer\n%s", out)
	}
	tsv := filepath.Join(e.work, "scripts", "profiles.tsv")
	body := readFileAbs(t, tsv)
	e.write(tsv, strings.Replace(body, "feature\tfeat,perf,refactor,breaking\t-\t-\t-\theld", "feature\tfeat,perf,refactor,breaking\t-\t-\tFound-by,Introduced-by\theld", 1))
	if out, ok := e.trailersLint(msg); ok || !strings.Contains(out, "Found-by") {
		t.Errorf("one edited row makes a feat owe the trailers (ok=%v)\n%s", ok, out)
	}
	if out, ok := e.trailersLint("feat(kb): x\n\nFound-by: gate\nIntroduced-by: unknown\n"); !ok {
		t.Errorf("and the lines satisfy it\n%s", out)
	}
}

// A file that is missing, or a row with the wrong number of fields, exits 2 naming the row, in
// the triage and in the trailers check.
func TestProfilesAMalformedFileFailsClosed(t *testing.T) {
	e := newLandEnv(t)
	e.git("checkout", "-q", "-B", "feat/m", "main")
	e.commitFiles("wip", map[string]string{"internal/kb/m.go": "package kb\n"})
	tsv := filepath.Join(e.work, "scripts", "profiles.tsv")
	good := readFileAbs(t, tsv)
	check := func(label string) {
		t.Helper()
		out, errb, code := e.acScript("pr-triage.sh", []string{"TRIAGE_BODY=chore(kb): x\n"})
		if code != 2 || (label == "row" && !strings.Contains(errb, "row 'broken'")) {
			t.Errorf("%s: pr-triage.sh exits %d, want 2\n%s%s", label, code, out, errb)
		}
		m := filepath.Join(e.tmp, "m")
		e.write(m, "fix(kb): x\n\nFound-by: gate\nIntroduced-by: unknown\n")
		out, errb, code = e.acScript("trailers-lint.sh", nil, m)
		if code != 2 || (label == "row" && !strings.Contains(errb, "row 'broken'")) {
			t.Errorf("%s: trailers-lint.sh exits %d, want 2\n%s%s", label, code, out, errb)
		}
	}
	e.write(tsv, good+"broken\t*\t-\n")
	check("row")
	if err := os.Remove(tsv); err != nil {
		t.Fatal(err)
	}
	check("missing")
	// A path row that requires trailers is a malformed file for the trailers check.
	e.write(tsv, good+"odd\t*\t^x/\t-\tFound-by\t-\t-\n")
	m := filepath.Join(e.tmp, "m2")
	e.write(m, "chore(kb): x\n")
	if _, errb, code := e.acScript("trailers-lint.sh", nil, m); code != 2 || !strings.Contains(errb, "row 'odd'") {
		t.Errorf("a path row that requires trailers: exit %d\n%s", code, errb)
	}
}

// make ready: a fix in an invariant area is refused without the trailers of a fix; PROFILE=live
// queues with the Profile line; another PROFILE is refused listing the declared rows.
func TestReadyReadsTheProfiles(t *testing.T) {
	e := newLandEnv(t)
	e.git("checkout", "-q", "-b", "feat/prof")
	e.commitFiles("wip", map[string]string{"internal/mcpserver/x.go": "package mcpserver\n"})
	ready := func(msg string, extra ...string) (string, int) {
		m := filepath.Join(e.tmp, "msg")
		e.write(m, msg)
		out, errb, code := e.acScript("ready.sh", append([]string{"MSG=" + m, "READY_NO_LANDER=1"}, extra...))
		return out + errb, code
	}
	if out, code := ready("fix(mcp): x\n\nWhy.\n"); code == 0 || !strings.Contains(out, "Found-by") || e.refs("ready/") != "" {
		t.Errorf("a fix in an invariant area owes the trailers of a fix (exit %d)\n%s", code, out)
	}
	if out, code := ready("fix(mcp): x\n\nFound-by: gate\nIntroduced-by: unknown\n"); code != 0 {
		t.Fatalf("with the trailers it queues\n%s", out)
	}
	if out, code := ready("fix(mcp): x\n\nFound-by: gate\nIntroduced-by: unknown\n", "PROFILE=nothing"); code == 0 || !strings.Contains(out, "live") {
		t.Errorf("an unknown PROFILE is refused, listing the declared ones (exit %d)\n%s", code, out)
	}
	if out, code := ready("fix(mcp): x\n\nFound-by: gate\nIntroduced-by: unknown\n", "PROFILE=fix"); code == 0 {
		t.Errorf("a row selected by its types is not declarable\n%s", out)
	}
	if out, code := ready("fix(mcp): x\n\nFound-by: gate\nIntroduced-by: unknown\nProfile: other\n", "PROFILE=live"); code != 0 {
		t.Fatalf("PROFILE=live queues\n%s", out)
	}
	msg := e.upGit("log", "-1", "--format=%B", "ready/prof")
	if !strings.Contains(msg, "Profile: live") || strings.Contains(msg, "Profile: other") {
		t.Errorf("the queued message carries the declared profile only:\n%s", msg)
	}
}

// The lander holds the title types of the `held` rows while the freeze is open, from the file:
// with the `feature` row's freeze column off, a feat lands.
func TestLanderReadsTheFrozenTypesFromTheProfiles(t *testing.T) {
	e := newLandEnv(t)
	tsv := filepath.Join(e.work, "scripts", "profiles.tsv")
	e.write(tsv, strings.Replace(readFileAbs(t, tsv), "\theld\t", "\t-\t", 1))
	e.git("commit", "-qam", "profiles: no freeze")
	e.git("push", "-q", "origin", "main")
	e.queue("feature", "feat(kb): feature\n", "2026-01-01T11:00:00Z", map[string]string{"f.txt": "f\n"})
	out, ok := e.land("FAKE_FREEZE=0")
	if !ok || e.refs("ready/") != "" {
		t.Errorf("no row holds feat: it lands (ok=%v)\n%s", ok, out)
	}
	e = newLandEnv(t)
	e.write(filepath.Join(e.work, "scripts", "profiles.tsv"), "bad\trow\n")
	if out, ok := e.land("FAKE_FREEZE=0"); ok || !strings.Contains(out, "profiles.tsv") {
		t.Errorf("a malformed file stops the lander (ok=%v)\n%s", ok, out)
	}
}

// AC-P3-18, the tier-floor half: a row edited to a higher tier raises the triage's tier, nothing
// else changed. The `fix` row gets the floor, and a small fix is light before and full after.
func TestProfilesATierFloorIsOneRow(t *testing.T) {
	e := newLandEnv(t)
	e.git("checkout", "-q", "-B", "feat/floor", "main")
	e.commitFiles("wip", map[string]string{"internal/kb/f.go": "package kb\n"})
	tier := func() string {
		t.Helper()
		out, errb, code := e.acScript("pr-triage.sh", []string{"TRIAGE_BODY=fix(kb): x\n\nFound-by: gate\n"})
		if code != 0 {
			t.Fatalf("pr-triage.sh: exit %d\n%s%s", code, out, errb)
		}
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "Tier: ") {
				return strings.Fields(l)[1]
			}
		}
		t.Fatalf("no Tier line\n%s", out)
		return ""
	}
	if got := tier(); got != "light" {
		t.Fatalf("a small fix outside the areas is light, got %s", got)
	}
	tsv := filepath.Join(e.work, "scripts", "profiles.tsv")
	e.write(tsv, strings.Replace(readFileAbs(t, tsv), "fix\tfix\t-\t-\t", "fix\tfix\t-\tfull\t", 1))
	if got := tier(); got != "full" {
		t.Errorf("the row's floor raises the tier, got %s", got)
	}
}

// The readers fail closed on a value they do not know: an unknown trailer name, an unknown freeze
// value and a path pattern that is no regular expression, each exit 2 naming the row; a held row of
// types `*` holds every type.
func TestProfilesReadersFailClosedOnUnknownValues(t *testing.T) {
	e := newLandEnv(t)
	tsv := filepath.Join(e.work, "scripts", "profiles.tsv")
	good := readFileAbs(t, tsv)
	e.git("checkout", "-q", "-B", "feat/fc", "main")
	e.commitFiles("wip", map[string]string{"internal/kb/f.go": "package kb\n"})
	m := filepath.Join(e.tmp, "m")
	e.write(m, "chore(kb): x\n")
	for name, c := range map[string]struct{ row, script string }{
		"trailer": {"odd\tchore\t-\t-\tFound_by\t-\t-\n", "trailers-lint.sh"},
		"regexp":  {"odd\t*\t^(x\t-\t-\t-\t-\n", "pr-triage.sh"},
	} {
		e.write(tsv, good+c.row)
		out, errb, code := e.acScript(c.script, []string{"TRIAGE_BODY=chore(kb): x\n"}, m)
		if c.script == "pr-triage.sh" {
			out, errb, code = e.acScript(c.script, []string{"TRIAGE_BODY=chore(kb): x\n"})
		}
		if code != 2 || !strings.Contains(errb, "row 'odd'") {
			t.Errorf("%s: exit %d, want 2 naming the row\n%s%s", name, code, out, errb)
		}
	}
	// The lander: an unknown freeze value stops it with exit 2, and a held row of `*` freezes a chore.
	e.write(tsv, good)
	e.write(tsv, strings.Replace(good, "feature\tfeat,perf,refactor,breaking\t-\t-\t-\theld", "feature\tfeat,perf,refactor,breaking\t-\t-\t-\tfrozen", 1))
	e.git("checkout", "-q", "main")
	e.git("commit", "-qam", "profiles: bad freeze")
	e.git("push", "-q", "origin", "main")
	if out, rc := e.landArgs([]string{"--dry-run"}, "FAKE_FREEZE=0"); rc != 2 || !strings.Contains(out, "row feature") {
		t.Errorf("an unknown freeze value stops the lander with 2 (exit %d)\n%s", rc, out)
	}
	e.write(tsv, good+"all\t*\t-\t-\t-\theld\t-\n")
	e.git("commit", "-qam", "profiles: everything held")
	e.git("push", "-q", "origin", "main")
	e.queue("chore", "chore(kb): c\n", "2026-01-01T11:00:00Z", map[string]string{"c.txt": "c\n"})
	if out, ok := e.land("FAKE_FREEZE=0"); !ok || e.refs("ready/") != "chore" {
		t.Errorf("a held row of types * holds a chore while the freeze is open (ok=%v)\n%s", ok, out)
	}
}
