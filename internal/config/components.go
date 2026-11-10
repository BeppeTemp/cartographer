package config

import (
	"fmt"
	"reflect"
	"regexp"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/componentproto"
	"gopkg.in/yaml.v3"
)

// ComponentSpec is one entry of the components: registry, a component the
// server expects to hear from.
type ComponentSpec struct {
	Kind        string
	Name        string
	KBs         []string
	MinReplicas int
}

// rawComponent mirrors one `components:` entry. MinReplicas is a pointer so an
// absent value (default 1) differs from an explicit 0, which is refused.
type rawComponent struct {
	Kind        string   `yaml:"kind"`
	Name        string   `yaml:"name"`
	KBs         []string `yaml:"kbs"`
	MinReplicas *int     `yaml:"min_replicas"`
}

// MaxComponentReplicas is the cap of min_replicas: a principal holds at most
// this many live Surveyors (maxSurveyorsPerPrincipal in internal/mcpserver).
const MaxComponentReplicas = 16

// componentNameRe is the perimeter pattern of a Surveyor's heartbeat
// (perimeterRe in internal/mcpserver; a test there pins the two equal).
var componentNameRe = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

// ComponentNamePattern is the pattern a component name must match.
func ComponentNamePattern() string { return componentNameRe.String() }

// checkComponentKeys refuses an unknown key under a components: entry, which
// would otherwise be dropped and could silence the alarm (a misspelled
// min_replicas). It follows the rule of checkAuthKeys.
func checkComponentKeys(data []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}
	keys, vals := mappingPairs(root)
	for i, k := range keys {
		if k.Value == "components" {
			return checkKnownKeys(vals[i], reflect.TypeOf([]rawComponent{}), "components")
		}
	}
	return nil
}

// componentSpecs validates the registry as far as the file alone allows. The
// mounted-KB check needs the KBs and is UnmountedComponentKBs.
func componentSpecs(raw []rawComponent) ([]ComponentSpec, error) {
	var out []ComponentSpec
	seen := map[string]bool{}
	for i, r := range raw {
		where := fmt.Sprintf("components[%d]", i)
		known := false
		for _, k := range []string{componentproto.KindSurveyor} {
			known = known || r.Kind == k
		}
		if !known {
			return nil, fmt.Errorf("%s.kind %q: want %q", where, r.Kind, componentproto.KindSurveyor)
		}
		if !componentNameRe.MatchString(r.Name) {
			return nil, fmt.Errorf("%s.name %q: want the perimeter pattern %s", where, r.Name, componentNameRe)
		}
		key := r.Kind + "/" + r.Name
		if seen[key] {
			return nil, fmt.Errorf("%s: %s %q is declared twice", where, r.Kind, r.Name)
		}
		seen[key] = true
		if len(r.KBs) == 0 {
			return nil, fmt.Errorf("%s (%s): kbs must name at least one KB", where, r.Name)
		}
		min := 1
		if r.MinReplicas != nil {
			min = *r.MinReplicas
		}
		if min < 1 || min > MaxComponentReplicas {
			return nil, fmt.Errorf("%s (%s).min_replicas %d: want 1..%d", where, r.Name, min, MaxComponentReplicas)
		}
		kbs := append([]string(nil), r.KBs...)
		sort.Strings(kbs)
		out = append(out, ComponentSpec{Kind: r.Kind, Name: r.Name, KBs: kbs, MinReplicas: min})
	}
	return out, nil
}

// UnmountedComponentKBs lists, one line each, the registry entries that name a
// KB the server did not mount. It is not an error: a KB that failed to mount
// (skipped by the bootstrap) must not turn one typo or one bad KB into an
// outage of every other, so the server logs the lines loudly and keeps the
// entries; the rows of the KBs that are mounted still report a state.
func UnmountedComponentKBs(specs []ComponentSpec, mounted []string) []string {
	have := make(map[string]bool, len(mounted))
	for _, n := range mounted {
		have[n] = true
	}
	var out []string
	for i, c := range specs {
		for _, name := range c.KBs {
			if !have[name] {
				out = append(out, fmt.Sprintf("components[%d] (%s): kbs names %q, which is not a mounted KB", i, c.Name, name))
			}
		}
	}
	return out
}
