package plugin

import (
	"bytes"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

// The surveyor family of these tests exists only in the test binary: this build
// has no surveyor point yet, and a side must be loadable on its own.
func init() {
	core.RegisterFamily(core.Family{Side: core.SideSurveyor, Name: "reports", Level: 1, Dirs: []string{"reports"}})
	core.Register(core.Point{
		Dir: "reports", Side: core.SideSurveyor,
		Load: func(files []core.TreeFile) ([]core.Contribution, []string) {
			var cs []core.Contribution
			for _, f := range files {
				cs = append(cs, core.Contribution{Point: "reports", Kind: "report", Name: strings.TrimPrefix(f.Path, "reports/")})
			}
			return cs, nil
		},
		Preview: func(w io.Writer, c core.Contribution) { _, _ = w.Write([]byte("report " + c.Name + "\n")) },
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

func twoSideTree(extra ...core.TreeFile) []core.TreeFile {
	return schema2Tree(twoSides, append([]core.TreeFile{tf("reports/daily.txt", "x")}, extra...)...)
}

func TestReadTreeLoadsEachSideAlone(t *testing.T) {
	opts := v2Opts
	read := func(side core.Side) core.Plugin {
		opts.Side = side
		p, probs := core.ReadTree(twoSideTree(), opts)
		if len(probs) != 0 {
			t.Fatalf("%q: %v", side, probs)
		}
		return p
	}
	client, surveyor, every := read(core.SideClient), read(core.SideSurveyor), read("")
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
	if !slices.Equal(every.Sides, []core.Side{core.SideClient, core.SideSurveyor}) {
		t.Errorf("sides: %v", every.Sides)
	}
}

func TestRegisterNeedsAFamilyOfTheSide(t *testing.T) {
	for name, pt := range map[string]core.Point{
		"no family":  {Dir: "orphan", Side: core.SideSurveyor},
		"wrong side": {Dir: "reports", Side: core.SideAtlas},
	} {
		pt.Load = func([]core.TreeFile) ([]core.Contribution, []string) { return nil, nil }
		pt.Preview = func(io.Writer, core.Contribution) {}
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: Register did not panic", name)
				}
			}()
			core.Register(pt)
		}()
	}
	for _, pt := range core.RegisteredPoints() {
		if pt.Dir == "skills" && pt.Family != "artifacts" {
			t.Errorf("family of skills: %q", pt.Family)
		}
	}
}

func TestPreviewBySide(t *testing.T) {
	opts := v2Opts
	p, probs := core.ReadTree(twoSideTree(tf("extras/a.txt", "x")), opts)
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	show := func(side core.Side) string {
		var b bytes.Buffer
		Preview(&b, p, PreviewInfo{Source: "src", Side: side, Have: func(string) (string, bool) { return "", false }}, opts)
		return b.String()
	}
	client := show(core.SideClient)
	for _, want := range []string{"sdk: artifacts 1", "other sides, not loaded here: surveyor", "not known to this build, stored but never loaded: entry extras", "skills/:"} {
		if !strings.Contains(client, want) {
			t.Errorf("client preview lacks %q:\n%s", want, client)
		}
	}
	if strings.Contains(client, "report daily") {
		t.Errorf("client preview shows the surveyor:\n%s", client)
	}
	surveyor := show(core.SideSurveyor)
	if !strings.Contains(surveyor, "sdk: reports 1") || !strings.Contains(surveyor, "report daily.txt") ||
		!strings.Contains(surveyor, "other sides, not loaded here: client") || strings.Contains(surveyor, "skills/:") {
		t.Errorf("surveyor preview:\n%s", surveyor)
	}
	all := show("")
	if !strings.Contains(all, "client side:") || !strings.Contains(all, "surveyor side:") || strings.Contains(all, "not loaded here") {
		t.Errorf("every-side preview:\n%s", all)
	}
}
