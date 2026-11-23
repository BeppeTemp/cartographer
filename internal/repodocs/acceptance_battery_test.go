package repodocs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The acceptance battery of lane B is a table (cartographer-acceptance, references/battery.md):
// each row names what automates it, a path of this repository or `tester`. A path that does not
// exist is a row that tests nothing, and a scenario of test/e2e/optional/ that no row names runs
// nightly for nobody.
const batteryFile = ".claude/skills/cartographer-acceptance/references/battery.md"

var batteryPath = regexp.MustCompile("`([^`]+)`")

// batteryRows returns, per table row (id, item, automated by), the id and the paths of the last
// column; tester is returned as the single entry "tester".
func batteryRows(t *testing.T, root string) (map[string][]string, []string) {
	t.Helper()
	rows := map[string][]string{}
	var dups []string
	for _, line := range strings.Split(readFile(t, root, batteryFile), "\n") {
		if !strings.HasPrefix(line, "| ") {
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		if len(cells) != 3 {
			t.Errorf("battery row with %d columns, want 3: %s", len(cells), line)
			continue
		}
		id := strings.TrimSpace(cells[0])
		if id == "id" || strings.HasPrefix(id, "---") {
			continue
		}
		if _, seen := rows[id]; seen {
			dups = append(dups, id)
		}
		last := strings.TrimSpace(cells[2])
		if last == "tester" {
			rows[id] = []string{"tester"}
			continue
		}
		var paths []string
		for _, m := range batteryPath.FindAllStringSubmatch(last, -1) {
			paths = append(paths, m[1])
		}
		if len(paths) == 0 {
			t.Errorf("battery row %s: automated by is neither `tester` nor a backticked path: %q", id, last)
		}
		rows[id] = paths
	}
	return rows, dups
}

// checkBattery returns what is wrong with the battery table under root.
func checkBattery(t *testing.T, root string) []string {
	t.Helper()
	var bad []string
	named := map[string]bool{}
	rows, dups := batteryRows(t, root)
	for _, id := range dups {
		bad = append(bad, "duplicate row id "+id)
	}
	if len(rows) == 0 {
		return []string{"the battery has no row"}
	}
	for id, paths := range rows {
		for _, p := range paths {
			if p == "tester" {
				continue
			}
			named[p] = true
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
				bad = append(bad, "row "+id+" names a path that does not exist: "+p)
			}
		}
	}
	files, err := filepath.Glob(filepath.Join(root, "test", "e2e", "optional", "*.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		rel := "test/e2e/optional/" + filepath.Base(f)
		if !named[rel] {
			bad = append(bad, "no row of the battery names "+rel)
		}
	}
	upgrade := false
	for _, p := range rows["B21"] {
		upgrade = upgrade || p == "test/e2e/optional/26_upgrade_from_previous.sh"
	}
	if !upgrade {
		bad = append(bad, "row B21, the upgrade from the previous release, must name test/e2e/optional/26_upgrade_from_previous.sh")
	}
	return bad
}

// TestAcceptanceBattery: AC-P4-9 — every battery row names an existing path or `tester`, and every
// optional scenario is named by a row.
func TestAcceptanceBattery(t *testing.T) {
	for _, b := range checkBattery(t, repoRoot(t)) {
		t.Error(b)
	}
}

// TestAcceptanceBatteryCatchesDrift: the check fails on a missing path, on an optional scenario no
// row names and on a row that lost the upgrade scenario, so a green TestAcceptanceBattery means something.
func TestAcceptanceBatteryCatchesDrift(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("test/e2e/optional/26_upgrade_from_previous.sh", "")
	write("test/e2e/optional/40_orphan.sh", "")
	write(batteryFile, "| id | item | automated by |\n|---|---|---|\n"+
		"| B20 | gone | `test/e2e/scenarios/missing.sh` |\n"+
		"| B21 | upgrade | tester |\n"+
		"| B21 | again | tester |\n")
	got := strings.Join(checkBattery(t, root), "\n")
	for _, want := range []string{
		"row B20 names a path that does not exist: test/e2e/scenarios/missing.sh",
		"no row of the battery names test/e2e/optional/40_orphan.sh",
		"no row of the battery names test/e2e/optional/26_upgrade_from_previous.sh",
		"duplicate row id B21",
		"row B21, the upgrade from the previous release, must name",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("checkBattery lacks %q in:\n%s", want, got)
		}
	}
}

// TestE2ENightlyTarget: AC-P4-10 — `make e2e` still runs scenarios/ alone, `make e2e-nightly`
// adds optional/ without the upgrade scenario (it needs the tags and has its own target).
func TestE2ENightlyTarget(t *testing.T) {
	root := repoRoot(t)
	mk := readFile(t, root, "Makefile")
	if !regexp.MustCompile(`(?m)^e2e: .*\n\t@\./test/e2e/run\.sh\n`).MatchString(mk) {
		t.Error("make e2e must run ./test/e2e/run.sh with no flag: the train gate does not grow")
	}
	if !regexp.MustCompile(`(?m)^e2e-nightly: .*\n\t@\./test/e2e/run\.sh --optional\n`).MatchString(mk) {
		t.Error("make e2e-nightly must run ./test/e2e/run.sh --optional")
	}
	run := readFile(t, root, "test/e2e/run.sh")
	if !strings.Contains(run, `"$(basename "$script" .sh)" == "26_upgrade_from_previous"`) {
		t.Error("run.sh --optional must skip 26_upgrade_from_previous")
	}
}
