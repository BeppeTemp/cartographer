package core

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

const validManifest = `schema: 1
name: team-forks
version: 1.2.3
display_name: Team forks
description: Closed forks of supported clients and the skills that go with them.
cartographer: ">=0.28.0 <0.30.0"
`

var testOpts = Options{
	RunningVersion: "0.28.0",
	ReservedName:   func(n string) bool { return n == "sync" || n == "plugin" },
}

// edit returns validManifest with the first line starting with key replaced by
// repl (empty repl drops the line), or repl appended when no line starts so.
func edit(key, repl string) string {
	var out []string
	done := false
	for _, l := range strings.Split(strings.TrimRight(validManifest, "\n"), "\n") {
		if strings.HasPrefix(l, key+":") {
			done = true
			if repl != "" {
				out = append(out, repl)
			}
			continue
		}
		out = append(out, l)
	}
	if !done && repl != "" {
		out = append(out, repl)
	}
	return strings.Join(out, "\n") + "\n"
}

func TestParseManifestValid(t *testing.T) {
	m, probs := ParseManifest([]byte(validManifest), testOpts)
	if len(probs) != 0 {
		t.Fatalf("a valid manifest has problems: %v", probs)
	}
	if m.Name != "team-forks" || m.Version != "1.2.3" || m.Cartographer != ">=0.28.0 <0.30.0" {
		t.Errorf("fields not read: %+v", m)
	}
}

func TestParseManifestFull(t *testing.T) {
	full := validManifest + `depends:
  base-skills: ">=1.0.0"
permissions:
  kb: read
  net:
    - gitlab.example.com
    - "*.example.com"
    - "*.github.io"
  files:
    read: ["{home}/.config/fork-cli/*.json", "{documents}"]
    write: ["{xdg_config}/fork-cli/state"]
  env: [FORK_TOKEN, LANG]
settings:
  endpoint:
    type: string
    default: https://example.com
    description: Where the fork talks to.
  verbose:
    type: bool
    default: false
    description: More output.
  retries:
    type: int
    default: 3
    description: How many times.
  nodefault:
    type: string
    description: No default.
`
	if _, probs := ParseManifest([]byte(full), testOpts); len(probs) != 0 {
		t.Fatalf("problems: %v", probs)
	}
}

