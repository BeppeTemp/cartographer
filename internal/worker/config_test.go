package worker

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/settingschema"
	"github.com/BeppeTemp/cartographer/internal/workerconfig"
	"gopkg.in/yaml.v3"
)

// cfgNameOption and cfgEveryOption are the shared declarations two test kinds
// reuse, the way later kind plans reuse each other's.
var (
	cfgNameKey  = settingschema.Key{Path: "tasks[].name", Scope: settingschema.ScopeWorker, Type: "string", Default: `""`, Description: "Name that tells the entries of a kind apart."}
	cfgEveryKey = settingschema.Key{Path: "tasks[].every", Scope: settingschema.ScopeWorker, Type: "duration", Default: "24h", Description: "How often the task may run."}
	cfgPathsKey = settingschema.Key{Path: "tasks[].paths", Scope: settingschema.ScopeWorker, Type: "list", Default: "[]", Description: "Paths the task is limited to."}
	cfgDepthKey = settingschema.Key{Path: "tasks[].depth", Scope: settingschema.ScopeWorker, Type: "int", Default: "1", Description: "How deep the task goes."}
	cfgFastKey  = settingschema.Key{Path: "tasks[].fast", Scope: settingschema.ScopeWorker, Type: "bool", Default: "false", Description: "Skip the slow checks."}
	cfgRootKey  = settingschema.Key{Path: "tasks[].root", Scope: settingschema.ScopeWorker, Type: "string", Default: `""`, Description: "Where the task starts."}
)

func cfgKinds() *Kinds {
	return NewKinds(
		Kind{Name: "ingest", Options: []KindOption{
			{Key: cfgNameKey}, {Key: cfgEveryKey}, {Key: cfgPathsKey}, {Key: cfgDepthKey}, {Key: cfgFastKey},
		}, Validate: func(o map[string]any) error {
			if o["every"].(time.Duration) < time.Hour {
				return fmt.Errorf("every: at least 1h")
			}
			return nil
		}},
		Kind{Name: "doctor", Options: []KindOption{{Key: cfgEveryKey}, {Key: cfgRootKey, Required: true}}},
	)
}

const cfgCanary = "canary-secret-7f3a"

