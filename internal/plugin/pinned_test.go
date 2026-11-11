package plugin

import (
	"os"
	"strings"
	"testing"
)

const surveyorOnly = "schema: 2\nname: feeds\nversion: 1.0.0\ndisplay_name: Feeds\ndescription: A feed reader.\ncartographer: \">=0.29.0\"\ncomponents:\n  surveyor: {}\n"

func TestLoadPinned(t *testing.T) {
	opts := Options{RunningVersion: "0.29.0"}
	root := testStore(t).Root
	rw := Store{Root: root}
	tree := []TreeFile{tf(ManifestFile, surveyorOnly)}
	p, probs := ReadTree(tree, opts)
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	if err := rw.Put("feeds", tree, p.Digest, opts); err != nil {
		t.Fatal(err)
	}
	other := strings.Repeat("d", 64)
	cases := []struct {
		name   string
		pin    Pin
		side   Side
		active bool
		reason string
	}{
		{"active", Pin{true, p.Digest}, SideSurveyor, true, ""},
		{"wrong digest is not fetched", Pin{true, other}, SideSurveyor, false, "not fetched"},
		{"no such side", Pin{true, p.Digest}, SideServer, false, "no server side"},
		{"disabled is skipped", Pin{false, other}, SideSurveyor, false, ""},
		{"malformed digest", Pin{true, "ABC"}, SideSurveyor, false, "64 lowercase hex"},
		{"empty digest", Pin{true, ""}, SideSurveyor, false, "64 lowercase hex"},
	}
	for _, c := range cases {
		got, pp := LoadPinned(root, map[string]Pin{"feeds": c.pin}, c.side, opts)
		switch {
		case c.active && (len(got) != 1 || len(pp) != 0):
			t.Errorf("%s: %v %v", c.name, got, pp)
		case !c.active && c.reason == "" && (len(got) != 0 || len(pp) != 0):
			t.Errorf("%s: %v %v", c.name, got, pp)
		case c.reason != "" && (len(got) != 0 || len(pp) != 1 || !strings.Contains(pp[0].Reason, c.reason)):
			t.Errorf("%s: %v %v", c.name, got, pp)
		}
	}
	// Wrong digest on disk: modified. A tree that no longer validates: invalid.
	if _, pp := LoadPinned(root, map[string]Pin{"feeds": {true, p.Digest}}, SideSurveyor, Options{RunningVersion: "0.28.0"}); len(pp) != 1 || pp[0].Reason != string(StatusInvalid) {
		t.Errorf("invalid: %v", pp)
	}
	if _, pp := LoadPinned(root, map[string]Pin{"../x": {true, other}}, SideSurveyor, opts); len(pp) != 1 {
		t.Errorf("a bad name: %v", pp)
	}
	if JoinPinProblems(nil) != nil {
		t.Error("no problems must be a nil error")
	}
}

func TestLoadPinnedModified(t *testing.T) {
	s := testStore(t)
	d := putGood(t, s)
	notes := s.Root + "/team-forks/" + d + "/skills/helper/notes.md"
	if err := writeFileForTest(notes); err != nil {
		t.Fatal(err)
	}
	_, pp := LoadPinned(s.Root, map[string]Pin{"team-forks": {true, d}}, SideClient, testOpts)
	if len(pp) != 1 || pp[0].Reason != string(StatusModified) {
		t.Errorf("%v", pp)
	}
}

func writeFileForTest(p string) error { return os.WriteFile(p, []byte("edited"), 0o644) }
