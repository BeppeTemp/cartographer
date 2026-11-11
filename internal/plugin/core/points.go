package core

import (
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// Side is where a contribution point is loaded.
type Side string

const (
	// SideClient points are loaded by the client of the machine.
	SideClient Side = "client"
	// SideSurveyor, SideServer and SideAtlas are the other components a schema 2
	// manifest declares. A point of a side loads only when that side is asked
	// for (Options.Side) or every side is (empty).
	SideSurveyor Side = "surveyor"
	SideServer   Side = "server"
	SideAtlas    Side = "atlas"
)

// Sides lists every side in canonical order.
func Sides() []Side { return []Side{SideClient, SideSurveyor, SideServer, SideAtlas} }

// Contribution is what one folder entry of a point contributes: one skill, one
// agent, one hook.
type Contribution struct {
	Point string // the point's Dir
	Kind  string // artifact kind: skill, agent, hook
	Name  string
	// Files are the artifact's files, paths relative to the artifact.
	Files []contract.ArtifactFile
	// Executable lists the paths (relative to the artifact) of every file with
	// the executable bit, for the install preview.
	Executable []string
	// Description is the frontmatter description of a skill or agent: what the
	// agent reads to decide to use it, so the install preview shows it.
	Description string
	// Command, Event and Requires are what a hook declares in hook.json.
	Command, Event string
	Requires       []string
	// Provider is the parsed descriptor of a providers/<id>.yaml; nil for every
	// other kind. A provider is registered with the registries, never installed
	// as an artifact.
	Provider *ProviderSpec
}

// Point is a place of the plugin tree that contributes something.
type Point struct {
	Dir  string // top-level folder name
	Side Side
	// Family is the registered family (family.go) that owns Dir; Register fills
	// it, and refuses a Dir no family of Side owns.
	Family string
	// Load validates the files under Dir (paths are tree-relative, so they start
	// with Dir+"/") and returns the contributions with every problem found.
	Load func(files []TreeFile) ([]Contribution, []string)
	// Preview prints what the install preview shows for one contribution.
	Preview func(w io.Writer, c Contribution)
	// Executable marks a point whose files run (hooks).
	Executable bool
}

var (
	dirRe  = regexp.MustCompile(`^[a-z][a-z0-9]{1,20}$`)
	points = map[string]Point{}
)

// Register adds a point. A duplicate or invalid Dir is an init-time programmer
// error and panics.
func Register(p Point) {
	if !dirRe.MatchString(p.Dir) || p.Load == nil || p.Preview == nil || !slices.Contains(Sides(), p.Side) {
		panic(fmt.Sprintf("plugin: invalid contribution point %q", p.Dir))
	}
	fam, ok := FamilyOf(p.Dir)
	if !ok || fam.Side != p.Side || !slices.Contains(fam.Dirs, p.Dir) {
		panic(fmt.Sprintf("plugin: contribution point %q has no registered family on side %s", p.Dir, p.Side))
	}
	p.Family = fam.Name
	if _, dup := points[p.Dir]; dup {
		panic(fmt.Sprintf("plugin: contribution point %q registered twice", p.Dir))
	}
	points[p.Dir] = p
}

// RegisteredPoints returns the points sorted by Dir.
func RegisteredPoints() []Point {
	out := make([]Point, 0, len(points))
	for _, p := range points {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dir < out[j].Dir })
	return out
}