// cfgFile writes body (after the %s-free default replaced) and sets the
// environment variables with the canary as value.
func cfgLoad(t *testing.T, body string) (*Config, error) {
	t.Helper()
	t.Setenv("WORKER_TOKEN", cfgCanary+"-token")
	t.Setenv("CLOUD_API_KEY", cfgCanary+"-key")
	path := filepath.Join(t.TempDir(), "worker.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return LoadConfig(path, cfgKinds())
}

const cfgGolden = `name: docs-night
server_url: https://wiki.example.com/mcp
token_env: WORKER_TOKEN
kbs: [kb-a, work-kb]
models:
  - name: cloud
    provider: anthropic
    model: claude-sonnet-4-5
    api_key_env: CLOUD_API_KEY
    tier: large
    budget:
      daily_tokens: 2000000
      run_tokens: 500000
  - name: local
    provider: openai
    base_url: http://llm.internal:8080/v1
    allow_insecure_http: true
    model: small-model
    budget:
      daily_tokens: 9000000
schedule:
  window:
    start: "23:00"
    end: "06:00"
    timezone: Europe/Rome
tasks:
  - kind: ingest
    model: cloud
    name: big
    every: 2h
    paths: [docs]
  - kind: ingest
    model: local
    name: small
  - kind: doctor
    model: local
    root: kb-a
`

// with returns the golden file with old replaced by new, failing if old is absent.
func cfgWith(t *testing.T, old, new string) string {
	t.Helper()
	if !strings.Contains(cfgGolden, old) {
		t.Fatalf("golden has no %q", old)
	}
	return strings.Replace(cfgGolden, old, new, 1)
}

func TestLoadConfigGolden(t *testing.T) {
	c, err := cfgLoad(t, cfgGolden)
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "docs-night" || len(c.KBs) != 2 || len(c.Models) != 2 || len(c.Tasks) != 3 {
		t.Fatalf("unexpected config: %#v", c)
	}
	cloud, _ := c.Profile("cloud")
	local, _ := c.Profile("local")
	if cloud.Tier != TierLarge || cloud.RunTokens != 500000 || local.Tier != TierSmall || local.RunTokens != 9000000 {
		t.Errorf("profiles: %v %v", cloud, local)
	}
	if c.PollSeconds != 60 || c.Window == nil || c.IdleMinutes != 0 {
		t.Errorf("schedule: %+v", c)
	}
	t0 := c.Tasks[0]
	if t0.Kind != "ingest" || t0.Profile != "cloud" || t0.Options["every"] != 2*time.Hour || t0.Options["depth"] != 1 || t0.Options["fast"] != false {
		t.Errorf("task 0: %+v", t0)
	}
	if got := t0.Options["paths"].([]string); len(got) != 1 || got[0] != "docs" {
		t.Errorf("paths: %v", got)
	}
	if c.Tasks[1].Options["every"] != 24*time.Hour || c.Tasks[2].Options["root"] != "kb-a" {
		t.Errorf("defaults/required: %+v %+v", c.Tasks[1], c.Tasks[2])
	}
	if c.Token() == "" || cloud.APIKey() == "" || c.Document() == nil || len(c.Warnings) != 0 {
		t.Errorf("token/key/doc/warnings: %q %q %v", c.Token(), cloud.APIKey(), c.Warnings)
	}
}

func TestLoadConfigErrors(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"empty file", "", "empty"},
		{"scalar root", "foo\n", "mapping"},
		{"missing name", strings.Replace(cfgGolden, "name: docs-night\n", "", 1), "name"},
		{"bad name", cfgWith(t, "docs-night", "Docs Night"), "name"},
		{"missing server_url", cfgWith(t, "server_url: https://wiki.example.com/mcp\n", ""), "server_url"},
		{"insecure server_url", cfgWith(t, "https://wiki.example.com/mcp", "http://wiki.example.com/mcp"), "server_url (line 2)"},
		{"missing token_env", cfgWith(t, "token_env: WORKER_TOKEN\n", ""), "token_env"},
		{"unset token env", cfgWith(t, "token_env: WORKER_TOKEN", "token_env: NOT_SET_ANYWHERE"), "NOT_SET_ANYWHERE"},
		{"missing kbs", cfgWith(t, "kbs: [kb-a, work-kb]\n", ""), "kbs"},
		{"duplicate kb", cfgWith(t, "[kb-a, work-kb]", "[kb-a, kb-a]"), "listed twice"},
		{"missing models", cfgGolden[:strings.Index(cfgGolden, "models:")] + cfgGolden[strings.Index(cfgGolden, "schedule:"):], "models"},
		{"duplicate profile", cfgWith(t, "name: local", "name: cloud"), `profile "cloud" is defined twice`},
		{"bad provider", cfgWith(t, "provider: openai", "provider: gemini"), "models[1].provider"},
		{"openai without base_url", cfgWith(t, "    base_url: http://llm.internal:8080/v1\n", ""), "models[1].base_url"},
		{"insecure base_url", cfgWith(t, "    allow_insecure_http: true\n", ""), "models[1].base_url"},
		{"credentials in base_url", cfgWith(t, "http://llm.internal", "http://u:p@llm.internal"), "credentials in the URL"},
		{"anthropic without key", cfgWith(t, "    api_key_env: CLOUD_API_KEY\n", ""), "models[0].api_key_env"},
		{"unset api key env", cfgWith(t, "CLOUD_API_KEY", "NO_SUCH_KEY_VAR"), "NO_SUCH_KEY_VAR"},
		{"bad tier", cfgWith(t, "tier: large", "tier: huge"), "models[0].tier"},
		{"missing daily", cfgWith(t, "      daily_tokens: 2000000\n", ""), "models[0].budget.daily_tokens"},
		{"run above daily", cfgWith(t, "run_tokens: 500000", "run_tokens: 3000000"), "models[0].budget.run_tokens"},
		{"missing model id", cfgWith(t, "    model: small-model\n", ""), "models[1].model"},
		{"equal window bounds", cfgWith(t, `end: "06:00"`, `end: "23:00"`), "equal"},
		{"bad clock", cfgWith(t, `start: "23:00"`, `start: "25:00"`), "schedule.window.start"},
		{"unknown zone", cfgWith(t, "Europe/Rome", "Mars/Olympus"), `unknown time zone "Mars/Olympus"`},
		{"no zone", cfgWith(t, "    timezone: Europe/Rome\n", ""), "schedule.window.timezone"},
		{"no trigger", cfgGolden[:strings.Index(cfgGolden, "schedule:")] + cfgGolden[strings.Index(cfgGolden, "tasks:"):], "set schedule.window"},
		{"idle only", cfgGolden[:strings.Index(cfgGolden, "schedule:")] + "schedule:\n  idle_minutes: 30\n" + cfgGolden[strings.Index(cfgGolden, "tasks:"):], "reports KB activity"},
		{"poll too low", cfgWith(t, "schedule:\n", "schedule:\n  poll_seconds: 5\n"), "schedule.poll_seconds"},
		{"missing tasks", cfgGolden[:strings.Index(cfgGolden, "tasks:")], "tasks"},
		{"string task", cfgGolden[:strings.Index(cfgGolden, "tasks:")] + "tasks: [ingest]\n", "a mapping with kind and model"},
		{"unknown kind", cfgWith(t, "kind: doctor", "kind: dream"), `unknown kind "dream": the registered kinds are ingest, doctor`},
		{"unknown profile", cfgWith(t, "    model: local\n    root", "    model: gone\n    root"), `unknown profile "gone": defined cloud, local`},
		{"missing task model", cfgWith(t, "    model: local\n    root", "    root"), "tasks[2].model"},
		{"duplicate name", cfgWith(t, "name: small", "name: big"), `name "big" is used twice`},
		{"repeat without name", cfgWith(t, "    name: small\n", ""), "every entry sets name"},
		{"validate hook", cfgWith(t, "every: 2h", "every: 5m"), "every: at least 1h"},
		{"bad option type", cfgWith(t, "every: 2h", "every: [1]"), "tasks[0].every"},
		{"bad duration", cfgWith(t, "every: 2h", "every: soon"), "not a duration"},
		{"missing required option", cfgWith(t, "    root: kb-a\n", ""), `required by kind "doctor"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cfgLoad(t, tc.body)
			if err == nil {
				t.Fatalf("loaded; want error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q lacks %q", err, tc.want)
			}
			if strings.Contains(err.Error(), cfgCanary) {
				t.Errorf("error leaks a secret: %v", err)
			}
		})
	}
}

func TestLoadConfigKindTwiceWithoutName(t *testing.T) {
	body := strings.Replace(cfgGolden, "  - kind: ingest\n    model: local\n    name: small\n", "", 1)
	body = strings.Replace(body, "    name: big\n", "", 1) + "  - kind: doctor\n    model: cloud\n    root: x\n"
	_, err := cfgLoad(t, body)
	if err == nil || !strings.Contains(err.Error(), `kind "doctor" is listed twice (lines`) {
		t.Errorf("err = %v", err)
	}
}

func TestLoadConfigUnknownKeysNameThePlace(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"top level", cfgGolden + "extra: 1\n", `unknown key "extra" under the top level (line 38, column 1)`},
		{"profile", cfgWith(t, "    tier: large\n", "    tier: large\n    colour: red\n"), `unknown key "colour" under models[0] (line 11, column 5)`},
		{"budget", cfgWith(t, "      run_tokens: 500000\n", "      run_tokens: 500000\n      hourly: 1\n"), `unknown key "hourly" under models[0].budget`},
		{"window", cfgWith(t, "    timezone: Europe/Rome\n", "    timezone: Europe/Rome\n    day: mon\n"), `unknown key "day" under schedule.window`},
		{"task", cfgWith(t, "    root: kb-a\n", "    root: kb-a\n    nope: 1\n"), `unknown key "nope" under tasks[2] (kind "doctor"`},
		{"option of another kind", cfgWith(t, "    root: kb-a\n", "    root: kb-a\n    paths: [x]\n"), `unknown key "paths" under tasks[2]`},
		{"duplicate key", cfgWith(t, "name: docs-night\n", "name: docs-night\nname: other\n"), `duplicate key "name"`},
		{"duplicate task key", cfgWith(t, "    root: kb-a\n", "    root: kb-a\n    root: kb-b\n"), `duplicate key "root" under tasks[2]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cfgLoad(t, tc.body)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLoadConfigWarnings(t *testing.T) {
	// A profile no task uses, and an idle trigger beside a window.
	body := cfgWith(t, "    name: small\n", "    name: small\n")
	body = strings.Replace(body, "    model: local\n    name: small\n", "    model: cloud\n    name: small\n", 1)
	body = strings.Replace(body, "    model: local\n    root: kb-a", "    model: cloud\n    root: kb-a", 1)
	body = strings.Replace(body, "schedule:\n", "schedule:\n  idle_minutes: 30\n", 1)
	c, err := cfgLoad(t, body)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(c.Warnings, "\n")
	if !strings.Contains(joined, `profile "local" is used by no task`) || !strings.Contains(joined, "idle_minutes") {
		t.Errorf("warnings: %q", joined)
	}
}

func TestLoadConfigNoRegisteredKinds(t *testing.T) {
	t.Setenv("WORKER_TOKEN", "x")
	t.Setenv("CLOUD_API_KEY", "y")
	path := filepath.Join(t.TempDir(), "w.yaml")
	if err := os.WriteFile(path, []byte(cfgGolden), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path, NewKinds()) // an empty registry
	if err == nil || !strings.Contains(err.Error(), "no task kind is registered") {
		t.Errorf("err = %v", err)
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "nope.yaml"), DefaultKinds()); err == nil {
		t.Error("a missing file loads")
	}
}

// The token and the keys are in the environment as a canary: none may show in
// a %v or %+v or %#v of the config or of a profile, nor in any error above.
func TestConfigPrintsNoSecret(t *testing.T) {
	c, err := cfgLoad(t, cfgGolden)
	if err != nil {
		t.Fatal(err)
	}
	cloud, _ := c.Profile("cloud")
	for _, v := range []any{c, *c, cloud, &cloud, c.Models} {
		for _, f := range []string{"%v", "%+v", "%#v", "%s"} {
			if out := fmt.Sprintf(f, v); strings.Contains(out, cfgCanary) {
				t.Errorf("%s of %T leaks a secret: %s", f, v, out)
			}
		}
	}
}

func TestWindowContains(t *testing.T) {
	rome, _ := time.LoadLocation("Europe/Rome")
	night := Window{Start: 23 * 60, End: 6 * 60, Location: rome}
	day := Window{Start: 9 * 60, End: 17 * 60, Location: rome}
	at := func(w Window, h, m int) bool {
		return w.Contains(time.Date(2026, 3, 1, h, m, 0, 0, rome))
	}
	for _, tc := range []struct {
		w    Window
		h, m int
		want bool
	}{
		{night, 22, 59, false}, {night, 23, 0, true}, {night, 0, 0, true}, {night, 5, 59, true}, {night, 6, 0, false},
		{day, 8, 59, false}, {day, 9, 0, true}, {day, 16, 59, true}, {day, 17, 0, false},
	} {
		if got := at(tc.w, tc.h, tc.m); got != tc.want {
			t.Errorf("%d:%02d in %+v = %v", tc.h, tc.m, tc.w, got)
		}
	}
	// The window is read in its own zone whatever zone the instant is in: 21:30
	// UTC is 22:30 in Rome in winter and 23:30 in summer.
	if night.Contains(time.Date(2026, 1, 15, 21, 30, 0, 0, time.UTC)) {
		t.Error("21:30 UTC in January is 22:30 in Rome, before the window")
	}
	if !night.Contains(time.Date(2026, 7, 15, 21, 30, 0, 0, time.UTC)) {
		t.Error("21:30 UTC in July is 23:30 in Rome, inside the window")
	}
}

// The alpine image has no zoneinfo: the embedded copy must be linked. A
// behavioural test cannot tell (time.LoadLocation prefers the system's data), so
// the import is pinned here.
func TestTzdataIsImported(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "config.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range f.Imports {
		if imp.Path.Value == `"time/tzdata"` {
			return
		}
	}
	t.Error(`internal/worker/config.go must import _ "time/tzdata": the container image has no zoneinfo`)
}

func TestTimeZones(t *testing.T) {
	c, err := cfgLoad(t, cfgGolden)
	if err != nil {
		t.Fatal(err)
	}
	if c.Window.Location.String() != "Europe/Rome" {
		t.Errorf("zone = %v", c.Window.Location)
	}
}

func TestNewKindsPanicsOnBadDeclarations(t *testing.T) {
	mk := func(k settingschema.Key, req bool) Kind {
		return Kind{Name: "x", Options: []KindOption{{Key: k, Required: req}}}
	}
	bad := map[string]func(){
		"reserved kind":  func() { NewKinds(mk(settingschema.Key{Path: "tasks[].kind", Type: "string", Default: `""`}, false)) },
		"reserved model": func() { NewKinds(mk(settingschema.Key{Path: "tasks[].model", Type: "string", Default: `""`}, false)) },
		"wrong prefix":   func() { NewKinds(mk(settingschema.Key{Path: "options.x", Type: "string", Default: `""`}, false)) },
		"bad type":       func() { NewKinds(mk(settingschema.Key{Path: "tasks[].x", Type: "float", Default: "1"}, false)) },
		"bad default":    func() { NewKinds(mk(settingschema.Key{Path: "tasks[].x", Type: "int", Default: "many"}, false)) },
		"twice":          func() { NewKinds(Kind{Name: "x"}, Kind{Name: "x"}) },
		"unnamed":        func() { NewKinds(Kind{}) },
	}
	for name, f := range bad {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("did not panic")
				}
			}()
			f()
		})
	}
}