func TestParseManifestProblems(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // substring of one problem
	}{
		{"empty", "", "empty"},
		{"unknown key", validManifest + "colour: red\n", `unknown key "colour"`},
		{"unknown nested key", validManifest + "permissions:\n  disk: all\n", `unknown key "disk"`},
		{"alias", validManifest + "x: &a 1\ny: *a\n", "aliases"},
		{"second document", validManifest + "---\nschema: 1\n", "more than one"},
		{"oversized", validManifest + "# " + strings.Repeat("x", contract.ArtifactMaxFileSize) + "\n", "over the"},
		{"not yaml", "schema: [", "manifest:"},
		{"no schema", edit("schema", ""), "schema: required"},
		{"schema 3", edit("schema", "schema: 3"), "unsupported plugin schema 3"},
		{"schema 0", edit("schema", "schema: 0"), "unsupported plugin schema 0"},
		{"no name", edit("name", ""), "name: required"},
		{"name upper", edit("name", "name: Team"), "name:"},
		{"name short", edit("name", "name: a"), "name:"},
		{"name long", edit("name", "name: a"+strings.Repeat("b", 41)), "name:"},
		{"name underscore", edit("name", "name: team_forks"), "name:"},
		{"name bundle", edit("name", "name: bundle"), "reserved"},
		{"name kb", edit("name", "name: kb"), "name:"},
		{"name windows device", edit("name", "name: con"), "name:"},
		{"name windows device nul", edit("name", "name: nul"), "name:"},
		{"name provider id", edit("name", "name: claude"), "built-in client"},
		{"name command", edit("name", "name: sync"), "built-in command"},
		{"name command plugin", edit("name", "name: plugin"), "built-in command"},
		{"no version", edit("version", ""), "version: required"},
		{"version two parts", edit("version", "version: 1.2"), "version:"},
		{"version v", edit("version", "version: v1.2.3"), "version:"},
		{"version pre", edit("version", "version: 1.2.3-rc.1"), "version:"},
		{"version zero padded", edit("version", "version: 01.2.3"), "version:"},
		{"no display_name", edit("display_name", ""), "display_name: required"},
		{"display_name long", edit("display_name", "display_name: "+strings.Repeat("a", 61)), "display_name: 61 characters"},
		{"display_name control", edit("display_name", `display_name: "a\u0007b"`), "display_name: contains a control"},
		{"display_name bidi", edit("display_name", `display_name: "a\u202eb"`), "display_name: contains a control"},
		{"display_name newline", edit("display_name", `display_name: "a\nb"`), "display_name: must be on one line"},
		{"no description", edit("description", ""), "description: required"},
		{"description long", edit("description", "description: "+strings.Repeat("a", 301)), "description: 301 characters"},
		{"description two paragraphs", edit("description", `description: "one\n\ntwo"`), "one paragraph"},
		{"description control", edit("description", `description: "a\u0007b"`), "description: contains a control"},
		{"no range", edit("cartographer", ""), "cartographer: required"},
		{"bad range", edit("cartographer", `cartographer: "~0.28"`), "cartographer:"},
		{"depends bad range", validManifest + "depends:\n  other: latest\n", "depends.other:"},
		{"depends bad name", validManifest + "depends:\n  Other: \">=1.0.0\"\n", "depends:"},
		{"permissions kb", validManifest + "permissions:\n  kb: admin\n", "permissions.kb:"},
		{"net scheme", validManifest + "permissions:\n  net: [\"https://example.com\"]\n", "permissions.net[0]"},
		{"net port", validManifest + "permissions:\n  net: [\"example.com:443\"]\n", "permissions.net[0]"},
		{"net path", validManifest + "permissions:\n  net: [\"example.com/x\"]\n", "permissions.net[0]"},
		{"net upper", validManifest + "permissions:\n  net: [\"Example.com\"]\n", "permissions.net[0]"},
		{"net star mid", validManifest + "permissions:\n  net: [\"a.*.com\"]\n", "permissions.net[0]"},
		{"net tld", validManifest + "permissions:\n  net: [\"*.com\"]\n", "permissions.net[0]"},
		{"net star dot", validManifest + "permissions:\n  net: [\"*.\"]\n", "permissions.net[0]"},
		{"type error alone", edit("name", "name: [a, b]"), "manifest:"},
		{"net refuses 127.0.0.1", validManifest + "permissions:\n  net: [\"127.0.0.1\"]\n", "permissions.net[0]"},
		{"net refuses 2130706433", validManifest + "permissions:\n  net: [\"2130706433\"]\n", "permissions.net[0]"},
		{"net refuses localhost", validManifest + "permissions:\n  net: [\"localhost\"]\n", "permissions.net[0]"},
		{"net refuses example", validManifest + "permissions:\n  net: [\"example\"]\n", "permissions.net[0]"},
		{"net refuses *.com", validManifest + "permissions:\n  net: [\"*.com\"]\n", "permissions.net[0]"},
		{"net refuses *.", validManifest + "permissions:\n  net: [\"*.\"]\n", "permissions.net[0]"},
		{"net refuses *.1.2", validManifest + "permissions:\n  net: [\"*.1.2\"]\n", "permissions.net[0]"},
		{"net refuses 10.0.0.1", validManifest + "permissions:\n  net: [\"10.0.0.1\"]\n", "permissions.net[0]"},
		{"net refuses ::1", validManifest + "permissions:\n  net: [\"::1\"]\n", "permissions.net[0]"},
		{"net refuses [::1]", validManifest + "permissions:\n  net: [\"[::1]\"]\n", "permissions.net[0]"},
		{"net refuses *.localhost", validManifest + "permissions:\n  net: [\"*.localhost\"]\n", "permissions.net[0]"},
		{"env refuses CARTOGRAPHER_TOKEN", validManifest + "permissions:\n  env: [CARTOGRAPHER_TOKEN]\n", "permissions.env[0]"},
		{"env refuses LD_PRELOAD", validManifest + "permissions:\n  env: [LD_PRELOAD]\n", "permissions.env[0]"},
		{"env refuses DYLD_INSERT_LIBRARIES", validManifest + "permissions:\n  env: [DYLD_INSERT_LIBRARIES]\n", "permissions.env[0]"},
		{"env refuses PATH", validManifest + "permissions:\n  env: [PATH]\n", "permissions.env[0]"},
		{"env refuses HOME", validManifest + "permissions:\n  env: [HOME]\n", "permissions.env[0]"},
		{"files read refuses {home}/.cartographer.yaml", validManifest + "permissions:\n  files:\n    read: [\"{home}/.cartographer.yaml\"]\n", "permissions.files.read[0]"},
		{"files read refuses {home}/.cartographer-x/y", validManifest + "permissions:\n  files:\n    read: [\"{home}/.cartographer-x/y\"]\n", "permissions.files.read[0]"},
		{"files read refuses {xdg_config}/cartographer/config.yaml", validManifest + "permissions:\n  files:\n    read: [\"{xdg_config}/cartographer/config.yaml\"]\n", "permissions.files.read[0]"},
		{"files read refuses {home}/.config/cartographer", validManifest + "permissions:\n  files:\n    read: [\"{home}/.config/cartographer\"]\n", "permissions.files.read[0]"},
		{"files read refuses {home}/.cache/Cartographer/plugins", validManifest + "permissions:\n  files:\n    read: [\"{home}/.cache/Cartographer/plugins\"]\n", "permissions.files.read[0]"},
		{"files read refuses {app_data}/cartographer/*", validManifest + "permissions:\n  files:\n    read: [\"{app_data}/cartographer/*\"]\n", "permissions.files.read[0]"},
		{"files read refuses {local_app_data}/cartographer", validManifest + "permissions:\n  files:\n    read: [\"{local_app_data}/cartographer\"]\n", "permissions.files.read[0]"},
		{"files read refuses {xdg_config}/*", validManifest + "permissions:\n  files:\n    read: [\"{xdg_config}/*\"]\n", "permissions.files.read[0]"},
		{"files read refuses {home}/.config/*/x", validManifest + "permissions:\n  files:\n    read: [\"{home}/.config/*/x\"]\n", "permissions.files.read[0]"},
		{"files read refuses {home}/.cartographer*", validManifest + "permissions:\n  files:\n    read: [\"{home}/.cartographer*\"]\n", "permissions.files.read[0]"},
		{"files read refuses {home}", validManifest + "permissions:\n  files:\n    read: [\"{home}\"]\n", "permissions.files.read[0]"},
		{"files read refuses {xdg_config}", validManifest + "permissions:\n  files:\n    read: [\"{xdg_config}\"]\n", "permissions.files.read[0]"},
		{"files read refuses {app_data}", validManifest + "permissions:\n  files:\n    read: [\"{app_data}\"]\n", "permissions.files.read[0]"},
		{"files read refuses {local_app_data}", validManifest + "permissions:\n  files:\n    read: [\"{local_app_data}\"]\n", "permissions.files.read[0]"},
		{"files read refuses {home}/**", validManifest + "permissions:\n  files:\n    read: [\"{home}/**\"]\n", "permissions.files.read[0]"},
		{"files write refuses {home}/.cartographer.yaml", validManifest + "permissions:\n  files:\n    write: [\"{home}/.cartographer.yaml\"]\n", "permissions.files.write[0]"},
		{"files write refuses {home}/.cartographer-x/y", validManifest + "permissions:\n  files:\n    write: [\"{home}/.cartographer-x/y\"]\n", "permissions.files.write[0]"},
		{"files write refuses {xdg_config}/cartographer/config.yaml", validManifest + "permissions:\n  files:\n    write: [\"{xdg_config}/cartographer/config.yaml\"]\n", "permissions.files.write[0]"},
		{"files write refuses {home}/.config/cartographer", validManifest + "permissions:\n  files:\n    write: [\"{home}/.config/cartographer\"]\n", "permissions.files.write[0]"},
		{"files write refuses {home}/.cache/Cartographer/plugins", validManifest + "permissions:\n  files:\n    write: [\"{home}/.cache/Cartographer/plugins\"]\n", "permissions.files.write[0]"},
		{"files write refuses {app_data}/cartographer/*", validManifest + "permissions:\n  files:\n    write: [\"{app_data}/cartographer/*\"]\n", "permissions.files.write[0]"},
		{"files write refuses {local_app_data}/cartographer", validManifest + "permissions:\n  files:\n    write: [\"{local_app_data}/cartographer\"]\n", "permissions.files.write[0]"},
		{"files write refuses {xdg_config}/*", validManifest + "permissions:\n  files:\n    write: [\"{xdg_config}/*\"]\n", "permissions.files.write[0]"},
		{"files write refuses {home}/.config/*/x", validManifest + "permissions:\n  files:\n    write: [\"{home}/.config/*/x\"]\n", "permissions.files.write[0]"},
		{"files write refuses {home}/.cartographer*", validManifest + "permissions:\n  files:\n    write: [\"{home}/.cartographer*\"]\n", "permissions.files.write[0]"},
		{"files write refuses {home}", validManifest + "permissions:\n  files:\n    write: [\"{home}\"]\n", "permissions.files.write[0]"},
		{"files write refuses {xdg_config}", validManifest + "permissions:\n  files:\n    write: [\"{xdg_config}\"]\n", "permissions.files.write[0]"},
		{"files write refuses {app_data}", validManifest + "permissions:\n  files:\n    write: [\"{app_data}\"]\n", "permissions.files.write[0]"},
		{"files write refuses {local_app_data}", validManifest + "permissions:\n  files:\n    write: [\"{local_app_data}\"]\n", "permissions.files.write[0]"},
		{"files write refuses {home}/**", validManifest + "permissions:\n  files:\n    write: [\"{home}/**\"]\n", "permissions.files.write[0]"},
		{"files read refuses {home}/", validManifest + "permissions:\n  files:\n    read: [\"{home}/\"]\n", "permissions.files.read[0]"},
		{"files read refuses {home}/.", validManifest + "permissions:\n  files:\n    read: [\"{home}/.\"]\n", "permissions.files.read[0]"},
		{"files read refuses {home}//", validManifest + "permissions:\n  files:\n    read: [\"{home}//\"]\n", "permissions.files.read[0]"},
		{"files read refuses {xdg_config}/", validManifest + "permissions:\n  files:\n    read: [\"{xdg_config}/\"]\n", "permissions.files.read[0]"},
		{"files read refuses {app_data}/.", validManifest + "permissions:\n  files:\n    read: [\"{app_data}/.\"]\n", "permissions.files.read[0]"},
		{"files read refuses {local_app_data}/./", validManifest + "permissions:\n  files:\n    read: [\"{local_app_data}/./\"]\n", "permissions.files.read[0]"},
		{"files read refuses {home}/a//b", validManifest + "permissions:\n  files:\n    read: [\"{home}/a//b\"]\n", "permissions.files.read[0]"},
		{"files read refuses {home}/a/./b", validManifest + "permissions:\n  files:\n    read: [\"{home}/a/./b\"]\n", "permissions.files.read[0]"},
		{"files read refuses {home}/a/", validManifest + "permissions:\n  files:\n    read: [\"{home}/a/\"]\n", "permissions.files.read[0]"},
		{"files read refuses {documents}/", validManifest + "permissions:\n  files:\n    read: [\"{documents}/\"]\n", "permissions.files.read[0]"},
		{"files read refuses {home}/cartographer.", validManifest + "permissions:\n  files:\n    read: [\"{home}/cartographer.\"]\n", "permissions.files.read[0]"},
		{"files read refuses {home}/cartographer. ", validManifest + "permissions:\n  files:\n    read: [\"{home}/cartographer. \"]\n", "permissions.files.read[0]"},
		{"files read refuses {home}/.cartographer.yaml.", validManifest + "permissions:\n  files:\n    read: [\"{home}/.cartographer.yaml.\"]\n", "permissions.files.read[0]"},
		{"files read refuses {home}/CARTOG~1", validManifest + "permissions:\n  files:\n    read: [\"{home}/CARTOG~1\"]\n", "permissions.files.read[0]"},
		{"files read refuses {home}/x:stream", validManifest + "permissions:\n  files:\n    read: [\"{home}/x:stream\"]\n", "permissions.files.read[0]"},
		{"files read refuses {home}/a/ ", validManifest + "permissions:\n  files:\n    read: [\"{home}/a/ \"]\n", "permissions.files.read[0]"},
		{"net refuses 0x7f.0x0.0x0.0x1", validManifest + "permissions:\n  net: [\"0x7f.0x0.0x0.0x1\"]\n", "permissions.net[0]"},
		{"net refuses 0x7f.0x1", validManifest + "permissions:\n  net: [\"0x7f.0x1\"]\n", "permissions.net[0]"},
		{"net refuses 127.0x1", validManifest + "permissions:\n  net: [\"127.0x1\"]\n", "permissions.net[0]"},
		{"net refuses 0177.0.0.1", validManifest + "permissions:\n  net: [\"0177.0.0.1\"]\n", "permissions.net[0]"},
		{"net refuses 1.2.3.0xa", validManifest + "permissions:\n  net: [\"1.2.3.0xa\"]\n", "permissions.net[0]"},
		{"net refuses a.0x1", validManifest + "permissions:\n  net: [\"a.0x1\"]\n", "permissions.net[0]"},
		{"env refuses PATHEXT", validManifest + "permissions:\n  env: [PATHEXT]\n", "permissions.env[0]"},
		{"env refuses COMSPEC", validManifest + "permissions:\n  env: [COMSPEC]\n", "permissions.env[0]"},
		{"env refuses USERPROFILE", validManifest + "permissions:\n  env: [USERPROFILE]\n", "permissions.env[0]"},
		{"env refuses APPDATA", validManifest + "permissions:\n  env: [APPDATA]\n", "permissions.env[0]"},
		{"env refuses LOCALAPPDATA", validManifest + "permissions:\n  env: [LOCALAPPDATA]\n", "permissions.env[0]"},
		{"env refuses SYSTEMROOT", validManifest + "permissions:\n  env: [SYSTEMROOT]\n", "permissions.env[0]"},
		{"env refuses TMPDIR", validManifest + "permissions:\n  env: [TMPDIR]\n", "permissions.env[0]"},
		{"env refuses TEMP", validManifest + "permissions:\n  env: [TEMP]\n", "permissions.env[0]"},
		{"env refuses TMP", validManifest + "permissions:\n  env: [TMP]\n", "permissions.env[0]"},
		{"env refuses XDG_CONFIG_HOME", validManifest + "permissions:\n  env: [XDG_CONFIG_HOME]\n", "permissions.env[0]"},
		{"env refuses XDG_DATA_DIRS", validManifest + "permissions:\n  env: [XDG_DATA_DIRS]\n", "permissions.env[0]"},
		{"net bare star", validManifest + "permissions:\n  net: [\"*\"]\n", "permissions.net[0]"},
		{"net too many", validManifest + "permissions:\n  net: [" + strings.TrimSuffix(strings.Repeat("a.example.com,", 17), ",") + "]\n", "at most 16"},
		{"env lower", validManifest + "permissions:\n  env: [token]\n", "permissions.env[0]"},
		{"env too many", validManifest + "permissions:\n  env: [" + strings.TrimSuffix(strings.Repeat("A,", 17), ",") + "]\n", "at most 16"},
		{"files absolute", validManifest + "permissions:\n  files:\n    read: [\"/etc/passwd\"]\n", "permissions.files.read[0]"},
		{"files relative", validManifest + "permissions:\n  files:\n    write: [\"notes/x\"]\n", "permissions.files.write[0]"},
		{"files dotdot", validManifest + "permissions:\n  files:\n    read: [\"{home}/../etc\"]\n", "must not contain .."},
		{"files unknown placeholder", validManifest + "permissions:\n  files:\n    read: [\"{tmp}/x\"]\n", "permissions.files.read[0]"},
		{"files late placeholder", validManifest + "permissions:\n  files:\n    read: [\"{home}/{documents}\"]\n", "placeholder"},
		{"files backslash", validManifest + "permissions:\n  files:\n    read: [\"{home}\\\\x\"]\n", "backslash"},
		{"files prefix only", validManifest + "permissions:\n  files:\n    read: [\"{homey}/x\"]\n", "permissions.files.read[0]"},
		{"setting key", validManifest + "settings:\n  Bad:\n    type: string\n    description: d\n", "settings:"},
		{"setting no description", validManifest + "settings:\n  a:\n    type: string\n", "settings.a.description: required"},
		{"setting no type", validManifest + "settings:\n  a:\n    description: d\n", "settings.a.type: required"},
		{"setting bad type", validManifest + "settings:\n  a:\n    type: float\n    description: d\n", "settings.a.type:"},
		{"setting string default", validManifest + "settings:\n  a:\n    type: string\n    default: 3\n    description: d\n", "settings.a.default:"},
		{"setting bool default", validManifest + "settings:\n  a:\n    type: bool\n    default: \"yes\"\n    description: d\n", "settings.a.default:"},
		{"setting int default", validManifest + "settings:\n  a:\n    type: int\n    default: 1.5\n    description: d\n", "settings.a.default:"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, probs := ParseManifest([]byte(c.in), testOpts)
			if len(probs) == 0 {
				t.Fatalf("accepted; want a problem containing %q", c.want)
			}
			for _, p := range probs {
				if strings.Contains(p, c.want) {
					return
				}
			}
			t.Errorf("problems %q lack %q", probs, c.want)
		})
	}
}

