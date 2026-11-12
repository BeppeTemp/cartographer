// Package recipetest runs a plugin's recipes against recorded fixtures.
//
// It is the library of `cartographer plugin test` (docs/plugins.md): the scenario
// an author wrote in tests/<name>.yaml, then the checks every recipe must pass
// whatever the scenario asserts. It judges with the production recipe types, so
// a recipe passes here only when a Surveyor would accept what it prints.
package recipetest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// maxFile bounds a scenario and each body_file: fixtures are small. maxTotal
// bounds everything one scenario reads, so many body_files cannot add up.
const (
	maxFile  = 1 << 20
	maxTotal = 4 << 20
)

// Scenario is tests/<name>.yaml. It is not stored with the plugin and not part
// of its digest: the tree reader ignores the tests/ folder.
type Scenario struct {
	// Settings are the recipe's configuration (typed scalars), as plugins.<name>.settings.
	Settings map[string]any `yaml:"settings"`
	// Env gives the test value of each variable the manifest declares; {tests}
	// expands to the absolute tests/ folder. A declared name left out gets a placeholder.
	Env    map[string]string `yaml:"env"`
	Source Source            `yaml:"source"`
	// HTTP are the recorded exchanges of a Starlark recipe's http.request.
	HTTP  []Recording `yaml:"http"`
	Steps []Step      `yaml:"steps"`
	// Calls are the tool calls of a connector's scenario.
	Calls []Call `yaml:"calls"`

	// fixturePaths are the env names whose value holds {tests}: a path the
	// author's own folder gave, not a credential, so the secrets check does not
	// watch it.
	fixturePaths map[string]bool
}

// Call is one tool call of a connector, made through the connector set a
// Surveyor opens, so the allowlist and the result cap apply.
type Call struct {
	Tool string         `yaml:"tool"`
	Args map[string]any `yaml:"args"`
	// ExpectError says the call must be refused or answer an error result.
	ExpectError        bool   `yaml:"expect_error"`
	ExpectTextContains string `yaml:"expect_text_contains"`
}

// Source is the Source the recipe is asked about.
type Source struct {
	ID         string   `yaml:"id"`
	Title      string   `yaml:"title"`
	Locator    string   `yaml:"locator"`
	SourceKind string   `yaml:"source_kind"`
	Include    []string `yaml:"include"`
}

// Recording is one recorded exchange. A stage groups the recordings a step sees:
// a step of stage s sees those of stage s and those without one, so a second
// listing can see a changed world.
type Recording struct {
	Stage    string            `yaml:"stage"`
	Method   string            `yaml:"method"`
	URL      string            `yaml:"url"`
	Query    map[string]string `yaml:"query"`
	Response struct {
		Status   int               `yaml:"status"`
		Headers  map[string]string `yaml:"headers"`
		Body     string            `yaml:"body"`
		BodyFile string            `yaml:"body_file"`
	} `yaml:"response"`
}

// Step is one verb call and what it must answer.
type Step struct {
	Verb    string      `yaml:"verb"`
	Stage   string      `yaml:"stage"`
	Request StepRequest `yaml:"request"`
	Expect  Expect      `yaml:"expect"`
}

// StepRequest holds the cursor of a list ("@next" is the previous list's
// next_cursor) and the keys of a fetch.
type StepRequest struct {
	Cursor string   `yaml:"cursor"`
	Keys   []string `yaml:"keys"`
}

// Expect is what a step asserts; every field is optional.
type Expect struct {
	OK *bool `yaml:"ok"`
	// Entries and Deletes are a count or the exact list of keys.
	Entries any `yaml:"entries"`
	Deletes any `yaml:"deletes"`
	// NextCursor is empty, nonempty or the literal cursor.
	NextCursor      string `yaml:"next_cursor"`
	Error           string `yaml:"error"`
	ContentContains string `yaml:"content_contains"`
}

var stepVerbs = map[string]bool{"spec": true, "check": true, "list": true, "fetch": true}

// ReadFile reads and judges the shape of a scenario file; dir is the plugin's tests/ folder.
func ReadFile(dir, name string) (*Scenario, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	budget := maxTotal
	data, err := readCapped(root, name+".yaml", &budget)
	if err != nil {
		return nil, err
	}
	var sc Scenario
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&sc); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s.yaml: %v", name, err)
	}
	if sc.Env == nil {
		sc.Env = map[string]string{}
	}
	abs, _ := filepath.Abs(dir)
	for k, v := range sc.Env {
		if strings.Contains(v, "{tests}") {
			if sc.fixturePaths == nil {
				sc.fixturePaths = map[string]bool{}
			}
			sc.fixturePaths[k] = true
		}
		sc.Env[k] = strings.ReplaceAll(v, "{tests}", abs)
	}
	for i := range sc.HTTP {
		r := &sc.HTTP[i].Response
		if r.BodyFile == "" {
			continue
		}
		if r.Body != "" || !filepath.IsLocal(r.BodyFile) {
			return nil, fmt.Errorf("%s.yaml: http[%d]: body_file is a path inside tests/ and replaces body", name, i)
		}
		b, err := readCapped(root, r.BodyFile, &budget)
		if err != nil {
			return nil, fmt.Errorf("%s.yaml: http[%d]: %v", name, i, err)
		}
		r.Body = string(b)
	}
	if len(sc.Steps) == 0 && len(sc.Calls) == 0 {
		return nil, fmt.Errorf("%s.yaml: needs steps (a recipe) or calls (a connector)", name)
	}
	hasList := false
	for i, s := range sc.Steps {
		if !stepVerbs[s.Verb] {
			return nil, fmt.Errorf("%s.yaml: steps[%d]: verb is spec, check, list or fetch, not %q", name, i, s.Verb)
		}
		hasList = hasList || s.Verb == "list"
	}
	if len(sc.Steps) > 0 && !hasList {
		return nil, fmt.Errorf("%s.yaml: steps needs at least one list step", name)
	}
	return &sc, nil
}

// readCapped reads a regular file of the tests/ folder, never a symlink or a
// device: a fixture is data the author committed, and the folder may come from
// a plugin that is not trusted. budget is what the scenario may still read.
func readCapped(root *os.Root, rel string, budget *int) ([]byte, error) {
	fi, err := root.Lstat(rel)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file (a symlink is refused)", filepath.Base(rel))
	}
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	limit := min(maxFile, *budget)
	b, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err == nil && len(b) > limit {
		if limit < maxFile {
			err = fmt.Errorf("the scenario reads over %d bytes in total", maxTotal)
		} else {
			err = fmt.Errorf("%s is over %d bytes", filepath.Base(rel), maxFile)
		}
	}
	*budget -= len(b)
	return b, err
}