func TestKindsCheckSchema(t *testing.T) {
	table := func(keys ...settingschema.Key) settingschema.Schema {
		// The production kind options are left out: the test supplies its own.
		var own []settingschema.Key
		for _, k := range workerconfig.Schema.Keys {
			if opt := strings.HasPrefix(k.Path, "tasks[]."); !opt || k.Path == "tasks[].kind" || k.Path == "tasks[].model" {
				own = append(own, k)
			}
		}
		return settingschema.Schema{Keys: append(own, keys...)}
	}
	all := table(cfgNameKey, cfgEveryKey, cfgPathsKey, cfgDepthKey, cfgFastKey, cfgRootKey)
	if err := cfgKinds().CheckSchema(all); err != nil {
		t.Errorf("a registry matching the table fails: %v", err)
	}
	if err := cfgKinds().CheckSchema(table(cfgNameKey)); err == nil || !strings.Contains(err.Error(), "no row") {
		t.Errorf("an option missing from the table passes: %v", err)
	}
	drift := cfgEveryKey
	drift.Default = "12h"
	r := NewKinds(Kind{Name: "a", Options: []KindOption{{Key: cfgEveryKey}}}, Kind{Name: "b", Options: []KindOption{{Key: drift}}})
	if err := r.CheckSchema(table(cfgEveryKey)); err == nil || !strings.Contains(err.Error(), "differently") {
		t.Errorf("a shared option declared twice with drift passes: %v", err)
	}
	if err := DefaultKinds().CheckSchema(workerconfig.Schema); err != nil {
		t.Errorf("the production registry does not match the key table: %v", err)
	}
}