func TestParseManifestSettingsCap(t *testing.T) {
	var b strings.Builder
	b.WriteString(validManifest + "settings:\n")
	for i := 0; i < 33; i++ {
		b.WriteString("  k" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + ":\n    type: bool\n    description: d\n")
	}
	_, probs := ParseManifest([]byte(b.String()), testOpts)
	if len(probs) != 1 || !strings.Contains(probs[0], "at most 32") {
		t.Errorf("problems = %q; want the 32-key cap", probs)
	}
}

func TestParseManifestCollectsEveryProblem(t *testing.T) {
	in := edit("version", "version: 1")
	in = strings.Replace(in, "name: team-forks", "name: Bad", 1)
	_, probs := ParseManifest([]byte(in), testOpts)
	if len(probs) != 2 {
		t.Fatalf("problems = %q; want the name and the version", probs)
	}
	if !(strings.HasPrefix(probs[0], "name:") && strings.HasPrefix(probs[1], "version:")) {
		t.Errorf("problems are not sorted by path: %q", probs)
	}
}

func TestParseManifestRangeFirstAndOnly(t *testing.T) {
	// An unmet range is the only error, whatever else is wrong, even a key this
	// version does not know (a manifest of a newer schema).
	in := "schema: 2\nname: Bad Name\ncartographer: \">=0.28.0\"\nfuture_key: 1\n"
	_, probs := ParseManifest([]byte(in), Options{RunningVersion: "0.27.3"})
	want := "cartographer: needs Cartographer >=0.28.0 (this is 0.27.3)"
	if len(probs) != 1 || probs[0] != want {
		t.Errorf("problems = %q; want only %q", probs, want)
	}
}

