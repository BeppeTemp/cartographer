package plugin

import (
	"bytes"
	"io"
	"slices"
	"strings"
	"testing"
)

// The surveyor family of these tests exists only in the test binary: this build
// has no surveyor point yet, and a side must be loadable on its own.
func init() {
	RegisterFamily(Family{Side: SideSurveyor, Name: "reports", Level: 1, Dirs: []string{"reports"}})
	Register(Point{
		Dir: "reports", Side: SideSurveyor,
		Load: func(files []TreeFile) ([]Contribution, []string) {
			var cs []Contribution
			for _, f := range files {
				cs = append(cs, Contribution{Point: "reports", Kind: "report", Name: strings.TrimPrefix(f.Path, "reports/")})
			}
			return cs, nil
		},
		Preview: func(w io.Writer, c Contribution) { _, _ = w.Write([]byte("report " + c.Name + "\n")) },
	})
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

func twoSideTree(extra ...TreeFile) []TreeFile {
	return schema2Tree(twoSides, append([]TreeFile{tf("reports/daily.txt", "x")}, extra...)...)
}

func TestReadTreeLoadsEachSideAlone(t *testing.T) {
	opts := v2Opts
	read := func(side Side) Plugin {
		opts.Side = side
		p, probs := ReadTree(twoSideTree(), opts)
		if len(probs) != 0 {
			t.Fatalf("%q: %v", side, probs)
		}
		return p
	}
	client, surveyor, every := read(SideClient), read(SideSurveyor), read("")
	if len(client.Contributions["skills"]) != 1 || len(client.Contributions["reports"]) != 0 {
		t.Errorf("client side: %v", client.Contributions)
	}
	if len(surveyor.Contributions["reports"]) != 1 || len(surveyor.Contributions["skills"]) != 0 {
		t.Errorf("surveyor side: %v", surveyor.Contributions)
	}
	if len(every.Contributions["reports"]) != 1 || len(every.Contributions["skills"]) != 1 {
		t.Errorf("every side: %v", every.Contributions)
	}
	if client.Digest != surveyor.Digest || client.Digest != every.Digest {
		t.Error("the digest depends on the side asked for")
	}
	// Only the client's contributions are artifacts to install.
	if len(every.Artifacts) != 1 || every.Artifacts[0].Kind != "skill" {
		t.Errorf("artifacts: %v", every.Artifacts)
	}
	if !slices.Equal(every.Sides, []Side{SideClient, SideSurveyor}) {
		t.Errorf("sides: %v", every.Sides)
	}
}

func TestUnsupportedSideIsNotLoaded(t *testing.T) {
	manifest := strings.Replace(twoSides, "reports: 1", "reports: 2", 1)
	opts := v2Opts
	opts.Side = SideSurveyor
	p, probs := ReadTree(schema2Tree(manifest, tf("reports/daily.txt", "x")), opts)
	if len(probs) != 0 || p.Unsupported[SideSurveyor] != "needs sdk reports 2 (this build has 1)" || len(p.Contributions["reports"]) != 0 {
		t.Errorf("%v %v %v", probs, p.Unsupported, p.Contributions)
	}
}

func TestRegisterNeedsAFamilyOfTheSide(t *testing.T) {
	for name, pt := range map[string]Point{
		"no family":  {Dir: "orphan", Side: SideSurveyor},
		"wrong side": {Dir: "reports", Side: SideAtlas},
	} {
		pt.Load = func([]TreeFile) ([]Contribution, []string) { return nil, nil }
		pt.Preview = func(io.Writer, Contribution) {}
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: Register did not panic", name)
				}
			}()
			Register(pt)
		}()
	}
	if pt, _ := points["skills"]; pt.Family != "artifacts" {
		t.Errorf("family of skills: %q", pt.Family)
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

func TestPreviewBySide(t *testing.T) {
	opts := v2Opts
	p, probs := ReadTree(twoSideTree(tf("extras/a.txt", "x")), opts)
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	show := func(side Side) string {
		var b bytes.Buffer
		Preview(&b, p, PreviewInfo{Source: "src", Side: side, Have: func(string) (string, bool) { return "", false }}, opts)
		return b.String()
	}
	client := show(SideClient)
	for _, want := range []string{"sdk: artifacts 1", "other sides, not loaded here: surveyor", "not known to this build, stored but never loaded: entry extras", "skills/:"} {
		if !strings.Contains(client, want) {
			t.Errorf("client preview lacks %q:\n%s", want, client)
		}
	}
	if strings.Contains(client, "report daily") {
		t.Errorf("client preview shows the surveyor:\n%s", client)
	}
	surveyor := show(SideSurveyor)
	if !strings.Contains(surveyor, "sdk: reports 1") || !strings.Contains(surveyor, "report daily.txt") ||
		!strings.Contains(surveyor, "other sides, not loaded here: client") || strings.Contains(surveyor, "skills/:") {
		t.Errorf("surveyor preview:\n%s", surveyor)
	}
	all := show("")
	if !strings.Contains(all, "client side:") || !strings.Contains(all, "surveyor side:") || strings.Contains(all, "not loaded here") {
		t.Errorf("every-side preview:\n%s", all)
	}
}