// yamlPaths lists the YAML key paths of a struct, the way the table writes them.
func yamlPaths(t reflect.Type, prefix string, out *[]string) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}
		path := prefix + name
		ft := f.Type
		for ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		switch {
		case ft.Kind() == reflect.Struct:
			yamlPaths(ft, path+".", out)
		case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Struct && ft.Elem() != reflect.TypeOf(yaml.Node{}):
			*out = append(*out, path)
			yamlPaths(ft.Elem(), path+"[].", out)
		default:
			*out = append(*out, path)
		}
	}
}

// groups hold only other keys and need no row of their own.
var cfgGroups = map[string]bool{"models[].budget": true, "schedule": true, "schedule.window": true}

// The key table is the one list of known keys, so a field of the file without a
// row would load but go undocumented, and a row without a field would be
// accepted by the walker and silently ignored. tasks[] children are the
// kinds', checked by the registry test.
func TestSchemaMatchesStructs(t *testing.T) {
	var want []string
	yamlPaths(reflect.TypeOf(rawConfig{}), "", &want)
	real := map[string]bool{"tasks": true, "tasks[].kind": true, "tasks[].model": true}
	for _, p := range want {
		if !cfgGroups[p] {
			real[p] = true
		}
	}
	have := map[string]bool{}
	for _, k := range workerconfig.Schema.Keys {
		have[k.Path] = true
		if !real[k.Path] && !strings.HasPrefix(k.Path, "tasks[].") {
			t.Errorf("table row %s maps to no field of rawConfig", k.Path)
		}
	}
	for p := range real {
		if !have[p] {
			t.Errorf("field %s is parsed but has no row in internal/workerconfig/schema.go", p)
		}
	}
}

