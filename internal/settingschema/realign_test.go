package settingschema

import (
	"strings"
	"testing"
)

var testRemoved = []Removed{
	{Path: "git.profile", Replacement: "none: the server git profile is gone (#903)", Plan: "#903"},
	{Path: "items[].tag", Replacement: "use id", Plan: "#903"},
}

var testOffs = []OffReset{
	{Path: "git.sync", Off: "false"},
}

// A file with a removed key, an off value and a custom value is realigned: the
// removed key is dropped, the off value is reset to the default, the custom
// value is kept, and the report lists both changes.
func TestRealign_DropsResetsAndKeeps(t *testing.T) {
	in := "name: custom\ngit:\n  sync: false\n  profile: local\n"
	out, report, err := testSchema.Realign([]byte(in), testRemoved, testOffs)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)

	if strings.Contains(s, "profile:") && !strings.Contains(s, "# profile:") {
		t.Errorf("removed key git.profile is still set:\n%s", s)
	}
	if !strings.Contains(s, "sync: true") {
		t.Errorf("off value git.sync was not reset to the default:\n%s", s)
	}
	if !strings.Contains(s, "name: custom") {
		t.Errorf("custom value was dropped:\n%s", s)
	}
	if !report.Changed {
		t.Error("report must say the file changed")
	}
	if !contains(report.RemovedKeys, "git.profile") {
		t.Errorf("report must list the removed key: %+v", report.RemovedKeys)
	}
	if !contains(report.ResetKeys, "git.sync") {
		t.Errorf("report must list the reset key: %+v", report.ResetKeys)
	}
}

// A removed key inside a list entry is dropped too.
func TestRealign_DropsListEntryKey(t *testing.T) {
	in := "items:\n  - id: a\n    tag: old\n"
	out, report, err := testSchema.Realign([]byte(in), testRemoved, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "tag:") {
		t.Errorf("removed list-entry key items[].tag is still there:\n%s", out)
	}
	if !contains(report.RemovedKeys, "items[].tag") {
		t.Errorf("report must list items[].tag: %+v", report.RemovedKeys)
	}
	if !strings.Contains(string(out), "id: a") {
		t.Errorf("the kept entry key was lost:\n%s", out)
	}
}

// A second run changes nothing: the realigned file is already canonical, so it
// comes back byte-for-byte and Changed is false (no backup is written).
func TestRealign_SecondRunIsNoOp(t *testing.T) {
	in := "name: custom\ngit:\n  sync: false\n  profile: local\n"
	once, first, err := testSchema.Realign([]byte(in), testRemoved, testOffs)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Changed {
		t.Fatal("first run must change the file")
	}
	twice, second, err := testSchema.Realign(once, testRemoved, testOffs)
	if err != nil {
		t.Fatal(err)
	}
	if second.Changed {
		t.Errorf("second run must be a no-op, report: %+v", second)
	}
	if string(twice) != string(once) {
		t.Errorf("second run rewrote the file:\n--- once\n%s\n--- twice\n%s", once, twice)
	}
}

// A removed key added back by hand is reported by RemovedKeysIn, so the loader
// can warn (not error) naming the replacement.
func TestRemovedKeysIn_WarnsNotErrors(t *testing.T) {
	in := "name: a\ngit:\n  profile: local\n"
	hits := testSchema.RemovedKeysIn([]byte(in), testRemoved)
	if len(hits) != 1 || hits[0].Path != "git.profile" {
		t.Fatalf("want one hit git.profile, got %+v", hits)
	}
	if !strings.Contains(hits[0].Replacement, "#903") && !strings.Contains(hits[0].Replacement, "903") {
		t.Errorf("replacement should guide the user: %q", hits[0].Replacement)
	}
	// A file with no removed key reports nothing.
	if h := testSchema.RemovedKeysIn([]byte("name: a\n"), testRemoved); len(h) != 0 {
		t.Errorf("a clean file must report no removed keys, got %+v", h)
	}
}

// A file that only needs reformatting (no removed key, no off value) still
// reports Changed, and a second pass is then a no-op.
func TestRealign_ReformatThenStable(t *testing.T) {
	in := "name: a\n" // no comments yet: Render will add them
	out, report, err := testSchema.Realign([]byte(in), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Changed || !report.Reformatted {
		t.Errorf("a file lacking its canonical comments must be reformatted: %+v", report)
	}
	_, second, err := testSchema.Realign(out, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Changed {
		t.Errorf("a canonical file must be stable: %+v", second)
	}
}

func TestBackupName(t *testing.T) {
	if got := BackupName("/etc/config.yaml", "0.25.0"); got != "/etc/config.yaml.bak-0.25.0" {
		t.Errorf("BackupName = %q", got)
	}
}

func contains(s []string, v string) bool {
	for _, e := range s {
		if e == v {
			return true
		}
	}
	return false
}
