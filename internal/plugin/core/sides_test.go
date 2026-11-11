package core

import (
	"strings"
	"testing"
)

// The surveyor family exists only in the test binary: this build has no
// surveyor point yet, and a side must be loadable on its own.
func init() {
	RegisterFamily(Family{Side: SideSurveyor, Name: "reports", Level: 1, Dirs: []string{"reports"}})
}

const twoSides = `schema: 2
name: team-forks
version: 1.2.3
display_name: Team forks
description: Closed forks of supported clients.
cartographer: ">=0.29.0"
components:
  client:
    sdk: {artifacts: 1}
  surveyor:
    sdk: {reports: 1}
`

func TestUnsupportedSideIsNotLoaded(t *testing.T) {
	manifest := strings.Replace(twoSides, "reports: 1", "reports: 2", 1)
	opts := v2Opts
	opts.Side = SideSurveyor
	p, probs := ReadTree(schema2Tree(manifest, tf("reports/daily.txt", "x")), opts)
	if len(probs) != 0 || p.Unsupported[SideSurveyor] != "needs sdk reports 2 (this build has 1)" || len(p.Contributions["reports"]) != 0 {
		t.Errorf("%v %v %v", probs, p.Unsupported, p.Contributions)
	}
}

func TestTestsFolderIsIgnoredUnderSchema2Only(t *testing.T) {
	base, _ := ReadTree(schema2Tree(validV2), v2Opts)
	with, probs := ReadTree(schema2Tree(validV2, tf("tests/case/in.txt", "x")), v2Opts)
	if len(probs) != 0 || with.Digest != base.Digest || len(with.Unknown) != 0 {
		t.Errorf("%v: digest equal %v, unknown %v", probs, with.Digest == base.Digest, with.Unknown)
	}
	for _, f := range with.Files {
		if strings.HasPrefix(f.Path, "tests/") {
			t.Errorf("stored %s", f.Path)
		}
	}
	// A top-level file named tests is not the folder.
	if _, probs = ReadTree(schema2Tree(validV2, tf("tests", "x")), v2Opts); len(probs) != 0 {
		t.Errorf("a schema 2 file is reported, not fatal: %v", probs)
	}
	// Schema 1 stays what 0.28 said.
	_, probs = ReadTree(append(goodTree(), tf("tests/a.txt", "x")), testOpts)
	if len(probs) != 1 || !strings.HasPrefix(probs[0], "unknown entry `tests`") {
		t.Errorf("schema 1: %v", probs)
	}
}

func TestSchema1StaysClientOnly(t *testing.T) {
	_, probs := ReadTree(append(goodTree(), tf("reports/a.txt", "x")), testOpts)
	if len(probs) != 1 || !strings.HasPrefix(probs[0], "unknown entry `reports`") {
		t.Errorf("a schema 1 tree with another side's folder: %v", probs)
	}
}