// A URL key never echoes its value: it may carry credentials, with a host or
// without one, and no load error nor printed Config may contain them.
func TestConfigURLsNeverLeakCredentials(t *testing.T) {
	for _, u := range []string{"https://u:pw@", "https://u:pw@wiki.example.com/mcp", "https://u:pw@/x", "http://u:pw@[::1", "https://u:pw@ho st"} {
		for name, body := range map[string]string{
			"server_url": cfgWith(t, "https://wiki.example.com/mcp", u),
			"base_url":   cfgWith(t, "http://llm.internal:8080/v1", u),
		} {
			_, err := cfgLoad(t, body)
			if err == nil {
				t.Errorf("%s %s loads", name, u)
			} else if strings.Contains(err.Error(), "pw") {
				t.Errorf("%s %s leaks credentials: %v", name, u, err)
			}
		}
	}
	if _, err := cfgLoad(t, cfgWith(t, "https://wiki.example.com/mcp", "https://u:pw@wiki.example.com/mcp")); err == nil || !strings.Contains(err.Error(), "server_url (line 2)") || !strings.Contains(err.Error(), "credentials in the URL") {
		t.Errorf("server_url credentials: %v", err)
	}
}

func TestLoadConfigWrongShapeNamesKeyAndLine(t *testing.T) {
	tail := cfgGolden[strings.Index(cfgGolden, "schedule:"):]
	head := cfgGolden[:strings.Index(cfgGolden, "models:")]
	sched := cfgGolden[strings.Index(cfgGolden, "schedule:"):]
	for name, tc := range map[string]struct{ body, want string }{
		"models scalar":   {head + "models: foo\n" + tail, "models (line 5): must be a list of mappings"},
		"models entry":    {head + "models: [bar]\n" + tail, "models[0] (line 5): must be a mapping of keys"},
		"schedule scalar": {strings.Replace(cfgGolden, sched[:strings.Index(sched, "tasks:")], "schedule: 5\n", 1), "schedule (line 21): must be a mapping of keys"},
		"window list":     {cfgWith(t, "  window:\n    start: \"23:00\"\n    end: \"06:00\"\n    timezone: Europe/Rome\n", "  window: [a]\n"), "schedule.window (line 22): must be a mapping of keys"},
		"budget scalar":   {cfgWith(t, "    budget:\n      daily_tokens: 2000000\n      run_tokens: 500000\n", "    budget: lots\n"), "models[0].budget (line 11): must be a mapping of keys"},
		"tasks scalar":    {cfgGolden[:strings.Index(cfgGolden, "tasks:")] + "tasks: ingest\n", "tasks (line 26): must be a list of mappings"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := cfgLoad(t, tc.body)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}