func TestParseManifestRunningVersions(t *testing.T) {
	for _, run := range []string{"dev", "", "0.28.0", "v0.28.0", "0.28.0-rc.1", "0.28.0-4-g1a2b3c4", "0.29.5"} {
		if _, probs := ParseManifest([]byte(validManifest), Options{RunningVersion: run}); len(probs) != 0 {
			t.Errorf("running %q refused: %v", run, probs)
		}
	}
	for _, run := range []string{"0.27.9", "0.30.0", "0.30.0-rc.1"} {
		if _, probs := ParseManifest([]byte(validManifest), Options{RunningVersion: run}); len(probs) != 1 || !strings.Contains(probs[0], "needs Cartographer") {
			t.Errorf("running %q = %q; want the range error", run, probs)
		}
	}
}

func TestParseManifestMalformedRangeIsAManifestError(t *testing.T) {
	_, probs := ParseManifest([]byte(edit("cartographer", `cartographer: ">=0.28"`)), Options{RunningVersion: "0.1.0"})
	if len(probs) != 1 || !strings.HasPrefix(probs[0], "cartographer:") || strings.Contains(probs[0], "needs Cartographer") {
		t.Errorf("problems = %q; want a malformed-range error", probs)
	}
}

func TestParseManifestNoReservedNameFunc(t *testing.T) {
	// A nil ReservedName reserves no command; the other name rules still hold.
	in := edit("name", "name: sync")
	if _, probs := ParseManifest([]byte(in), Options{RunningVersion: "0.28.0"}); len(probs) != 0 {
		t.Errorf("problems = %v", probs)
	}
}

// A wildcard is refused only while it can still lead into a known location of
// Cartographer's own state; deeper inside a client's own folder it passes (#1783).
// A literal "cartographer" or ".cartographer*" segment is refused at any depth.
func TestGlobProblemWildcardsInsideAClientFolder(t *testing.T) {
	for _, g := range []string{
		"{xdg_config}/vendor-cli/**", "{xdg_config}/x/**", "{home}/.x/*/y", "{home}/.x/c*",
		"{home}/.vendor-cli/*/y", "{home}/.vendor-cli/c*",
		"{home}/.vscode/extensions/vendor.ext-*/package.json", "{home}/.config/vendor-cli/*",
		"{app_data}/vendor/*", "{local_app_data}/vendor/c*", "{documents}/*", "{documents}/Rules/c*",
	} {
		if why := GlobProblem(g); why != "" {
			t.Errorf("%q refused: %s", g, why)
		}
	}
	for _, g := range []string{
		"{home}/*", "{home}/**", "{home}/.config/*", "{home}/.config/c*", "{home}/c*", "{home}/.c*",
		"{xdg_config}/*", "{xdg_config}/c*", "{app_data}/*", "{local_app_data}/c*",
		"{home}/Library/*/cartographer", "{home}/Library/Application Support/*", "{home}/x/.cartographer.yaml",
		"{home}/x/cartographer", "{home}/x/.cartographer-*", "{xdg_config}/x/cartographer/**", "{documents}/x/cartographer",
		"{home}/*.yaml", "{home}/cartographer-data/**", "{home}/cartographer-data/*", "{home}/.local/share/cartographer/*",
		"{xdg_config}/cartographer/*", "{app_data}/cartographer/**",
	} {
		if why := GlobProblem(g); why == "" {
			t.Errorf("%q accepted", g)
		}
	}
}
